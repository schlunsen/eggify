// Package terminal provides integrated terminal support for agent sessions.
package terminal

import (
	"errors"
	"os"
	"sync"
	"time"
)

// TerminalSession represents an active terminal session.
type TerminalSession struct {
	ID             string
	AgentSessionID string
	Shell          string
	WorkingDir     string
	Env            []string
	CreatedAt      time.Time
	LastActivityAt time.Time
	Rows           int
	Cols           int
	ExitCode       *int
	EndedAt        *time.Time
	PTYFile        *os.File    `json:"-"` // Not serialized
	Cmd            interface{} `json:"-"` // *exec.Cmd, not serialized
	// owner is the signed-in user who opened the terminal ("" when it was
	// opened without one, e.g. with user auth off). Only that user may attach
	// to it or stop it. In memory only: PTYs do not survive a restart.
	owner string
	mu    sync.RWMutex
}

// SetOwner records the user who opened the terminal.
func (s *TerminalSession) SetOwner(username string) {
	s.mu.Lock()
	s.owner = username
	s.mu.Unlock()
}

// Owner returns the user who opened the terminal ("" for none).
func (s *TerminalSession) Owner() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.owner
}

// MayAttach reports whether username may attach to (and so take over) the
// terminal: a terminal opened by a signed-in user belongs to that user only.
func (s *TerminalSession) MayAttach(username string) bool {
	owner := s.Owner()
	return owner == "" || owner == username
}

// WebSocketMessage represents messages sent over the terminal WebSocket.
type WebSocketMessage struct {
	Type  string      `json:"type"`
	Data  interface{} `json:"data,omitempty"`
	Cols  int         `json:"cols,omitempty"`
	Rows  int         `json:"rows,omitempty"`
	Error string      `json:"error,omitempty"`
}

// TerminalStartResponse is sent when a terminal is started.
type TerminalStartResponse struct {
	TerminalID string `json:"terminal_id"`
	Shell      string `json:"shell"`
	CWD        string `json:"cwd"`
	Rows       int    `json:"rows"`
	Cols       int    `json:"cols"`
}

// Config holds terminal configuration.
type Config struct {
	Enabled                bool              `json:"enabled"`
	DefaultShell           string            `json:"default_shell"`
	IdleTimeoutMinutes     int               `json:"idle_timeout_minutes"`
	MaxConcurrentTerminals int               `json:"max_concurrent_terminals"`
	AllowedShells          []string          `json:"allowed_shells"`
	RestrictedMode         bool              `json:"restricted_mode"`
	CommandHistoryEnabled  bool              `json:"command_history_enabled"`
	MaxOutputBufferKB      int               `json:"max_output_buffer_kb"`
	Environment            map[string]string `json:"environment"`
}

// DefaultConfig returns the default terminal configuration.
func DefaultConfig() Config {
	return Config{
		Enabled:                true,
		DefaultShell:           "/bin/sh",
		IdleTimeoutMinutes:     30,
		MaxConcurrentTerminals: 5,
		AllowedShells:          []string{"/bin/bash", "/bin/zsh", "/bin/sh"},
		RestrictedMode:         false,
		CommandHistoryEnabled:  true,
		MaxOutputBufferKB:      1024,
		Environment: map[string]string{
			"TERM":      "xterm-256color",
			"COLORTERM": "truecolor",
			"LANG":      "en_US.UTF-8",
		},
	}
}

// Stats holds terminal statistics.
type Stats struct {
	TotalTerminalSessions  int            `json:"total_terminal_sessions"`
	ActiveTerminals        int            `json:"active_terminals"`
	TotalCommandsExecuted  int            `json:"total_commands_executed"`
	TotalDataTransferred   int64          `json:"total_data_transferred"`
	AverageSessionDuration float64        `json:"average_session_duration_seconds"`
	CommandStats           map[string]int `json:"command_stats"`
}

// ErrTerminalNotFound is returned for a terminal that is not running.
var ErrTerminalNotFound = errors.New("terminal session not found")
