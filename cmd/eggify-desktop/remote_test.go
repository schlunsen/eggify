package main

import (
	"strings"
	"testing"

	"github.com/schlunsen/eggify/internal/remoteaccess"
)

func TestTrayToggleReflectsStatus(t *testing.T) {
	cases := []struct {
		st      remoteaccess.Status
		label   string
		checked bool
	}{
		{remoteaccess.Status{Status: remoteaccess.StateDisconnected}, "Remote access: Off", false},
		{remoteaccess.Status{Status: remoteaccess.StateConnecting, Running: true}, "Remote access: Connecting…", true},
		{remoteaccess.Status{Status: remoteaccess.StateConnected, Running: true, PublicURL: "https://x.ngrok.app"}, "Remote access: On", true},
		{remoteaccess.Status{Status: remoteaccess.StateError, Error: "bad token"}, "Remote access: Failed (click to retry)", false},
	}
	for _, c := range cases {
		if got := trayToggleLabel(c.st); got != c.label {
			t.Errorf("label(%s) = %q, want %q", c.st.Status, got, c.label)
		}
		if got := trayToggleChecked(c.st); got != c.checked {
			t.Errorf("checked(%s) = %v, want %v", c.st.Status, got, c.checked)
		}
	}
}

func TestTrayNotifications(t *testing.T) {
	off := remoteaccess.Status{Status: remoteaccess.StateDisconnected}
	connecting := remoteaccess.Status{Status: remoteaccess.StateConnecting, Running: true}
	on := remoteaccess.Status{Status: remoteaccess.StateConnected, Running: true, PublicURL: "https://x.ngrok.app"}
	reconnecting := on
	reconnecting.Status = remoteaccess.StateConnecting // session dropped, URL kept
	failed := remoteaccess.Status{Status: remoteaccess.StateError, Error: "ngrok rejected the authtoken"}

	if title, body, ok := trayNotification(connecting, on); !ok || title != "Remote access is on" || body != on.PublicURL {
		t.Fatalf("connect: %q %q %v", title, body, ok)
	}
	if _, _, ok := trayNotification(on, on); ok {
		t.Fatal("repeated status notified twice")
	}
	if _, _, ok := trayNotification(reconnecting, on); ok {
		t.Fatal("reconnect at the same URL should stay quiet")
	}
	if title, body, ok := trayNotification(connecting, failed); !ok || title != "Remote access failed" || body != failed.Error {
		t.Fatalf("failure: %q %q %v", title, body, ok)
	}
	if _, _, ok := trayNotification(failed, failed); ok {
		t.Fatal("same failure notified twice")
	}
	if _, _, ok := trayNotification(on, off); ok {
		t.Fatal("switching off should not notify")
	}
}

func TestTrayAgentLimitNotificationSaysWhatToDo(t *testing.T) {
	connecting := remoteaccess.Status{Status: remoteaccess.StateConnecting, Running: true}
	limit := remoteaccess.Status{Status: remoteaccess.StateError, ErrorCode: remoteaccess.ErrCodeAgentLimit,
		Error: "ngrok: Your account is limited to 3 simultaneous ngrok agent sessions (ERR_NGROK_108)"}
	title, body, ok := trayNotification(connecting, limit)
	if !ok || title != "ngrok agent limit reached" || !strings.Contains(body, "Remote access settings") {
		t.Fatalf("108: %q %q %v", title, body, ok)
	}
}

func TestNotificationOpenPathOnlySettings(t *testing.T) {
	if p, ok := notificationOpenPath(map[string]interface{}{notificationOpenKey: remoteSettingsPath}); !ok || p != "/settings#access" {
		t.Fatalf("settings path refused: %q %v", p, ok)
	}
	for _, bad := range []interface{}{"https://evil.test", "//evil.test", "/api/tunnel/start", "/settingsx", 42, nil} {
		if _, ok := notificationOpenPath(map[string]interface{}{notificationOpenKey: bad}); ok {
			t.Fatalf("%v accepted", bad)
		}
	}
	if _, ok := notificationOpenPath(nil); ok {
		t.Fatal("nil userInfo accepted")
	}
}
