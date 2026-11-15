package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func newTestBridge(t *testing.T) (*bridge, *fiber.App) {
	t.Helper()
	tr := newBridgeTransport()
	_ = tr.ServeAssets(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("cookies must not be forwarded to Wails")
		}
		_, _ = io.WriteString(w, "wails:"+r.URL.Path)
	}))
	b, err := newBridge(3335, tr)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	b.routes(app)
	return b, app
}

func TestBootstrapRequiresToken(t *testing.T) {
	b, app := newTestBridge(t)

	resp, _ := app.Test(httptest.NewRequest("GET", bootstrapPath+"?t=wrong", nil))
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("wrong token: status %d", resp.StatusCode)
	}

	resp, _ = app.Test(httptest.NewRequest("GET", strings.TrimPrefix(b.bootstrapURL("/agents"), b.origin), nil))
	if resp.StatusCode != fiber.StatusFound || resp.Header.Get("Location") != "/agents" {
		t.Fatalf("bootstrap: status %d location %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	var bridgeSet, markerSet bool
	for _, c := range resp.Cookies() {
		switch c.Name {
		case bridgeCookie:
			bridgeSet = c.Value == b.token && c.HttpOnly && c.Path == "/wails"
		case markerCookie:
			markerSet = c.Value == "wails" && !c.HttpOnly
		}
	}
	if !bridgeSet || !markerSet {
		t.Fatalf("cookies not set as expected: %v", resp.Cookies())
	}
}

func TestWailsRoutesNeedBridgeCookie(t *testing.T) {
	b, app := newTestBridge(t)

	resp, _ := app.Test(httptest.NewRequest("GET", "/wails/runtime.js", nil))
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("no cookie: status %d", resp.StatusCode)
	}

	req := httptest.NewRequest("POST", "/wails/runtime", nil)
	req.Header.Set("Cookie", bridgeCookie+"="+b.token)
	req.Header.Set("Origin", "http://evil.example")
	resp, _ = app.Test(req)
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("foreign origin: status %d", resp.StatusCode)
	}

	req = httptest.NewRequest("GET", "/wails/runtime.js", nil)
	req.Header.Set("Cookie", bridgeCookie+"="+b.token)
	req.Header.Set("Origin", b.origin)
	resp, _ = app.Test(req)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "wails:/wails/runtime.js" {
		t.Fatalf("forward: status %d body %q", resp.StatusCode, body)
	}
}

func TestSafeNextAndDeepLinks(t *testing.T) {
	for in, want := range map[string]string{
		"":                    "/",
		"/agents":             "/agents",
		"/agents?session=1":   "/agents?session=1",
		"//evil.example":      "/",
		"https://evil":        "/",
		"/api/config/api-key": "/",
		"/wails/runtime":      "/",
		"/__desktop/x":        "/",
		"/agent/ws":           "/",
		"/a\\b":               "/",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
	if p, ok := deepLinkPath("eggify://skills"); !ok || p != "/skills" {
		t.Errorf("deep link skills: %q %v", p, ok)
	}
	if p, ok := deepLinkPath("eggify://agents?session=abc"); !ok || p != "/agents?session=abc" {
		t.Errorf("deep link agents: %q %v", p, ok)
	}
	if p, ok := deepLinkPath("eggify://api/config/api-key"); !ok || p != "/" {
		t.Errorf("deep link to API must land on /: %q", p)
	}
	if _, ok := deepLinkPath("https://example.com"); ok {
		t.Error("non-eggify URL accepted")
	}
}

// ⌘, (Eggify → Settings…) opens the settings page.
func TestSettingsShortcutPath(t *testing.T) {
	if got := safeNext(settingsPath); got != "/settings" {
		t.Fatalf("safeNext(%q) = %q", settingsPath, got)
	}
}
