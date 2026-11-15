package main

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// shortcutEvent is the Wails event a shortcut menu item emits; its data is
// the shortcut id. The dashboard runs it through its shortcut registry
// (internal/server/frontend/app/composables/useKeyboardShortcuts.ts).
const shortcutEvent = "eggify:shortcut"

// shortcutMenuItem is a native menu item that owns a dashboard shortcut.
//
// The ids and keys come from the dashboard's shortcut catalogue
// (internal/server/frontend/app/utils/shortcuts/catalog.json, desktopMenu:
// true). In the desktop app the page leaves those keys alone, so the menu
// accelerator fires (once) and sends the id back. shortcut_menu_test.go checks
// the two lists match.
type shortcutMenuItem struct {
	Menu  string // submenu: File, View, Go, Session, Help
	Label string
	ID    string
	Accel string // Wails accelerator
	// SeparatorAfter puts a separator after the item.
	SeparatorAfter bool
}

var shortcutMenuItems = []shortcutMenuItem{
	{Menu: "File", Label: "New Session", ID: "session.new", Accel: "CmdOrCtrl+N"},
	{Menu: "File", Label: "New Project…", ID: "project.new", Accel: "CmdOrCtrl+Shift+N", SeparatorAfter: true},

	{Menu: "View", Label: "Toggle Sidebar", ID: "sidebar.toggle", Accel: "CmdOrCtrl+B"},
	{Menu: "View", Label: "Toggle Right Sidebar", ID: "sidebar.right.toggle", Accel: "OptionOrAlt+CmdOrCtrl+B"},
	{Menu: "View", Label: "Zen / Live", ID: "view.zen", Accel: "CmdOrCtrl+Shift+Return", SeparatorAfter: true},
	// Overlays over whatever page is open (not places you go to).
	{Menu: "View", Label: "Agent Overview", ID: "overview.toggle", Accel: "CmdOrCtrl+O"},
	{Menu: "View", Label: "All Agents", ID: "allagents.toggle", Accel: "CmdOrCtrl+Shift+S", SeparatorAfter: true},
	{Menu: "View", Label: "Next Theme", ID: "theme.next", Accel: "OptionOrAlt+CmdOrCtrl+]"},
	{Menu: "View", Label: "Previous Theme", ID: "theme.previous", Accel: "OptionOrAlt+CmdOrCtrl+[", SeparatorAfter: true},

	{Menu: "Go", Label: "Command Palette…", ID: "palette.open", Accel: "CmdOrCtrl+K", SeparatorAfter: true},
	{Menu: "Go", Label: "Switch Project…", ID: "project.switch", Accel: "CmdOrCtrl+Shift+P"},
	{Menu: "Go", Label: "Go to File…", ID: "file.goto", Accel: "CmdOrCtrl+P", SeparatorAfter: true},
	{Menu: "Go", Label: "Previous Session", ID: "session.prev", Accel: "CmdOrCtrl+["},
	{Menu: "Go", Label: "Next Session", ID: "session.next", Accel: "CmdOrCtrl+]"},
	{Menu: "Go", Label: "Session Above", ID: "session.up", Accel: "OptionOrAlt+CmdOrCtrl+Up"},
	{Menu: "Go", Label: "Session Below", ID: "session.down", Accel: "OptionOrAlt+CmdOrCtrl+Down"},

	{Menu: "Session", Label: "Search Messages", ID: "messages.search", Accel: "CmdOrCtrl+F"},
	{Menu: "Session", Label: "Change Model…", ID: "model.change", Accel: "CmdOrCtrl+Shift+M"},
	{Menu: "Session", Label: "Subagents", ID: "subagents.toggle", Accel: "CmdOrCtrl+Shift+A", SeparatorAfter: true},
	{Menu: "Session", Label: "Terminal", ID: "tab.terminal", Accel: "CmdOrCtrl+Shift+T"},
	{Menu: "Session", Label: "Files", ID: "tab.files", Accel: "CmdOrCtrl+Shift+E", SeparatorAfter: true},
	{Menu: "Session", Label: "Add Tag…", ID: "session.tag", Accel: "CmdOrCtrl+S", SeparatorAfter: true},
	{Menu: "Session", Label: "Dictate", ID: "voice.dictate", Accel: "CmdOrCtrl+D"},
	{Menu: "Session", Label: "Stop Agent", ID: "agent.stop", Accel: "CmdOrCtrl+."},

	{Menu: "Help", Label: "Keyboard Shortcuts", ID: "help.shortcuts", Accel: "CmdOrCtrl+/"},
}

// whatsNewMenuID is what Help → What's New sends on shortcutEvent. It has no
// key, so it is not in the catalogue; the layout listens for it
// (DESKTOP_WHATS_NEW_ID in utils/desktop.ts) and opens the changelog.
const whatsNewMenuID = "changelog.open"

