package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/schlunsen/eggify/internal/agentenv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	claude "github.com/schlunsen/claude-agent-sdk-go"
	"github.com/schlunsen/claude-agent-sdk-go/types"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/skills"
)

// Message Handling
// This file contains functions for sending prompts and receiving responses.

// SendPrompt sends a prompt and attributes the message to the authenticated user
// SECURITY: authenticatedUser MUST be set from the Fiber context, never from client input
func (sm *SessionManager) SendPrompt(sessionID uuid.UUID, prompt string, authenticatedUser *string) error {
	// Auto-detect system commands that should not be saved to database
	trimmedPrompt := strings.TrimSpace(prompt)
	if trimmedPrompt == "continue" {
		logging.Info("📝 Auto-detected system command '%s' - using silent mode", trimmedPrompt)
		return sm.sendPromptInternal(sessionID, prompt, false, authenticatedUser)
	}
	return sm.sendPromptInternal(sessionID, prompt, true, authenticatedUser)
}

func (sm *SessionManager) SendPromptSilent(sessionID uuid.UUID, prompt string) error {
	return sm.sendPromptInternal(sessionID, prompt, false, nil)
}

// markPromptStart marks the session processing for a new prompt and clears a
// previous error so the session can recover. UpdatedAt moves to now, except
// for a prompt flagged with keepUpdatedAtOnce (a resume after a restart), so
// the session keeps its place in the sidebar. The caller holds sm.mu.
func markPromptStart(session *AgentSession) {
	session.Status = SessionStatusProcessing
	session.ErrorMessage = nil
	session.promptedThisRun.Store(true)
	if !session.keepUpdatedAtOnce.Swap(false) {
		session.UpdatedAt = time.Now()
	}

	// Reset interruption saved flag when new messages start flowing
	// This allows saving a new interruption message for the next processing cycle
	session.interruptionMu.Lock()
	session.interruptionSaved = false
	session.interruptionMu.Unlock()
}

