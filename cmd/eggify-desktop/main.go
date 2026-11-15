// Command eggify-desktop is the Wails v3 desktop app for Eggify (spike).
//
// Unlike the Tauri app (src-tauri), which spawns the eggify binary as a
// sidecar, this binary *is* the server: it runs internal/server in-process on
// a loopback-only listener and points a Wails webview window at it. See
// docs/desktop-wails.md.
//
// It lives in its own Go module so the Wails dependency (beta, cgo, GTK4 /
// WebKitGTK 6 on Linux) never touches `go build ./...` or `go test ./...` of
// the main module.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/schlunsen/eggify/internal/cmd"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server"
	"github.com/schlunsen/eggify/internal/shellenv"
)

const (
	appID       = "com.eggify.desktop.wails"
	defaultPort = 3335 // CLI: 3333, Tauri app: 3334
	minWidth    = 900
	minHeight   = 600

	defaultWindowTitle = "Eggify — AI Agent Control Center"
)

//go:embed splash
var splashFiles embed.FS

// dlog is the desktop shell's own logger. The server redirects the standard
// logger to ~/.claude/eggify/logs once it is set up, so don't use log.* here.
var dlog = log.New(os.Stderr, "[eggify-desktop] ", log.LstdFlags|log.Lmicroseconds)

func main() {
	// The hosted server re-executes its own binary for helpers such as the
	// eggify-tools MCP server (`<binary> mcp-server`), so act as the CLI then.
	if cmd.HasSubcommand(os.Args[1:]) {
		if err := cmd.Execute(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// GUI apps get a minimal environment; import the login shell's (PATH, API
	// keys, ...) before the server reads its config.
	if os.Getenv("EGGIFY_SKIP_SHELL_ENV") != "1" {
		n, err := shellenv.Import()
		if err != nil {
			dlog.Printf("login shell environment not imported: %v", err)
		} else {
			dlog.Printf("imported %d variables from the login shell", n)
		}
	}

	if err := run(); err != nil {
		dlog.Fatal(err)
	}
}

// startup tracks the in-process server for the splash page.
type startup struct {
	mu    sync.Mutex
	err   string
	ready bool
}

func (s *startup) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == "" {
		s.err = err.Error()
	}
	dlog.Printf("startup failed: %v", err)
}

func (s *startup) snapshot() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready, s.err
}