// pageMenuItems are Go menu items without a key that open one of the
// dashboard's pages (the same pages the command palette lists).
var pageMenuItems = []struct{ Label, Path string }{
	{"Agents", "/agents"},
	{"Projects", "/projects"},
	{"Skills", "/skills"},
	{"Connectors", "/connectors"},
	{"Providers", "/providers"},
}

// nativeShortcutOwners are catalogue shortcuts (desktopMenu: true) whose menu
// item does something native instead of sending the id: Settings… opens the
// settings page, Toggle Full Screen is the window's own full screen, Switch
// Machine / Previous Machine move the window between paired machines (machine_switch.go;
// native, so it works on another machine's page whatever its version).
var nativeShortcutOwners = map[string]string{
	"settings.open":    "CmdOrCtrl+,",
	"view.fullscreen":  "Ctrl+Cmd+F",
	"machine.switch":   "OptionOrAlt+CmdOrCtrl+Right",
	"machine.previous": "OptionOrAlt+CmdOrCtrl+Left",
}

// menuShortcutKeys are the user's own keys for menu items, as Wails
// accelerators by shortcut id; "" means the item has no key. Items not in
// the map keep their default (shortcutMenuItems, nativeShortcutOwners).
type menuShortcutKeys map[string]string

// accel returns the accelerator for a shortcut: the user's, else def.
func (k menuShortcutKeys) accel(id, def string) string {
	if a, ok := k[id]; ok {
		return a
	}
	return def
}

// setAccel sets an item's key; "" leaves it without one.
func setAccel(it *application.MenuItem, accel string) *application.MenuItem {
	if accel != "" {
		it.SetAccelerator(accel)
	}
	return it
}

// customizableMenuShortcut reports whether the page may change the item's
// key: the items that send their id, Settings… and the machine items (Toggle
// Full Screen is the window's own role and keeps ⌃⌘F).
func customizableMenuShortcut(id string) bool {
	if id == "settings.open" || id == "machine.switch" || id == "machine.previous" {
		return true
	}
	for _, it := range shortcutMenuItems {
		if it.ID == id {
			return true
		}
	}
	return false
}

var fKey = regexp.MustCompile(`^f([1-9]|1[0-2])$`)

var specKey = regexp.MustCompile(`^([a-z0-9]|f([1-9]|1[0-2])|[\[\].\/,;'` + "`" + `\\=-]|enter|space|tab|up|down|left|right)$`)

// specToAccelerator turns a key spec from the dashboard's catalogue syntax
// ("mod+shift+k", "ctrl+alt+up"; see app/utils/shortcuts/keys.ts) into a
// Wails accelerator ("CmdOrCtrl+Shift+K"). "" stays "" (no key).
func specToAccelerator(spec string) (string, error) {
	if spec == "" {
		return "", nil
	}
	parts := strings.Split(strings.ToLower(spec), "+")
	key := parts[len(parts)-1]
	if !specKey.MatchString(key) {
		return "", fmt.Errorf("unsupported key %q in %q", key, spec)
	}
	var mods []string
	seen := map[string]bool{}
	for _, p := range parts[:len(parts)-1] {
		var m string
		switch p {
		case "mod":
			m = "CmdOrCtrl"
		case "ctrl":
			m = "Ctrl"
		case "alt", "option":
			m = "OptionOrAlt"
		case "shift":
			m = "Shift"
		default:
			return "", fmt.Errorf("unknown modifier %q in %q", p, spec)
		}
		if seen[m] {
			return "", fmt.Errorf("modifier %q twice in %q", p, spec)
		}
		seen[m] = true
		mods = append(mods, m)
	}
	// ⌘Tab / Alt+Tab switch apps and windows (the system takes them); Tab
	// goes with Ctrl only (CmdOrCtrl is Ctrl off macOS).
	ctrl := seen["Ctrl"] || (seen["CmdOrCtrl"] && runtime.GOOS != "darwin")
	if key == "tab" && (!ctrl || seen["OptionOrAlt"] || (seen["CmdOrCtrl"] && runtime.GOOS == "darwin")) {
		return "", fmt.Errorf("%q: Tab only goes with Ctrl", spec)
	}
	// Shift alone types a character; F-keys may stand alone.
	if !seen["CmdOrCtrl"] && !seen["Ctrl"] && !seen["OptionOrAlt"] && !fKey.MatchString(key) {
		return "", fmt.Errorf("%q needs a modifier", spec)
	}
	switch key {
	case "enter":
		key = "Return"
	case "space", "tab", "up", "down", "left", "right":
		key = strings.ToUpper(key[:1]) + key[1:]
	default:
		key = strings.ToUpper(key)
	}
	return strings.Join(append(mods, key), "+"), nil
}

// menuShortcutKeysFromSpecs checks the page's overrides (id → spec) and
// converts them to accelerators. Unknown ids and bad specs are refused.
func menuShortcutKeysFromSpecs(specs map[string]string) (menuShortcutKeys, error) {
	if len(specs) > 64 {
		return nil, fmt.Errorf("too many shortcuts")
	}
	out := menuShortcutKeys{}
	for id, spec := range specs {
		if !customizableMenuShortcut(id) {
			return nil, fmt.Errorf("unknown menu shortcut %q", id)
		}
		a, err := specToAccelerator(spec)
		if err != nil {
			return nil, err
		}
		out[id] = a
	}
	return out, nil
}

