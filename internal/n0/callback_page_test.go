package n0

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The result page hands back to the desktop app via eggify:// (html/template
// must not blank the scheme), and to the dashboard for browser users.
func TestRenderCallbackPageReturnsToDesktopApp(t *testing.T) {
	w := httptest.NewRecorder()
	RenderCallbackPage(w, FlowStatus{Status: "connected", ConnectionID: 7, ReturnTo: "eggify://connectors"})
	body := w.Body.String()
	if !strings.Contains(body, `href="eggify://connectors?n0_connected=7"`) {
		t.Fatalf("missing deep link back to the app:\n%s", body)
	}
	if strings.Contains(body, "ZgotmplZ") {
		t.Fatal("html/template blanked the eggify: link")
	}
	if !strings.Contains(body, "Switching back to the Eggify app") || !strings.Contains(body, "Open Eggify") {
		t.Fatalf("desktop wording missing:\n%s", body)
	}

	w = httptest.NewRecorder()
	RenderCallbackPage(w, FlowStatus{Status: "connected", ConnectionID: 7, ReturnTo: "https://eggify.example/connectors"})
	body = w.Body.String()
	if !strings.Contains(body, "Back to Eggify → Connectors") || strings.Contains(body, "Switching back") {
		t.Fatalf("browser wording wrong:\n%s", body)
	}
}
