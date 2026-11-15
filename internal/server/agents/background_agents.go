package agents

import (
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
)

// BackgroundAgentManager tracks background agents spawned via Task tool
type BackgroundAgentManager struct {
	mu     sync.RWMutex
	agents map[string]*BackgroundAgent // Key: AgentID

	// Callback for broadcasting updates
	broadcastCallback func(sessionID uuid.UUID, msg interface{})

	// Storage for persistence
	storage SessionStorage

	// Debounce tracking for output updates
	lastPersist   map[string]time.Time
	lastPersistMu sync.Mutex

	// Writes not yet stored, newest state per agent, and the agents a writer
	// goroutine is draining (guarded by persistMu). One writer per agent keeps
	// its writes in order, so a late output write can never overwrite the row
	// of an agent that has since finished.
	persistMu      sync.Mutex
	persistPending map[string]pendingPersist
	persistActive  map[string]bool

	// Per-agent sequence numbers for subagent_message ordering (guarded by mu)
	sequences map[string]int

	// CLI task id -> agent id, for task_* events that only carry the task id (guarded by mu)
	taskAgents map[string]string

	// SendMessage tool_use id -> the finished agent it resumes, until its result (guarded by mu)
	pendingResumes map[string]string

	// Agents whose own stream reported the usage limit (guarded by mu), and
	// the receiver told when the limit stops an agent (usage_limit.go).
	limitHit     map[string]bool
	onLimitPause func(agent BackgroundAgent)

	// Periodic eviction of finished agents (see StartCleanup)
	cleanupMu   sync.Mutex
	cleanupStop chan struct{}
	cleanupDone chan struct{}
}

// OutputLine is one line of agent output for AddOutputLines.
type OutputLine struct {
	Text    string
	IsError bool
	// At is when the line was produced; zero means now (when it was added).
	At time.Time
}

// Defaults for the periodic eviction of finished agents from memory. Their
// rows stay in the database, and SubagentsForSession restores them on demand.
const (
	backgroundAgentCleanupInterval = time.Hour
	backgroundAgentMaxAge          = 24 * time.Hour
)

// stalledAgentAfter is how long a running agent may go without any output or
// event before the cleanup loop takes it as stopped (its end was never
// reported). If it speaks again it is reopened (handleSubagentMessageAt).
const stalledAgentAfter = 6 * time.Hour

// StalledAgentMessage is the error recorded for an agent stopped by
// StopStalledAgents; StaleRunningAgentMessage for a stored row still marked
// running although no agent of this process runs it. Both read as an
// interruption (IsSubagentInterruption).
const (
	StalledAgentMessage      = "Agent stopped: no activity for 6 hours"
	StaleRunningAgentMessage = "Agent stopped: it was no longer running"
)

// maxRecentOutputLines caps the output kept per agent, so a reload or a later
// viewer can still show what an agent did.
const maxRecentOutputLines = 300

// clone copies an agent, including its output slices, so the copy can be read
// or persisted without holding the manager lock. Only the newest
// maxRecentOutputLines lines are copied (the live buffer may hold more, see
// appendRecentOutput).
func (a *BackgroundAgent) clone() BackgroundAgent {
	c := *a
	c.RecentOutput = slices.Clone(a.RecentOutput)
	c.RecentOutputAt = slices.Clone(a.RecentOutputAt)
	c.KeepNewestOutput(maxRecentOutputLines)
	return c
}

// KeepNewestOutput trims RecentOutput to its newest n lines and keeps
// RecentOutputAt parallel to it: padded with 0 (unknown) for lines without a
// time, and nil when no line has one.
func (a *BackgroundAgent) KeepNewestOutput(n int) {
	if over := len(a.RecentOutput) - n; over > 0 {
		a.RecentOutput = a.RecentOutput[over:]
	}
	a.RecentOutputAt = alignOutputTimes(a.RecentOutputAt, len(a.RecentOutput))
}

// alignOutputTimes returns times for the newest n lines: older lines lose
// their times first, lines without one get 0. All zero reads as nil.
func alignOutputTimes(times []int64, n int) []int64 {
	if over := len(times) - n; over > 0 {
		times = times[over:]
	}
	if !slices.ContainsFunc(times, func(t int64) bool { return t > 0 }) {
		return nil
	}
	if missing := n - len(times); missing > 0 {
		times = append(make([]int64, missing, n), times...)
	}
	return times
}

