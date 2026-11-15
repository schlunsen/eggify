package remoteaccess

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeNgrokErr struct{ code, msg string }

func (e fakeNgrokErr) Error() string     { return e.msg + "\n\n" + e.code }
func (e fakeNgrokErr) Msg() string       { return e.msg + "\nmore detail" }
func (e fakeNgrokErr) ErrorCode() string { return e.code }

func TestNgrokErrorClassification(t *testing.T) {
	bad := fmt.Errorf("authentication failed: %w", fakeNgrokErr{"ERR_NGROK_105", "The authtoken you specified is invalid."})
	if !isNgrokRejection(bad) {
		t.Fatal("ngrok-coded error not treated as a rejection")
	}
	msg := cleanNgrokError(bad).Error()
	if !strings.Contains(msg, "ERR_NGROK_105") || !strings.Contains(msg, "dashboard.ngrok.com") || strings.Contains(msg, "\n") {
		t.Fatalf("clean message = %q", msg)
	}
	if isNgrokRejection(errors.New("failed to dial ngrok server with address \"connect.ngrok-agent.com:443\": dial tcp: lookup failed")) {
		t.Fatal("network error treated as permanent")
	}
	if !isNgrokRejection(errors.New("authentication failed: bad token")) {
		t.Fatal("remote auth failure not permanent")
	}
	if got := cleanNgrokError(fakeNgrokErr{"ERR_NGROK_320", "domain not reserved"}).Error(); !strings.Contains(got, "domain not reserved (ERR_NGROK_320)") {
		t.Fatalf("generic code message = %q", got)
	}
}

func TestTunnelConfigBuilds(t *testing.T) {
	for _, p := range []Protection{
		{Mode: ProtectionNone},
		{Mode: ProtectionOAuth, OAuthProvider: "google", AllowEmails: []string{"a@b.co"}, AllowDomains: []string{"b.co"}},
		{Mode: ProtectionBasic, BasicUser: "me", BasicPassword: "password123"},
	} {
		if cfg := tunnelConfig(OpenConfig{Domain: "me.ngrok.app", Protection: p}); cfg == nil {
			t.Fatalf("nil config for %+v", p)
		}
	}
}

func TestAgentLimitErrorKeepsCodeAndExplains(t *testing.T) {
	err := fmt.Errorf("authentication failed: %w", fakeNgrokErr{ErrCodeAgentLimit, "Your account is limited to 3 simultaneous ngrok agent sessions."})
	if !isNgrokRejection(err) {
		t.Fatal("108 must be permanent (retrying only burns more attempts)")
	}
	pe := newNgrokPermanentError(err)
	if pe.Code != ErrCodeAgentLimit {
		t.Fatalf("code = %q", pe.Code)
	}
	msg := pe.Error()
	for _, want := range []string{"limited to 3", "ERR_NGROK_108", "dashboard.ngrok.com/agents", "API key"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q lacks %q", msg, want)
		}
	}
}
