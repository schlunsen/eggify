package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The dashboard's shortcut catalogue; the menu must match it.
const catalogPath = "../../internal/server/frontend/app/utils/shortcuts/catalog.json"

type catalogShortcut struct {
	ID          string            `json:"id"`
	Keys        map[string]string `json:"keys"`
	DesktopMenu bool              `json:"desktopMenu"`
}

func loadCatalog(t *testing.T) map[string]catalogShortcut {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(catalogPath))
	if err != nil {
		t.Fatalf("read catalogue: %v", err)
	}
	var c struct {
		Shortcuts []catalogShortcut `json:"shortcuts"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parse catalogue: %v", err)
	}
	out := map[string]catalogShortcut{}
	for _, s := range c.Shortcuts {
		out[s.ID] = s
	}
	return out
}

// canonical turns a catalogue key spec ("mod+shift+enter") or a Wails
// accelerator ("CmdOrCtrl+Shift+Return") into one comparable form.
func canonical(s string) string {
	parts := strings.Split(strings.ToLower(s), "+")
	key := parts[len(parts)-1]
	switch key {
	case "return":
		key = "enter"
	}
	var mods []string
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "mod", "cmdorctrl", "cmd", "command":
			mods = append(mods, "mod")
		case "ctrl":
			mods = append(mods, "ctrl")
		case "alt", "option", "optionoralt":
			mods = append(mods, "alt")
		case "shift":
			mods = append(mods, "shift")
		default:
			mods = append(mods, "?"+p)
		}
	}
	sort.Strings(mods)
	return strings.Join(append(mods, key), "+")
}

func TestShortcutMenuMatchesCatalogue(t *testing.T) {
	catalog := loadCatalog(t)
	seenIDs := map[string]bool{}
	seenAccels := map[string]string{}
	for _, it := range shortcutMenuItems {
		def, ok := catalog[it.ID]
		if !ok {
			t.Errorf("menu item %q: id %q is not in the catalogue", it.Label, it.ID)
			continue
		}
		if !def.DesktopMenu {
			t.Errorf("menu item %q owns %s, but the catalogue entry lacks desktopMenu: true (the page would handle it too)", it.Label, it.ID)
		}
		if got, want := canonical(it.Accel), canonical(def.Keys["mac"]); got != want {
			t.Errorf("menu item %q: accelerator %s (%s), catalogue says %s (%s)", it.Label, it.Accel, got, def.Keys["mac"], want)
		}
		if seenIDs[it.ID] {
			t.Errorf("id %s is on two menu items", it.ID)
		}
		seenIDs[it.ID] = true
		if other, dup := seenAccels[canonical(it.Accel)]; dup {
			t.Errorf("accelerator %s is on %q and %q", it.Accel, other, it.Label)
		}
		seenAccels[canonical(it.Accel)] = it.Label
	}
	for id, accel := range nativeShortcutOwners {
		def, ok := catalog[id]
		if !ok || !def.DesktopMenu {
			t.Errorf("native owner %s: not a desktopMenu shortcut in the catalogue", id)
			continue
		}
		if canonical(accel) != canonical(def.Keys["mac"]) {
			t.Errorf("native owner %s: %s vs catalogue %s", id, accel, def.Keys["mac"])
		}
		seenIDs[id] = true
	}
	// Every shortcut the page leaves to the menu has a menu item, or the key
	// would do nothing in the desktop app.
	for id, def := range catalog {
		if def.DesktopMenu && !seenIDs[id] {
			t.Errorf("catalogue shortcut %s has desktopMenu: true but no menu item", id)
		}
	}
}

func TestShortcutMenuAcceleratorsParse(t *testing.T) {
	// SetAccelerator drops an accelerator it cannot parse; build each item and
	// check it kept one.
	for _, menuName := range []string{"File", "View", "Go", "Session", "Help"} {
		m := application.NewMenu()
		var ran []string
		addShortcutItems(m, menuName, nil, func(id string) { ran = append(ran, id) })
		for _, it := range shortcutMenuItems {
			if it.Menu != menuName {
				continue
			}
			item := m.FindByLabel(it.Label)
			if item == nil {
				t.Fatalf("%s: item %q missing", menuName, it.Label)
			}
			if item.GetAccelerator() == "" {
				t.Errorf("%s: %q lost its accelerator %q", menuName, it.Label, it.Accel)
			}
		}
	}
}

func TestShortcutMenuIDsAreRegistryIDs(t *testing.T) {
	// The page drops ids it does not know; make sure every menu id is spelled
	// exactly like the catalogue (dot-separated, lower case).
	for _, it := range shortcutMenuItems {
		if it.ID != strings.ToLower(it.ID) || !strings.Contains(it.ID, ".") {
			t.Errorf("odd shortcut id %q", it.ID)
		}
	}
	if shortcutEvent != "eggify:shortcut" {
		t.Fatalf("event %q must match DESKTOP_SHORTCUT_EVENT in utils/desktop.ts", shortcutEvent)
	}
}

// menuAccelerators walks a menu and collects every item's accelerator as Wails
// resolved it (MenuItem.GetAccelerator: the parsed modifiers named for this OS,
// sorted, then the key), with the paths of the items that have it.
func menuAccelerators(m *application.Menu, path string, out map[string][]string) {
	for i := 0; ; i++ {
		it := m.ItemAt(i)
		if it == nil {
			return
		}
		p := path + " > " + it.Label()
		if sub := it.GetSubmenu(); sub != nil {
			menuAccelerators(sub, p, out)
			continue
		}
		if acc := it.GetAccelerator(); acc != "" {
			out[acc] = append(out[acc], p)
		}
	}
}

func TestAppMenuAcceleratorsResolveToDistinctKeys(t *testing.T) {
	// The whole menu bar as the app builds it, roles included (Edit, Reload,
	// Full Screen…), through Wails' own accelerator parser. Two items on the
	// same resolved key: only one of them can ever fire.
	// Some roles (About, Hide, Quit) read the app name, so an app must exist;
	// it is never run.
	app := application.New(application.Options{Name: "Eggify"})
	noop := func() {}
	menu := buildAppMenu(app, nil, func(string) {}, noop, func(string) {}, func(int) {}, noop, func(int) {})
	accels := map[string][]string{}
	menuAccelerators(menu, "", accels)
	if len(accels) < len(shortcutMenuItems) {
		t.Fatalf("found only %d accelerators in the menu", len(accels))
	}
	for acc, items := range accels {
		if len(items) > 1 {
			t.Errorf("%s is on %d items: %s", acc, len(items), strings.Join(items, ", "))
		}
	}

	// The theme and Terminal keys as AppKit gets them on macOS. Wails maps
	// "OptionOrAlt" to Option and "CmdOrCtrl" to Command, so the theme items
	// are ⌥⌘] / ⌥⌘[, apart from Terminal's ⇧⌘T.
	if runtime.GOOS != "darwin" {
		return
	}
	for label, want := range map[string]string{
		"Next Theme":     "Cmd+Option+]",
		"Previous Theme": "Cmd+Option+[",
		"Terminal":       "Cmd+Shift+T",
	} {
		found := false
		for acc, items := range accels {
			for _, p := range items {
				if strings.HasSuffix(p, " > "+label) {
					found = true
					if acc != want {
						t.Errorf("%s resolves to %s, want %s", label, acc, want)
					}
				}
			}
		}
		if !found {
			t.Errorf("%s not found in the menu", label)
		}
	}
}

func TestSpecToAccelerator(t *testing.T) {
	for spec, want := range map[string]string{
		"":                "",
		"mod+k":           "CmdOrCtrl+K",
		"shift+mod+enter": "Shift+CmdOrCtrl+Return",
		"ctrl+alt+up":     "Ctrl+OptionOrAlt+Up",
		"alt+mod+]":       "OptionOrAlt+CmdOrCtrl+]",
		"mod+/":           "CmdOrCtrl+/",
		"f5":              "F5",
		"mod+space":       "CmdOrCtrl+Space",
		"ctrl+tab":        "Ctrl+Tab",
		"ctrl+shift+tab":  "Ctrl+Shift+Tab",
	} {
		got, err := specToAccelerator(spec)
		if err != nil || got != want {
			t.Errorf("specToAccelerator(%q) = %q, %v; want %q", spec, got, err, want)
		}
	}
	for _, bad := range []string{"k", "shift+k", "hyper+k", "mod+tab", "alt+tab", "shift+tab", "tab", "ctrl+mod+tab", "mod+mod+k", "mod+", "mod+kk", "f"} {
		if got, err := specToAccelerator(bad); err == nil {
			t.Errorf("specToAccelerator(%q) = %q, want an error", bad, got)
		}
	}
}

func TestMenuShortcutKeysFromSpecs(t *testing.T) {
	keys, err := menuShortcutKeysFromSpecs(map[string]string{"session.new": "mod+j", "settings.open": ""})
	if err != nil {
		t.Fatal(err)
	}
	if keys["session.new"] != "CmdOrCtrl+J" || keys["settings.open"] != "" {
		t.Fatalf("keys = %v", keys)
	}
	// Not a menu item (the page handles it), or the window's own full screen.
	for _, id := range []string{"zen.fly", "view.fullscreen", "nope"} {
		if _, err := menuShortcutKeysFromSpecs(map[string]string{id: "mod+j"}); err == nil {
			t.Errorf("%s: want an error", id)
		}
	}
	if _, err := menuShortcutKeysFromSpecs(map[string]string{"session.new": "j"}); err == nil {
		t.Error("bare key: want an error")
	}
}

func TestAppMenuTakesUserKeys(t *testing.T) {
	app := application.New(application.Options{Name: "Eggify"})
	noop := func() {}
	keys := menuShortcutKeys{"session.new": "CmdOrCtrl+J", "palette.open": "", "settings.open": "CmdOrCtrl+Shift+,", "machine.switch": "Ctrl+Cmd+Y"}
	menu := buildAppMenu(app, keys, func(string) {}, noop, func(string) {}, func(int) {}, noop, func(int) {})
	accels := map[string][]string{}
	menuAccelerators(menu, "", accels)
	byItem := map[string]string{}
	for acc, items := range accels {
		for _, it := range items {
			byItem[it] = acc
		}
	}
	find := func(label string) (string, bool) {
		for path, acc := range byItem {
			if strings.HasSuffix(path, "> "+label) {
				return acc, true
			}
		}
		return "", false
	}
	if acc, _ := find("New Session"); !strings.HasSuffix(acc, "+J") {
		t.Errorf("New Session = %q, want the user's J", acc)
	}
	if acc, ok := find("Command Palette…"); ok {
		t.Errorf("Command Palette… kept %q, want no key", acc)
	}
	if acc, _ := find("Settings…"); !strings.Contains(acc, "Shift") {
		t.Errorf("Settings… = %q, want the user's ⇧⌘,", acc)
	}
	if acc, _ := find("Next Machine"); !strings.HasSuffix(acc, "+Y") {
		t.Errorf("Next Machine = %q, want the user's ⌃⌘Y", acc)
	}
	if acc, _ := find("Previous Machine"); !strings.HasSuffix(strings.ToLower(acc), "left") {
		t.Errorf("Previous Machine = %q, want its default ⌥⌘←", acc)
	}
	if acc, _ := find("Agent Overview"); !strings.HasSuffix(acc, "+O") {
		t.Errorf("Agent Overview = %q, want its default", acc)
	}
}

func TestPageMenuItemsOpenRealPages(t *testing.T) {
	// Each Go menu page item opens a page the dashboard has, carries no key
	// (every key stays on one item).
	const pagesDir = "../../internal/server/frontend/app/pages"
	app := application.New(application.Options{Name: "Eggify"})
	noop := func() {}
	menu := buildAppMenu(app, nil, func(string) {}, noop, func(string) {}, func(int) {}, noop, func(int) {})
	var goMenu *application.Menu
	for i := 0; ; i++ {
		it := menu.ItemAt(i)
		if it == nil {
			break
		}
		if it.Label() == "Go" {
			goMenu = it.GetSubmenu()
		}
	}
	if goMenu == nil {
		t.Fatal("no Go menu")
	}
	for _, p := range pageMenuItems {
		name := strings.TrimPrefix(p.Path, "/")
		_, errFile := os.Stat(filepath.Join(pagesDir, name+".vue"))
		_, errDir := os.Stat(filepath.Join(pagesDir, name, "index.vue"))
		if errFile != nil && errDir != nil {
			t.Errorf("%s: no page for %s", p.Label, p.Path)
		}
		item := goMenu.FindByLabel(p.Label)
		if item == nil {
			t.Errorf("Go menu lacks %q", p.Label)
			continue
		}
		if acc := item.GetAccelerator(); acc != "" {
			t.Errorf("%s has key %q; page items have none", p.Label, acc)
		}
	}
	for _, it := range shortcutMenuItems {
		if it.ID == "allagents.toggle" && it.Menu != "View" {
			t.Errorf("All Agents is in %s, want View", it.Menu)
		}
	}
}

func TestHelpMenuOpensWhatsNew(t *testing.T) {
	// Help → What's New sends whatsNewMenuID, which the page listens for by
	// the same name (utils/desktop.ts), and it has no key.
	app := application.New(application.Options{Name: "Eggify"})
	noop := func() {}
	menu := buildAppMenu(app, nil, func(string) {}, noop, func(string) {}, func(int) {}, noop, func(int) {})
	var help *application.Menu
	for i := 0; ; i++ {
		it := menu.ItemAt(i)
		if it == nil {
			break
		}
		if it.Label() == "Help" {
			help = it.GetSubmenu()
		}
	}
	if help == nil {
		t.Fatal("no Help menu")
	}
	item := help.FindByLabel("What's New")
	if item == nil {
		t.Fatal("Help menu lacks What's New")
	}
	if acc := item.GetAccelerator(); acc != "" {
		t.Errorf("What's New has key %q, want none", acc)
	}
	src, err := os.ReadFile("../../internal/server/frontend/app/utils/desktop.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "DESKTOP_WHATS_NEW_ID = '"+whatsNewMenuID+"'") {
		t.Errorf("utils/desktop.ts DESKTOP_WHATS_NEW_ID does not match %q", whatsNewMenuID)
	}
}
