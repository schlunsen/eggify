package toolguard

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// Environment variables that raise or lower the GPU spending caps. They are
// read by the Eggify server process only (agents cannot change a running server's
// environment).
const (
	EnvMaxGPUPodsPerSession = "EGGIFY_GPU_MAX_PODS_PER_SESSION"
	EnvMaxGPULaunchesPerDay = "EGGIFY_GPU_MAX_LAUNCHES_PER_DAY"
)

// Caps are simple GPU spending caps. Zero or negative means "no cap".
type Caps struct {
	MaxGPUPodsPerSession int `json:"max_gpu_pods_per_session"` // running GPU pods a session may have started
	MaxGPULaunchesPerDay int `json:"max_gpu_launches_per_day"` // GPU launches/resumes across all sessions per (local) day
}

// DefaultCaps: one running GPU pod per session, ten launches a day.
func DefaultCaps() Caps { return Caps{MaxGPUPodsPerSession: 1, MaxGPULaunchesPerDay: 10} }

// CapsFromEnv applies the env overrides to the defaults.
func CapsFromEnv() Caps {
	c := DefaultCaps()
	if n, err := strconv.Atoi(os.Getenv(EnvMaxGPUPodsPerSession)); err == nil {
		c.MaxGPUPodsPerSession = n
	}
	if n, err := strconv.Atoi(os.Getenv(EnvMaxGPULaunchesPerDay)); err == nil {
		c.MaxGPULaunchesPerDay = n
	}
	return c
}

// Limiter tracks GPU launches in memory. It is approximate on purpose: it
// counts approved launches and reported stops/terminations, and resets when
// the Eggify server restarts.
type Limiter struct {
	mu     sync.Mutex
	caps   Caps
	active map[string]int
	day    string
	today  int
	now    func() time.Time
}

// NewLimiter creates a limiter with the given caps.
func NewLimiter(c Caps) *Limiter {
	return &Limiter{caps: c, active: map[string]int{}, now: time.Now}
}

func (l *Limiter) rollDay() {
	if d := l.now().Format("2006-01-02"); d != l.day {
		l.day, l.today = d, 0
	}
}

// Reserve takes a GPU slot for a session, or explains which cap is reached.
// Call Unreserve if the launch does not happen (e.g. the user declines).
func (l *Limiter) Reserve(sessionID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollDay()
	if c := l.caps.MaxGPUPodsPerSession; c > 0 && l.active[sessionID] >= c {
		return fmt.Errorf("GPU cap reached: this session already started %d running GPU pod(s) (limit %d). Stop or terminate it first, or ask the user to raise %s on the Eggify server", l.active[sessionID], c, EnvMaxGPUPodsPerSession)
	}
	if c := l.caps.MaxGPULaunchesPerDay; c > 0 && l.today >= c {
		return fmt.Errorf("GPU cap reached: %d GPU launches today (limit %d). Ask the user to raise %s on the Eggify server", l.today, c, EnvMaxGPULaunchesPerDay)
	}
	l.active[sessionID]++
	l.today++
	return nil
}

// Unreserve gives back a slot taken by Reserve that was not used.
func (l *Limiter) Unreserve(sessionID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active[sessionID] > 0 {
		l.active[sessionID]--
	}
	if l.today > 0 {
		l.today--
	}
}

// Release records that a session stopped or terminated a GPU pod.
func (l *Limiter) Release(sessionID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active[sessionID] > 0 {
		l.active[sessionID]--
	}
	if l.active[sessionID] == 0 {
		delete(l.active, sessionID)
	}
}

// Caps returns the limiter's caps.
func (l *Limiter) Caps() Caps { return l.caps }

// Active returns the running GPU pods a session has started.
func (l *Limiter) Active(sessionID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active[sessionID]
}