// sendPromptInternal is the internal implementation of SendPrompt
// authenticatedUser is the username from the request context (required for message attribution)
func (sm *SessionManager) sendPromptInternal(sessionID uuid.UUID, prompt string, saveToDb bool, authenticatedUser *string) error {
	logging.Debug("sendPromptInternal: Getting session %s (saveToDb=%v)", sessionID, saveToDb)
	session, err := sm.GetSession(sessionID)
	if err != nil {
		logging.Error("sendPromptInternal: Failed to get session: %v", err)
		return err
	}

	// Update session status and clear any previous error so the session can recover
	// This fixes the stuck "Invalid API key" state after session interruption
	sm.mu.Lock()
	markPromptStart(session)

	// Only increment message count if we're saving to database
	// This prevents sequence gaps for silent prompts
	var userMsgSequence int
	if saveToDb {
		session.MessageCount++
		userMsgSequence = session.MessageCount
	}
	sm.mu.Unlock()

	// Loop mode: a fresh user-authored prompt (saveToDb) restarts the loop so that
	// the next completed turn begins a new verify-and-retry cycle. Silent retry
	// prompts (saveToDb == false) intentionally do NOT reset, keeping the loop going.
	if saveToDb && sm.isLoopMode(session) {
		sm.resetLoopState(session)
	}

	// Replace the response channel to guarantee any leaked streamFiberResponses goroutines exit.
	// When the SDK finishes without sending a "result" message (crash, timeout, API error),
	// the old streamFiberResponses goroutine is stuck in `for msg := range responseChan`
	// because session.responseChan is never closed (only InterruptSession closes it).
	// Simply draining isn't enough — we must close the old channel so the old goroutine
	// unblocks and exits, then create a fresh channel for the new query.
	//
	// A direct-provider turn that is still running takes this prompt itself
	// (direct_provider_turns.go) and keeps sending on the current channel, so
	// the channel is left alone: swapping it would cut the UI off that turn.
	if !session.directTurnRunning() {
		swapResponseChan(session)

		// Reset activeStreamerCount to 0 so the session reader doesn't
		// incorrectly route messages through the channel before the new streamer starts.
		if old := atomic.SwapInt32(&session.activeStreamerCount, 0); old != 0 {
			logging.Warning("Session %s: Reset stale activeStreamerCount from %d to 0 before new prompt", session.ID, old)
		}
		// Reset missed message counter for the new prompt turn
		atomic.StoreInt32(&session.missedMessageCount, 0)
	}

	// PROACTIVE STATE UPDATE: Broadcast session state change immediately
	// This ensures frontend shows "processing" status as soon as prompt is sent
	sm.broadcastSessionUpdate(session)

	// Save user prompt message to database (only if saveToDb is true)
	// SECURITY: authenticatedUser comes from request context, preventing impersonation
	if saveToDb {
		if err := sm.saveMessageToDB(session.ID, userMsgSequence, "user", prompt, "", nil, authenticatedUser); err != nil {
			logging.Error("Failed to save user message to database (session=%s, seq=%d): %v", session.ID, userMsgSequence, err)
			// Roll back sequence number since message wasn't persisted
			sm.releaseSequence(session, userMsgSequence)
			return fmt.Errorf("failed to save user message to database: %w", err)
		}
	} else {
		logging.Info("📝 Skipping database save AND sequence increment for silent prompt: %s", prompt)
	}

	// Messages from linked sessions (loop reports, the parent's instructions)
	// go in front of the prompt the agent sees; the stored prompt stays as typed.
	prompt = sm.withInbox(session, prompt)

	logging.Debug("SendPrompt: Executing query for session %s", sessionID)

	// Determine permission mode
	permMode := types.PermissionModeDefault
	if session.Options.PermissionMode != nil {
		switch *session.Options.PermissionMode {
		case "allow-all":
			if !autopilotSession(&session.Options) {
				permMode = types.PermissionModeBypassPermissions
			}
		case "read-only":
			permMode = types.PermissionModeDefault
		default:
			permMode = types.PermissionModeDefault
		}
	}

	// Build SDK options
	logging.Debug("SendPrompt: Building SDK options (model: %s, permMode: %v, verbose: %v)", sm.config.Model, permMode, sm.config.Verbose)

	// The same permission callback every Claude CLI start path uses. A second
	// copy here sent AskUserQuestion through the permission queue and denied it
	// after 60 s, so the agent guessed while the question stayed on screen.
	canUseTool := sm.createPermissionCallback(session)

	// Define available tools (Claude Code standard tools)
	allowedTools := []string{
		"Bash", "Read", "Write", "Edit", "Glob", "Grep",
		"WebSearch", "WebFetch",
	}

	// If session options specify tools, use those instead
	if len(session.Options.Tools) > 0 {
		allowedTools = session.Options.Tools
	}

	logging.Info("Allowed tools for session %s: %v", sessionID, allowedTools)
	logging.Info("Permission mode: %v", permMode)

	// Determine model to use: session's stored model > session options model > config default
	modelToUse := sm.config.Model
	if session.ModelName != "" {
		modelToUse = session.ModelName
		logging.Info("DEBUG: Using session's stored model: %s", modelToUse)
	} else if session.Options.Model != nil && *session.Options.Model != "" {
		modelToUse = *session.Options.Model
		logging.Info("DEBUG: Using session options model: %s", modelToUse)
	} else {
		logging.Info("DEBUG: Using session manager default model: %s", modelToUse)
	}

	// === PROVIDER ROUTING ===
	// Route non-Claude models through the direct OpenAI-compatible path ONLY when their
	// provider doesn't have an Anthropic-compatible URL. Providers like DeepSeek, Kimi,
	// and GLM have /anthropic endpoints that work with the Claude SDK — keep those on
	// the SDK path. Only providers with OpenAI-compatible URLs (e.g. litellm, Ollama)
	// need the direct provider path.
	// Codex sessions run on the Codex CLI (codex-sdk-go) and never touch the
	// Claude SDK or the direct OpenAI-compatible loop. Checked first because
	// "gpt-5-codex" would otherwise be inferred as the "openai" direct provider.
	if isCodexSession(session) {
		logging.Info("🟢 Routing to Codex provider path for model: %s", modelToUse)
		return sm.sendPromptCodex(session, sessionID, prompt, userMsgSequence)
	}

	isClaude := isClaudeModel(modelToUse)
	shouldDirect := sm.shouldUseDirectProvider(session)
	logging.Info("🔀 ROUTING CHECK: model=%s, isClaude=%v, shouldDirect=%v, provider=%v",
		modelToUse, isClaude, shouldDirect, session.Options.Provider)
	if !isClaude && shouldDirect {
		if isSandboxed(&session.Options) {
			return sm.failPrompt(session, ErrSandboxUnsupported)
		}
		logging.Info("🔀 Routing to direct provider path for non-Claude model: %s", modelToUse)
		beforeSequence := 0
		if saveToDb {
			beforeSequence = userMsgSequence
		}
		return sm.sendPromptDirect(session, sessionID, prompt, beforeSequence)
	}

	logging.Info("🔵 Routing to Claude SDK path for model: %s (isClaude=%v, shouldDirect=%v)", modelToUse, isClaude, shouldDirect)

	// Drop over-broad entries older versions wrote to settings.local.json
	// (Eggify's own edit: a trusted folder stays trusted).
	sm.pruneLegacySettingsPermissions(session)

	// Workspace trust decides whether the folder's own Claude config (hooks,
	// allow rules, env, .mcp.json) is loaded, and recycles the cached CLI when
	// that decision changed since it was started (see workspace_trust.go).
	trust := sm.prepareWorkspaceTrust(session)
	// Same for the approved skills: a changed set restarts the CLI between turns.
	sm.prepareSessionSkills(session)

	opts := types.NewClaudeAgentOptions().
		WithModel(modelToUse).
		WithPermissionMode(permMode).
		WithVerbose(sm.config.Verbose).
		WithCanUseTool(canUseTool)
		// Don't set AllowedTools - let permission mode control tool access
		// WithAllowedTools(allowedTools...)
	// Project instructions (CLAUDE.md), slash commands and settings load only
	// for trusted folders or folders without trust-relevant config.
	opts = applyWorkspaceTrust(opts, trust)
	// Approved skills as a plugin, also in untrusted folders and sandboxes.
	opts, skillsKey := sm.applySessionSkills(opts, &session.Options)

	// Enable extended thinking mode (10,000 token budget)
	// This allows Claude to show its reasoning process in ThinkingBlocks
	// Using the proper SDK method instead of ExtraArgs (fixed in v0.2.7+)
	opts = opts.WithMaxThinkingTokens(10000)

	logging.Info("Extended thinking enabled with 10k token budget for session %s", sessionID)

	// Skills are native (native_skills.go): the approved ones that apply come
	// in as a plugin (--plugin-dir, below) and a /name prompt goes to the CLI
	// as typed. An unknown /name triggers discovery (pending only), and a
	// skill waiting for review gets a notice instead of silently nothing.
	sm.noticeSlashSkill(session, prompt)

	// System prompt additions: the session's own prompt (project/area
	// context), memories and the n0 hint, appended to Claude Code's prompt.
	systemPrompt := customSystemPrompt(&session.Options)

	// Inject relevant memories from the project's Memory Palace
	// InjectMemories defaults to true (nil = true), only skip if explicitly false
	injectMemories := session.Options.InjectMemories == nil || *session.Options.InjectMemories
	if sm.memoryInjector != nil && injectMemories && session.Options.ProjectID != nil && *session.Options.ProjectID != "" {
		var areaID *string
		if session.Options.ProjectAreaID != nil && *session.Options.ProjectAreaID != "" {
			areaID = session.Options.ProjectAreaID
		}
		injectedPrompt, err := sm.memoryInjector.InjectMemoriesIntoPrompt(
			*session.Options.ProjectID, areaID, systemPrompt,
		)
		if err != nil {
			logging.Warning("Failed to inject memories into session %s: %v", sessionID, err)
		} else if injectedPrompt != systemPrompt {
			systemPrompt = injectedPrompt
			logging.Info("Injected Memory Palace context into session %s", sessionID)
		}
	}

	logging.Debug("SendPrompt: system prompt additions: %d chars", len(systemPrompt))
	opts = opts.WithSystemPrompt(claudeSystemPrompt(sm.withN0Hint(sessionID, systemPrompt)))

	// Add YOLO Mode flags if enabled (takes precedence over permission mode)
	logging.Debug("SendPrompt: Checking YOLO mode flags for session %s", sessionID)
	logging.Debug("  DangerouslySkipPermissions: %v", session.Options.DangerouslySkipPermissions)
	logging.Debug("  AllowDangerouslySkipPermissions: %v", session.Options.AllowDangerouslySkipPermissions)

	if autopilotSession(&session.Options) {
		logging.Info("Autopilot: sandboxed with prompts off - no skip flags, the callback decides")
	} else if session.Options.DangerouslySkipPermissions != nil && *session.Options.DangerouslySkipPermissions {
		logging.Info("🚨 YOLO Mode enabled - bypassing all permissions")
		opts = opts.WithAllowDangerouslySkipPermissions(true).
			WithDangerouslySkipPermissions(true)
	} else if session.Options.AllowDangerouslySkipPermissions != nil && *session.Options.AllowDangerouslySkipPermissions {
		logging.Info("🛡️ YOLO Mode safety switch enabled (but not active)")
		opts = opts.WithAllowDangerouslySkipPermissions(true)
	} else {
		logging.Debug("  YOLO Mode NOT enabled - using default permission handling")
	}

	// Model credentials: session key > providers table > server config, base
	// URL: providers table > session > known defaults. With the provider proxy
	// running the CLI only gets a session capability, never the key.
	opts = sm.applyClaudeProviderAuth(sessionID, &session.Session, opts)

	// Set working directory if provided
	if session.Options.WorkingDirectory != nil && *session.Options.WorkingDirectory != "" {
		logging.Debug("SendPrompt: Setting working directory: %s", *session.Options.WorkingDirectory)
		opts = opts.WithCWD(*session.Options.WorkingDirectory)
		// eggify-tools reaches the CLI through --mcp-config (below); Eggify no longer
		// writes a .mcp.json into the user's folder, which would be repo config
		// the workspace trust check has to ask about.
	}

	// Resume existing conversation if Claude session ID exists
	if session.ClaudeSessionID != "" {
		logging.Debug("SendPrompt: Resuming conversation from Claude session: %s", session.ClaudeSessionID)
		opts = opts.WithResume(session.ClaudeSessionID)
	}

	// Inject connector credentials as environment variables (hot-pluggable)
	sm.injectConnectorCredentials(sessionID, opts)

	// Configure eggify MCP server (stdio) for session handover, skills, GPU tools, and app deployment.
	// The SDK serializes this to --mcp-config JSON for the Claude CLI.
	// Must use full .mcp.json format with "mcpServers" wrapper key.
	// Always injected (not gated on EGGIFY_SUBDOMAIN) so MCP tools work in all environments.
	// Uses the current executable path so it works in both sandbox and local environments.
	opts = opts.WithMcpServers(sm.eggifyToolsMCPConfig(sessionID))

	// Register RTK PreToolUse hook if enabled — must happen BEFORE NewClient(),
	// as hooks are wired via the initialize control-protocol message at
	// subprocess startup and cannot be added later. See applyRTKHook() in
	// manager.go for the long-form comment on why this lives in three places.
	opts = applyRTKHook(opts, session.Options.EnableRTK)

	// Deny rules for Eggify's secrets, plus the Bash sandbox for sandboxed sessions.
	opts, settingsErr := sm.applyClaudeSessionSettingsFor(session.ID, func(t string) { sm.emitSessionNotice(session, t) }, opts, &session.Options)
	if settingsErr != nil {
		return sm.failPrompt(session, settingsErr)
	}

	// Reuse existing client if available (preserves conversation context)
	// Otherwise create a new client
	client := sm.reusableClaudeClient(session)
	session.mu.Lock()
	hasClaudeSession := session.ClaudeSessionID != ""
	session.mu.Unlock()

	// A fresh CLI conversation (no client, nothing to --resume) after a model
	// switch must be seeded with the stored transcript.
	replayHistory := client == nil && !hasClaudeSession && sm.historyReplayPending(session)

	if client == nil {
		// Execute query using streaming mode (required for permission callbacks via control protocol)
		if hasClaudeSession {
			logging.Info("SendPrompt: Creating new client for restored session %s (Claude session: %s)", sessionID, session.ClaudeSessionID)
		} else {
			logging.Debug("SendPrompt: Creating streaming client...")
		}
		logging.Debug("SendPrompt: API Key length: %d", len(sm.config.APIKey))
		logging.Debug("Creating streaming client for session %s with options: model=%s, permMode=%v",
			sessionID, sm.config.Model, permMode)

		newClient, err := claude.NewClient(session.ctx, agentenv.ScrubClaudeOptions(opts))
		if err != nil {
			logging.Error("SendPrompt: Failed to create client: %v", err)
			sm.mu.Lock()
			errMsg := err.Error()
			session.ErrorMessage = &errMsg
			session.Status = SessionStatusError
			sm.mu.Unlock()
			return fmt.Errorf("failed to create client: %w", err)
		}

		// Connect to Claude
		if err := newClient.Connect(session.ctx); err != nil {
			logging.Error("SendPrompt: Failed to connect client: %v", err)
			sm.mu.Lock()
			errMsg := err.Error()
			session.ErrorMessage = &errMsg
			session.Status = SessionStatusError
			sm.mu.Unlock()
			return fmt.Errorf("failed to connect client: %w", err)
		}

		// Store client reference
		session.mu.Lock()
		session.client = newClient
		session.trustKey = trust.Key()
		session.skillsKey = skillsKey
		session.mu.Unlock()
		client = newClient
	} else {
		logging.Info("SendPrompt: Reusing existing client for session %s (preserves conversation context)", sessionID)
	}

	// After a switch from a provider that held the history natively, this is a
	// brand-new CLI conversation: seed it with the transcript stored in eggify's DB.
	var replayContext string
	if replayHistory {
		replayContext = sm.historyReplayPrefix(session, userMsgSequence)
		prompt = replayContext + prompt
	}

	// Send the query
	if err := client.Query(session.ctx, prompt); err != nil {
		logging.Error("SendPrompt: Failed to send query: %v", err)
		sm.mu.Lock()
		errMsg := err.Error()
		session.ErrorMessage = &errMsg
		session.Status = SessionStatusError
		sm.mu.Unlock()
		return fmt.Errorf("failed to send query: %w", err)
	}
	session.claudeTurnOpen.Store(true)

	sm.captureModelSwitchContext(sessionID, userMsgSequence, replayContext, "text")

	// One reader drains this client for as long as it lives; it is started
	// here only if it is not already running (see ensureSessionReader).
	sm.ensureSessionReader(session, client)
	logging.Info("SendPrompt: query sent for session %s", sessionID)

	logging.Debug("SendPrompt: Completed successfully for session %s", sessionID)
	return nil
}

