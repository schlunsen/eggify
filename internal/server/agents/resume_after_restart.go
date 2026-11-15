package agents

// Continue sessions after a restart (see CLAUDE.md, "Continue sessions after an
// update"). At shutdown the server writes the sessions that were working to
// ~/.claude/eggify/resume-after-restart.json; the next boot claims the file
// once. After an update those sessions get a fixed resume prompt; after any
// other shutdown they are offered to the user (GET/POST/DELETE
// /api/agent/resume-after-restart).
//
// Security: the file is HMAC-signed with a key derived from the connector
// encryption key, it can only name sessions that exist and have not ended, and
// it carries no text that reaches an agent as instructions: the resume prompt
// is fixed server text and subagent descriptions/prompts are framed as data
// with their tags neutralised. Nothing in it changes permissions, YOLO, the
// model or the provider.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/logging"
)

// Shutdown reasons recorded in the snapshot.
const (
	ShutdownReasonUpdate = "update" // the desktop self-updater restarts the app
	ShutdownReasonQuit   = "quit"   // the user quit the app
	ShutdownReasonSignal = "signal" // SIGINT/SIGTERM (CLI)
)

// ResumeAfterRestartFile is the snapshot's name inside ~/.claude/eggify.
const ResumeAfterRestartFile = "resume-after-restart.json"

// ResumeAfterUpdateSettingKey turns the automatic resume after an update off
// ("false"). It is a plain preference, not a security setting.
const ResumeAfterUpdateSettingKey = "resume_after_update"

const (
	resumeSnapshotVersion = 1
	// resumeAutoMaxAge: an update restart older than this is offered, not sent.
	resumeAutoMaxAge = 30 * time.Minute
	// resumeOfferMaxAge: interrupted sessions are offered this long.
	resumeOfferMaxAge = 24 * time.Hour
	// resumeMaxSessions caps the entries read from (and written to) the file.
	resumeMaxSessions = 100
	// resumeMaxSubagents caps the subagents listed per session.
	resumeMaxSubagents           = 10
	resumeSubagentDescriptionMax = 200
	resumeSubagentPromptMax      = 600
	resumeSubagentsTag           = "interrupted_subagents"
)

// Why a session was working when Eggify stopped.
const (
	ResumeWhyTurn      = "turn"
	ResumeWhySubagents = "subagents"
)

// ResumeSubagent is a subagent that was running when Eggify stopped.
type ResumeSubagent struct {
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
}

// ResumeSession is one session that was working when Eggify stopped.
type ResumeSession struct {
	ID            uuid.UUID `json:"id"`
	Why           string    `json:"why"`
	UpdatedAt     time.Time `json:"updated_at"`
	InterruptedAt time.Time `json:"interrupted_at"`
	// Auto: continue it by itself on the next boot (it was stopped by an update).
	Auto      bool             `json:"auto,omitempty"`
	Subagents []ResumeSubagent `json:"subagents,omitempty"`
}

// ResumeSnapshot is the file written at shutdown.
type ResumeSnapshot struct {
	V           int             `json:"v"`
	Reason      string          `json:"reason"`
	FromVersion string          `json:"from_version,omitempty"`
	WrittenAt   time.Time       `json:"written_at"`
	Sessions    []ResumeSession `json:"sessions"`
	Sig         string          `json:"sig,omitempty"`
}

// resumeState is the handler's view of interrupted sessions: the ones being
// resumed now and the ones offered to the user.
type resumeState struct {
	mu      sync.Mutex
	pending []ResumeSession // offered: "Continue N interrupted sessions"
	reason  string          // reason of the restart that interrupted them

	// send delivers a resume prompt; tests replace it.
	send func(sessionID uuid.UUID, prompt string) error
	// batch and gap pace the resumes: batch sessions at a time, gap apart.
	// startDelay lets the server come up before the first resume after boot.
	batch      int
	gap        time.Duration
	startDelay time.Duration
	sleep      func(time.Duration)
}