// appendRecentOutput appends lines (and their times) to the agent's output
// buffer. The buffer may grow to twice maxRecentOutputLines before the newest
// lines are moved to the front in place, so trimming costs amortized O(1) per
// line and does not reallocate. Readers go through clone, which takes only the
// newest lines.
func (a *BackgroundAgent) appendRecentOutput(lines []OutputLine) {
	// Lines restored from an older row have no times: pad so the two
	// slices stay parallel.
	for len(a.RecentOutputAt) < len(a.RecentOutput) {
		a.RecentOutputAt = append(a.RecentOutputAt, 0)
	}
	a.RecentOutputAt = a.RecentOutputAt[:len(a.RecentOutput)]
	now := time.Now()
	for _, line := range lines {
		a.RecentOutput = append(a.RecentOutput, line.Text)
		at := line.At
		if at.IsZero() {
			at = now
		}
		a.RecentOutputAt = append(a.RecentOutputAt, at.UnixMilli())
	}
	if n := len(a.RecentOutput); n > 2*maxRecentOutputLines {
		keep := copy(a.RecentOutput, a.RecentOutput[n-maxRecentOutputLines:])
		clear(a.RecentOutput[keep:n]) // drop references to evicted lines
		a.RecentOutput = a.RecentOutput[:keep]
		copy(a.RecentOutputAt, a.RecentOutputAt[n-maxRecentOutputLines:])
		a.RecentOutputAt = a.RecentOutputAt[:keep]
	}
}

// NewBackgroundAgentManager creates a new background agent manager
func NewBackgroundAgentManager() *BackgroundAgentManager {
	return &BackgroundAgentManager{
		agents:      make(map[string]*BackgroundAgent),
		lastPersist: make(map[string]time.Time),
		sequences:   make(map[string]int),

		persistPending: make(map[string]pendingPersist),
		persistActive:  make(map[string]bool),
		taskAgents:     make(map[string]string),

		pendingResumes: make(map[string]string),
		limitHit:       make(map[string]bool),
	}
}

// SetStorage sets the storage backend for persistence
func (m *BackgroundAgentManager) SetStorage(storage SessionStorage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.storage = storage
}

// RestoreAgent adds an agent to the in-memory map without broadcasting (used on startup)
func (m *BackgroundAgentManager) RestoreAgent(agent *BackgroundAgent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if agent.Status == BackgroundAgentStatusFailed && IsSubagentInterruption(agent.ErrorMessage) {
		agent.Interrupted = true
	}
	agent.LimitPaused = agent.Status == BackgroundAgentStatusFailed && IsSubagentLimitPause(agent.ErrorMessage)
	m.agents[agent.AgentID] = agent
	if agent.TaskID != "" {
		m.taskAgents[agent.TaskID] = agent.AgentID
	}
}

// pendingPersist is the newest unsaved state of one agent.
type pendingPersist struct {
	agent BackgroundAgent
	isNew bool
}

// persistAgent saves agent state to the database without blocking (errors are
// logged). Writes of one agent are stored in order by a single writer, and
// states queued while a write runs are folded into the newest one.
func (m *BackgroundAgentManager) persistAgent(agent *BackgroundAgent, isNew bool) {
	if m.storage == nil {
		return
	}

	agentCopy := agent.clone()
	id := agentCopy.AgentID
	m.persistMu.Lock()
	if queued, ok := m.persistPending[id]; ok && queued.isNew {
		isNew = true // the row may not exist yet
	}
	m.persistPending[id] = pendingPersist{agent: agentCopy, isNew: isNew}
	if m.persistActive[id] {
		m.persistMu.Unlock()
		return
	}
	m.persistActive[id] = true
	m.persistMu.Unlock()
	go m.drainPersist(id)
}

// drainPersist writes an agent's queued states until none is left.
func (m *BackgroundAgentManager) drainPersist(id string) {
	for {
		m.persistMu.Lock()
		next, ok := m.persistPending[id]
		if !ok {
			delete(m.persistActive, id)
			m.persistMu.Unlock()
			return
		}
		delete(m.persistPending, id)
		m.persistMu.Unlock()

		var err error
		if next.isNew {
			err = m.storage.SaveBackgroundAgent(&next.agent)
		} else {
			err = m.storage.UpdateBackgroundAgent(&next.agent)
		}
		if err != nil {
			logging.Error("Failed to persist background agent %s: %v", id, err)
		}
	}
}