// SendPromptWithContent sends structured content and attributes the message to the authenticated user
// SECURITY: authenticatedUser MUST be set from the Fiber context, never from client input
func (sm *SessionManager) SendPromptWithContent(sessionID uuid.UUID, content []ContentBlock, authenticatedUser *string) error {
	logging.Debug("SendPromptWithContent: Getting session %s", sessionID)
	session, err := sm.GetSession(sessionID)
	if err != nil {
		logging.Error("SendPromptWithContent: Failed to get session: %v", err)
		return err
	}

	// Update session status and clear any previous error so the session can recover
	sm.mu.Lock()
	session.Status = SessionStatusProcessing
	session.ErrorMessage = nil
	session.promptedThisRun.Store(true)
	session.UpdatedAt = time.Now()

	// Reset interruption saved flag when new messages start flowing
	// This allows saving a new interruption message for the next processing cycle
	session.interruptionMu.Lock()
	session.interruptionSaved = false
	session.interruptionMu.Unlock()

	session.MessageCount++
	userMsgSequence := session.MessageCount
	sm.mu.Unlock()

	// Replace the response channel to guarantee any leaked streamFiberResponses goroutines exit.
	// (Same fix as sendPromptInternal — see comment there for full rationale.)
	swapResponseChan(session)
	if old := atomic.SwapInt32(&session.activeStreamerCount, 0); old != 0 {
		logging.Warning("Session %s: Reset stale activeStreamerCount from %d to 0 before new prompt (WithContent)", session.ID, old)
	}
	atomic.StoreInt32(&session.missedMessageCount, 0)

	// Convert content blocks to JSON string for database storage
	contentJSON, err := json.Marshal(content)
	if err != nil {
		logging.Error("Failed to marshal content for database: %v", err)
		contentJSON = []byte("[]")
	}

	// Save user prompt message to database (with structured content)
	// SECURITY: authenticatedUser comes from request context, preventing impersonation
	if err := sm.saveMessageToDB(session.ID, userMsgSequence, "user", string(contentJSON), "", nil, authenticatedUser); err != nil {
		logging.Error("Failed to save user message to database (session=%s, seq=%d): %v", session.ID, userMsgSequence, err)
		// Roll back sequence number since message wasn't persisted
		sm.releaseSequence(session, userMsgSequence)
		return fmt.Errorf("failed to save user message to database: %w", err)
	}

	// Codex sessions: images are handed to the CLI as local files.
	if isCodexSession(session) {
		inputs, cleanup, err := codexInputsFromContent(content)
		if err != nil {
			return err
		}
		logging.Info("🟢 Routing structured content to Codex provider path for session %s", sessionID)
		return sm.sendPromptCodexInputs(session, sessionID, inputs, cleanup, userMsgSequence)
	}

	logging.Debug("SendPromptWithContent: Building stream-json message for session %s", sessionID)

	// Workspace trust first: it may close a CLI started under another decision.
	trust := sm.prepareWorkspaceTrust(session)
	sm.prepareSessionSkills(session)

	// Get or create client (same as SendPrompt method)
	client := sm.reusableClaudeClient(session)

	replayHistory := client == nil && session.ClaudeSessionID == "" && sm.historyReplayPending(session)

	// If no client exists, we need to create one first
	if client == nil {
		// Create client using the same logic as SendPrompt
		// For brevity, we'll call SendPrompt with an empty string to initialize the client
		// then send our structured content
		logging.Info("SendPromptWithContent: No client exists, initializing session first")

		// We need to create the client but not send a query yet
		// Let's replicate the client creation logic here
		permMode := types.PermissionModeDefault
		if session.Options.PermissionMode != nil {
			switch *session.Options.PermissionMode {
			case "allow-all":
				if !autopilotSession(&session.Options) {
					permMode = types.PermissionModeBypassPermissions
				}
			case "read-only":
				permMode = types.PermissionModeDefault
			default:
				permMode = types.PermissionModeDefault
			}
		}

		// Create permission callback (same as SendPrompt)
		canUseTool := sm.createPermissionCallback(session)

		// Determine model to use: session's stored model > session options model > config default
		modelToUse := sm.config.Model
		if session.ModelName != "" {
			modelToUse = session.ModelName
		} else if session.Options.Model != nil && *session.Options.Model != "" {
			modelToUse = *session.Options.Model
		}

		opts := types.NewClaudeAgentOptions().
			WithModel(modelToUse).
			WithPermissionMode(permMode).
			WithVerbose(sm.config.Verbose).
			WithCanUseTool(canUseTool)
		// CLAUDE.md, slash commands and the folder's settings only when trusted.
		opts = applyWorkspaceTrust(opts, trust)
		recordClientTrust(session, trust)
		var skillsKey string
		opts, skillsKey = sm.applySessionSkills(opts, &session.Options)
		recordClientSkills(session, skillsKey)

		// Claude Code's own system prompt plus the session's additions.
		opts = opts.WithSystemPrompt(claudeSystemPrompt(sm.withN0Hint(sessionID, customSystemPrompt(&session.Options))))

		// Add YOLO Mode flags if enabled (takes precedence over permission mode)
		if autopilotSession(&session.Options) {
			logging.Info("Autopilot: sandboxed with prompts off - no skip flags, the callback decides")
		} else if session.Options.DangerouslySkipPermissions != nil && *session.Options.DangerouslySkipPermissions {
			logging.Info("🚨 YOLO Mode enabled - bypassing all permissions")
			opts = opts.WithAllowDangerouslySkipPermissions(true).
				WithDangerouslySkipPermissions(true)
		} else if session.Options.AllowDangerouslySkipPermissions != nil && *session.Options.AllowDangerouslySkipPermissions {
			logging.Info("🛡️ YOLO Mode safety switch enabled (but not active)")
			opts = opts.WithAllowDangerouslySkipPermissions(true)
		}

		// Same credential resolution as sendPromptInternal (provider proxy aware).
		opts = sm.applyClaudeProviderAuth(sessionID, &session.Session, opts)

		if session.Options.WorkingDirectory != nil && *session.Options.WorkingDirectory != "" {
			opts = opts.WithCWD(*session.Options.WorkingDirectory)
		}

		if session.ClaudeSessionID != "" {
			opts = opts.WithResume(session.ClaudeSessionID)
		}

		// Inject connector credentials as environment variables (hot-pluggable)
		sm.injectConnectorCredentials(sessionID, opts)

		// Configure eggify MCP server (same as sendPromptInternal)
		opts = opts.WithMcpServers(sm.eggifyToolsMCPConfig(sessionID))

		// Register RTK PreToolUse hook if enabled — must happen BEFORE NewClient(),
		// as hooks are wired via the initialize control-protocol message at
		// subprocess startup and cannot be added later. See applyRTKHook() in
		// manager.go. This is the multi-modal (image/attachment) prompt path;
		// without this call RTK silently no-ops for any Claude session that
		// sends content blocks instead of a plain string.
		opts = applyRTKHook(opts, session.Options.EnableRTK)

		// Deny rules for Eggify's secrets, plus the Bash sandbox for sandboxed sessions.
		opts, settingsErr := sm.applyClaudeSessionSettingsFor(session.ID, func(t string) { sm.emitSessionNotice(session, t) }, opts, &session.Options)
		if settingsErr != nil {
			return sm.failPrompt(session, settingsErr)
		}

		// Create new client
		newClient, err := claude.NewClient(session.ctx, agentenv.ScrubClaudeOptions(opts))
		if err != nil {
			logging.Error("SendPromptWithContent: Failed to create client: %v", err)
			sm.mu.Lock()
			errMsg := err.Error()
			session.ErrorMessage = &errMsg
			session.Status = SessionStatusError
			sm.mu.Unlock()
			return fmt.Errorf("failed to create client: %w", err)
		}

		if err := newClient.Connect(session.ctx); err != nil {
			logging.Error("SendPromptWithContent: Failed to connect client: %v", err)
			sm.mu.Lock()
			errMsg := err.Error()
			session.ErrorMessage = &errMsg
			session.Status = SessionStatusError
			sm.mu.Unlock()
			return fmt.Errorf("failed to connect client: %w", err)
		}

		session.mu.Lock()
		session.client = newClient
		session.mu.Unlock()
		client = newClient
	}

	// After a model switch onto a fresh CLI conversation, seed it with the
	// stored transcript (same as sendPromptInternal).
	var replayContext string
	if replayHistory {
		replayContext = sm.historyReplayPrefix(session, userMsgSequence)
		if replayContext != "" {
			content = append([]ContentBlock{{Type: "text", Text: replayContext}}, content...)
		}
	}

	// Convert ContentBlock array to interface{} for SDK
	// The SDK's QueryWithContent accepts interface{} which can be a content array
	contentInterface := make([]interface{}, len(content))
	for i, block := range content {
		blockMap := make(map[string]interface{})
		blockMap["type"] = block.Type

		if block.Type == "text" {
			blockMap["text"] = block.Text
		} else if block.Type == "image" && block.Source != nil {
			blockMap["source"] = map[string]interface{}{
				"type":       block.Source.Type,
				"media_type": block.Source.MediaType,
				"data":       block.Source.Data,
			}
		}

		contentInterface[i] = blockMap
	}

	logging.Info("SendPromptWithContent: Sending %d content blocks to Claude CLI", len(content))

	// Use the new QueryWithContent method to send structured content
	if err := client.QueryWithContent(session.ctx, contentInterface); err != nil {
		logging.Error("SendPromptWithContent: Failed to send query: %v", err)
		sm.mu.Lock()
		errMsg := err.Error()
		session.ErrorMessage = &errMsg
		session.Status = SessionStatusError
		sm.mu.Unlock()
		return fmt.Errorf("failed to send query: %w", err)
	}
	session.claudeTurnOpen.Store(true)
	sm.captureModelSwitchContext(sessionID, userMsgSequence, replayContext, "text")

	sm.ensureSessionReader(session, client)
	logging.Info("SendPromptWithContent: query sent for session %s", sessionID)

	logging.Debug("SendPromptWithContent: Completed successfully for session %s", sessionID)
	return nil
}