// addShortcutItems appends the items of one submenu; run receives the id.
func addShortcutItems(m *application.Menu, menu string, keys menuShortcutKeys, run func(id string)) {
	for _, it := range shortcutMenuItems {
		if it.Menu != menu {
			continue
		}
		id := it.ID
		setAccel(m.Add(it.Label), keys.accel(id, it.Accel)).OnClick(func(*application.Context) { run(id) })
		if it.SeparatorAfter {
			m.AddSeparator()
		}
	}
}

// buildAppMenu builds the menu bar: Eggify, File, Edit, View, Go, Session,
// Window, Help.
//
// checkUpdates is nil when the updater is off (then there is no "Check for
// Updates…" item). keys holds the user's own keys (nil: the defaults).
// openPage opens a dashboard page (settingsPath, "/help", pageMenuItems).
func buildAppMenu(app *application.App, keys menuShortcutKeys, openPage func(path string), checkUpdates func(), runShortcut func(id string), switchMachine func(dir int), addMachine func(), zoom func(dir int)) *application.Menu {
	menu := app.NewMenu()

	// The app menu, built by hand so it can hold Check for Updates… and
	// Settings… (⌘,), like every Mac app.
	appMenu := menu.AddSubmenu("Eggify")
	appMenu.AddRole(application.About)
	if checkUpdates != nil {
		appMenu.Add("Check for Updates…").OnClick(func(*application.Context) { checkUpdates() })
	}
	appMenu.AddSeparator()
	setAccel(appMenu.Add("Settings…"), keys.accel("settings.open", nativeShortcutOwners["settings.open"])).OnClick(func(*application.Context) { openPage(settingsPath) })
	appMenu.AddSeparator()
	appMenu.AddRole(application.ServicesMenu)
	appMenu.AddSeparator()
	appMenu.AddRole(application.Hide)
	appMenu.AddRole(application.HideOthers)
	appMenu.AddRole(application.ShowAll)
	appMenu.AddSeparator()
	appMenu.AddRole(application.Quit)

	file := menu.AddSubmenu("File")
	addShortcutItems(file, "File", keys, runShortcut)
	// ⌘W closes (hides) the window; agents keep running, the tray brings it back.
	file.AddRole(application.CloseWindow)

	// Edit: copy/paste in the webview needs it.
	menu.AddRole(application.EditMenu)

	view := menu.AddSubmenu("View")
	addShortcutItems(view, "View", keys, runShortcut)
	view.AddRole(application.Reload)
	view.AddRole(application.ForceReload)
	view.AddSeparator()
	// Page zoom, not Wails' magnification roles (those enlarge the rendered page
	// beyond the window and cause scroll bars). dir 0 resets.
	view.Add("Actual Size").SetAccelerator("CmdOrCtrl+0").OnClick(func(*application.Context) { zoom(0) })
	view.Add("Zoom In").SetAccelerator("CmdOrCtrl+plus").OnClick(func(*application.Context) { zoom(1) })
	view.Add("Zoom Out").SetAccelerator("CmdOrCtrl+-").OnClick(func(*application.Context) { zoom(-1) })
	view.AddSeparator()
	view.AddRole(application.ToggleFullscreen) // ⌃⌘F

	goMenu := menu.AddSubmenu("Go")
	addShortcutItems(goMenu, "Go", keys, runShortcut)
	goMenu.AddSeparator()
	for _, it := range pageMenuItems {
		path := it.Path
		goMenu.Add(it.Label).OnClick(func(*application.Context) { openPage(path) })
	}
	goMenu.AddSeparator()
	for _, it := range []struct {
		label, id string
		dir       int
	}{{"Previous Machine", "machine.previous", -1}, {"Next Machine", "machine.switch", 1}} {
		item := setAccel(goMenu.Add(it.label), keys.accel(it.id, nativeShortcutOwners[it.id]))
		if switchMachine == nil {
			item.SetEnabled(false)
			continue
		}
		dir := it.dir
		item.OnClick(func(*application.Context) { switchMachine(dir) })
	}
	// Also in the menu bar icon → Machines; here so it can be found.
	add := goMenu.Add("Add a Mac from Clipboard…")
	if addMachine != nil {
		add.OnClick(func(*application.Context) { addMachine() })
	} else {
		add.SetEnabled(false)
	}
	addShortcutItems(menu.AddSubmenu("Session"), "Session", keys, runShortcut)

	menu.AddRole(application.WindowMenu)

	help := menu.AddSubmenu("Help")
	addShortcutItems(help, "Help", keys, runShortcut)
	help.Add("What's New").OnClick(func(*application.Context) { runShortcut(whatsNewMenuID) })
	help.Add("Eggify Help").OnClick(func(*application.Context) { openPage("/help") })
	return menu
}