// shouldPersistOutput checks if enough time has passed to debounce output persistence
func (m *BackgroundAgentManager) shouldPersistOutput(agentID string) bool {
	m.lastPersistMu.Lock()
	defer m.lastPersistMu.Unlock()

	last, exists := m.lastPersist[agentID]
	if !exists || time.Since(last) >= 3*time.Second {
		m.lastPersist[agentID] = time.Now()
		return true
	}
	return false
}

// SetBroadcastCallback sets the callback for broadcasting updates to WebSocket clients
func (m *BackgroundAgentManager) SetBroadcastCallback(cb func(sessionID uuid.UUID, msg interface{})) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broadcastCallback = cb
}

// RegisterAgent registers a new background agent
func (m *BackgroundAgentManager) RegisterAgent(
	agentID string,
	parentSessionID uuid.UUID,
	subagentType string,
	description string,
) *BackgroundAgent {
	return m.RegisterSubagent(agentID, parentSessionID, subagentType, description, "", false)
}

// RegisterSubagent registers an agent with its nesting parent and launch mode.
// Registering a known agent fills in metadata it was missing (an agent first
// seen through its own messages learns its type and description later) and
// otherwise leaves it unchanged.
func (m *BackgroundAgentManager) RegisterSubagent(
	agentID string,
	parentSessionID uuid.UUID,
	subagentType string,
	description string,
	parentAgentID string,
	background bool,
) *BackgroundAgent {
	return m.RegisterSubagentWithPrompt(agentID, parentSessionID, subagentType, description, parentAgentID, background, "")
}

// RegisterSubagentWithPrompt is RegisterSubagent that also records what the
// agent was asked to do, so the background_agent_started event carries it.
func (m *BackgroundAgentManager) RegisterSubagentWithPrompt(
	agentID string,
	parentSessionID uuid.UUID,
	subagentType string,
	description string,
	parentAgentID string,
	background bool,
	prompt string,
) *BackgroundAgent {
	return m.RegisterLaunchedSubagent(agentID, parentSessionID, subagentType, description, parentAgentID, background, prompt, "")
}

// RegisterLaunchedSubagent is RegisterSubagentWithPrompt that also records the
// model the launching call asked for (its "model" input), shown as requested
// until the agent's own messages report the model it actually runs on.
func (m *BackgroundAgentManager) RegisterLaunchedSubagent(
	agentID string,
	parentSessionID uuid.UUID,
	subagentType string,
	description string,
	parentAgentID string,
	background bool,
	prompt string,
	requestedModel string,
) *BackgroundAgent {
	prompt = capSubagentPrompt(prompt)
	requestedModel = cleanSubagentModel(requestedModel)
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, exists := m.agents[agentID]; exists {
		learned := false
		if existing.Prompt == "" && prompt != "" {
			existing.Prompt = prompt
			learned = true
		}
		if existing.Model == "" && requestedModel != "" {
			existing.Model, existing.ModelSource = requestedModel, ModelSourceRequested
			learned = true
		}
		if learned {
			m.persistAgent(existing, false)
			// Re-announce it so an open view learns the prompt; clients keep
			// the output they hold (the event carries the kept output too).
			if m.broadcastCallback != nil {
				m.broadcastCallback(existing.ParentSessionID, BackgroundAgentStartedMessage{
					BaseMessage: BaseMessage{Type: MessageTypeBackgroundAgentStarted},
					SessionID:   existing.ParentSessionID,
					Agent:       existing.clone(),
				})
			}
		}
		if existing.SubagentType == "" {
			existing.SubagentType = subagentType
		}
		if existing.Description == "" {
			existing.Description = description
		}
		if existing.ParentAgentID == "" {
			existing.ParentAgentID = parentAgentID
		}
		existing.Background = existing.Background || background
		return existing
	}

	now := time.Now()
	agent := &BackgroundAgent{
		AgentID:         agentID,
		ParentSessionID: parentSessionID,
		SubagentType:    subagentType,
		Description:     description,
		Status:          BackgroundAgentStatusRunning,
		Progress:        0.0,
		CreatedAt:       now,
		UpdatedAt:       now,
		ParentAgentID:   parentAgentID,
		Background:      background,
		Prompt:          prompt,
	}
	if requestedModel != "" {
		agent.Model, agent.ModelSource = requestedModel, ModelSourceRequested
	}

	m.agents[agentID] = agent

	logging.Info("📦 Background agent registered: id=%s, type=%s, parent=%s",
		agentID, subagentType, parentSessionID)

	// Persist to database
	m.persistAgent(agent, true)

	// Broadcast agent started event
	if m.broadcastCallback != nil {
		msg := BackgroundAgentStartedMessage{
			BaseMessage: BaseMessage{Type: MessageTypeBackgroundAgentStarted},
			SessionID:   parentSessionID,
			Agent:       agent.clone(),
		}
		m.broadcastCallback(parentSessionID, msg)
	}

	return agent
}

