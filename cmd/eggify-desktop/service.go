package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/schlunsen/eggify/internal/perfmon"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// DesktopService is the Go side of the frontend's desktop adapter
// (internal/server/frontend/app/utils/desktop.ts). Its exported methods are
// Wails bindings, callable only from the app's webview (see bridge.go).
type DesktopService struct {
	app           *application.App
	notifications *notifications.NotificationService
	updater       *Updater
	// machines is the window's machine switcher, addMachine pairs a Mac
	// from the clipboard (machines.go); set once the window exists.
	machines   *machineSwitcher
	addMachine func()
	// machineUsage reads CPU and memory per machine (machines.go Usage).
	machineUsage func(ctx context.Context) map[string]*perfmon.Usage
	// setMenuKeys rebuilds the menu bar with the user's shortcut keys.
	setMenuKeys func(keys menuShortcutKeys)
	// notch is the Notch Theatre window (notch.go).
	notch *notchTheatre

	// beforeUpdateRestart runs right before the app quits to restart into
	// an update: it tells the server the shutdown is for an update, so
	// working sessions continue after the restart.
	beforeUpdateRestart func()
}

// SetMenuShortcuts gives the menu items the user's own keys (Settings →
// Keyboard shortcuts): shortcut id → key spec in the dashboard's syntax
// ("mod+shift+k", "" = no key). Ids left out get their default key again.
func (s *DesktopService) SetMenuShortcuts(specs map[string]string) error {
	keys, err := menuShortcutKeysFromSpecs(specs)
	if err != nil {
		return err
	}
	if s.setMenuKeys == nil {
		return errors.New("menu not ready")
	}
	s.setMenuKeys(keys)
	return nil
}

// NotchSetExpanded grows the Notch Theatre into its stage (hover) or shrinks
// it back to the pill around the camera housing.
func (s *DesktopService) NotchSetExpanded(expanded bool) {
	if s.notch != nil {
		s.notch.setExpanded(expanded)
	}
}

// NotchOpenSession shows a session (by id; anything else opens the agents
// page) in the main window, on this Mac.
func (s *DesktopService) NotchOpenSession(sessionID string) {
	if s.notch != nil {
		s.notch.openSession(sessionID)
	}
}

// MachinesEvent is emitted with a MachineList when the machines or the one
// the window shows change.
const MachinesEvent = "eggify:machines"

// Machines lists this Mac and the paired Macs, and which one the window
// shows (the top bar's machine chip). A page shown from another Mac calls
// this Mac's app too (the machine proxy serves /wails locally), so the
// answer is right on either page.
func (s *DesktopService) Machines() MachineList {
	if s.machines == nil {
		return MachineList{Machines: []MachineInfo{}}
	}
	return s.machines.list()
}

// MachineUsage returns CPU and memory per machine id ("" = this Mac) for the
// meter next to the machine chip. Macs that don't answer are left out.
func (s *DesktopService) MachineUsage() map[string]*perfmon.Usage {
	if s.machineUsage == nil {
		u := perfmon.ReadUsage()
		return map[string]*perfmon.Usage{"": &u}
	}
	return s.machineUsage(context.Background())
}

// SwitchMachine shows the machine with id ("" = this Mac) with the
// transition. Switching to the machine already shown does nothing.
func (s *DesktopService) SwitchMachine(id string) error {
	if s.machines == nil {
		return errors.New("machines not ready")
	}
	i := s.machines.index(id)
	if i < 0 {
		return errors.New("unknown machine")
	}
	if _, cur := s.machines.snapshot(); cur == i {
		return nil
	}
	s.machines.switchToMachine(i)
	return nil
}

// AddMachineFromClipboard pairs with the Mac whose pair link is on the
// clipboard (asks first, like the tray item). It returns at once; the
// dialogs say how it went.
func (s *DesktopService) AddMachineFromClipboard() error {
	if s.addMachine == nil {
		return errors.New("machines not ready")
	}
	go s.addMachine()
	return nil
}

// UpdateState returns the self-updater's current state (cheap, no network).
func (s *DesktopService) UpdateState() UpdateInfo {
	if s.updater == nil {
		return UpdateInfo{Phase: "idle"}
	}
	return s.updater.State()
}

