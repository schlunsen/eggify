package agents

import (
	"fmt"
	"log"
	"sync"
	"time"

	fiberws "github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
)

// AnalyticsHub interface for broadcasting events to analytics WebSocket clients
type AnalyticsHub interface {
	BroadcastSessionData(event, sessionID string, data interface{})
}

// AgentHandler manages WebSocket connections and Claude Agent SDK integration
type AgentHandler struct {
	Config               *Config                                         // Exported for server access
	SessionManager       *SessionManager                                 // Exported for server access
	ProjectWatcherMgr    *ProjectWatcherManager                          // Manages all project git watchers
	BackgroundAgentMgr   *BackgroundAgentManager                         // Manages background agents spawned via Task tool
	Digests              *SessionDigestService                           // AI summaries and tags of sessions, for the overview
	APIKey               string                                          // API key for WebSocket authentication
	Mu                   sync.Mutex                                      // Exported for server access
	Active               int                                             // Exported for server access
	db                   *database.Database                              // Database wrapper for user lookups
	connMu               map[*fiberws.Conn]*connWriter                   // Per-connection write state (lock, pending count, broken flag)
	connMuLock           sync.RWMutex                                    // Protects connMu map
	sessionConnections   map[uuid.UUID][]*fiberws.Conn                   // Maps session IDs to their WebSocket connections
	sessionConnectionsMu sync.RWMutex                                    // Protects sessionConnections map
	sessionBuffers       map[uuid.UUID]*MessageBuffer                    // Per-session message buffers for disconnected clients
	sessionBuffersMu     sync.RWMutex                                    // Protects sessionBuffers map
	allConnections       []*fiberws.Conn                                 // All active WebSocket connections for broadcasting
	allConnectionsMu     sync.RWMutex                                    // Protects allConnections slice
	analyticsHub         AnalyticsHub                                    // Analytics WebSocket hub for broadcasting tool use events
	connUsers            map[*fiberws.Conn]*string                       // Maps WebSocket connections to authenticated usernames (SECURITY: for multi-user)
	connUsersLock        sync.RWMutex                                    // Protects connUsers map
	SessionValidator     func(token string) (username string, err error) // Validates session tokens for WebSocket auth (set by server; MFA-complete sessions only)
	// SessionTokenHashValid reports whether the session behind a token hash is
	// still signed in (set by server). Connections authenticated with a session
	// token are re-checked with it and closed once it is revoked or expired.
	SessionTokenHashValid func(tokenHash string) bool
	// IsAdminUser reports whether a username is an admin account (set by
	// server; falls back to the users table).
	IsAdminUser func(username string) bool
	// PreAuthTimeout is how long a connection may stay unauthenticated
	// (default wsPreAuthTimeout; tests shorten it).
	PreAuthTimeout  time.Duration
	connTokenHashes map[*fiberws.Conn]string // session token hash per signed-in connection (under connUsersLock)
	connLives       sync.Map                 // *fiberws.Conn -> *connLife: connections whose handler is still running
	kickedConns     sync.Map                 // *fiberws.Conn -> struct{}: signed out, closing (closeSignedOut)
	resumeOnce      sync.Once                // Lazily creates resumeSt
	resumeSt        *resumeState             // Sessions continued/offered after a restart (resume_after_restart.go)
	workflowOnce    sync.Once                // Lazily creates workflowSt
	workflowSt      *workflowTracker         // Workflow runs whose agents are followed (workflow_agents.go)
}