// Where BackgroundAgent.Model came from.
const (
	ModelSourceActual    = "actual"    // the agent's own assistant messages
	ModelSourceRequested = "requested" // the launching call's model input
)

// cleanSubagentModel trims a model name and drops the CLI's placeholders
// ("<synthetic>" on messages it makes up itself, e.g. error notices).
func cleanSubagentModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" || strings.HasPrefix(model, "<") || len(model) > 200 {
		return ""
	}
	return model
}

// SetModel records the model an agent runs on. An actual model (from the
// agent's own messages) replaces a requested one and is replaced by a newer
// actual one; a requested model never replaces an actual one. A change is
// persisted and re-announced (background_agent_started with the whole agent;
// clients keep the output they hold). It reports whether the model changed.
func (m *BackgroundAgentManager) SetModel(agentID, model, source string) bool {
	model = cleanSubagentModel(model)
	if model == "" || (source != ModelSourceActual && source != ModelSourceRequested) {
		return false
	}
	m.mu.Lock()
	agent, exists := m.agents[agentID]
	if !exists || (agent.Model == model && agent.ModelSource == source) ||
		(source == ModelSourceRequested && agent.ModelSource == ModelSourceActual) {
		m.mu.Unlock()
		return false
	}
	agent.Model, agent.ModelSource = model, source
	agentCopy := agent.clone()
	cb := m.broadcastCallback
	m.mu.Unlock()

	m.persistAgent(&agentCopy, false)
	if cb != nil {
		cb(agentCopy.ParentSessionID, BackgroundAgentStartedMessage{
			BaseMessage: BaseMessage{Type: MessageTypeBackgroundAgentStarted},
			SessionID:   agentCopy.ParentSessionID,
			Agent:       agentCopy,
		})
	}
	return true
}

// UpdateProgress updates agent progress and broadcasts the update
func (m *BackgroundAgentManager) UpdateProgress(agentID string, progress float64, output string) {
	m.mu.Lock()
	agent, exists := m.agents[agentID]
	if !exists {
		m.mu.Unlock()
		return
	}

	agent.Progress = progress
	agent.UpdatedAt = time.Now()
	if output != "" {
		agent.LastOutput = output
		agent.OutputLines++
	}
	parentSessionID := agent.ParentSessionID
	agentCopy := agent.clone()
	m.mu.Unlock()

	// Debounced persistence for progress updates
	if m.shouldPersistOutput(agentID) {
		m.persistAgent(&agentCopy, false)
	}

	logging.Debug("📊 Background agent progress: id=%s, progress=%.1f%%", agentID, progress*100)

	// Broadcast progress update
	if m.broadcastCallback != nil {
		msg := BackgroundAgentProgressMessage{
			BaseMessage: BaseMessage{Type: MessageTypeBackgroundAgentProgress},
			SessionID:   parentSessionID,
			AgentID:     agentID,
			Status:      string(BackgroundAgentStatusRunning),
			Progress:    progress,
			Output:      output,
		}
		m.broadcastCallback(parentSessionID, msg)
	}
}

// AddOutput adds one line of incremental output from the agent.
func (m *BackgroundAgentManager) AddOutput(agentID string, output string, isError bool) {
	m.AddOutputLines(agentID, []OutputLine{{Text: output, IsError: isError}})
}

