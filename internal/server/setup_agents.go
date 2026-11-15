package server

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/agents"
)

// setupAgents initializes the agent configuration and handler
func (s *Server) setupAgents(serverAPIKey string) error {
	// Initialize agent configuration
	agentAPIKey := os.Getenv("ANTHROPIC_API_KEY")
	if agentAPIKey == "" {
		agentAPIKey = os.Getenv("CLAUDE_API_KEY")
	}

	// Log API key status
	if agentAPIKey == "" {
		if !s.quiet {
			fmt.Printf("⚠️  WARNING: No API key found in environment variables (ANTHROPIC_API_KEY or CLAUDE_API_KEY)\n")
		}
		if s.verbose {
			logging.Warning("No API key found in environment variables")
		}
	} else {
		if s.verbose {
			logging.Info("API key loaded from environment (length: %d characters)", len(agentAPIKey))
		}
	}

	// Set retention defaults if not specified
	retentionDays := s.config.Agent.SessionRetentionDays
	if retentionDays == 0 {
		retentionDays = 30 // Default: 30 days
	}

	cleanupEnabled := s.config.Agent.CleanupEnabled
	// If not explicitly set in config, default to true

	cleanupInterval := s.config.Agent.CleanupIntervalHours
	if cleanupInterval == 0 {
		cleanupInterval = 24 // Default: 24 hours
	}

	agentConfig := &agents.Config{
		Model:                 s.config.Agent.Model,
		APIKey:                agentAPIKey,
		MaxConcurrentSessions: s.config.Agent.MaxConcurrentSessions,
		Verbose:               s.verbose,
		SessionRetentionDays:  retentionDays,
		CleanupEnabled:        cleanupEnabled,
		CleanupIntervalHours:  cleanupInterval,
		DefaultProvider:       s.config.Agent.DefaultProvider,
		DefaultModel:          s.config.Agent.DefaultModel,
		PermissionMode:        s.config.Agent.PermissionMode,
	}
	s.agentConfig = agentConfig

	// Initialize agent handler (requires database and repository for skill injection)
	agentHandler, err := agents.NewAgentHandler(agentConfig, s.db, s.repo)
	if err != nil {
		return fmt.Errorf("failed to initialize agent handler: %w", err)
	}
	// Set the API key for WebSocket authentication
	agentHandler.APIKey = serverAPIKey

	// Wire up session token validation for WebSocket auth fallback.
	// This allows mobile clients to authenticate via session token when
	// cookies aren't forwarded (e.g. through ngrok).
	if s.userStore != nil {
		store := s.userStore
		agentHandler.SessionValidator = agentWSSessionValidator(store)
		// Connections signed in with a session token are closed once that
		// session is signed out, revoked or expired.
		agentHandler.SessionTokenHashValid = store.sessionHashSignedIn
		agentHandler.IsAdminUser = store.isAdminUsername
	}

	s.agentHandler = agentHandler
	// Privileged-action policy (signed-in user vs API key) is set in
	// configurePrivilegedActionPolicy (principal.SetEnforced).

	logging.Info("Agent handler initialized: model=%s, maxSessions=%d, verbose=%v, apiKeySet=%v",
		agentConfig.Model, agentConfig.MaxConcurrentSessions, agentConfig.Verbose, agentAPIKey != "")

	// Start session cleanup job
	s.agentHandler.SessionManager.StartCleanupJob()

	return nil
}

// sessionHashSignedIn reports whether the session stored under tokenHash
// (database.HashToken of its token) exists, has not expired, has completed
// its MFA step and belongs to a known user.
func (us *UserStore) sessionHashSignedIn(tokenHash string) bool {
	us.mu.RLock()
	defer us.mu.RUnlock()
	sess, ok := us.sessions[tokenHash]
	if !ok || sess == nil || time.Now().After(sess.ExpiresAt) {
		return false
	}
	if sess.MFARequired && !sess.MFAVerified {
		return false
	}
	_, known := us.users[sess.Username]
	return known
}

// isAdminUsername reports whether username is an admin account.
func (us *UserStore) isAdminUsername(username string) bool {
	us.mu.RLock()
	defer us.mu.RUnlock()
	u, ok := us.users[username]
	return ok && u != nil && u.IsAdmin
}

// closeSocketsForToken closes the /agent/ws, analytics /ws and terminal
// connections signed in with the session token whose hash is tokenHash
// (logout, device revoked).
func (s *Server) closeSocketsForToken(tokenHash string) {
	if s == nil || tokenHash == "" {
		return
	}
	if s.wsHub != nil {
		if n := s.wsHub.CloseConnectionsForSessionToken(tokenHash); n > 0 {
			logging.Info("Closed %d analytics WebSocket connection(s) of a signed-out session", n)
		}
	}
	if s.agentHandler != nil {
		if n := s.agentHandler.CloseConnectionsForSessionToken(tokenHash); n > 0 {
			logging.Info("Closed %d agent WebSocket connection(s) of a signed-out session", n)
		}
	}
	if n := s.terminalHandler.CloseConnectionsForSessionToken(tokenHash); n > 0 {
		logging.Info("Closed %d terminal WebSocket connection(s) of a signed-out session", n)
	}
}

// agentWSSessionValidator validates a session token presented on /agent/ws.
// SECURITY: userForSessionToken refuses tokens whose MFA step is not
// complete; ValidateSession alone would accept them.
func agentWSSessionValidator(store *UserStore) func(token string) (string, error) {
	return func(token string) (string, error) {
		user := store.userForSessionToken(token)
		if user == nil {
			return "", errors.New("invalid or incomplete session")
		}
		return user.Username, nil
	}
}
