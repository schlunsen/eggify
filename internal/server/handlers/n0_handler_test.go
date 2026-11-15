package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/connectors"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/n0"
	"github.com/schlunsen/eggify/internal/n0/n0test"
)

func TestN0HandlerConnectBindDisconnect(t *testing.T) {
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.ResetInstance()
	key := make([]byte, 32)
	m := n0.NewManager(db, key)
	ls, err := n0.StartLoopback(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ls.Close()
	fake := n0test.New()
	defer fake.Close()

	h := NewN0Handler(m, db, key, ls.CallbackURL)
	stale := 0
	h.SetSessionHooks(nil, func(uuid.UUID) { stale++ })
	app := fiber.New()
	api := app.Group("/api")
	h.Register(api)
	app.Get(n0.CallbackPath, h.HandleCallback)

	do := func(method, path, body string) (int, map[string]interface{}) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Host = "localhost:3333"
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		for _, sec := range fake.Secrets() {
			if strings.Contains(string(b), sec) {
				t.Fatalf("%s %s leaks a token", method, path)
			}
		}
		var out map[string]interface{}
		_ = json.Unmarshal(b, &out)
		return resp.StatusCode, out
	}

	code, ov := do("GET", "/api/connectors/n0", "")
	if code != 200 || len(ov["hosts"].([]interface{})) != len(n0.KnownHosts) || ov["legacy"].(map[string]interface{})["configured"] != false {
		t.Fatalf("overview: %d %v", code, ov)
	}

	code, st := do("POST", "/api/connectors/n0/oauth/start", `{"host":"`+fake.URL+`","return_to":"http://localhost:3333/connectors"}`)
	if code != 200 {
		t.Fatalf("start: %d %v", code, st)
	}
	authURL := st["authorize_url"].(string)
	if !strings.Contains(authURL, "redirect_uri="+strings.ReplaceAll(ls.CallbackURL(), ":", "%3A")) && !strings.Contains(authURL, "127.0.0.1") {
		t.Fatalf("loopback redirect not used: %s", authURL)
	}
	_, s1 := do("GET", "/api/connectors/n0/oauth/status?state="+st["state"].(string), "")
	if s1["status"] != "pending" {
		t.Fatalf("status: %v", s1)
	}
	loc, err := fake.Approve(authURL, "demo-app")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(loc)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "n0 connected") || !strings.Contains(string(page), "http://localhost:3333/connectors?n0_connected=") {
		t.Fatalf("callback page: %s", page)
	}
	_, s2 := do("GET", "/api/connectors/n0/oauth/status?state="+st["state"].(string), "")
	if s2["status"] != "connected" {
		t.Fatalf("status after callback: %v", s2)
	}
	connID := int64(s2["connection_id"].(float64))

	_, ov = do("GET", "/api/connectors/n0", "")
	conns := ov["connections"].([]interface{})
	c0 := conns[0].(map[string]interface{})
	if len(conns) != 1 || c0["app_subdomain"] != "demo-app" || c0["workspace_name"] != "Acme" || c0["status"] != "active" ||
		!strings.HasSuffix(c0["connected_tools_url"].(string), "/api-tokens") {
		t.Fatalf("connections: %v", conns)
	}

	// Session binding.
	sid := uuid.NewString()
	db.GetDB().Exec(`INSERT INTO agent_sessions (id, status) VALUES (?, 'idle')`, sid)
	_, ss := do("GET", "/api/connectors/n0/sessions/"+sid, "")
	if ss["enabled"] != false {
		t.Fatalf("session state: %v", ss)
	}
	code, ss = do("PUT", "/api/connectors/n0/sessions/"+sid, `{"connection_id":`+jsonInt(connID)+`}`)
	if code != 200 || ss["enabled"] != true || int64(ss["connection_id"].(float64)) != connID || stale != 1 {
		t.Fatalf("bind: %d %v stale=%d", code, ss, stale)
	}
	_, bs := do("GET", "/api/connectors/n0/bindings", "")
	b := bs["bindings"].(map[string]interface{})[sid].(map[string]interface{})
	if b["app_subdomain"] != "demo-app" {
		t.Fatalf("bindings: %v", bs)
	}
	code, ss = do("PUT", "/api/connectors/n0/sessions/"+sid, `{"connection_id":null}`)
	if code != 200 || ss["enabled"] != false {
		t.Fatalf("unbind: %v", ss)
	}

	// Public mode over plain http is refused with the admin hint.
	code, pe := do("POST", "/api/connectors/n0/oauth/start", `{"host":"`+fake.URL+`","redirect_mode":"public"}`)
	if code != 400 || pe["error_code"] != "invalid_redirect_uri" || pe["hint"] == "" {
		t.Fatalf("public over http: %d %v", code, pe)
	}

	// Disconnect.
	code, dc := do("DELETE", "/api/connectors/n0/connections/"+jsonInt(connID), "")
	if code != 200 || dc["revoked_at_n0"] != true {
		t.Fatalf("disconnect: %d %v", code, dc)
	}
	_, ov = do("GET", "/api/connectors/n0", "")
	if len(ov["connections"].([]interface{})) != 0 {
		t.Fatal("connection still listed")
	}
}

