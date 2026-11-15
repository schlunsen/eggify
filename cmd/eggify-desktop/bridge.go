package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"github.com/schlunsen/eggify/internal/machineproxy"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// The window loads the dashboard straight from the in-process server's
// loopback listener (http://127.0.0.1:<port>), so the page is same-origin
// with the API and WebSockets and needs no CSP/cookie changes. Wails' JS
// runtime calls `fetch(location.origin + "/wails/runtime")`, i.e. that same
// listener, so the desktop routes below forward /wails/* to Wails' own asset
// server (which hosts runtime.js and the binding transport).
//
// Any local process can reach the loopback port, so /wails/* is gated by a
// per-launch bridge token: the window is first sent to /__desktop/bootstrap
// with the token, which stores it in an HttpOnly, SameSite=Strict cookie
// scoped to /wails. Only the app's own webview has that cookie.

const (
	bridgeCookie   = "eggify_desktop_bridge"
	markerCookie   = "eggify_desktop" // read by the frontend (utils/desktop.ts)
	bootstrapPath  = "/__desktop/bootstrap"
	wailsPathPrefx = "/wails/"
)

// bridgeTransport is Wails' default HTTP transport that also hands us Wails'
// asset handler (runtime.js + /wails/runtime) so the loopback listener can
// serve it to the dashboard page.
type bridgeTransport struct {
	*application.HTTPTransport
	assets atomic.Pointer[http.Handler]
	// marker is a per-launch secret. Wails also serves its asset server (and
	// with it the /wails/runtime call endpoint) to every page in the webview
	// through the wails:// URL scheme, without any origin check; a page of
	// another Mac's dashboard could post a binding call there and skip the
	// machine proxy's filter. Handler() therefore answers 404 for the call
	// endpoint unless the request carries the marker, which only
	// bridge.serveWails adds (after deleting whatever the page sent).
	marker string
}

func newBridgeTransport() *bridgeTransport {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err) // no randomness, no safe marker
	}
	return &bridgeTransport{HTTPTransport: application.NewHTTPTransport(), marker: hex.EncodeToString(buf)}
}

// Handler is Wails' HTTP transport middleware behind the marker gate. Wails
// puts it in front of the asset server for wails:// requests and for the
// requests the bridge forwards.
func (t *bridgeTransport) Handler() func(next http.Handler) http.Handler {
	inner := t.HTTPTransport.Handler()
	return func(next http.Handler) http.Handler {
		return gateRuntimeCalls(t.marker, inner(next))
	}
}

// bridgeMarkerHeader is machineproxy.BridgeMarkerHeader.
const bridgeMarkerHeader = machineproxy.BridgeMarkerHeader

// gateRuntimeCalls answers 404 for the runtime call endpoint unless the
// request carries the marker (compared in constant time).
func gateRuntimeCalls(marker string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wails/runtime" {
			got := r.Header.Get(bridgeMarkerHeader)
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(marker)) != 1 {
				http.NotFound(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ServeAssets implements application.AssetServerTransport.
func (t *bridgeTransport) ServeAssets(h http.Handler) error {
	t.assets.Store(&h)
	return nil
}

func (t *bridgeTransport) assetHandler() http.Handler {
	if h := t.assets.Load(); h != nil {
		return *h
	}
	return nil
}

// bridge holds the per-launch token and registers the desktop routes.
type bridge struct {
	token     string
	origin    string // http://127.0.0.1:<port>
	transport *bridgeTransport
}

func newBridge(port int, transport *bridgeTransport) (*bridge, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return &bridge{
		token:     hex.EncodeToString(buf),
		origin:    fmt.Sprintf("http://127.0.0.1:%d", port),
		transport: transport,
	}, nil
}

// bootstrapURL is where the window goes once the server is up; next must be a
// local path.
func (b *bridge) bootstrapURL(next string) string {
	return b.origin + bootstrapPath + "?t=" + b.token + "&next=" + url.QueryEscape(safeNext(next))
}

func (b *bridge) tokenOK(got string) bool {
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(b.token)) == 1
}

// routes is passed to server.DesktopOptions.Routes. They run before the
// server's own middleware, so they authenticate themselves.
func (b *bridge) routes(app *fiber.App) {
	app.Get(bootstrapPath, func(c *fiber.Ctx) error {
		if !b.tokenOK(c.Query("t")) {
			return c.Status(fiber.StatusForbidden).SendString("forbidden")
		}
		c.Set("Cache-Control", "no-store")
		c.Set("Referrer-Policy", "no-referrer")
		c.Cookie(&fiber.Cookie{
			Name:     bridgeCookie,
			Value:    b.token,
			Path:     "/wails",
			HTTPOnly: true,
			SameSite: "Strict",
		})
		c.Cookie(&fiber.Cookie{
			Name:     markerCookie,
			Value:    "wails",
			Path:     "/",
			SameSite: "Strict",
			Expires:  time.Now().Add(365 * 24 * time.Hour),
		})
		return c.Redirect(safeNext(c.Query("next")), fiber.StatusFound)
	})

	forward := adaptor.HTTPHandlerFunc(b.serveWails)
	app.All(wailsPathPrefx+"*", func(c *fiber.Ctx) error {
		if !b.tokenOK(c.Cookies(bridgeCookie)) {
			return c.Status(fiber.StatusForbidden).SendString("forbidden")
		}
		if o := c.Get("Origin"); o != "" && o != b.origin {
			return c.Status(fiber.StatusForbidden).SendString("forbidden")
		}
		return forward(c)
	})
}

func (b *bridge) serveWails(w http.ResponseWriter, r *http.Request) {
	h := b.transport.assetHandler()
	if h == nil {
		http.Error(w, "desktop runtime not ready", http.StatusServiceUnavailable)
		return
	}
	// Don't hand the dashboard's session cookie to Wails. The marker is ours:
	// whatever the page sent is dropped and the real one set.
	r.Header.Del("Cookie")
	r.Header.Del(bridgeMarkerHeader)
	r.Header.Set(bridgeMarkerHeader, b.transport.marker)
	h.ServeHTTP(w, r)
}

// proxiedWailsHandler serves /wails/* on another machine's page (machine
// proxy, ADR 014), so the page keeps the desktop bindings and the menu's
// shortcut events. The bridge cookie lives on 127.0.0.1 and does not reach
// the proxies on localhost; the proxy's own gate cookie (same per-launch
// secret, only the window has it) and Origin check stand in for it.
//
// Wails gives a binding no caller origin, so what such a page may call is
// decided in front of Wails (wails_filter.go): only the machine switcher's
// bindings.
func (b *bridge) proxiedWailsHandler() http.Handler {
	return filterMachinePageWails(http.HandlerFunc(b.serveWails))
}

// safeNext keeps redirects/deep links on the dashboard: an absolute local path
// that is not protocol-relative and not one of the non-page routes.
func safeNext(p string) string {
	if p == "" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\r\n") {
		return "/"
	}
	for _, deny := range []string{"/api", "/wails", "/__desktop", "/ws", "/agent/ws", "/internal"} {
		if p == deny || strings.HasPrefix(p, deny+"/") || strings.HasPrefix(p, deny+"?") {
			return "/"
		}
	}
	return p
}