func run() error {
	verbose := os.Getenv("EGGIFY_VERBOSE") == "1"
	for _, a := range os.Args[1:] {
		if a == "-v" || a == "--verbose" {
			verbose = true
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	claudeDir := filepath.Join(home, ".claude")

	ln, port, err := listenLoopback()
	if err != nil {
		return err
	}
	// eggify-tools (spawned by the server) finds the API through EGGIFY_PORT.
	os.Setenv("EGGIFY_PORT", strconv.Itoa(port))
	os.Setenv("EGGIFY_NO_BROWSER", "1")

	transport := newBridgeTransport()
	br, err := newBridge(port, transport)
	if err != nil {
		return err
	}

	srv := server.NewServerWithOptions(claudeDir, port, false, verbose)
	// paired (machines.go) is set below, before the server starts.
	var paired *machineManager
	srv.EnableDesktopMode(server.DesktopOptions{
		Routes: br.routes,
		PairedMachines: func() server.PairedMachines {
			if paired == nil {
				return nil
			}
			return paired
		},
	})

	status := &startup{}
	notifier := notifications.New()
	svc := &DesktopService{
		notifications: notifier,
		// Sessions working when the app restarts into an update continue
		// by themselves afterwards (any other quit only offers to).
		beforeUpdateRestart: func() { srv.SetShutdownReason(server.ShutdownReasonUpdate) },
	}
	var updater *Updater
	if os.Getenv("EGGIFY_DISABLE_UPDATES") != "1" {
		updater = newUpdater()
		// No manifest URL (a plain source build): no updater, like the env switch.
		if updater.manifestURL == "" {
			updater = nil
		} else {
			svc.updater = updater
		}
	}

	var (
		window      *application.WebviewWindow
		quitting    atomic.Bool
		serverReady atomic.Bool
		pendingMu   sync.Mutex
		pendingPath = "/"
		machines    *machineSwitcher // set once the window exists
	)

	// open shows the window on a dashboard path: on this Mac (deep links,
	// notifications) or on the machine the window shows (tray, menu).
	open := func(p string, local bool) {
		p = safeNext(p)
		if !serverReady.Load() {
			pendingMu.Lock()
			pendingPath = p
			pendingMu.Unlock()
			return
		}
		if window == nil {
			return
		}
		switch {
		case machines == nil:
			window.SetURL(br.origin + p)
		case local:
			machines.openLocal(p)
		default:
			machines.openHere(p)
		}
		window.Show()
		window.Focus()
	}
	openPath := func(p string) { open(p, false) }
	openLocal := func(p string) { open(p, true) }

	opts := application.Options{
		Name:        "Eggify",
		Description: "Eggify — AI Agent Control Center",
		Services: []application.Service{
			application.NewService(svc),
			application.NewService(notifier),
		},
		Assets: application.AssetOptions{
			Handler: splashHandler(status),
		},
		Transport: transport,
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: appID,
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				for _, a := range data.Args {
					if p, ok := deepLinkPath(a); ok {
						openLocal(p)
						return
					}
				}
				if window != nil {
					window.Show()
					window.Restore()
					window.Focus()
				}
			},
		},
		ShouldQuit: func() bool {
			quitting.Store(true)
			return true
		},
		OnShutdown: func() {
			if err := srv.Shutdown(); err != nil {
				dlog.Printf("server shutdown: %v", err)
			}
		},
	}
	// Raw page messages: the machine switch's timings and requests (always,
	// strictly checked in machine_switch_timing.go), plus diag mode's log.
	diag := os.Getenv("EGGIFY_DESKTOP_DIAG") == "1"
	opts.RawMessageHandler = func(_ application.Window, message string, oi *application.OriginInfo) {
		if machines != nil && oi != nil && machines.handleMessage(oi.Origin, oi.IsMainFrame, message) {
			return
		}
		if diag {
			dlog.Printf("webview: %s", message)
		}
	}

	app := application.New(opts)
	svc.app = app
	if updater != nil {
		updater.onChange = func(info UpdateInfo) { app.Event.Emit("eggify:update", info) }
	}

	// Menu bar (shortcut_menu.go): app menu, File, Edit, View, Go, Session,
	// Window, Help. Shortcut items send their id to the dashboard.
	runShortcut := func(id string) {
		if !serverReady.Load() || window == nil {
			return
		}
		window.Show()
		window.Focus()
		app.Event.Emit(shortcutEvent, id)
	}
	currentZoom := loadZoom(zoomStatePath(claudeDir))
	var checkUpdates func()
	if updater != nil {
		checkUpdates = func() { go checkForUpdatesInteractive(app, window, updater) }
	}
	// The page sends the user's own keys (SetMenuShortcuts); the menu is
	// rebuilt with them.
	setMenu := func(keys menuShortcutKeys) {
		app.Menu.Set(buildAppMenu(app, keys,
			openPath,
			checkUpdates,
			runShortcut,
			func(dir int) {
				if machines == nil || !serverReady.Load() {
					return
				}
				window.Show()
				window.Focus()
				machines.step(dir)
			},
			func() {
				if paired != nil {
					go paired.addFromClipboard()
				}
			},
			func(dir int) {
				if window == nil {
					return
				}
				zoom := defaultZoom
				if dir != 0 {
					zoom = nextZoom(currentZoom, dir)
				}
				currentZoom = zoom
				applyZoom(window, zoom)
				_ = saveZoom(zoomStatePath(claudeDir), zoom)
			},
		))
	}
	setMenu(nil)
	svc.setMenuKeys = func(keys menuShortcutKeys) { application.InvokeSync(func() { setMenu(keys) }) }

	statePath := windowStatePath(claudeDir)
	winOpts := application.WebviewWindowOptions{
		Name:             "main",
		Title:            defaultWindowTitle,
		Width:            1280,
		Height:           800,
		MinWidth:         minWidth,
		MinHeight:        minHeight,
		URL:              "/",
		BackgroundColour: application.NewRGB(0x0a, 0x0a, 0x1a),
		// No visible title bar on macOS: the traffic lights float over the
		// page (Tauri: titleBarStyle "Overlay"). The dashboard makes its navbar
		// the drag region in desktop mode (frontend assets/css/desktop-shell.css).
		// Windows/Linux keep their native frames.
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,
		},
		// Dictation uses getUserMedia on our own loopback page. With the default,
		// WebKit asks again on every page load on top of the one-time macOS
		// microphone prompt (which still appears: NSMicrophoneUsageDescription +
		// the audio-input entitlement). Camera keeps the default.
		Permissions: map[application.PermissionType]application.Permission{
			application.PermissionMicrophone: application.PermissionAllow,
		},
	}
	if st, ok := loadWindowState(statePath); ok {
		winOpts.Width, winOpts.Height = st.Width, st.Height
		winOpts.X, winOpts.Y = st.X, st.Y
		winOpts.InitialPosition = application.WindowXY
		if st.Maximised {
			winOpts.StartState = application.WindowStateMaximised
		}
	}
	if os.Getenv("EGGIFY_DESKTOP_DIAG") == "1" {
		winOpts.JS = diagJS
		// Test harness only: extra JS to run on every page (e.g. to drive the
		// login flow in automated checks). Never set for normal launches.
		if f := os.Getenv("EGGIFY_DESKTOP_DIAG_SCRIPT"); f != "" {
			if extra, err := os.ReadFile(f); err == nil {
				winOpts.JS += "\n" + string(extra)
			}
		}
	}
	window = app.Window.NewWithOptions(winOpts)
	if currentZoom != defaultZoom {
		applyZoom(window, currentZoom)
	}

	// Window state: save the frame (debounced) whenever it moves or resizes.
	var saveTimer *time.Timer
	var saveMu sync.Mutex
	saveState := func() {
		saveMu.Lock()
		defer saveMu.Unlock()
		if saveTimer != nil {
			saveTimer.Stop()
		}
		saveTimer = time.AfterFunc(500*time.Millisecond, func() {
			b := window.Bounds()
			_ = saveWindowState(statePath, windowState{
				X: b.X, Y: b.Y, Width: b.Width, Height: b.Height,
				Maximised: window.IsMaximised(),
			})
		})
	}
	window.OnWindowEvent(events.Common.WindowDidMove, func(*application.WindowEvent) { saveState() })
	window.OnWindowEvent(events.Common.WindowDidResize, func(*application.WindowEvent) { saveState() })

	// Closing the window hides it: agent sessions keep running, the tray and
	// the Dock icon bring it back, Cmd+Q / tray Quit exit.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitting.Load() {
			return
		}
		window.Hide()
		e.Cancel()
	})
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
		window.Show()
		window.Focus()
	})

	// Deep links: eggify://<dashboard path>, e.g. eggify://agents.
	app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(e *application.ApplicationEvent) {
		if p, ok := deepLinkPath(e.Context().URL()); ok {
			openLocal(p)
		}
	})

	notify := func(title, body, open string) {
		if err := svc.notify(title, body, open); err != nil {
			dlog.Printf("notification %q not shown: %v", title, err)
		}
	}
	// Clicking a notification that names a page (e.g. the remote access
	// "agent limit" one) opens it.
	notifier.OnNotificationResponse(func(r notifications.NotificationResult) {
		if r.Error != nil {
			return
		}
		if p, ok := notificationOpenPath(r.Response.UserInfo); ok {
			openLocal(p)
		}
	})
	// Other Macs the window can switch to (ADR 014, machines.go); loaded
	// once the server has set up the secret store.
	machines = newMachineSwitcher(window, machineTarget{
		name:   "This Mac",
		hue:    252,
		open:   func(p string) string { return br.origin + safeNext(p) },
		enter:  func(p string) string { return br.origin + safeNext(p) },
		origin: func() string { return br.origin },
	})
	machines.logf = logging.Info
	paired = newMachineManager(app, machines, br, notify)
	svc.machines, svc.addMachine = machines, paired.addFromClipboard
	svc.machineUsage = paired.Usage
	machines.onUpdate = func() { app.Event.Emit(MachinesEvent, machines.list()) }
	// The Notch Theatre (notch.go): always this Mac's page.
	notch := newNotchTheatre(app, claudeDir, br.bootstrapURL, openLocal)
	svc.notch = notch
	// The remote access items are about this Mac.
	remote := setupTray(app, window, openPath, openLocal, updater, notify, func(m *application.Menu) {
		notchItem := m.AddCheckbox("Notch Theatre", notch.enabled())
		notchItem.OnClick(func(ctx *application.Context) { go notch.setEnabled(ctx.ClickedMenuItem().Checked()) })
		m.AddSeparator()
		paired.addTrayMenu(m)
	})

	// Boot the server off the main thread; the window shows the splash page
	// until it answers, then goes through the bridge bootstrap.
	go func() {
		if err := srv.Setup(); err != nil {
			status.fail(fmt.Errorf("server setup: %w", err))
			return
		}
		// Before Serve, which may autostart remote access.
		remote.attach(srv.RemoteAccess())
		if err := paired.start(claudeDir); err != nil {
			dlog.Printf("machines: %v", err)
		}
		go func() {
			if err := srv.Serve(ln); err != nil && !quitting.Load() {
				status.fail(fmt.Errorf("server: %w", err))
			}
		}()
		if err := waitHealthy(br.origin, 30*time.Second); err != nil {
			status.fail(err)
			return
		}
		status.mu.Lock()
		status.ready = true
		status.mu.Unlock()
		serverReady.Store(true)
		pendingMu.Lock()
		next := pendingPath
		pendingMu.Unlock()
		dlog.Printf("server ready on %s", br.origin)
		window.SetURL(br.bootstrapURL(next))
		notch.start()
		if updater != nil {
			go updater.RunPeriodic(context.Background())
		}
	}()

	return app.Run()
}

