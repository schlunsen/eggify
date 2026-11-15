package main

import (
	"errors"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/schlunsen/eggify/internal/remoteaccess"
)

// remoteTray puts remote access (ngrok) in the tray menu: an on/off toggle,
// "Copy public URL", and native notifications when the tunnel connects or
// fails. It talks to the in-process server's remoteaccess.Manager directly,
// not over HTTP with the API key: the tray is native UI only the person at
// the machine can click, like the Settings page they are signed in to.
//
// The menu exists before the server is up; attach() wires the manager once
// Setup has run (before Serve, so an autostarted tunnel is reported too).
type remoteTray struct {
	app      *application.App
	toggle   *application.MenuItem
	copyURL  *application.MenuItem
	openPath func(string)
	notify   func(title, body, open string)

	mu      sync.Mutex
	mgr     *remoteaccess.Manager
	last    remoteaccess.Status
	updates chan remoteaccess.Status
}

func newRemoteTray(app *application.App, m *application.Menu, openPath func(string), notify func(title, body, open string)) *remoteTray {
	rt := &remoteTray{app: app, openPath: openPath, notify: notify, updates: make(chan remoteaccess.Status, 16)}
	rt.toggle = m.AddCheckbox(trayToggleLabel(remoteaccess.Status{Status: remoteaccess.StateDisconnected}), false)
	rt.toggle.SetEnabled(false) // until the server is up
	rt.toggle.OnClick(func(*application.Context) { rt.onToggle() })
	rt.copyURL = m.Add("Copy public URL")
	rt.copyURL.SetEnabled(false)
	rt.copyURL.OnClick(func(*application.Context) { rt.onCopy() })
	m.Add("Remote access settings…").OnClick(func(*application.Context) { openPath(remoteSettingsPath) })
	go rt.loop()
	return rt
}

// attach connects the tray to the server's manager.
func (rt *remoteTray) attach(mgr *remoteaccess.Manager) {
	if mgr == nil {
		return
	}
	rt.mu.Lock()
	rt.mgr = mgr
	rt.mu.Unlock()
	// The manager calls subscribers from its own goroutines (sometimes while
	// it holds its lock), so hand the status to our loop instead of touching
	// the menu or calling back into the manager here.
	mgr.Subscribe(func(st remoteaccess.Status) {
		select {
		case rt.updates <- st:
		default: // the loop is behind; the next status will catch up
		}
	})
	rt.updates <- mgr.Status()
}

func (rt *remoteTray) manager() *remoteaccess.Manager {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.mgr
}

func (rt *remoteTray) loop() {
	for st := range rt.updates {
		rt.mu.Lock()
		prev := rt.last
		rt.last = st
		rt.mu.Unlock()

		rt.toggle.SetEnabled(true)
		rt.toggle.SetLabel(trayToggleLabel(st))
		rt.toggle.SetChecked(trayToggleChecked(st))
		rt.copyURL.SetEnabled(st.Status == remoteaccess.StateConnected && st.PublicURL != "")
		if title, body, ok := trayNotification(prev, st); ok && rt.notify != nil {
			open := ""
			if st.Status == remoteaccess.StateError {
				open = remoteSettingsPath // clicking a failure shows what to do
			}
			rt.notify(title, body, open)
		}
	}
}

func (rt *remoteTray) onToggle() {
	mgr := rt.manager()
	if mgr == nil {
		return
	}
	st := mgr.Status()
	if st.Running {
		_ = mgr.Stop()
		return
	}
	if err := mgr.Start(); err != nil {
		// Not set up yet (no token, user auth off, ...): show the settings.
		rt.updates <- mgr.Status() // undo the checkbox's own toggle
		var g *remoteaccess.GuardError
		msg := err.Error()
		if errors.As(err, &g) {
			msg = g.Msg
		}
		if rt.notify != nil {
			rt.notify("Remote access needs setup", msg, remoteSettingsPath)
		}
		rt.openPath(remoteSettingsPath)
	}
}

func (rt *remoteTray) onCopy() {
	mgr := rt.manager()
	if mgr == nil {
		return
	}
	if u := mgr.PublicURL(); u != "" {
		rt.app.Clipboard.SetText(u)
	}
}

// trayToggleLabel is the toggle's label for a status.
func trayToggleLabel(st remoteaccess.Status) string {
	switch st.Status {
	case remoteaccess.StateConnected:
		return "Remote access: On"
	case remoteaccess.StateConnecting:
		return "Remote access: Connecting…"
	case remoteaccess.StateError:
		return "Remote access: Failed (click to retry)"
	default:
		return "Remote access: Off"
	}
}

func trayToggleChecked(st remoteaccess.Status) bool {
	return st.Running || st.Status == remoteaccess.StateConnected || st.Status == remoteaccess.StateConnecting
}

// remoteSettingsPath is Settings → Remote access.
const remoteSettingsPath = "/settings#access"

// trayNotification decides whether a status change deserves a native
// notification: the tunnel came up at a (new) URL, or failed. A session that
// drops and comes back at the same URL stays quiet.
func trayNotification(prev, next remoteaccess.Status) (title, body string, ok bool) {
	switch {
	case next.Status == remoteaccess.StateConnected && next.PublicURL != prev.PublicURL:
		return "Remote access is on", next.PublicURL, true
	case next.Status == remoteaccess.StateError && (prev.Status != remoteaccess.StateError || prev.Error != next.Error):
		if next.ErrorCode == remoteaccess.ErrCodeAgentLimit {
			return "ngrok agent limit reached",
				"Other ngrok agents are using all of your account's sessions. Click to stop them in Remote access settings, then try again.", true
		}
		return "Remote access failed", next.Error, true
	}
	return "", "", false
}
