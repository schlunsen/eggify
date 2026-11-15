// Package handlers contains HTTP request handlers for the Eggify server.
// This file implements analytics-related endpoints including conversation data,
// statistics, command history, and reset operations.
package handlers

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/analytics"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/server/agents"
	"github.com/schlunsen/eggify/internal/server/principal"
	ws "github.com/schlunsen/eggify/internal/websocket"
)

// AnalyticsHandler handles analytics-related endpoints
type AnalyticsHandler struct {
	conversationAnalyzer *analytics.ConversationAnalyzer
	conversationParser   *analytics.ConversationParser
	stateCalculator      *analytics.StateCalculator
	processDetector      *analytics.ProcessDetector
	shellDetector        *analytics.ShellDetector
	resetTracker         *analytics.ResetTracker
	modelProviderLookup  *analytics.ModelProviderLookup
	repo                 *database.Repository
	db                   *database.Database
	wsHub                *ws.Hub
	agentHandler         *agents.AgentHandler
	claudeDir            string
	quiet                bool
	visibleSessions      func(username string, ids []uuid.UUID) map[uuid.UUID]bool
}

// SetSessionVisibility sets the check of which sessions a signed-in user may
// see; the command history lists are filtered with it.
func (h *AnalyticsHandler) SetSessionVisibility(fn func(username string, ids []uuid.UUID) map[uuid.UUID]bool) {
	h.visibleSessions = fn
}

// historyFilter returns a predicate for the conversation ids of command
// history rows the caller may see, or nil when the caller sees all of them
// (the API key, an admin, or user auth off). A signed-in non-admin sees only
// rows of agent sessions they may see; hook conversations that are no agent
// session belong to the host and stay hidden.
func (h *AnalyticsHandler) historyFilter(c *fiber.Ctx, convIDs []string) func(string) bool {
	p := principal.From(c)
	if !principal.Enforced() || !p.IsUser() || p.IsAdmin() {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(convIDs))
	for _, cid := range convIDs {
		if id, err := uuid.Parse(cid); err == nil {
			ids = append(ids, id)
		}
	}
	var visible map[uuid.UUID]bool
	if h.visibleSessions != nil {
		visible = h.visibleSessions(p.Username, ids)
	}
	return func(cid string) bool {
		id, err := uuid.Parse(cid)
		return err == nil && visible[id]
	}
}

// NewAnalyticsHandler creates a new analytics handler
func NewAnalyticsHandler(
	conversationAnalyzer *analytics.ConversationAnalyzer,
	conversationParser *analytics.ConversationParser,
	stateCalculator *analytics.StateCalculator,
	processDetector *analytics.ProcessDetector,
	shellDetector *analytics.ShellDetector,
	resetTracker *analytics.ResetTracker,
	modelProviderLookup *analytics.ModelProviderLookup,
	repo *database.Repository,
	db *database.Database,
	wsHub *ws.Hub,
	agentHandler *agents.AgentHandler,
	claudeDir string,
	quiet bool,
) *AnalyticsHandler {
	return &AnalyticsHandler{
		conversationAnalyzer: conversationAnalyzer,
		conversationParser:   conversationParser,
		stateCalculator:      stateCalculator,
		processDetector:      processDetector,
		shellDetector:        shellDetector,
		resetTracker:         resetTracker,
		modelProviderLookup:  modelProviderLookup,
		repo:                 repo,
		db:                   db,
		wsHub:                wsHub,
		agentHandler:         agentHandler,
		claudeDir:            claudeDir,
		quiet:                quiet,
	}
}

// HandleGetData handles GET /api/data
func (h *AnalyticsHandler) HandleGetData(c *fiber.Ctx) error {
	conversations, err := h.conversationAnalyzer.LoadConversations(h.stateCalculator)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	processes, _ := h.processDetector.DetectRunningClaudeProcesses()

	return c.JSON(fiber.Map{
		"conversations":      conversations,
		"activeProcessCount": len(processes),
		"claudeDir":          h.claudeDir,
		"timestamp":          time.Now(),
	})
}