// NewAgentHandler creates a new agent handler with the given config and database
func NewAgentHandler(config *Config, db *database.Database, repo ...*database.Repository) (*AgentHandler, error) {
	// Pass repository to session manager for skill injection support
	var sessionManager *SessionManager
	var err error
	if len(repo) > 0 && repo[0] != nil {
		sessionManager, err = NewSessionManager(config, db.GetDB(), repo[0])
	} else {
		sessionManager, err = NewSessionManager(config, db.GetDB())
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create session manager: %w", err)
	}

	backgroundAgentMgr := NewBackgroundAgentManager()
	backgroundAgentMgr.SetStorage(sessionManager.Storage)

	// Load persisted background agents from database
	loadPersistedBackgroundAgents(sessionManager, backgroundAgentMgr)

	// Evict long-finished agents from memory; their rows stay in the database
	// and are restored on demand by SubagentsForSession.
	backgroundAgentMgr.StartCleanup(backgroundAgentCleanupInterval, backgroundAgentMaxAge)

	handler := &AgentHandler{
		Config:             config,
		SessionManager:     sessionManager,
		ProjectWatcherMgr:  NewProjectWatcherManager(),
		BackgroundAgentMgr: backgroundAgentMgr,
		db:                 db,
		Active:             0,
		connMu:             make(map[*fiberws.Conn]*connWriter),
		sessionConnections: make(map[uuid.UUID][]*fiberws.Conn),
		sessionBuffers:     make(map[uuid.UUID]*MessageBuffer),
		connUsers:          make(map[*fiberws.Conn]*string),
	}

	// Register session update callback to broadcast state changes
	sessionManager.SetSessionUpdateCallback(handler.broadcastSessionStateUpdate)

	// Register broadcast message callback for silent message recovery
	// Wrap broadcastToSession since it has additional parameters
	sessionManager.SetBroadcastMessageCallback(func(sessionID uuid.UUID, msg types.Message) {
		handler.broadcastToSession(sessionID, msg, nil)
	})

	// Register broadcast-to-session callback for sending arbitrary messages to a session's connections
	sessionManager.SetBroadcastToSessionCallback(func(sessionID uuid.UUID, msg interface{}) {
		handler.broadcastToAllConnections(sessionID, msg)
	})

	// Register broadcast callback for background agent updates
	backgroundAgentMgr.SetBroadcastCallback(func(sessionID uuid.UUID, msg interface{}) {
		handler.broadcastBackgroundAgentUpdate(sessionID, msg)
	})

	// What a worker is doing goes to the connections of the session that
	// started it (live only, never buffered).
	sessionManager.SetLinkedSessionCallback(func(parent uuid.UUID, owner *string, msg interface{}) {
		handler.sendToParentConnections(parent, uuid.Nil, owner, msg)
	})

	// Subagent messages and task_* events go to the background agent tracker,
	// not into the parent conversation.
	sessionManager.SetSubagentCallback(handler.handleSubagentTraffic)
	// Background subagents die with the session's Claude CLI.
	sessionManager.SetClientClosedCallback(handler.stopSessionSubagents)
	// Claude Code /loop wakeups, shown on the session.
	sessionManager.SetScheduledLoopCallback(handler.broadcastScheduledLoop)
	// Usage limit: waiting sessions are shown, and subagents the limit
	// stopped put their session in the wait.
	sessionManager.SetLimitWaitCallback(handler.broadcastLimitWait)
	// Asynchronous: an agent can be failed while sm.mu is held (closing the
	// CLI during a model switch), and queueing its session takes sm.mu.
	backgroundAgentMgr.SetLimitPauseCallback(func(agent BackgroundAgent) {
		go sessionManager.noteSubagentLimitPause(agent.ParentSessionID, agent)
	})

	// AskUserQuestion waits for the user, independent of browser connections.
	sessionManager.SetAskUserQuestionHandler(handler.askUserQuestions)

	if sessionManager.db != nil && sessionManager.Storage != nil {
		handler.Digests = NewSessionDigestService(sessionManager.db, sessionManager.Storage, nil)
	}

	return handler, nil
}

// loadPersistedBackgroundAgents loads background agents from the database on startup
func loadPersistedBackgroundAgents(sm *SessionManager, bgMgr *BackgroundAgentManager) {
	sm.mu.RLock()
	sessionIDs := make([]uuid.UUID, 0, len(sm.sessions))
	for id := range sm.sessions {
		sessionIDs = append(sessionIDs, id)
	}
	sm.mu.RUnlock()

	totalLoaded := 0
	for _, sessionID := range sessionIDs {
		agents, err := sm.Storage.GetBackgroundAgentsForSession(sessionID)
		if err != nil {
			logging.Warning("Failed to load background agents for session %s: %v", sessionID, err)
			continue
		}

		for _, agent := range agents {
			// Mark agents that were "running" when app died as failed
			if agent.Status == BackgroundAgentStatusRunning {
				agent.Status = BackgroundAgentStatusFailed
				agent.ErrorMessage = AppRestartedAgentMessage
				agent.Interrupted = true
				agent.UpdatedAt = time.Now()
				// Persist the status change
				if err := sm.Storage.UpdateBackgroundAgent(agent); err != nil {
					logging.Warning("Failed to update stale agent %s: %v", agent.AgentID, err)
				}
			}
			bgMgr.RestoreAgent(agent)
			totalLoaded++
		}
	}

	if totalLoaded > 0 {
		logging.Info("📦 Loaded %d background agents from database", totalLoaded)
	}
}

// broadcastBackgroundAgentUpdate broadcasts a background agent event to all WebSocket clients for a session
func (h *AgentHandler) broadcastBackgroundAgentUpdate(sessionID uuid.UUID, msg interface{}) {
	h.sessionConnectionsMu.RLock()
	connections := h.sessionConnections[sessionID]
	h.sessionConnectionsMu.RUnlock()

	if len(connections) == 0 {
		logging.Warning("⏳ No WebSocket connections, buffering background agent update for session %s", sessionID)
		h.bufferRawMessage(sessionID, msg)
		return
	}

	log.Printf("📦 Broadcasting background agent update to %d connection(s) for session %s", len(connections), sessionID)

	for _, conn := range connections {
		go func(c *fiberws.Conn) {
			if err := h.safeWriteJSON(c, msg); err != nil {
				log.Printf("Failed to broadcast background agent update: %v", err)
			}
		}(conn)
	}
}

// SetAnalyticsHub sets the analytics WebSocket hub for broadcasting tool use events
func (h *AgentHandler) SetAnalyticsHub(hub AnalyticsHub) {
	h.analyticsHub = hub
}

// GetStats returns current handler statistics
func (h *AgentHandler) GetStats() map[string]interface{} {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	sessions := h.SessionManager.ListSessions()

	return map[string]interface{}{
		"active_connections": h.Active,
		"max_connections":    h.Config.MaxConcurrentSessions,
		"active_sessions":    len(sessions),
	}
}

// Cleanup ends all active sessions gracefully
func (h *AgentHandler) Cleanup() error {
	if h.BackgroundAgentMgr != nil {
		h.BackgroundAgentMgr.StopCleanup()
	}
	count := h.SessionManager.EndAllSessions(nil)
	log.Printf("Cleaned up %d active agent sessions", count)
	return nil
}