func (h *AgentHandler) resume() *resumeState {
	h.resumeOnce.Do(func() {
		h.resumeSt = &resumeState{batch: 2, gap: 4 * time.Second, startDelay: 3 * time.Second, sleep: time.Sleep}
	})
	return h.resumeSt
}

// SetResumeSender replaces how resume prompts are delivered (tests).
func (h *AgentHandler) SetResumeSender(send func(sessionID uuid.UUID, prompt string) error) {
	st := h.resume()
	st.mu.Lock()
	st.send = send
	st.startDelay = 0
	st.mu.Unlock()
}

// signingKey derives the snapshot's HMAC key from the connector encryption key.
func resumeSigningKey(secret []byte) []byte {
	if len(secret) == 0 {
		return nil
	}
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("eggify resume-after-restart v1"))
	return m.Sum(nil)
}

func (s *ResumeSnapshot) signature(key []byte) (string, error) {
	c := *s
	c.Sig = ""
	b, err := json.Marshal(&c)
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, key)
	m.Write(b)
	return hex.EncodeToString(m.Sum(nil)), nil
}

// SnapshotWorkingSessions lists the sessions that are working right now: a
// turn in flight (including one waiting on a permission card or a question,
// whose card is lost with the restart) or background subagents still running.
// Oldest activity first. Offered sessions from an earlier restart that are
// not working are carried over, so the offer survives another restart.
func (h *AgentHandler) SnapshotWorkingSessions(reason, fromVersion string, now time.Time) *ResumeSnapshot {
	snap := &ResumeSnapshot{V: resumeSnapshotVersion, Reason: reason, FromVersion: fromVersion, WrittenAt: now.UTC()}
	sm := h.SessionManager
	if sm == nil {
		return snap
	}
	type cand struct {
		id        uuid.UUID
		status    SessionStatus
		updatedAt time.Time
		prompted  bool
	}
	sm.mu.RLock()
	cands := make([]cand, 0, len(sm.sessions))
	for id, s := range sm.sessions {
		cands = append(cands, cand{id, s.Status, s.UpdatedAt, s.promptedThisRun.Load()})
	}
	sm.mu.RUnlock()

	seen := map[uuid.UUID]bool{}
	for _, c := range cands {
		if c.status == SessionStatusEnded {
			continue
		}
		var running []BackgroundAgent
		if h.BackgroundAgentMgr != nil {
			running = h.BackgroundAgentMgr.GetRunningAgentsForSession(c.id)
		}
		why := ""
		switch {
		// A "processing" status loaded from the database with no prompt
		// since this process started is stale, not a turn in flight.
		case c.status == SessionStatusProcessing && c.prompted:
			why = ResumeWhyTurn
		case len(running) > 0:
			why = ResumeWhySubagents
		default:
			continue
		}
		entry := ResumeSession{
			ID: c.id, Why: why, UpdatedAt: c.updatedAt.UTC(), InterruptedAt: now.UTC(),
			Auto: reason == ShutdownReasonUpdate,
		}
		for i, a := range running {
			if i == resumeMaxSubagents {
				break
			}
			entry.Subagents = append(entry.Subagents, ResumeSubagent{
				Description: resumeTrim(a.Description, resumeSubagentDescriptionMax),
				Prompt:      resumeTrim(a.Prompt, resumeSubagentPromptMax),
			})
		}
		snap.Sessions = append(snap.Sessions, entry)
		seen[c.id] = true
	}

	// Still offered from an earlier restart: keep offering (never auto).
	st := h.resume()
	st.mu.Lock()
	for _, p := range st.pending {
		if !seen[p.ID] {
			p.Auto = false
			snap.Sessions = append(snap.Sessions, p)
			seen[p.ID] = true
		}
	}
	st.mu.Unlock()

	sortResumeOldestFirst(snap.Sessions)
	if len(snap.Sessions) > resumeMaxSessions {
		snap.Sessions = snap.Sessions[len(snap.Sessions)-resumeMaxSessions:]
	}
	return snap
}