// AddOutputLines adds several output lines at once: one state update, at most
// one (debounced) persist and one broadcast for the whole batch. The agent is
// cloned only when it is actually persisted.
func (m *BackgroundAgentManager) AddOutputLines(agentID string, lines []OutputLine) {
	if len(lines) == 0 {
		return
	}
	m.mu.Lock()
	agent, exists := m.agents[agentID]
	if !exists {
		m.mu.Unlock()
		return
	}

	// Times: a line's own, else now.
	now := time.Now()
	times := make([]int64, len(lines))
	for i, line := range lines {
		if line.At.IsZero() {
			lines[i].At = now
		}
		times[i] = lines[i].At.UnixMilli()
	}

	last := lines[len(lines)-1]
	agent.UpdatedAt = now
	agent.LastOutput = last.Text
	agent.OutputLines += len(lines)
	agent.appendRecentOutput(lines)
	parentSessionID := agent.ParentSessionID

	// Debounced persistence for output updates
	var agentCopy *BackgroundAgent
	if m.storage != nil && m.shouldPersistOutput(agentID) {
		c := agent.clone()
		agentCopy = &c
	}
	cb := m.broadcastCallback
	m.mu.Unlock()

	if agentCopy != nil {
		m.persistAgent(agentCopy, false)
	}

	if cb == nil {
		return
	}
	// One event per batch. Output/IsError keep the single-line shape (the
	// last line, and whether any line is an error); Lines carries the whole
	// batch when there is more than one line.
	msg := BackgroundAgentOutputMessage{
		BaseMessage: BaseMessage{Type: MessageTypeBackgroundAgentOutput},
		SessionID:   parentSessionID,
		AgentID:     agentID,
		Output:      last.Text,
		At:          times[len(times)-1],
	}
	for _, line := range lines {
		msg.IsError = msg.IsError || line.IsError
	}
	if len(lines) > 1 {
		msg.Lines = make([]string, len(lines))
		for i, line := range lines {
			msg.Lines[i] = line.Text
		}
		msg.LinesAt = times
	}
	cb(parentSessionID, msg)
}

// CompleteAgent marks an agent as completed
func (m *BackgroundAgentManager) CompleteAgent(agentID string, finalOutput string) {
	// An agent cut off by the usage limit "completes" with the limit notice:
	// it is paused, not done.
	if m.stoppedByLimit(agentID, finalOutput) {
		m.FailAgent(agentID, UsageLimitAgentMessage)
		return
	}
	m.mu.Lock()
	agent, exists := m.agents[agentID]
	if !exists {
		m.mu.Unlock()
		return
	}

	now := time.Now()
	agent.Status = BackgroundAgentStatusCompleted
	agent.Progress = 1.0
	agent.UpdatedAt = now
	agent.CompletedAt = &now
	if finalOutput != "" {
		agent.LastOutput = finalOutput
	}
	parentSessionID := agent.ParentSessionID
	agentCopy := agent.clone()
	m.mu.Unlock()

	// Always persist completion
	m.persistAgent(&agentCopy, false)

	logging.Info("✅ Background agent completed: id=%s", agentID)

	// Broadcast completion event
	if m.broadcastCallback != nil {
		msg := BackgroundAgentCompletedMessage{
			BaseMessage: BaseMessage{Type: MessageTypeBackgroundAgentCompleted},
			SessionID:   parentSessionID,
			AgentID:     agentID,
			FinalOutput: finalOutput,
			CompletedAt: now,
		}
		m.broadcastCallback(parentSessionID, msg)
	}
}

// AppRestartedAgentMessage is the error recorded for agents that were still
// running when Eggify stopped.
const AppRestartedAgentMessage = "App restarted while agent was running"

// subagentInterruptionRE matches the error messages of agents that were cut
// off rather than failing on their own: Eggify restarted, the CLI reported
// the task stopped/killed/cancelled ("Agent stopped: ..."), a Codex child was
// interrupted ("Sub-agent x interrupted"), or the user interrupted the turn.
// Keep in sync with isInterruptionMessage in
// frontend/app/utils/agents/subagentActivity.ts.
var subagentInterruptionRE = regexp.MustCompile(`(?i)^(app restarted|agent (stopped|killed|interrupted|paused|cancell?ed|aborted)\b|sub-agent .{0,200}? (stopped|interrupted|cancell?ed)\b|session (ended|closed|stopped))|\[request interrupted`)

// IsSubagentInterruption reports whether a subagent's error message means it
// was interrupted (see BackgroundAgent.Interrupted), not that it failed.
func IsSubagentInterruption(message string) bool {
	return subagentInterruptionRE.MatchString(strings.TrimSpace(message))
}