// listenLoopback binds 127.0.0.1:<EGGIFY_DESKTOP_PORT|3335>, falling back to a
// random free port if that one is taken (the dashboard's localStorage is per
// origin, so a stable port is preferred).
func listenLoopback() (net.Listener, int, error) {
	port := defaultPort
	if v := os.Getenv("EGGIFY_DESKTOP_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p <= 0 || p > 65535 {
			return nil, 0, fmt.Errorf("invalid EGGIFY_DESKTOP_PORT %q", v)
		}
		port = p
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		dlog.Printf("port %d unavailable (%v), using a random port", port, err)
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, 0, err
		}
	}
	return ln, ln.Addr().(*net.TCPAddr).Port, nil
}

func waitHealthy(origin string, timeout time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(origin + "/api/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("server did not become healthy in time")
}

// settingsPath is where ⌘, (Eggify → Settings…) takes you.
const settingsPath = "/settings"

// deepLinkPath maps eggify://agents?x=1 to /agents?x=1.
func deepLinkPath(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "eggify" {
		return "", false
	}
	p := "/" + u.Host + u.Path
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return safeNext(p), true
}

// splashHandler serves the splash page (wails://localhost/) and its /status.
func splashHandler(st *startup) http.Handler {
	sub, _ := fs.Sub(splashFiles, "splash")
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			ready, errMsg := st.snapshot()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"ready": ready, "error": errMsg})
			return
		}
		files.ServeHTTP(w, r)
	})
}