func TestLegacyN0KeyOnlyForExistingUsers(t *testing.T) {
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.ResetInstance()
	key := make([]byte, 32)
	repo := database.NewRepository(db)
	h := NewConnectorHandler(repo, key)
	app := fiber.New()
	app.Post("/api/connectors/:slug/connect", h.HandleConnect)
	app.Delete("/api/connectors/:slug/disconnect", h.HandleDisconnect)

	post := func() int {
		req := httptest.NewRequest("POST", "/api/connectors/n0/connect", strings.NewReader(`{"api_key":"n0-key"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req, -1)
		return resp.StatusCode
	}
	if code := post(); code != 400 {
		t.Fatalf("new legacy key accepted: %d", code)
	}
	enc, _ := connectors.Encrypt("old", key)
	repo.Connector.SaveConnection(&database.UserConnection{ConnectorSlug: "n0", APIKey: &enc, Status: "active"})
	if code := post(); code != 200 {
		t.Fatalf("existing legacy user cannot update key: %d", code)
	}

	// With OAuth connections, removing the legacy key keeps the marker row.
	repo.N0.CreateN0Connection(&database.N0Connection{Host: "h", Issuer: "i", Resource: "r", ClientID: "c", RedirectURI: "u", TokenEndpoint: "t"})
	req := httptest.NewRequest("DELETE", "/api/connectors/n0/disconnect", nil)
	if resp, _ := app.Test(req, -1); resp.StatusCode != 200 {
		t.Fatalf("disconnect: %d", resp.StatusCode)
	}
	uc, _ := repo.Connector.GetConnectionBySlug("n0")
	if uc == nil || (uc.APIKey != nil && *uc.APIKey != "") {
		t.Fatalf("expected keyless marker, got %+v", uc)
	}
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }

// From the desktop app the consent page runs in the system browser, which has
// no session on the app's loopback server, so it returns to the app through
// its eggify:// deep link. Only exactly eggify://connectors is accepted.
func TestN0SafeReturnToDesktopDeepLink(t *testing.T) {
	h := &N0Handler{}
	origin := "http://127.0.0.1:3335"
	cases := map[string]string{
		"eggify://connectors":               "eggify://connectors",
		"eggify://connectors?x=1":           "eggify://connectors",
		"eggify://agents":                   origin + "/connectors",
		"eggify://connectors/../api/config": origin + "/connectors",
		"eggify://user@connectors":          origin + "/connectors",
		"evil://connectors":                 origin + "/connectors",
		"https://evil.example/connectors":   origin + "/connectors",
		origin + "/connectors?tab=n0":       origin + "/connectors?tab=n0",
		"":                                  origin + "/connectors",
	}
	for in, want := range cases {
		if got := h.safeReturnTo(in, origin); got != want {
			t.Errorf("safeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}