// FailAgent marks an agent as failed
func (m *BackgroundAgentManager) FailAgent(agentID string, errorMessage string) {
	if !IsSubagentLimitPause(errorMessage) && m.stoppedByLimit(agentID, errorMessage) {
		errorMessage = UsageLimitAgentMessage
	}
	m.mu.Lock()
	agent, exists := m.agents[agentID]
	if !exists {
		m.mu.Unlock()
		return
	}

	agent.Status = BackgroundAgentStatusFailed
	agent.UpdatedAt = time.Now()
	agent.ErrorMessage = errorMessage
	agent.Interrupted = IsSubagentInterruption(errorMessage)
	agent.LimitPaused = IsSubagentLimitPause(errorMessage)
	delete(m.limitHit, agentID)
	interrupted, limitPaused := agent.Interrupted, agent.LimitPaused
	onLimitPause := m.onLimitPause
	parentSessionID := agent.ParentSessionID
	lastOutput := agent.LastOutput
	agentCopy := agent.clone()
	m.mu.Unlock()

	// Always persist failure
	m.persistAgent(&agentCopy, false)

	logging.Error("❌ Background agent failed: id=%s, error=%s", agentID, errorMessage)

	// Broadcast failure event
	if m.broadcastCallback != nil {
		msg := BackgroundAgentFailedMessage{
			BaseMessage:  BaseMessage{Type: MessageTypeBackgroundAgentFailed},
			SessionID:    parentSessionID,
			AgentID:      agentID,
			ErrorMessage: errorMessage,
			LastOutput:   lastOutput,
			Interrupted:  interrupted,
			LimitPaused:  limitPaused,
		}
		m.broadcastCallback(parentSessionID, msg)
	}
	if limitPaused && onLimitPause != nil {
		onLimitPause(agentCopy)
	}
}

// MarkLimitHit notes that an agent's own stream reported the usage limit, so
// it is paused (not completed or failed) when it ends.
func (m *BackgroundAgentManager) MarkLimitHit(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if agent, ok := m.agents[agentID]; ok && agent.Status == BackgroundAgentStatusRunning {
		m.limitHit[agentID] = true
	}
}

// SetLimitPauseCallback registers the receiver told when the usage limit
// stops an agent.
func (m *BackgroundAgentManager) SetLimitPauseCallback(cb func(agent BackgroundAgent)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onLimitPause = cb
}

// stoppedByLimit reports whether a running agent that ends now was stopped
// by the usage limit: its stream said so, or its final words are the notice.
func (m *BackgroundAgentManager) stoppedByLimit(agentID, text string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	agent, ok := m.agents[agentID]
	if !ok || agent.Status != BackgroundAgentStatusRunning {
		return false
	}
	return m.limitHit[agentID] || IsUsageLimitText(text)
}

// IsSubagentLimitPause reports whether a subagent's error message means the
// usage limit stopped it (see BackgroundAgent.LimitPaused).
func IsSubagentLimitPause(message string) bool {
	return strings.HasPrefix(strings.TrimSpace(message), UsageLimitAgentMessage)
}

// ReopenAgent moves a finished agent back to running. The CLI resumes a
// finished agent when it is sent a message (SendMessage), and the resumed run
// reports under the same agent. It reports whether the agent was reopened;
// running and unknown agents are left alone.
func (m *BackgroundAgentManager) ReopenAgent(agentID string) bool {
	m.mu.Lock()
	agent, exists := m.agents[agentID]
	if !exists || agent.Status == BackgroundAgentStatusRunning {
		m.mu.Unlock()
		return false
	}

	agent.Status = BackgroundAgentStatusRunning
	agent.UpdatedAt = time.Now()
	agent.CompletedAt = nil
	agent.ErrorMessage = ""
	agent.Interrupted = false
	agent.LimitPaused = false
	agent.Progress = 0
	parentSessionID := agent.ParentSessionID
	agentCopy := agent.clone()
	cb := m.broadcastCallback
	m.mu.Unlock()

	m.persistAgent(&agentCopy, false)

	logging.Info("🔁 Background agent resumed: id=%s", agentID)

	// A progress event with status running: clients already apply its status
	// to a known agent, without resetting the output they hold.
	if cb != nil {
		cb(parentSessionID, BackgroundAgentProgressMessage{
			BaseMessage: BaseMessage{Type: MessageTypeBackgroundAgentProgress},
			SessionID:   parentSessionID,
			AgentID:     agentID,
			Status:      string(BackgroundAgentStatusRunning),
		})
	}
	return true
}