// HandleGetProcesses handles GET /api/processes
func (h *AnalyticsHandler) HandleGetProcesses(c *fiber.Ctx) error {
	processes, err := h.processDetector.DetectRunningClaudeProcesses()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	stats, _ := h.processDetector.GetProcessStats()

	return c.JSON(fiber.Map{
		"processes": processes,
		"stats":     stats,
	})
}

// HandleGetStats handles GET /api/stats
func (h *AnalyticsHandler) HandleGetStats(c *fiber.Ctx) error {
	// A signed-in non-admin sees the totals of their own visible agent
	// sessions only: the CLI conversations on disk are the host's.
	if p := principal.From(c); principal.Restricted(p) {
		return c.JSON(h.restrictedStats(p.Username))
	}
	// Get CLI conversation stats
	conversations, err := h.conversationAnalyzer.LoadConversations(h.stateCalculator)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	cliTotalTokens := 0
	cliActiveCount := 0

	for _, conv := range conversations {
		cliTotalTokens += conv.Tokens
		if conv.Status == "active" {
			cliActiveCount++
		}
	}

	// Get agent session stats
	var agentSessions []agents.Session
	var agentTotalTokens int64
	var agentActiveCount int
	var agentTotalCost float64

	if h.agentHandler != nil {
		allSessions, err := h.agentHandler.SessionManager.ListAllSessions("all")
		if err == nil {
			agentSessions = allSessions
			for _, session := range allSessions {
				// Estimate tokens from message count (rough approximation)
				// TODO: Track actual tokens in messages
				agentTotalTokens += int64(session.MessageCount * 100)
				agentTotalCost += session.CostUSD

				// Count active sessions (not ended)
				if session.Status != agents.SessionStatusEnded {
					agentActiveCount++
				}
			}
		}
	}

	// Combine stats
	totalTokens := cliTotalTokens + int(agentTotalTokens)
	totalConversations := len(conversations) + len(agentSessions)
	activeCount := cliActiveCount + agentActiveCount

	// Apply soft reset delta if present
	adjustedTokens, adjustedConversations := h.resetTracker.ApplyDelta(totalTokens, totalConversations)

	avgTokens := 0
	if adjustedConversations > 0 {
		avgTokens = adjustedTokens / adjustedConversations
	}

	response := fiber.Map{
		// Combined stats
		"totalConversations":  adjustedConversations,
		"activeConversations": activeCount,
		"totalTokens":         adjustedTokens,
		"avgTokens":           avgTokens,
		"timestamp":           time.Now(),

		// Breakdown by type
		"cliConversations": len(conversations),
		"cliActive":        cliActiveCount,
		"cliTokens":        cliTotalTokens,
		"agentSessions":    len(agentSessions),
		"agentActive":      agentActiveCount,
		"agentTokens":      agentTotalTokens,
		"agentTotalCost":   agentTotalCost,
	}

	// Include reset info if present
	if resetPoint := h.resetTracker.GetResetPoint(); resetPoint != nil {
		response["resetActive"] = true
		response["resetTimestamp"] = resetPoint.Timestamp
		response["resetReason"] = resetPoint.Reason
	} else {
		response["resetActive"] = false
	}

	return c.JSON(response)
}

// HandleGetShells handles GET /api/shells
func (h *AnalyticsHandler) HandleGetShells(c *fiber.Ctx) error {
	shells, err := h.shellDetector.DetectBackgroundShells()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	stats, _ := h.shellDetector.GetShellStats()

	return c.JSON(fiber.Map{
		"shells": shells,
		"stats":  stats,
	})
}

// HandleRefresh handles POST /api/refresh
func (h *AnalyticsHandler) HandleRefresh(c *fiber.Ctx) error {
	// Clear caches
	h.stateCalculator.ClearCache()
	h.processDetector.ClearCache()
	h.shellDetector.ClearCache()

	return c.JSON(fiber.Map{
		"status": "refreshed",
		"time":   time.Now(),
	})
}