func sortResumeOldestFirst(list []ResumeSession) {
	slices.SortStableFunc(list, func(a, b ResumeSession) int {
		if c := a.UpdatedAt.Compare(b.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
}

// WriteResumeSnapshot writes the snapshot atomically with mode 0600, signed
// with secret (the connector encryption key). An empty snapshot removes the file.
func WriteResumeSnapshot(path string, snap *ResumeSnapshot, secret []byte) error {
	if snap == nil || len(snap.Sessions) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if key := resumeSigningKey(secret); key != nil {
		sig, err := snap.signature(key)
		if err != nil {
			return err
		}
		snap.Sig = sig
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".resume-after-restart-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ClaimResumeSnapshot reads and deletes the snapshot (each file is used at
// most once). A missing, unreadable, unsigned (when a key is set), wrongly
// signed or too old file yields nil.
func ClaimResumeSnapshot(path string, secret []byte, now time.Time) *ResumeSnapshot {
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logging.Warning("resume after restart: cannot read %s: %v", path, err)
			_ = os.Remove(path)
		}
		return nil
	}
	if err := os.Remove(path); err != nil {
		// Never act on a file that could be used again.
		logging.Warning("resume after restart: cannot remove %s, ignoring it: %v", path, err)
		return nil
	}
	if len(b) > 1<<20 {
		logging.Warning("resume after restart: snapshot too large, ignored")
		return nil
	}
	var snap ResumeSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		logging.Warning("resume after restart: unreadable snapshot ignored: %v", err)
		return nil
	}
	if snap.V != resumeSnapshotVersion {
		logging.Warning("resume after restart: snapshot version %d ignored", snap.V)
		return nil
	}
	if key := resumeSigningKey(secret); key != nil {
		want, err := snap.signature(key)
		if err != nil || !hmac.Equal([]byte(want), []byte(snap.Sig)) {
			logging.Warning("resume after restart: snapshot signature does not match, ignored")
			return nil
		}
	}
	age := now.Sub(snap.WrittenAt)
	if age < -time.Minute || age > resumeOfferMaxAge {
		logging.Info("resume after restart: snapshot from %s ignored (too old)", snap.WrittenAt.Format(time.RFC3339))
		return nil
	}
	switch snap.Reason {
	case ShutdownReasonUpdate, ShutdownReasonQuit, ShutdownReasonSignal:
	default:
		logging.Warning("resume after restart: unknown reason %q ignored", snap.Reason)
		return nil
	}
	if len(snap.Sessions) > resumeMaxSessions {
		snap.Sessions = snap.Sessions[:resumeMaxSessions]
	}
	return &snap
}

// ResumeOptions configures ApplyResumeSnapshot.
type ResumeOptions struct {
	Version    string // the running (new) version, for the notice
	AutoResume bool   // the resume_after_update setting
	Now        time.Time
}

// ResumePlan is what ApplyResumeSnapshot decided, for logs and tests.
type ResumePlan struct {
	Resume  []ResumeSession // continued now (after an update)
	Offered []ResumeSession // offered to the user
	Loops   []ResumeSession // loop sessions: notice only
}

// ApplyResumeSnapshot acts on a claimed snapshot: entries for sessions that no
// longer exist or have ended are dropped; a stale "processing" status goes back
// to idle (UpdatedAt untouched). Sessions stopped by an update are continued,
// oldest first and paced (when AutoResume is on); the others are offered.
// The resumes run in the background; the returned plan says what happens.
func (h *AgentHandler) ApplyResumeSnapshot(snap *ResumeSnapshot, opts ResumeOptions) ResumePlan {
	var plan ResumePlan
	if snap == nil || h.SessionManager == nil {
		return plan
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	sm := h.SessionManager
	freshUpdate := snap.Reason == ShutdownReasonUpdate && opts.Now.Sub(snap.WrittenAt) <= resumeAutoMaxAge

	seen := map[uuid.UUID]bool{}
	entries := append([]ResumeSession(nil), snap.Sessions...)
	sortResumeOldestFirst(entries)
	for _, e := range entries {
		if seen[e.ID] || e.ID == uuid.Nil {
			continue
		}
		seen[e.ID] = true
		if e.Why != ResumeWhyTurn && e.Why != ResumeWhySubagents {
			continue
		}
		if opts.Now.Sub(e.InterruptedAt) > resumeOfferMaxAge {
			continue
		}
		session, err := sm.GetSession(e.ID)
		if err != nil {
			continue // a forged or stale entry cannot name a new session
		}
		sm.mu.Lock()
		status := session.Status
		stale := status == SessionStatusProcessing
		if stale {
			session.Status = SessionStatusIdle // UpdatedAt stays: keep the order
		}
		loop := sm.isLoopMode(session)
		sm.mu.Unlock()
		if status == SessionStatusEnded {
			continue
		}
		if stale {
			sm.persistSessionState(session)
		}
		e.Subagents = sanitizeResumeSubagents(e.Subagents)

		switch {
		case e.Auto && freshUpdate && opts.AutoResume && loop:
			plan.Loops = append(plan.Loops, e)
		case e.Auto && freshUpdate && opts.AutoResume:
			plan.Resume = append(plan.Resume, e)
		default:
			e.Auto = false
			plan.Offered = append(plan.Offered, e)
		}
	}

	st := h.resume()
	st.mu.Lock()
	st.pending = append([]ResumeSession(nil), plan.Offered...)
	st.reason = snap.Reason
	delay := st.startDelay
	st.mu.Unlock()

	for _, e := range plan.Loops {
		if s, err := sm.GetSession(e.ID); err == nil {
			sm.emitSessionNotice(s, updateRestartNotice(opts.Version)+". This loop was stopped by the restart; start it again to continue.")
		}
	}
	if len(plan.Resume) > 0 {
		go func() {
			if delay > 0 {
				st.sleep(delay)
			}
			h.runResumes(plan.Resume, opts.Version, true)
		}()
	}
	logging.Info("🔄 Resume after restart (%s from %s): %d continued, %d offered, %d loop sessions",
		snap.Reason, snap.FromVersion, len(plan.Resume), len(plan.Offered), len(plan.Loops))
	return plan
}

func sanitizeResumeSubagents(in []ResumeSubagent) []ResumeSubagent {
	if len(in) > resumeMaxSubagents {
		in = in[:resumeMaxSubagents]
	}
	out := make([]ResumeSubagent, 0, len(in))
	for _, a := range in {
		out = append(out, ResumeSubagent{
			Description: resumeTrim(a.Description, resumeSubagentDescriptionMax),
			Prompt:      resumeTrim(a.Prompt, resumeSubagentPromptMax),
		})
	}
	return out
}

func updateRestartNotice(version string) string {
	if version == "" {
		return "🔄 Eggify updated and restarted"
	}
	return fmt.Sprintf("🔄 Eggify updated to %s and restarted", version)
}

// runResumes continues the sessions in order: batch at a time, gap apart.
func (h *AgentHandler) runResumes(list []ResumeSession, version string, afterUpdate bool) {
	st := h.resume()
	for i, e := range list {
		if i > 0 && st.batch > 0 && i%st.batch == 0 && st.gap > 0 {
			st.sleep(st.gap)
		}
		h.resumeOne(e, version, afterUpdate)
	}
}

func (h *AgentHandler) resumeOne(e ResumeSession, version string, afterUpdate bool) {
	sm := h.SessionManager
	session, err := sm.GetSession(e.ID)
	if err != nil {
		return
	}
	sm.mu.RLock()
	status := session.Status
	sm.mu.RUnlock()
	if status == SessionStatusEnded || status == SessionStatusProcessing {
		return // ended meanwhile, or the user already sent something
	}
	notice := "🔄 Eggify restarted; continuing where this session left off"
	if afterUpdate {
		notice = updateRestartNotice(version) + "; picking up where you left off"
	}
	sm.emitSessionNotice(session, notice)

	st := h.resume()
	st.mu.Lock()
	send := st.send
	st.mu.Unlock()
	if send == nil {
		send = func(id uuid.UUID, prompt string) error {
			session.keepUpdatedAtOnce.Store(true)
			err := sm.SendPromptSilent(id, prompt)
			if err != nil {
				session.keepUpdatedAtOnce.Store(false)
			}
			return err
		}
	}
	if err := send(e.ID, BuildResumePrompt(e, afterUpdate)); err != nil {
		logging.Warning("resume after restart: session %s: %v", e.ID, err)
	}
}

// BuildResumePrompt is the fixed text sent to a resumed session. The only
// variable parts are the interrupted subagents, framed as data.
func BuildResumePrompt(e ResumeSession, afterUpdate bool) string {
	var b strings.Builder
	if afterUpdate {
		b.WriteString("Eggify restarted to install an update while you were working. ")
	} else {
		b.WriteString("Eggify restarted while you were working, and the user asked you to continue. ")
	}
	b.WriteString("Any command or subagent that was running was stopped, and any pending permission request or question was dropped.\n")
	writeSubagentData(&b, resumeSubagentsTag, "These subagents were running and were stopped", e.Subagents)
	b.WriteString("\nCheck the current state first (for example git status and the files you were changing), then continue the task. Relaunch a stopped subagent only if its work is still needed. If the task was already done, say so briefly.")
	return b.String()
}

// PendingResumes returns the sessions offered for "Continue", oldest first,
// dropping ones that ended or expired.
func (h *AgentHandler) PendingResumes() (list []ResumeSession, reason string) {
	st := h.resume()
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	kept := st.pending[:0]
	for _, p := range st.pending {
		if now.Sub(p.InterruptedAt) > resumeOfferMaxAge {
			continue
		}
		if !h.sessionOpen(p.ID) {
			continue
		}
		kept = append(kept, p)
	}
	st.pending = kept
	return append([]ResumeSession(nil), kept...), st.reason
}

// takePending removes the offered sessions accepted by allow and returns them.
func (h *AgentHandler) takePending(allow func(uuid.UUID) bool) []ResumeSession {
	h.PendingResumes() // drop ended/expired ones first
	st := h.resume()
	st.mu.Lock()
	defer st.mu.Unlock()
	var taken, kept []ResumeSession
	for _, p := range st.pending {
		if allow == nil || allow(p.ID) {
			taken = append(taken, p)
		} else {
			kept = append(kept, p)
		}
	}
	st.pending = kept
	return taken
}

// ResumePending continues the offered sessions accepted by allow (the
// caller's visible sessions), the same way as after an update. It returns
// how many are continued.
func (h *AgentHandler) ResumePending(allow func(uuid.UUID) bool) int {
	list := h.takePending(allow)
	if len(list) > 0 {
		go h.runResumes(list, "", false)
	}
	return len(list)
}

// DismissPending drops the offered sessions accepted by allow.
func (h *AgentHandler) DismissPending(allow func(uuid.UUID) bool) int {
	return len(h.takePending(allow))
}

// WorkingSessionCount is how many sessions a restart would interrupt now.
func (h *AgentHandler) WorkingSessionCount(allow func(uuid.UUID) bool) int {
	snap := h.SnapshotWorkingSessions(ShutdownReasonQuit, "", time.Now())
	pending, _ := h.PendingResumes()
	offered := map[uuid.UUID]bool{}
	for _, p := range pending {
		offered[p.ID] = true
	}
	n := 0
	for _, s := range snap.Sessions {
		// Offers carried over from an earlier restart are not working.
		if offered[s.ID] && !s.InterruptedAt.Equal(snap.WrittenAt) {
			continue
		}
		if allow == nil || allow(s.ID) {
			n++
		}
	}
	return n
}

// sessionOpen reports whether the session exists and has not ended.
func (h *AgentHandler) sessionOpen(id uuid.UUID) bool {
	sm := h.SessionManager
	s, err := sm.GetSession(id)
	if err != nil {
		return false
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return s.Status != SessionStatusEnded
}

func resumeTrim(s string, limit int) string {
	return truncateRunes(strings.TrimSpace(s), limit)
}
