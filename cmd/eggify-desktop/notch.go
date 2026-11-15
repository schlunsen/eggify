package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// The Notch Theatre: a small always-on-top window around the MacBook camera
// housing (or the middle of the menu bar on displays without one) that shows
// the dashboard's /notch page: a miniature puppet theatre with one Circus egg
// per working session, which drops down into a playbill with approval cards
// on hover. The page draws the black shape itself; the window is transparent,
// sized tightly to the pill while collapsed (so it never blocks clicks on the
// menu bar around it) and to the stage while expanded.
//
// It always shows this Mac (never a machine reached through the proxy) and
// loads the page through the bridge bootstrap like the main window.

const (
	// notchEar is the width of each "ear" next to the camera housing.
	notchEar = 104
	// notchBareWidth is the pill's width on a display without a notch.
	notchBareWidth = 220
	// The expanded stage (below the menu bar row).
	notchStageWidth  = 460
	notchStageHeight = 300
	notchPath        = "/notch"
	notchPrefsFile   = "desktop-notch.json"
)

// notchGeometry is what the page needs to lay itself out around the camera
// housing, in points.
type notchGeometry struct {
	HasNotch   bool
	NotchWidth float64 // camera housing width (0 without one)
	MenuHeight float64 // menu bar (= housing) height
}

func (g notchGeometry) normalised() notchGeometry {
	if g.MenuHeight < 22 || g.MenuHeight > 64 || math.IsNaN(g.MenuHeight) {
		g.MenuHeight = 24
	}
	if !g.HasNotch || g.NotchWidth < 60 || g.NotchWidth > 400 || math.IsNaN(g.NotchWidth) {
		g.HasNotch, g.NotchWidth = false, 0
	}
	return g
}

// size is the window size for the collapsed pill or the expanded stage.
func (g notchGeometry) size(expanded bool) (int, int) {
	g = g.normalised()
	w := notchBareWidth
	if g.HasNotch {
		w = int(math.Round(g.NotchWidth)) + 2*notchEar
	}
	h := int(math.Round(g.MenuHeight))
	if expanded {
		if w < notchStageWidth {
			w = notchStageWidth
		}
		h += notchStageHeight
	}
	return w, h
}

// pagePath is /notch with the geometry the page lays out around.
func (g notchGeometry) pagePath() string {
	g = g.normalised()
	q := url.Values{}
	q.Set("nw", fmt.Sprintf("%d", int(math.Round(g.NotchWidth))))
	q.Set("mh", fmt.Sprintf("%d", int(math.Round(g.MenuHeight))))
	q.Set("ear", fmt.Sprintf("%d", notchEar))
	return notchPath + "?" + q.Encode()
}

type notchPrefs struct {
	Enabled bool `json:"enabled"`
}

func loadNotchPrefs(dir string) notchPrefs {
	p := notchPrefs{Enabled: true}
	if b, err := os.ReadFile(filepath.Join(dir, notchPrefsFile)); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}

func saveNotchPrefs(dir string, p notchPrefs) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, notchPrefsFile), b, 0o600)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".notch-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// notchSessionPath is the only page the notch may open in the main window:
// the agents page, optionally on one session.
var notchSessionRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func notchSessionPath(sessionID string) string {
	if notchSessionRe.MatchString(sessionID) {
		return "/agents?session=" + sessionID
	}
	return "/agents"
}

type notchTheatre struct {
	app       *application.App
	claudeDir string
	page      func(path string) string // bootstrap URL for a local path
	openLocal func(path string)

	mu       sync.Mutex
	win      *application.WebviewWindow
	geom     notchGeometry
	expanded bool
	prefs    notchPrefs
	ready    bool // the server answers; the page can load
}

func newNotchTheatre(app *application.App, claudeDir string, page func(string) string, openLocal func(string)) *notchTheatre {
	n := &notchTheatre{app: app, claudeDir: claudeDir, page: page, openLocal: openLocal, prefs: loadNotchPrefs(claudeDir)}
	app.Event.OnApplicationEvent(events.Mac.ApplicationDidChangeScreenParameters, func(*application.ApplicationEvent) {
		n.relayout()
	})
	return n
}