// HandleResetArchive handles POST /api/reset/archive
func (h *AnalyticsHandler) HandleResetArchive(c *fiber.Ctx) error {
	err := h.conversationAnalyzer.ArchiveConversations()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error":  err.Error(),
			"status": "failed",
		})
	}

	// Clear caches after reset
	h.stateCalculator.ClearCache()
	h.processDetector.ClearCache()
	h.shellDetector.ClearCache()

	// Broadcast update to WebSocket clients with data
	h.wsHub.BroadcastData("reset_archive", fiber.Map{
		"action":  "archive",
		"message": "All conversations have been archived",
	})

	return c.JSON(fiber.Map{
		"status":  "archived",
		"message": "All conversations have been archived",
		"time":    time.Now(),
	})
}

// HandleResetClear handles POST /api/reset/clear
func (h *AnalyticsHandler) HandleResetClear(c *fiber.Ctx) error {
	err := h.conversationAnalyzer.ClearConversations()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error":  err.Error(),
			"status": "failed",
		})
	}

	// Clear caches after reset
	h.stateCalculator.ClearCache()
	h.processDetector.ClearCache()
	h.shellDetector.ClearCache()

	// Clear any soft reset
	h.resetTracker.ClearResetPoint()

	// Broadcast update to WebSocket clients with data
	h.wsHub.BroadcastData("reset_clear", fiber.Map{
		"action":  "clear",
		"message": "All conversations have been permanently deleted",
	})

	return c.JSON(fiber.Map{
		"status":  "cleared",
		"message": "All conversations have been permanently deleted",
		"time":    time.Now(),
	})
}

// HandleResetSoft handles POST /api/reset/soft
func (h *AnalyticsHandler) HandleResetSoft(c *fiber.Ctx) error {
	// Get current totals
	conversations, err := h.conversationAnalyzer.LoadConversations(h.stateCalculator)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	totalTokens := 0
	for _, conv := range conversations {
		totalTokens += conv.Tokens
	}

	// Set reset point with current totals
	reason := "Manual soft reset"
	if err := h.resetTracker.SetResetPoint(totalTokens, len(conversations), reason); err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error":  err.Error(),
			"status": "failed",
		})
	}

	// Broadcast update to WebSocket clients with data
	h.wsHub.BroadcastData("reset_soft", fiber.Map{
		"action":                "soft",
		"message":               "Soft reset applied",
		"previousTokens":        totalTokens,
		"previousConversations": len(conversations),
	})

	return c.JSON(fiber.Map{
		"status":                "reset",
		"message":               "Soft reset applied - counts will now start from zero",
		"previousTokens":        totalTokens,
		"previousConversations": len(conversations),
		"time":                  time.Now(),
	})
}

// HandleClearReset handles POST /api/reset/clear-reset
func (h *AnalyticsHandler) HandleClearReset(c *fiber.Ctx) error {
	if !h.resetTracker.HasResetPoint() {
		return c.JSON(fiber.Map{
			"status":  "no_reset",
			"message": "No active reset to clear",
		})
	}

	if err := h.resetTracker.ClearResetPoint(); err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Broadcast update to WebSocket clients with data
	h.wsHub.BroadcastData("reset_cleared", fiber.Map{
		"action":  "cleared",
		"message": "Reset point cleared - showing original counts",
	})

	return c.JSON(fiber.Map{
		"status":  "cleared",
		"message": "Reset point cleared - showing original counts",
		"time":    time.Now(),
	})
}

// HandleResetStatus handles GET /api/reset/status
func (h *AnalyticsHandler) HandleResetStatus(c *fiber.Ctx) error {
	resetPoint := h.resetTracker.GetResetPoint()

	if resetPoint == nil {
		return c.JSON(fiber.Map{
			"active": false,
		})
	}

	return c.JSON(fiber.Map{
		"active":            true,
		"timestamp":         resetPoint.Timestamp,
		"reason":            resetPoint.Reason,
		"tokenDelta":        resetPoint.TokenDelta,
		"conversationDelta": resetPoint.ConversationDelta,
	})
}

