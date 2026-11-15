package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func terminalUpgradeRequest(query string) *http.Request {
	req := httptest.NewRequest("GET", "/api/agent/sessions/s1/terminal/ws"+query, nil)
	req.Host = "localhost:3333"
	req.Header.Set("Origin", "https://localhost:3333")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	return req
}

func TestTerminalWebSocketRejectsInvalidToken(t *testing.T) {
	validate := func(tok string) bool { return tok == "valid" }

	cases := []struct {
		name     string
		query    string
		validate func(string) bool
		want     int
	}{
		{"no token", "", validate, fiber.StatusUnauthorized},
		{"arbitrary token", "?token=x", validate, fiber.StatusUnauthorized},
		{"nil validator fails closed", "?token=valid", nil, fiber.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			registerTerminalWebSocketRoute(app, nil, nil, tc.validate, nil)
			resp, err := app.Test(terminalUpgradeRequest(tc.query))
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestTerminalWebSocketValidTokenPassesAuth(t *testing.T) {
	app := fiber.New()
	var seen string
	registerTerminalWebSocketRoute(app, nil, nil, func(tok string) bool { seen = tok; return tok == "valid" }, nil)
	resp, err := app.Test(terminalUpgradeRequest("?token=valid"))
	if err != nil {
		t.Fatal(err)
	}
	if seen != "valid" {
		t.Fatalf("validator saw %q", seen)
	}
	if resp.StatusCode == fiber.StatusUnauthorized || resp.StatusCode == fiber.StatusForbidden {
		t.Fatalf("valid token rejected with %d", resp.StatusCode)
	}
}