func setupTray(app *application.App, window *application.WebviewWindow, openPath, openLocal func(string), updater *Updater, notify func(title, body, open string), extra func(*application.Menu)) *remoteTray {
	icon, _ := splashFiles.ReadFile("splash/tray.png")
	tray := app.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip("Eggify — AI Agent Control Center")

	m := app.NewMenu()
	m.Add("Show Eggify").OnClick(func(*application.Context) {
		window.Show()
		window.Focus()
	})
	m.AddSeparator()
	m.Add("Agents").OnClick(func(*application.Context) { openPath("/agents") })
	m.Add("Skills Manager").OnClick(func(*application.Context) { openPath("/skills") })
	m.AddSeparator()
	remote := newRemoteTray(app, m, openLocal, notify)
	m.AddSeparator()
	if extra != nil {
		extra(m)
	}
	if updater != nil {
		m.Add("Check for Updates…").OnClick(func(*application.Context) {
			go checkForUpdatesInteractive(app, window, updater)
		})
		m.AddSeparator()
	}
	m.Add("Quit Eggify").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(m)
	return remote
}

// diagJS (EGGIFY_DESKTOP_DIAG=1 only) reports page state, CSP violations and
// console errors to the Go log, so a headless test run can check them.
const diagJS = `(function(){
  if (window.__eggifyDiag) return; window.__eggifyDiag = true;
  var post = function(kind, data){ try { window.webkit.messageHandlers.external.postMessage('eggify-diag:' + kind + ' ' + JSON.stringify(data)); } catch (e) {} };
  document.addEventListener('securitypolicyviolation', function(e){ post('csp', {directive: e.violatedDirective, blocked: e.blockedURI, source: e.sourceFile, line: e.lineNumber}); });
  var origErr = console.error; console.error = function(){ try { post('console.error', Array.prototype.map.call(arguments, function(a){ return String(a && a.stack || a); }).join(' ').slice(0, 500)); } catch (e) {} return origErr.apply(console, arguments); };
  window.addEventListener('error', function(e){ post('error', {message: e.message, source: e.filename, line: e.lineno}); });
  var OrigWS = window.WebSocket;
  window.WebSocket = function(u, p){ var ws = p === undefined ? new OrigWS(u) : new OrigWS(u, p); post('ws.new', {url: String(u)}); ws.addEventListener('open', function(){ post('ws.open', {url: String(u)}); }); ws.addEventListener('close', function(e){ post('ws.close', {url: String(u), code: e.code}); }); return ws; };
  window.WebSocket.prototype = OrigWS.prototype; ['CONNECTING','OPEN','CLOSING','CLOSED'].forEach(function(k){ window.WebSocket[k] = OrigWS[k]; });
  var last = '';
  setInterval(function(){ var s = location.pathname + ' | ' + document.title + ' | nuxt=' + !!(document.getElementById('__nuxt') && document.getElementById('__nuxt').children.length) + ' | shell=' + (document.cookie.indexOf('eggify_desktop=wails') >= 0 ? 'wails' : 'web'); if (s !== last) { last = s; post('page', s); } }, 1000);
  post('page', 'diag installed at ' + location.href.replace(/t=[0-9a-f]+/, 't=***'));
})();`

// checkForUpdatesInteractive is the tray's "Check for Updates…": the result is
// shown either as the dashboard's update banner or in a dialog.
func checkForUpdatesInteractive(app *application.App, window *application.WebviewWindow, u *Updater) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	info, err := u.Check(ctx)
	switch {
	case err != nil:
		app.Dialog.Warning().SetTitle("Update check failed").SetMessage(err.Error()).Show()
	case info.UpdateAvailable:
		window.Show()
		window.Focus()
		// Reopen the dashboard's update dialog even if it was put off with "Later".
		app.Event.Emit(updateShowEvent, info)
	case info.Blocked != "":
		app.Dialog.Info().SetTitle("Update available").SetMessage(info.Blocked + ".").Show()
	default:
		app.Dialog.Info().SetTitle("You're up to date").
			SetMessage(fmt.Sprintf("Eggify %s is the latest version.", info.CurrentVersion)).Show()
	}
}