// HandleGetShellHistory handles GET /api/history/shell
func (h *AnalyticsHandler) HandleGetShellHistory(c *fiber.Ctx) error {
	query := &database.CommandHistoryQuery{
		ConversationID: c.Query("conversation_id"),
		Limit:          c.QueryInt("limit", 100),
		Offset:         c.QueryInt("offset", 0),
	}

	commands, err := fetchHistoryPage(h, c, query.Limit, query.Offset, func(limit, offset int) ([]*database.ShellCommand, error) {
		q := *query
		q.Limit, q.Offset = limit, offset
		return h.repo.GetShellCommands(&q)
	}, func(cmd *database.ShellCommand) string { return cmd.ConversationID })
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"commands": commands,
		"count":    len(commands),
		"query":    query,
	})
}

// HandleGetClaudeHistory handles GET /api/history/claude
func (h *AnalyticsHandler) HandleGetClaudeHistory(c *fiber.Ctx) error {
	query := &database.CommandHistoryQuery{
		ConversationID: c.Query("conversation_id"),
		ToolName:       c.Query("tool_name"),
		Limit:          c.QueryInt("limit", 100),
		Offset:         c.QueryInt("offset", 0),
	}

	commands, err := fetchHistoryPage(h, c, query.Limit, query.Offset, func(limit, offset int) ([]*database.ClaudeCommand, error) {
		q := *query
		q.Limit, q.Offset = limit, offset
		return h.repo.GetClaudeCommands(&q)
	}, func(cmd *database.ClaudeCommand) string { return cmd.ConversationID })
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"commands": commands,
		"count":    len(commands),
		"query":    query,
	})
}

// HandleGetCommandStats handles GET /api/history/stats
func (h *AnalyticsHandler) HandleGetCommandStats(c *fiber.Ctx) error {
	commandType := c.Query("type") // 'shell', 'claude', or empty for all
	limit := c.QueryInt("limit", 50)

	stats, err := h.repo.GetCommandStats(commandType, limit)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"stats": stats,
		"count": len(stats),
	})
}

// formatBytes converts bytes to human readable format
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// HandleGetDBStats handles GET /api/db/stats
func (h *AnalyticsHandler) HandleGetDBStats(c *fiber.Ctx) error {
	stats, err := h.db.Stats()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Add human-readable size if db_size_bytes exists
	if sizeBytes, ok := stats["db_size_bytes"].(int64); ok {
		stats["db_size_human"] = formatBytes(sizeBytes)
	}

	return c.JSON(fiber.Map{
		"stats":     stats,
		"db_path":   h.db.Path(),
		"timestamp": time.Now(),
	})
}

// HandleClearAllHistory handles POST /api/history/clear
func (h *AnalyticsHandler) HandleClearAllHistory(c *fiber.Ctx) error {
	// Get database size before clearing
	var sizeBefore int64
	if stats, err := h.db.Stats(); err == nil {
		if size, ok := stats["db_size_bytes"].(int64); ok {
			sizeBefore = size
		}
	}

	err := h.repo.DeleteAllHistory()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error":  err.Error(),
			"status": "failed",
		})
	}

	// Vacuum database to reclaim disk space
	if !h.quiet {
		fmt.Printf("🗑️  Vacuuming database to reclaim disk space (size before: %s)...\n", formatBytes(sizeBefore))
	}
	if err := h.db.Vacuum(); err != nil {
		// Log the error but don't fail the request since data was deleted successfully
		if !h.quiet {
			fmt.Printf("⚠️  Warning: Failed to vacuum database after clearing history: %v\n", err)
		}
	} else {
		// Get database size after vacuum
		var sizeAfter int64
		if stats, err := h.db.Stats(); err == nil {
			if size, ok := stats["db_size_bytes"].(int64); ok {
				sizeAfter = size
			}
		}
		if !h.quiet {
			fmt.Printf("✅ Database vacuum completed successfully (size after: %s, reduced by: %s)\n",
				formatBytes(sizeAfter), formatBytes(sizeBefore-sizeAfter))
		}
	}

	// Broadcast update to WebSocket clients with data
	h.wsHub.BroadcastData("history_cleared", fiber.Map{
		"message": "All history deleted and database vacuumed",
	})

	return c.JSON(fiber.Map{
		"status":  "cleared",
		"message": "All history (prompts, shell commands, Claude commands, notifications, and agent sessions) have been deleted and database vacuumed",
		"time":    time.Now(),
	})
}