// readerSilenceWarning is how long a turn may go quiet, after it has started
// producing output, before the session reader logs a warning. It only warns.
// Quiet stretches are normal (extended thinking, a long tool run, a background
// wait), and the reader used to give up after five minutes of one, marking the
// session idle while the CLI was still working and leaving the rest of the
// turn unread until the next prompt. A CLI that exits is noticed because the
// SDK closes its message stream, not by waiting out a timeout.
var readerSilenceWarning = 5 * time.Minute

// responseSource yields the messages of one turn. In production it is the
// client's ReceiveResponse, which ends after the turn's ResultMessage, or when
// the client closes or its CLI exits. Tests substitute their own.
type responseSource func(ctx context.Context) <-chan types.Message

// ensureSessionReader makes sure exactly one reader drains client's messages.
//
// eggify used to start a reader for every prompt, and it stopped at that prompt's
// ResultMessage. The CLI does not stop there: when a background task finishes
// it injects a task notification and carries on with a new turn. Nothing read
// that output. The session showed idle, the messages waited in the SDK's
// channel, and the next prompt's reader delivered them all at once (up to 45
// assistant messages within three seconds of a prompt).
//
// So the reader now lives as long as the client. A prompt on a client that
// already has a reader only sends the query, since two readers on one client
// would split its messages between them. A new client (after a model switch,
// a reload or an interrupt) gets a fresh reader, and the old one is cancelled
// and exits without touching the session's status.
func (sm *SessionManager) ensureSessionReader(session *AgentSession, client *claude.Client) {
	sm.startSessionReader(session, client, client.ReceiveResponse)
}