// GetAgent retrieves an agent by ID
func (m *BackgroundAgentManager) GetAgent(agentID string) (*BackgroundAgent, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, exists := m.agents[agentID]
	if !exists {
		return nil, false
	}

	// Return a copy to prevent external modification
	agentCopy := agent.clone()
	return &agentCopy, true
}

// GetAgentsForSession returns all agents for a given parent session
func (m *BackgroundAgentManager) GetAgentsForSession(sessionID uuid.UUID) []BackgroundAgent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var agents []BackgroundAgent
	for _, agent := range m.agents {
		if agent.ParentSessionID == sessionID {
			agents = append(agents, agent.clone())
		}
	}

	// Sort by creation time to ensure consistent ordering
	slices.SortFunc(agents, func(a, b BackgroundAgent) int {
		if a.CreatedAt.Before(b.CreatedAt) {
			return -1
		} else if a.CreatedAt.After(b.CreatedAt) {
			return 1
		}
		return 0
	})

	return agents
}

// GetRunningAgentsForSession returns only running agents for a session
func (m *BackgroundAgentManager) GetRunningAgentsForSession(sessionID uuid.UUID) []BackgroundAgent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var agents []BackgroundAgent
	for _, agent := range m.agents {
		if agent.ParentSessionID == sessionID && agent.Status == BackgroundAgentStatusRunning {
			agents = append(agents, agent.clone())
		}
	}

	// Sort by creation time to ensure consistent ordering
	slices.SortFunc(agents, func(a, b BackgroundAgent) int {
		if a.CreatedAt.Before(b.CreatedAt) {
			return -1
		} else if a.CreatedAt.After(b.CreatedAt) {
			return 1
		}
		return 0
	})

	return agents
}

// CleanupCompletedAgents removes completed/failed agents older than the given
// duration from memory, together with their sequence counters, task bindings
// and persist-debounce entries. Database rows are kept.
func (m *BackgroundAgentManager) CleanupCompletedAgents(maxAge time.Duration) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := make(map[string]struct{})

	for id, agent := range m.agents {
		if agent.Status != BackgroundAgentStatusRunning && agent.UpdatedAt.Before(cutoff) {
			delete(m.agents, id)
			delete(m.sequences, id)
			removed[id] = struct{}{}
		}
	}

	if len(removed) > 0 {
		for taskID, agentID := range m.taskAgents {
			if _, gone := removed[agentID]; gone {
				delete(m.taskAgents, taskID)
			}
		}
		m.lastPersistMu.Lock()
		for id := range removed {
			delete(m.lastPersist, id)
		}
		m.lastPersistMu.Unlock()
		logging.Info("🧹 Cleaned up %d old background agents", len(removed))
	}

	return len(removed)
}

// StopStalledAgents marks running agents that have shown no activity for
// longer than after as stopped, and returns how many it stopped.
func (m *BackgroundAgentManager) StopStalledAgents(after time.Duration) int {
	cutoff := time.Now().Add(-after)
	var stalled []string
	m.mu.RLock()
	for id, agent := range m.agents {
		if agent.Status == BackgroundAgentStatusRunning && agent.UpdatedAt.Before(cutoff) {
			stalled = append(stalled, id)
		}
	}
	m.mu.RUnlock()
	for _, id := range stalled {
		m.FailAgent(id, StalledAgentMessage)
	}
	if len(stalled) > 0 {
		logging.Info("🧹 Stopped %d stalled background agents", len(stalled))
	}
	return len(stalled)
}

// settleStaleRunning marks a stored agent that is still "running" although
// this process does not run it (its finish was lost, or it ran in an earlier
// process) as stopped, and reports whether it changed it.
func settleStaleRunning(agent *BackgroundAgent) bool {
	if agent.Status != BackgroundAgentStatusRunning {
		return false
	}
	now := time.Now()
	agent.Status = BackgroundAgentStatusFailed
	agent.ErrorMessage = StaleRunningAgentMessage
	agent.Interrupted = true
	agent.UpdatedAt = now
	agent.CompletedAt = &now
	return true
}