// HandleRecordShellCommand handles POST /api/history/shell
func (h *AnalyticsHandler) HandleRecordShellCommand(c *fiber.Ctx) error {
	type RecordShellCommandRequest struct {
		SessionID        string `json:"session_id"`
		SessionName      string `json:"session_name"`
		Command          string `json:"command"`
		Description      string `json:"description"`
		WorkingDirectory string `json:"cwd"`
		GitBranch        string `json:"branch"`
		ModelProvider    string `json:"model_provider"`
		ModelName        string `json:"model_name"`
		ExitCode         *int   `json:"exit_code"`
		Stdout           string `json:"stdout"`
		Stderr           string `json:"stderr"`
		DurationMs       *int   `json:"duration_ms"`
	}

	var req RecordShellCommandRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	// Validate required fields
	if req.SessionID == "" || req.Command == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "session_id and command are required",
		})
	}

	// Use model info from request, fallback to Unknown if not provided
	modelProvider := req.ModelProvider
	modelName := req.ModelName
	if modelProvider == "" {
		modelProvider = "Unknown"
	}
	if modelName == "" {
		modelName = "Unknown"
	}

	// Translate URL-based provider to human-readable name
	if h.modelProviderLookup != nil {
		modelProvider = h.modelProviderLookup.GetProviderNameFromModelInfo(modelProvider, modelName)
	}

	// Create shell command record
	cmd := &database.ShellCommand{
		ConversationID:   req.SessionID,
		SessionName:      req.SessionName,
		Command:          req.Command,
		Description:      req.Description,
		WorkingDirectory: req.WorkingDirectory,
		GitBranch:        req.GitBranch,
		ModelProvider:    modelProvider,
		ModelName:        modelName,
		ExitCode:         req.ExitCode,
		Stdout:           req.Stdout,
		Stderr:           req.Stderr,
		DurationMs:       req.DurationMs,
		ExecutedAt:       time.Now(),
	}

	// Record the command
	if err := h.repo.RecordShellCommand(cmd); err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("failed to record shell command: %v", err),
		})
	}

	// Broadcast update to WebSocket clients with data
	h.wsHub.BroadcastSessionData("command_recorded", cmd.ConversationID, fiber.Map{
		"type": "shell",
		"data": cmd,
	})

	return c.JSON(fiber.Map{
		"status": "recorded",
		"id":     cmd.ID,
		"time":   cmd.ExecutedAt,
	})
}