// startSessionReader is ensureSessionReader with an injectable source.
func (sm *SessionManager) startSessionReader(session *AgentSession, client *claude.Client, source responseSource) {
	session.readerMu.Lock()
	defer session.readerMu.Unlock()

	if session.readerClient == client && session.readerCancel != nil {
		return // already draining this client
	}
	if session.readerCancel != nil {
		session.readerCancel() // the previous client's reader
	}
	ctx, cancel := context.WithCancel(session.ctx)
	session.readerClient = client
	session.readerCancel = cancel
	go sm.runSessionReader(ctx, cancel, session, client, source)
}

// runSessionReader drains one client turn after turn until the client closes,
// its CLI exits, or the reader is replaced.
func (sm *SessionManager) runSessionReader(ctx context.Context, cancel context.CancelFunc, session *AgentSession, client *claude.Client, source responseSource) {
	turnOpen := false
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			logging.Error("Session %s: PANIC in session reader: %v", session.ID, r)
		}

		session.readerMu.Lock()
		current := session.readerClient == client
		if current {
			session.readerClient, session.readerCancel = nil, nil
		}
		session.readerMu.Unlock()

		// A replaced reader leaves the session alone: its successor may already
		// be mid-turn. The current reader ending with a turn still open (the CLI
		// exited, the client closed, the session was interrupted) finishes that
		// turn so the session does not sit on "processing".
		sm.mu.Lock()
		processing := session.Status == SessionStatusProcessing
		if current {
			// The client is gone, so results for its pending tool calls
			// will never arrive.
			session.clearWorkspaceTools()
			session.claudeTurnOpen.Store(false)
		}
		sm.mu.Unlock()
		if current && (turnOpen || processing) {
			sm.completeTurn(session)
		}
		logging.Debug("Session %s: session reader stopped", session.ID)
	}()

	logging.Debug("Session %s: session reader started", session.ID)
	for {
		sawResult, sawMessages := sm.readTurn(ctx, session, source(ctx))
		if sawMessages {
			turnOpen = true
		}
		if !sawResult {
			return
		}
		sm.completeTurn(session)
		turnOpen = false
	}
}