// StartCleanup evicts finished agents older than maxAge from memory once now
// and then every interval, until StopCleanup. Calling it again while running
// is a no-op.
func (m *BackgroundAgentManager) StartCleanup(interval, maxAge time.Duration) {
	m.cleanupMu.Lock()
	defer m.cleanupMu.Unlock()
	if m.cleanupStop != nil {
		return
	}
	stop, done := make(chan struct{}), make(chan struct{})
	m.cleanupStop, m.cleanupDone = stop, done
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			m.StopStalledAgents(stalledAgentAfter)
			m.CleanupCompletedAgents(maxAge)
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
}

// StopCleanup stops the loop started by StartCleanup and waits for it to exit.
func (m *BackgroundAgentManager) StopCleanup() {
	m.cleanupMu.Lock()
	stop, done := m.cleanupStop, m.cleanupDone
	m.cleanupStop, m.cleanupDone = nil, nil
	m.cleanupMu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
}

// Count returns the total number of tracked agents
func (m *BackgroundAgentManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.agents)
}

// RunningCount returns the number of currently running agents
func (m *BackgroundAgentManager) RunningCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, agent := range m.agents {
		if agent.Status == BackgroundAgentStatusRunning {
			count++
		}
	}

	return count
}

// NextSequence returns the next subagent_message sequence number for an agent.
func (m *BackgroundAgentManager) NextSequence(agentID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sequences[agentID]++
	return m.sequences[agentID]
}

// Depth returns how deeply an agent is nested: 0 for an agent launched by the
// parent session, 1 for one launched by a subagent, and so on.
func (m *BackgroundAgentManager) Depth(agentID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	depth := 0
	for seen := map[string]bool{agentID: true}; ; depth++ {
		agent, ok := m.agents[agentID]
		if !ok || agent.ParentAgentID == "" || seen[agent.ParentAgentID] {
			return depth
		}
		agentID = agent.ParentAgentID
		seen[agentID] = true
	}
}

// BindTask records the CLI task id of a background agent, so task_* events,
// which carry only the task id, can be matched to it.
func (m *BackgroundAgentManager) BindTask(agentID, taskID string) {
	if agentID == "" || taskID == "" {
		return
	}
	m.mu.Lock()
	m.taskAgents[taskID] = agentID
	agent, exists := m.agents[agentID]
	if !exists || agent.TaskID == taskID {
		m.mu.Unlock()
		return
	}
	agent.TaskID = taskID
	agentCopy := agent.clone()
	m.mu.Unlock()
	m.persistAgent(&agentCopy, false)
}

// ExpectResume records that the SendMessage call toolUseID is addressed to
// agentID; ResumeFromResult reopens the agent once the call succeeds.
func (m *BackgroundAgentManager) ExpectResume(toolUseID, agentID string) {
	if toolUseID == "" || agentID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pendingResumes) >= maxPendingResumes {
		// Results that never came (an interrupted turn) must not pile up.
		clear(m.pendingResumes)
	}
	m.pendingResumes[toolUseID] = agentID
}

// maxPendingResumes bounds the SendMessage calls waiting for their result.
const maxPendingResumes = 256

// ResumeFromResult handles the result of a SendMessage call recorded with
// ExpectResume: a successful call resumed the agent, so it is reopened; a
// denied or failed one leaves it as it was. It reports whether toolUseID was
// such a call.
func (m *BackgroundAgentManager) ResumeFromResult(toolUseID string, isError bool) bool {
	m.mu.Lock()
	agentID, pending := m.pendingResumes[toolUseID]
	delete(m.pendingResumes, toolUseID)
	m.mu.Unlock()
	if pending && !isError {
		m.ReopenAgent(agentID)
	}
	return pending
}

// maxSubagentPromptChars caps the prompt kept per agent.
const maxSubagentPromptChars = 4000

// capSubagentPrompt trims a prompt and cuts it to maxSubagentPromptChars on a
// rune boundary.
func capSubagentPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if len(prompt) <= maxSubagentPromptChars {
		return prompt
	}
	cut := maxSubagentPromptChars
	for cut > 0 && !utf8.RuneStart(prompt[cut]) {
		cut--
	}
	return prompt[:cut] + "…"
}

// AgentForTask returns the agent bound to a CLI task id, or "".
func (m *BackgroundAgentManager) AgentForTask(taskID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.taskAgents[taskID]
}