// HandleRecordClaudeCommand handles POST /api/history/claude
func (h *AnalyticsHandler) HandleRecordClaudeCommand(c *fiber.Ctx) error {
	type RecordClaudeCommandRequest struct {
		SessionID        string `json:"session_id"`
		SessionName      string `json:"session_name"`
		ToolName         string `json:"tool_name"`
		Parameters       string `json:"parameters"`
		Result           string `json:"result"`
		WorkingDirectory string `json:"cwd"`
		GitBranch        string `json:"branch"`
		ModelProvider    string `json:"model_provider"`
		ModelName        string `json:"model_name"`
		Success          bool   `json:"success"`
		ErrorMessage     string `json:"error_message"`
		DurationMs       *int   `json:"duration_ms"`
	}

	var req RecordClaudeCommandRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	// Validate required fields
	if req.SessionID == "" || req.ToolName == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "session_id and tool_name are required",
		})
	}

	// Use model info from request, fallback to Unknown if not provided
	modelProvider := req.ModelProvider
	modelName := req.ModelName
	if modelProvider == "" {
		modelProvider = "Unknown"
	}
	if modelName == "" {
		modelName = "Unknown"
	}

	// Translate URL-based provider to human-readable name
	if h.modelProviderLookup != nil {
		modelProvider = h.modelProviderLookup.GetProviderNameFromModelInfo(modelProvider, modelName)
	}

	// Create Claude command record
	cmd := &database.ClaudeCommand{
		ConversationID:   req.SessionID,
		SessionName:      req.SessionName,
		ToolName:         req.ToolName,
		Parameters:       req.Parameters,
		Result:           req.Result,
		WorkingDirectory: req.WorkingDirectory,
		GitBranch:        req.GitBranch,
		ModelProvider:    modelProvider,
		ModelName:        modelName,
		Success:          req.Success,
		ErrorMessage:     req.ErrorMessage,
		DurationMs:       req.DurationMs,
		ExecutedAt:       time.Now(),
	}

	// Record the command
	if err := h.repo.RecordClaudeCommand(cmd); err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("failed to record claude command: %v", err),
		})
	}

	// Broadcast update to WebSocket clients with data
	h.wsHub.BroadcastSessionData("command_recorded", cmd.ConversationID, fiber.Map{
		"type": "claude",
		"data": cmd,
	})

	return c.JSON(fiber.Map{
		"status": "recorded",
		"id":     cmd.ID,
		"time":   cmd.ExecutedAt,
	})
}

// HandleGetAllHistory handles GET /api/history
func (h *AnalyticsHandler) HandleGetAllHistory(c *fiber.Ctx) error {
	conversationID := c.Query("conversation_id")
	limit := c.QueryInt("limit", 100)
	offset := c.QueryInt("offset", 0)

	query := &database.CommandHistoryQuery{
		ConversationID: conversationID,
		Limit:          limit,
		Offset:         offset,
	}

	// Fetch all four types
	shellCommands, err := fetchHistoryPage(h, c, query.Limit, query.Offset, func(limit, offset int) ([]*database.ShellCommand, error) {
		q := *query
		q.Limit, q.Offset = limit, offset
		return h.repo.GetShellCommands(&q)
	}, func(cmd *database.ShellCommand) string { return cmd.ConversationID })
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("failed to get shell commands: %v", err),
		})
	}

	claudeCommands, err := fetchHistoryPage(h, c, query.Limit, query.Offset, func(limit, offset int) ([]*database.ClaudeCommand, error) {
		q := *query
		q.Limit, q.Offset = limit, offset
		return h.repo.GetClaudeCommands(&q)
	}, func(cmd *database.ClaudeCommand) string { return cmd.ConversationID })
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": fmt.Sprintf("failed to get claude commands: %v", err),
		})
	}

	// Combine into unified response with type field
	type HistoryItem struct {
		Type             string      `json:"type"`
		ID               int64       `json:"id"`
		ConversationID   string      `json:"conversation_id"`
		SessionName      string      `json:"session_name,omitempty"`
		Timestamp        time.Time   `json:"timestamp"`
		WorkingDirectory string      `json:"working_directory,omitempty"`
		GitBranch        string      `json:"git_branch,omitempty"`
		Content          interface{} `json:"content"`
	}

	var allHistory []HistoryItem

	// Add shell commands
	for _, cmd := range shellCommands {
		allHistory = append(allHistory, HistoryItem{
			Type:             "shell",
			ID:               cmd.ID,
			ConversationID:   cmd.ConversationID,
			SessionName:      cmd.SessionName,
			Timestamp:        cmd.ExecutedAt,
			WorkingDirectory: cmd.WorkingDirectory,
			GitBranch:        cmd.GitBranch,
			Content:          cmd,
		})
	}

	// Add claude commands
	for _, cmd := range claudeCommands {
		allHistory = append(allHistory, HistoryItem{
			Type:             "claude",
			ID:               cmd.ID,
			ConversationID:   cmd.ConversationID,
			SessionName:      cmd.SessionName,
			Timestamp:        cmd.ExecutedAt,
			WorkingDirectory: cmd.WorkingDirectory,
			GitBranch:        cmd.GitBranch,
			Content:          cmd,
		})
	}

	// Sort by timestamp descending
	// Simple bubble sort since we're dealing with already sorted slices
	for i := 0; i < len(allHistory)-1; i++ {
		for j := i + 1; j < len(allHistory); j++ {
			if allHistory[i].Timestamp.Before(allHistory[j].Timestamp) {
				allHistory[i], allHistory[j] = allHistory[j], allHistory[i]
			}
		}
	}

	return c.JSON(fiber.Map{
		"history": allHistory,
		"count":   len(allHistory),
		"query":   query,
	})
}