// providerRetryNotice describes a failing provider in one line.
//
// The CLI reports each retry as a system message with subtype "api_retry",
// carrying the HTTP status and attempt count. Those details are top-level
// fields the SDK does not keep yet (claude-agent-sdk-go), so the text degrades
// gracefully when they are missing and fills in as soon as they arrive.
func providerRetryNotice(session *AgentSession, data map[string]interface{}) string {
	num := func(k string) int {
		if v, ok := data[k].(float64); ok {
			return int(v)
		}
		return 0
	}
	str := func(k string) string {
		if v, ok := data[k].(string); ok {
			return v
		}
		return ""
	}

	provider := session.Provider
	if provider == "" {
		provider = "The provider"
	}
	notice := fmt.Sprintf("⏳ %s is not answering", provider)
	if status := num("error_status"); status > 0 {
		notice += fmt.Sprintf(" (HTTP %d", status)
		if kind := str("error"); kind != "" {
			notice += ": " + kind
		}
		notice += ")"
	}
	if max := num("max_retries"); max > 0 {
		notice += fmt.Sprintf(" — retrying, attempt %d of %d", num("attempt"), max)
	} else {
		notice += " — the CLI is retrying with backoff"
	}
	return notice + ". Check the provider's status, plan or rate limits if this keeps up."
}

// emitSessionNotice records a server-generated note in the conversation and
// pushes it to connected clients, the way the interrupt and model-switch notes
// are recorded.
func (sm *SessionManager) emitSessionNotice(session *AgentSession, text string) {
	sm.mu.Lock()
	session.MessageCount++
	seq := session.MessageCount
	sm.mu.Unlock()

	if err := sm.saveMessageToDB(session.ID, seq, "system", text, "", nil, nil); err != nil {
		logging.Warning("Session %s: failed to save notice: %v", session.ID, err)
		sm.releaseSequence(session, seq)
		return
	}

	logging.Info("Session %s: %s", session.ID, text)
	sm.invokeBroadcastMessageCallback(session.ID, &types.SystemMessage{
		Type:    "system",
		Subtype: "notice",
		Data:    map[string]interface{}{"text": text},
	})
}

