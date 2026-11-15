package server

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/agents"
	"github.com/schlunsen/eggify/internal/version"
)

// Shutdown reasons for SetShutdownReason (see agents/resume_after_restart.go).
const (
	ShutdownReasonUpdate = agents.ShutdownReasonUpdate
	ShutdownReasonQuit   = agents.ShutdownReasonQuit
	ShutdownReasonSignal = agents.ShutdownReasonSignal
)

// SetShutdownReason records why the server is about to shut down: the desktop
// self-updater sets "update" before it quits (working sessions then continue
// by themselves after the restart), the CLI sets "signal". Unset means "quit".
func (s *Server) SetShutdownReason(reason string) {
	switch reason {
	case ShutdownReasonUpdate, ShutdownReasonQuit, ShutdownReasonSignal:
	default:
		return
	}
	s.shutdownMu.Lock()
	s.shutdownReason = reason
	s.shutdownMu.Unlock()
}

func (s *Server) currentShutdownReason() string {
	s.shutdownMu.Lock()
	defer s.shutdownMu.Unlock()
	if s.shutdownReason == "" {
		return ShutdownReasonQuit
	}
	return s.shutdownReason
}

func (s *Server) resumeSnapshotPath() string {
	return filepath.Join(s.claudeDir, "eggify", agents.ResumeAfterRestartFile)
}

// writeResumeSnapshot saves the working sessions (once per process), before
// agent cleanup and the database close.
func (s *Server) writeResumeSnapshot() {
	if s.agentHandler == nil || s.claudeDir == "" {
		return
	}
	s.resumeWrittenOnce.Do(func() {
		reason := s.currentShutdownReason()
		snap := s.agentHandler.SnapshotWorkingSessions(reason, version.Version, time.Now())
		if err := agents.WriteResumeSnapshot(s.resumeSnapshotPath(), snap, s.connectorKey); err != nil {
			logging.Warning("resume after restart: cannot write snapshot: %v", err)
			return
		}
		if len(snap.Sessions) > 0 {
			logging.Info("🔄 Shutdown (%s): %d working session(s) saved to continue after the restart", reason, len(snap.Sessions))
		}
	})
}

// resumeAfterUpdateEnabled reads the resume_after_update setting (default on).
func (s *Server) resumeAfterUpdateEnabled() bool {
	if s.repo == nil {
		return true
	}
	setting, err := s.repo.GetUserSetting(agents.ResumeAfterUpdateSettingKey)
	if err != nil || setting == nil {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(setting.Value), "false")
}

// setupResumeAfterRestart claims the snapshot of the previous shutdown and
// continues (after an update) or offers its sessions.
func (s *Server) setupResumeAfterRestart() {
	if s.agentHandler == nil || s.claudeDir == "" {
		return
	}
	snap := agents.ClaimResumeSnapshot(s.resumeSnapshotPath(), s.connectorKey, time.Now())
	if snap == nil {
		return
	}
	s.agentHandler.ApplyResumeSnapshot(snap, agents.ResumeOptions{
		Version:    version.Version,
		AutoResume: s.resumeAfterUpdateEnabled(),
	})
}
