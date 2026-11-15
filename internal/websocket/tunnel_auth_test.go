package websocket

import "testing"

func TestRedTeam_RemoteAccess_HubAuthorize(t *testing.T) {
	h := NewHub()
	h.SetAPIKey("k3y")
	h.SetSessionValidator(func(tok string) bool { return tok == "sess-ok" })

	cases := []struct {
		name      string
		tokens    []string
		viaTunnel bool
		want      authResult
	}{
		{"api key locally", []string{"k3y", ""}, false, authOK},
		{"api key over tunnel", []string{"k3y", ""}, true, authAPIKeyOverTunnel},
		{"api key + session cookie over tunnel", []string{"k3y", "sess-ok"}, true, authOK},
		{"session over tunnel", []string{"", "sess-ok"}, true, authOK},
		{"session locally", []string{"sess-ok"}, false, authOK},
		{"nothing over tunnel", []string{"", ""}, true, authInvalid},
		{"bad token locally", []string{"nope"}, false, authInvalid},
	}
	for _, c := range cases {
		if got := h.authorize(c.tokens, c.viaTunnel); got != c.want {
			t.Errorf("%s: authorize = %v, want %v", c.name, got, c.want)
		}
	}

	// Without a session validator nothing works over the tunnel.
	h2 := NewHub()
	h2.SetAPIKey("k3y")
	if got := h2.authorize([]string{"k3y"}, true); got != authAPIKeyOverTunnel {
		t.Errorf("no validator, api key over tunnel = %v", got)
	}
}