// completeTurn ends a turn: the session goes idle, and the work that used to
// run when the per-prompt reader exited runs here instead (persisting and
// broadcasting the idle state, recovering messages that fell back to direct
// broadcast, and the loop-mode / auto-handoff checks).
func (sm *SessionManager) completeTurn(session *AgentSession) {
	session.claudeTurnOpen.Store(false)
	if _, changed, err := sm.RefreshGitBranch(session.ID); err == nil && changed {
		logging.Debug("Session %s: Git branch updated after conversation turn", session.ID)
	}

	sm.mu.Lock()
	session.Status = SessionStatusIdle
	session.UpdatedAt = time.Now()
	sm.mu.Unlock()
	// Persist the idle state, or the database keeps showing "processing".
	sm.persistSessionState(session)

	sm.broadcastSessionUpdate(session)

	// Recover only if messages were actually dropped (they fell back to direct
	// broadcast when the response channel timed out). Unconditional recovery
	// used to re-broadcast every message after every turn.
	if atomic.LoadInt32(&session.missedMessageCount) > 0 {
		missed := atomic.SwapInt32(&session.missedMessageCount, 0)
		logging.Info("Session %s: %d messages used fallback broadcast during streaming, scheduling recovery", session.ID, missed)
		// Single delayed recovery - gives frontend time to reconnect after WebSocket drop
		go func() {
			time.Sleep(5 * time.Second)
			if err := sm.resendAllMessagesFromDB(session.ID); err != nil {
				logging.Debug("Session %s: Delayed recovery failed (expected if no connections): %v", session.ID, err)
			} else {
				logging.Debug("Session %s: Delayed recovery completed", session.ID)
			}
			sm.broadcastSessionUpdate(session)
		}()
	}

	// A turn refused by the usage limit waits for the reset: no loop verify
	// (the loop goes on with the same iteration), no auto-handoff, and no
	// inbox wake-up into the same limit (the inbox goes with the resume).
	if sm.afterTurnUsageLimit(session) {
		return
	}

	// Loop mode takes precedence over auto-handoff: a looping session runs its
	// verification and decides whether to re-prompt or stop.
	if sm.isLoopMode(session) {
		sm.loopAfterTurn(session, nil)
	} else {
		sm.checkAutoHandoff(session)
	}
	// A worker's reply goes to the session that started it.
	sm.reportWorkerTurnToParent(session)
	// Linked-session messages that arrived during the turn.
	sm.drainInbox(session)
	// A waiting session that was busy is due again once its turn ended.
	if sm.LimitWaitFor(session.ID) != nil {
		sm.scheduleLimitResumes()
	}
}

// readTurn consumes one turn's stream. It reports whether the stream ended with
// a ResultMessage (the turn is over and the client can take more) and whether
// it carried any messages. A stream that closes without a result means the
// client closed, its CLI exited, or ctx was cancelled.
func (sm *SessionManager) readTurn(ctx context.Context, session *AgentSession, messages <-chan types.Message) (sawResult, sawMessages bool) {
	messageCount := 0
	conversationCount := 0 // messages past the filters below: the turn's own
	retryNoticed := false
	quiet := time.NewTimer(readerSilenceWarning)
	defer quiet.Stop()

	for {
		select {
		case msg, ok := <-messages:
			if !ok {
				if messageCount > 0 {
					logging.Info("Session %s: Messages channel closed after %d messages", session.ID, messageCount)
				}
				return sawResult, messageCount > 0
			}

			messageCount++
			messageType := msg.GetMessageType()

			// FIXUP: The SDK sometimes sends messages with empty Type field.
			// Normalize using Go type assertion so sequence numbers, persistence, and
			// WebSocket routing all work correctly.
			if messageType == "" {
				switch msg.(type) {
				case *types.AssistantMessage:
					messageType = "assistant"
				case *types.UserMessage:
					messageType = "user"
				case *types.ResultMessage:
					messageType = "result"
				case *types.SystemMessage:
					messageType = "system"
				}
				if messageType != "" {
					logging.Debug("Session %s: Normalized empty message type to %q (Go type: %T)", session.ID, messageType, msg)
				}
			}

			logging.Debug("Session %s: Received message #%d, type: %s", session.ID, messageCount, messageType)

			// Usage-limit reports are account state, not conversation: recorded
			// and taken out of the stream, so they never wake an idle session.
			if info, ok := parseRateLimitEvent(msg, sm.limitNow()); ok {
				sm.observeUsageLimit(info)
				quiet.Reset(readerSilenceWarning)
				continue
			}

			// Subagent messages and task_* events are routed to the subagent
			// tracker. They are not the parent's conversation: never persisted as
			// its messages, broadcast into its chat, or allowed to wake it up.
			if sm.routeSubagentTraffic(session, msg) {
				quiet.Reset(readerSilenceWarning)
				continue
			}
			sm.observeCronJobs(session, msg, session.loopTurnStartedAt.Load())
			sm.observeScheduledLoop(session, msg)
			sm.noteTurnMessage(session, msg)

			// A provider that is failing must not look like a long turn. The CLI
			// retries HTTP errors up to ten times with growing backoff and says
			// nothing the frontend renders, so an outage showed up as a spinner
			// and nothing else. One notice per turn is enough to explain it.
			if sysMsg, ok := msg.(*types.SystemMessage); ok && sysMsg.Subtype == "api_retry" && !retryNoticed {
				retryNoticed = true
				sm.emitSessionNotice(session, providerRetryNotice(session, sysMsg.Data))
			}

			// System frames are host status, not conversation (see
			// system_messages.go): observed, shown as a notice when they carry
			// something for a person, and otherwise kept out of the chat. They
			// never wake an idle session (post_turn_summary and
			// session_state_changed arrive after the result).
			if sysMsg, ok := msg.(*types.SystemMessage); ok && messageType == "system" {
				sm.observeWorkspaceMessage(session, msg)
				if text, show := chatSystemText(sysMsg); show {
					sm.emitSessionNotice(session, text)
				}
				quiet.Reset(readerSilenceWarning)
				continue
			}
			conversationCount++

			// A message arriving while the session is idle means the CLI resumed on
			// its own, for example after a background task finished: show it as
			// processing again. The first message of a turn is broadcast either
			// way, confirming to the frontend that processing has started.
			sm.mu.Lock()
			resumed := session.Status != SessionStatusProcessing
			if resumed {
				session.Status = SessionStatusProcessing
				session.UpdatedAt = time.Now()
			}
			sm.mu.Unlock()
			if resumed {
				sm.persistSessionState(session)
				logging.Info("Session %s: CLI produced output while idle, back to processing", session.ID)
			}
			if resumed || conversationCount == 1 {
				sm.broadcastSessionUpdate(session)
			}

			branchMayHaveMoved := sm.observeWorkspaceMessage(session, msg)

			// Refresh the git branch before forwarding the message so it carries
			// the current branch. Each refresh forks git, so it runs only when the
			// branch can actually have changed: at the start of a turn, after a
			// shell-like tool finished, and otherwise at most every
			// gitBranchRefreshInterval as a safety net.
			if conversationCount == 1 || branchMayHaveMoved || gitRefreshDue(session) {
				if _, _, err := sm.RefreshGitBranch(session.ID); err != nil {
					logging.Debug("Session %s: Failed to refresh git branch: %v", session.ID, err)
				}
			}

			// Only allocate a sequence number for messages that will actually be persisted
			// This prevents sequence gaps from non-persisted message types (e.g. "result")
			var sequenceNum int
			if messageType == "assistant" {
				sm.mu.Lock()
				session.MessageCount++
				sequenceNum = session.MessageCount
				sm.mu.Unlock()
			}

			// Save message to database based on type with proper sequence number
			saved, saveErr := sm.persistSDKMessage(session.ID, sequenceNum, msg)
			if saveErr != nil {
				logging.Error("Session %s: FAILED to persist message #%d (type=%s) to database: %v", session.ID, messageCount, messageType, saveErr)
			}
			// If we allocated a sequence number but the message wasn't actually saved (e.g. empty assistant),
			// roll back the sequence to avoid gaps
			if messageType == "assistant" && !saved {
				sm.releaseSequence(session, sequenceNum)
			}

			// A worker's steps are also shown in the session that started it.
			sm.observeWorkerMessage(session, msg)

			// Check if there's an active streamer consuming the response channel.
			// If none is (SendPromptSilent without a frontend handler, or output
			// after the prompt's streamer already exited at its result), broadcast
			// directly to WebSocket clients instead of writing to the channel.
			// Messages are already persisted to DB above, so this is safe.
			if atomic.LoadInt32(&session.activeStreamerCount) > 0 {
				switch sm.sendResponse(session, msg) {
				case responseSent:
					logging.Debug("Session %s: Message #%d forwarded to response channel", session.ID, messageCount)
				case responseAborted:
					logging.Info("Session %s: Context cancelled after %d messages", session.ID, messageCount)
					return false, true
				default:
					// The timeout prevents a deadlock when activeStreamerCount > 0
					// but the reader goroutine has already exited (e.g., WebSocket died).
					// Fall through to direct broadcast so messages aren't lost forever.
					logging.Warning("Session %s: Channel send failed for message #%d (type=%s), falling back to direct broadcast",
						session.ID, messageCount, messageType)
					atomic.AddInt32(&session.missedMessageCount, 1)
					sm.invokeBroadcastMessageCallback(session.ID, msg)
				}
			} else {
				// No active streamer - broadcast directly to avoid channel deadlock
				logging.Info("Session %s: No active streamer, broadcasting message #%d (type=%s) directly", session.ID, messageCount, messageType)
				sm.invokeBroadcastMessageCallback(session.ID, msg)
			}

			if messageType == "result" {
				sawResult = true
			}

			// Check if we should reload after this message (from "Allow Similar" flow)
			session.pendingReloadMu.Lock()
			shouldReload := session.pendingReload
			if shouldReload {
				session.pendingReload = false // Clear the flag
			}
			session.pendingReloadMu.Unlock()

			if shouldReload {
				logging.Info("🔄 Pending reload detected - reloading session settings after message")
				// Reload in a goroutine so we don't block message processing
				go func() {
					time.Sleep(300 * time.Millisecond) // Small delay to ensure message is fully processed
					if err := sm.ReloadSessionSettings(session.ID); err != nil {
						logging.Error("Failed to reload session settings: %v", err)
						return
					}

					// After reloading, send "continue" to resume (silently, without showing in chat)
					logging.Info("▶️  Auto-resuming session with silent 'continue' command")
					time.Sleep(200 * time.Millisecond)
					if err := sm.SendPromptSilent(session.ID, "continue"); err != nil {
						logging.Error("Failed to auto-continue session: %v", err)
					}
				}()
			}

			quiet.Reset(readerSilenceWarning)

		case <-quiet.C:
			// Only a turn that started producing output and then went quiet is
			// worth a warning. Either way, keep waiting.
			if messageCount > 0 {
				logging.Warning("Session %s: no message from the CLI for %s (%d received this turn), still waiting", session.ID, readerSilenceWarning, messageCount)
			}
			quiet.Reset(readerSilenceWarning)

		case <-ctx.Done():
			logging.Info("Session %s: Context cancelled while waiting for messages", session.ID)
			return false, messageCount > 0
		}
	}
}