// CheckForUpdates fetches the update manifest now.
func (s *DesktopService) CheckForUpdates() (UpdateInfo, error) {
	if s.updater == nil {
		return UpdateInfo{Phase: "idle"}, errors.New("updates are disabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.updater.Check(ctx)
}

// InstallUpdate downloads and verifies the update found by the last check,
// swaps it in and restarts the app. The download URL comes from the manifest,
// never from the page.
func (s *DesktopService) InstallUpdate() error {
	if s.updater == nil {
		return errors.New("updates are disabled")
	}
	return s.updater.Install(context.Background(), func() {
		if s.beforeUpdateRestart != nil {
			s.beforeUpdateRestart()
		}
		s.app.Quit()
	})
}

// SelectFolder shows a native folder picker. Returns "" when cancelled.
func (s *DesktopService) SelectFolder() (string, error) {
	d := s.app.Dialog.OpenFile().
		SetTitle("Select Project Folder").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true)
	if w := s.app.Window.Current(); w != nil {
		d = d.AttachToWindow(w)
	}
	return d.PromptForSingleSelection()
}

// RevealFolder shows an existing folder in Finder (Explorer / the file
// manager elsewhere). It only reveals: on macOS `open -R` selects the item
// instead of opening it, so an .app bundle path is never launched.
func (s *DesktopService) RevealFolder(path string) error {
	p := filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(p) {
		return errors.New("path must be absolute")
	}
	info, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("cannot open %s: %w", p, err)
	}
	if !info.IsDir() {
		return errors.New("not a folder")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", p) // p is absolute, so never read as a flag
	case "windows":
		cmd = exec.Command("explorer", "/select,"+p)
	default:
		cmd = exec.Command("xdg-open", p)
	}
	return cmd.Start()
}

// OpenExternal opens an http(s) or mailto URL in the system browser/mail app.
func (s *DesktopService) OpenExternal(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
		if u.Host == "" {
			return errors.New("invalid URL: missing host")
		}
	case "mailto":
	default:
		return fmt.Errorf("refusing to open %q URLs", u.Scheme)
	}
	return s.app.Browser.OpenURL(u.String())
}

// appManagementSettingsURL opens System Settings → Privacy & Security →
// App Management (macOS 13+).
const appManagementSettingsURL = "x-apple.systempreferences:com.apple.preference.security?Privacy_AppBundles"

// OpenAppManagementSettings opens the App Management pane, where the user
// lets Eggify update itself when macOS blocked it (updater error_code
// app_management). The page names no URL: this is the only settings pane it
// can open.
func (s *DesktopService) OpenAppManagementSettings() error {
	if runtime.GOOS != "darwin" {
		return errors.New("App Management settings exist on macOS only")
	}
	return exec.Command("/usr/bin/open", appManagementSettingsURL).Start() // #nosec G204 -- fixed URL
}

// Notify shows a native notification. On macOS this needs a bundled app
// (.app with a bundle identifier) and the user's permission.
func (s *DesktopService) Notify(title, body string) error {
	return s.notify(title, body, "")
}

// notify shows a notification; clicking it opens openPath in the window
// when set (see notificationOpenPath). Not bound to the frontend.
func (s *DesktopService) notify(title, body, openPath string) error {
	if s.notifications == nil {
		return errors.New("notifications unavailable")
	}
	if len(title) > 256 {
		title = title[:256]
	}
	if len(body) > 2048 {
		body = body[:2048]
	}
	if ok, _ := s.notifications.CheckNotificationAuthorization(); !ok {
		if ok, err := s.notifications.RequestNotificationAuthorization(); err != nil || !ok {
			return fmt.Errorf("notifications not authorized: %v", err)
		}
	}
	opts := notifications.NotificationOptions{
		ID:    fmt.Sprintf("eggify-%d", time.Now().UnixNano()),
		Title: title,
		Body:  body,
	}
	if openPath != "" {
		opts.Data = map[string]interface{}{notificationOpenKey: openPath}
	}
	return s.notifications.SendNotification(opts)
}

// notificationOpenKey carries the in-app path a notification opens.
const notificationOpenKey = "eggify_open"

// notificationOpenPath returns the path a clicked notification should open:
// only the Settings page (the data comes back from the OS, so it is checked).
func notificationOpenPath(userInfo map[string]interface{}) (string, bool) {
	p, ok := userInfo[notificationOpenKey].(string)
	if !ok || !(p == "/settings" || strings.HasPrefix(p, "/settings#") || strings.HasPrefix(p, "/settings?")) {
		return "", false
	}
	return p, true
}