// restrictedStats is /api/stats for a caller who may only see their own
// sessions: the same fields, counted over the agent sessions they may see.
func (h *AnalyticsHandler) restrictedStats(username string) fiber.Map {
	var sessions []agents.Session
	if h.agentHandler != nil {
		if all, err := h.agentHandler.SessionManager.ListAllSessions("all"); err == nil {
			ids := make([]uuid.UUID, len(all))
			for i, s := range all {
				ids[i] = s.ID
			}
			var visible map[uuid.UUID]bool
			if h.visibleSessions != nil {
				visible = h.visibleSessions(username, ids)
			}
			for _, s := range all {
				if visible[s.ID] {
					sessions = append(sessions, s)
				}
			}
		}
	}
	var tokens int64
	var cost float64
	active := 0
	for _, s := range sessions {
		tokens += int64(s.MessageCount * 100)
		cost += s.CostUSD
		if s.Status != agents.SessionStatusEnded {
			active++
		}
	}
	avg := int64(0)
	if len(sessions) > 0 {
		avg = tokens / int64(len(sessions))
	}
	return fiber.Map{
		"totalConversations": len(sessions), "activeConversations": active,
		"totalTokens": tokens, "avgTokens": avg, "timestamp": time.Now(),
		"cliConversations": 0, "cliActive": 0, "cliTokens": 0,
		"agentSessions": len(sessions), "agentActive": active, "agentTokens": tokens, "agentTotalCost": cost,
		"resetActive": false,
	}
}

// fetchHistoryPage returns one page of command history. A caller who sees
// everything gets the repository's page as is. A restricted caller (their own
// sessions only) gets limit visible rows after skipping offset visible ones:
// batches are read and filtered until the page is full or the rows run out,
// so a page never comes back short because other users' rows took its place.
func fetchHistoryPage[T any](h *AnalyticsHandler, c *fiber.Ctx, limit, offset int, fetch func(limit, offset int) ([]T, error), convID func(T) string) ([]T, error) {
	if !principal.Restricted(principal.From(c)) {
		return fetch(limit, offset)
	}
	const batch, maxRounds = 200, 50
	if limit <= 0 {
		limit = 100
	}
	skip, pos := max(offset, 0), 0
	out := []T{}
	for round := 0; round < maxRounds; round++ {
		rows, err := fetch(batch, pos)
		if err != nil {
			return nil, err
		}
		ids := make([]string, len(rows))
		for i, r := range rows {
			ids[i] = convID(r)
		}
		allow := h.historyFilter(c, ids)
		for _, r := range rows {
			if allow != nil && !allow(convID(r)) {
				continue
			}
			if skip > 0 {
				skip--
				continue
			}
			out = append(out, r)
			if len(out) >= limit {
				return out, nil
			}
		}
		pos += len(rows)
		if len(rows) < batch {
			break
		}
	}
	return out, nil
}