func (sm *SessionManager) GetResponseChannel(sessionID uuid.UUID) (chan types.Message, error) {
	session, err := sm.GetSession(sessionID)
	if err != nil {
		return nil, err
	}

	return session.responseChan, nil
}

// autoDiscoverSkills scans the filesystem for SKILL.md files and syncs them to the database.
// This is called lazily when a slash command references a skill that isn't yet in the DB.
// Skill files can be written by agents, so new or edited files go to the review queue
// (pending) and are not used until a user approves them; nothing is approved here.
func (sm *SessionManager) autoDiscoverSkills(projectDir string) {
	if sm.repo == nil {
		return
	}

	discovered, err := skills.DiscoverAll(projectDir)
	if err != nil {
		logging.Warning("Auto-discovery of skills failed: %v", err)
		return
	}

	report := skills.SyncDiscovered(sm.repo.Skill, sm.skillStore(), discovered)
	for _, e := range report.Errors {
		logging.Warning("Auto-discovery of skills: %s", e)
	}
	if report.Pending > 0 {
		logging.Info("Auto-discovered %d skill file(s) in %s that wait for review before they can be used", report.Pending, projectDir)
	}
}

// releaseSequence gives back a sequence number that was allocated but whose
// message was not persisted. It only rolls the counter back when nothing was
// allocated after it. Otherwise (a user prompt arrived while the turn was
// streaming) a blind decrement made the next message reuse a sequence that
// already belonged to another message, so history paged and sorted wrongly
// after switching sessions. A gap in the sequence is harmless.
func (sm *SessionManager) releaseSequence(session *AgentSession, seq int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if session.MessageCount == seq {
		session.MessageCount--
	}
}