func (n *notchTheatre) enabled() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.prefs.Enabled
}

// start opens the window once the server is up (if the user has it on).
func (n *notchTheatre) start() {
	n.mu.Lock()
	n.ready = true
	on := n.prefs.Enabled
	n.mu.Unlock()
	if on {
		application.InvokeSync(n.show)
	}
}

// setEnabled is the tray's "Notch Theatre" checkbox.
func (n *notchTheatre) setEnabled(on bool) {
	n.mu.Lock()
	n.prefs.Enabled = on
	prefs, ready := n.prefs, n.ready
	n.mu.Unlock()
	if err := saveNotchPrefs(n.claudeDir, prefs); err != nil {
		dlog.Printf("notch: saving prefs: %v", err)
	}
	if !ready {
		return
	}
	application.InvokeSync(func() {
		if on {
			n.show()
		} else {
			n.close()
		}
	})
}

// show creates the window (main thread).
func (n *notchTheatre) show() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.win != nil {
		return
	}
	n.geom = readNotchGeometry()
	n.expanded = false
	w, h := n.geom.size(false)
	n.win = n.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "notch",
		Title:            "Eggify Notch Theatre",
		Width:            w,
		Height:           h,
		Frameless:        true,
		DisableResize:    true,
		Hidden:           true,
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		URL:              n.page(n.geom.pagePath()),
		Mac: application.MacWindow{
			Backdrop:      application.MacBackdropTransparent,
			DisableShadow: true,
			CornerType:    application.MacWindowCornerTypeSquare,
			WindowClass:   application.MacWindowClassPanel,
			PanelPreferences: application.MacPanelPreferences{
				NonActivating: true,
				FloatingPanel: true,
			},
			WindowLevel: application.MacWindowLevelPopUpMenu,
			CollectionBehavior: application.MacWindowCollectionBehaviorCanJoinAllSpaces |
				application.MacWindowCollectionBehaviorFullScreenAuxiliary |
				application.MacWindowCollectionBehaviorStationary |
				application.MacWindowCollectionBehaviorIgnoresCycle,
		},
	})
	win := n.win
	// Closed some other way (⌘W while it is key): forget it, so the tray
	// checkbox can open a new one.
	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		n.mu.Lock()
		if n.win == win {
			n.win = nil
		}
		n.mu.Unlock()
	})
	placeNotchWindow(win, w, h)
	win.Show()
	placeNotchWindow(win, w, h)
}

func (n *notchTheatre) close() {
	n.mu.Lock()
	win := n.win
	n.win = nil
	n.mu.Unlock()
	if win != nil {
		win.Close()
	}
}

// setExpanded grows the window to the stage or shrinks it to the pill.
func (n *notchTheatre) setExpanded(expanded bool) {
	n.mu.Lock()
	win := n.win
	changed := n.expanded != expanded
	n.expanded = expanded
	w, h := n.geom.size(expanded)
	n.mu.Unlock()
	if win == nil || !changed {
		return
	}
	application.InvokeSync(func() { placeNotchWindow(win, w, h) })
}

// relayout follows display changes (another screen, lid closed, resolution):
// new geometry means a new page layout, so the page is reloaded.
func (n *notchTheatre) relayout() {
	application.InvokeSync(func() {
		n.mu.Lock()
		win := n.win
		if win == nil {
			n.mu.Unlock()
			return
		}
		g := readNotchGeometry()
		changed := g.normalised() != n.geom.normalised()
		n.geom = g
		n.expanded = false
		w, h := g.size(false)
		n.mu.Unlock()
		placeNotchWindow(win, w, h)
		if changed {
			win.SetURL(n.page(g.pagePath()))
		}
	})
}

func (n *notchTheatre) openSession(sessionID string) {
	n.setExpanded(false)
	n.openLocal(notchSessionPath(sessionID))
}
