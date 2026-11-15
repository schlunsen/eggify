package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func runtimeCall(h http.Handler, marker string) int {
	r := httptest.NewRequest("POST", "/wails/runtime", strings.NewReader(`{"object":0,"method":0}`))
	if marker != "" {
		r.Header.Set(bridgeMarkerHeader, marker)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code
}

// Wails serves /wails/runtime to every page of the webview over wails:// with no
// origin check. Without the per-launch marker the call endpoint does not exist.
func TestRedTeam_Desktop_RuntimeEndpointNeedsTheBridgeMarker(t *testing.T) {
	reached := 0
	gate := gateRuntimeCalls("launch-marker", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached++ }))
	for _, m := range []string{"", "guess", "launch-marker ", "LAUNCH-MARKER", "launch-marke"} {
		if c := runtimeCall(gate, m); c != http.StatusNotFound {
			t.Errorf("marker %q: %d, want 404", m, c)
		}
	}
	if reached != 0 {
		t.Fatalf("%d calls reached the runtime without the marker", reached)
	}
	if runtimeCall(gate, "launch-marker") != 200 || reached != 1 {
		t.Fatal("the bridge's own call was refused")
	}
	// Static runtime assets are not the call endpoint.
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, httptest.NewRequest("GET", "/wails/runtime.js", nil))
	if rec.Code != 200 || reached != 2 {
		t.Errorf("runtime.js: %d", rec.Code)
	}
}

// The bridge (this Mac's window and the machine proxy's /wails) deletes any
// marker a page sends and adds the real one, so a page can neither supply nor
// learn it.
func TestRedTeam_Desktop_BridgeReplacesAPageSuppliedMarker(t *testing.T) {
	tr := newBridgeTransport()
	var seen []string
	var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.Header.Values(bridgeMarkerHeader) })
	h = gateRuntimeCalls(tr.marker, h)
	tr.assets.Store(&h)
	b := &bridge{transport: tr}

	r := httptest.NewRequest("POST", "/wails/runtime", strings.NewReader(`{}`))
	r.Header.Set(bridgeMarkerHeader, "page-chosen")
	r.Header.Add(bridgeMarkerHeader, "second")
	rec := httptest.NewRecorder()
	b.serveWails(rec, r)
	if len(seen) != 1 || seen[0] != tr.marker {
		t.Fatalf("downstream marker values %v", seen)
	}
	if strings.Contains(rec.Body.String(), tr.marker) {
		t.Fatal("marker leaked into a response")
	}
	// A page-supplied marker alone opens nothing (the gate, not the bridge).
	if runtimeCall(h, "page-chosen") != http.StatusNotFound {
		t.Fatal("a page-supplied marker was accepted")
	}
	// The marker is random per launch.
	if newBridgeTransport().marker == tr.marker || len(tr.marker) < 32 {
		t.Fatal("marker is not random")
	}
	// The transport really exposes the gated handler to Wails.
	var _ interface {
		Handler() func(http.Handler) http.Handler
	} = tr
}
