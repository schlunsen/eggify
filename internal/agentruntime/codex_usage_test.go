package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// The shape codex-cli 0.162 returns for a ChatGPT (prolite) sign-in.
const codexRateLimitsFixture = `{"rateLimits":{"limitId":"codex","primary":{"usedPercent":50,"windowDurationMins":10080,"resetsAt":1792310305},"secondary":null,"planType":"prolite","rateLimitReachedType":null},
"rateLimitsByLimitId":{"codex":{"limitId":"codex","primary":{"usedPercent":50,"windowDurationMins":10080,"resetsAt":1792310305},"secondary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":1792000000},"planType":"prolite","rateLimitReachedType":null}},
"ordinaryUsageAllowed":true}`

func TestParseCodexRateLimits(t *testing.T) {
	u, err := parseCodexRateLimits(json.RawMessage(codexRateLimitsFixture), time.Unix(1791000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if u.PlanType != "prolite" || u.LimitReached || len(u.Windows) != 2 {
		t.Fatalf("usage: %+v", u)
	}
	week := u.Windows[0]
	if week.UsedPercent != 50 || week.WindowMinutes != 10080 || week.ResetsAt == nil || week.ResetsAt.Unix() != 1792310305 {
		t.Errorf("weekly window: %+v", week)
	}

	reached, err := parseCodexRateLimits(json.RawMessage(`{"rateLimits":{"primary":{"usedPercent":100},"rateLimitReachedType":"rate_limit_reached"},"ordinaryUsageAllowed":false}`), time.Now())
	if err != nil || !reached.LimitReached {
		t.Errorf("reached: %+v %v", reached, err)
	}
	if _, err := parseCodexRateLimits(json.RawMessage(`{"rateLimits":{"primary":null,"secondary":null}}`), time.Now()); err == nil {
		t.Error("no windows should be an error")
	}
}

func TestCodexUsageIsCachedAndFailuresBackOff(t *testing.T) {
	d := NewDetector(nil)
	d.LookPath = func(string) (string, error) { return "/bin/codex", nil }
	calls := 0
	fail := false
	saved := readCodexRateLimits
	t.Cleanup(func() { readCodexRateLimits = saved })
	readCodexRateLimits = func(context.Context, string) (*CodexUsage, error) {
		calls++
		if fail {
			return nil, errors.New("not signed in with ChatGPT")
		}
		return parseCodexRateLimits(json.RawMessage(codexRateLimitsFixture), time.Now())
	}

	if u := d.CodexUsage(context.Background()); u == nil || calls != 1 {
		t.Fatalf("first read: %v, %d calls", u, calls)
	}
	d.CodexUsage(context.Background())
	if calls != 1 {
		t.Errorf("read again within the cache window: %d calls", calls)
	}

	// Expired: a failed read keeps the last good value and backs off.
	d.usageAt = time.Now().Add(-codexUsageMaxAge - time.Second)
	fail = true
	if u := d.CodexUsage(context.Background()); u == nil || calls != 2 {
		t.Errorf("failed read: %v, %d calls", u, calls)
	}
	d.CodexUsage(context.Background())
	if calls != 2 {
		t.Errorf("retried during the back-off: %d calls", calls)
	}
}
