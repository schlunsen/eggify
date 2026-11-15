package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// TestRedTeam_CookieRequestsNeedAnOwnOrigin: a page on another origin (or a
// same-site app such as a deployed sandbox app) can make the browser send the
// session cookie with a form post. State changes authenticated by the cookie
// must name this server in Origin or Referer; Bearer requests are unaffected.
func TestRedTeam_CookieRequestsNeedAnOwnOrigin(t *testing.T) {
	us := NewInMemoryUserStore()
	us.users["alice"] = &User{Username: "alice"}
	us.apiKey = "the-api-key"
	token := "sess-ok"
	PutTestSession(us, token, &Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour)})
	sam := NewSessionAuthMiddleware(us, true, false)
	sam.SetAllowedOrigins([]string{"https://dash.example.com"})
	app := fiber.New()
	app.Use(sam.Handler())
	reached := func(c *fiber.Ctx) error { return c.SendString("ok") }
	app.Post("/api/things", reached)
	app.Get("/api/things", reached)
	app.Post("/api/auth/logout", reached)
	app.Put("/api/*", reached)

	cases := []struct {
		name, method, path string
		header             map[string]string
		cookie, bearer     bool
		bearerTok          string // overrides the session token as the Bearer value
		want               int
	}{
		{"cross-site origin", "POST", "/api/things", map[string]string{"Origin": "https://evil.example"}, true, false, "", 403},
		{"null origin", "POST", "/api/things", map[string]string{"Origin": "null"}, true, false, "", 403},
		{"no origin, no referer", "POST", "/api/things", nil, true, false, "", 403},
		{"cross-site referer only", "POST", "/api/things", map[string]string{"Referer": "https://evil.example/x"}, true, false, "", 403},
		{"other port is another origin", "POST", "/api/things", map[string]string{"Origin": "http://127.0.0.1:8080"}, true, false, "", 403},
		{"logout from another site", "POST", "/api/auth/logout", map[string]string{"Origin": "https://evil.example"}, true, false, "", 403},
		{"own origin", "POST", "/api/things", map[string]string{"Origin": "http://127.0.0.1:3335"}, true, false, "", 200},
		{"own referer only", "POST", "/api/things", map[string]string{"Referer": "http://127.0.0.1:3335/agents"}, true, false, "", 200},
		{"configured origin", "POST", "/api/things", map[string]string{"Origin": "https://dash.example.com"}, true, false, "", 200},
		{"GET needs no origin", "GET", "/api/things", nil, true, false, "", 200},
		{"bearer without origin (iOS)", "POST", "/api/things", nil, false, true, "", 200},
		{"bearer from another origin", "POST", "/api/things", map[string]string{"Origin": "https://evil.example"}, false, true, "", 200},
		{"cookie + valid Bearer session (iOS)", "POST", "/api/things", nil, true, true, "", 200},
		{"cookie + API-key Bearer does not skip the check", "POST", "/api/things", nil, true, true, "the-api-key", 403},
		{"cookie + API-key Bearer, own origin", "POST", "/api/things", map[string]string{"Origin": "http://127.0.0.1:3335"}, true, true, "the-api-key", 200},
		{"dot segments fail closed", "PUT", "/api/settings/..", map[string]string{"Origin": "https://evil.example"}, true, false, "", 403},
		{"dot segments, no origin", "PUT", "/api/x/../y", nil, true, false, "", 403},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:3335"+tc.path, nil)
		for k, v := range tc.header {
			r.Header.Set(k, v)
		}
		if tc.cookie {
			r.AddCookie(&http.Cookie{Name: "session_token", Value: token})
		}
		if tc.bearer {
			bt := token
			if tc.bearerTok != "" {
				bt = tc.bearerTok
			}
			r.Header.Set("Authorization", "Bearer "+bt)
		}
		resp, err := app.Test(r)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
	}
}
