package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Codex's plan usage (ChatGPT sign-in), as codex app-server reports it with
// account/rateLimits/read: up to two windows (e.g. 5 hours and a week), each
// with the percentage used and when it resets. An API-key sign-in has no plan
// limits and gets nothing.

// UsageWindow is one rate-limit window.
type UsageWindow struct {
	UsedPercent   int        `json:"used_percent"`
	WindowMinutes int64      `json:"window_minutes,omitempty"`
	ResetsAt      *time.Time `json:"resets_at,omitempty"`
}

// CodexUsage is the account's latest Codex usage.
type CodexUsage struct {
	PlanType string        `json:"plan_type,omitempty"`
	Windows  []UsageWindow `json:"windows"`
	// LimitReached: Codex refuses ordinary use until a window resets.
	LimitReached bool      `json:"limit_reached"`
	ObservedAt   time.Time `json:"observed_at"`
}

const (
	codexUsageMaxAge   = 2 * time.Minute
	codexUsageRetryErr = 5 * time.Minute
)

type rateLimitWindowJSON struct {
	UsedPercent        int    `json:"usedPercent"`
	WindowDurationMins *int64 `json:"windowDurationMins"`
	ResetsAt           *int64 `json:"resetsAt"`
}

type rateLimitSnapshotJSON struct {
	PlanType             *string              `json:"planType"`
	Primary              *rateLimitWindowJSON `json:"primary"`
	Secondary            *rateLimitWindowJSON `json:"secondary"`
	RateLimitReachedType *string              `json:"rateLimitReachedType"`
}

// parseCodexRateLimits reads an account/rateLimits/read result.
func parseCodexRateLimits(raw json.RawMessage, now time.Time) (*CodexUsage, error) {
	var res struct {
		RateLimits           *rateLimitSnapshotJSON           `json:"rateLimits"`
		RateLimitsByLimitID  map[string]rateLimitSnapshotJSON `json:"rateLimitsByLimitId"`
		OrdinaryUsageAllowed *bool                            `json:"ordinaryUsageAllowed"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	snap := res.RateLimits
	if s, ok := res.RateLimitsByLimitID["codex"]; ok {
		snap = &s
	}
	if snap == nil {
		return nil, errors.New("no rate limits reported")
	}
	u := &CodexUsage{Windows: []UsageWindow{}, ObservedAt: now.UTC()}
	if snap.PlanType != nil {
		u.PlanType = *snap.PlanType
	}
	for _, w := range []*rateLimitWindowJSON{snap.Primary, snap.Secondary} {
		if w == nil {
			continue
		}
		win := UsageWindow{UsedPercent: w.UsedPercent}
		if w.WindowDurationMins != nil {
			win.WindowMinutes = *w.WindowDurationMins
		}
		if w.ResetsAt != nil && *w.ResetsAt > 0 {
			t := time.Unix(*w.ResetsAt, 0).UTC()
			win.ResetsAt = &t
		}
		u.Windows = append(u.Windows, win)
	}
	if len(u.Windows) == 0 {
		return nil, errors.New("no rate-limit windows reported")
	}
	u.LimitReached = snap.RateLimitReachedType != nil ||
		(res.OrdinaryUsageAllowed != nil && !*res.OrdinaryUsageAllowed)
	return u, nil
}

// readCodexRateLimits asks codex app-server for the account's usage.
var readCodexRateLimits = func(ctx context.Context, path string) (*CodexUsage, error) {
	app, err := startCodexApp(ctx, path)
	if err != nil {
		return nil, err
	}
	defer app.Close()
	var raw json.RawMessage
	if err := app.Call(ctx, "account/rateLimits/read", map[string]any{}, &raw); err != nil {
		return nil, err
	}
	return parseCodexRateLimits(raw, time.Now())
}

// CodexUsage returns the account's Codex usage, read at most every two
// minutes (a failure, e.g. an API-key sign-in or no codex, every five). nil
// when it isn't known.
func (d *Detector) CodexUsage(ctx context.Context) *CodexUsage {
	d.usageMu.Lock()
	defer d.usageMu.Unlock()
	now := time.Now()
	if d.usage != nil && now.Sub(d.usageAt) < codexUsageMaxAge {
		return d.usage
	}
	if !d.usageErrAt.IsZero() && now.Sub(d.usageErrAt) < codexUsageRetryErr {
		return d.usage
	}
	path, _ := d.locate(Codex)
	if path == "" {
		d.usageErrAt = now
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	u, err := readCodexRateLimits(ctx, path)
	if err != nil {
		d.usageErrAt = now
		return d.usage // keep the last good one
	}
	d.usage, d.usageAt, d.usageErrAt = u, now, time.Time{}
	return u
}
