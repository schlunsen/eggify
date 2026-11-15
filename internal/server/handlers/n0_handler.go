package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/connectors"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/n0"
	"github.com/schlunsen/eggify/internal/remoteaccess"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// requestScheme is "https" when the client reached Eggify over https: a TLS
// listener or the ngrok tunnel (https at the edge, plain inside the
// process). Request headers (X-Forwarded-Proto) never count.
func requestScheme(c *fiber.Ctx) string {
	if c.Context().IsTLS() || remoteaccess.IsTunnelConn(c.Context().Conn()) {
		return "https"
	}
	return "http"
}

// N0Handler serves the n0 OAuth connector API used by the Connectors page
// and the session UI. No response ever contains an n0 token.
type N0Handler struct {
	m *n0.Manager
	// loopbackCallback returns the loopback OAuth callback URL ("" if the
	// loopback listener is not running).
	loopbackCallback func() string
	// authorizeSession reports whether the caller may manage a session; when
	// it returns false it has already written the error response.
	authorizeSession func(c *fiber.Ctx, id uuid.UUID) bool
	// toolsChanged tells the session manager a session's n0 tools changed.
	toolsChanged  func(id uuid.UUID)
	connectorRepo *database.ConnectorRepository
	key           []byte
}

// NewN0Handler creates the handler.
func NewN0Handler(m *n0.Manager, db *database.Database, key []byte, loopbackCallback func() string) *N0Handler {
	return &N0Handler{m: m, loopbackCallback: loopbackCallback, connectorRepo: database.NewConnectorRepository(db), key: key}
}

// SetSessionHooks wires session ownership checks and tool reloads.
func (h *N0Handler) SetSessionHooks(authorize func(c *fiber.Ctx, id uuid.UUID) bool, toolsChanged func(id uuid.UUID)) {
	h.authorizeSession = authorize
	h.toolsChanged = toolsChanged
}

// Register adds the n0 routes under /api/connectors/n0.
func (h *N0Handler) Register(api fiber.Router) {
	g := api.Group("/connectors/n0")
	// Connecting, disconnecting and binding n0 to a session change which
	// credentials a session can use: a signed-in user only.
	changeN0 := principal.RequireUser("Changing the n0 connector")
	g.Get("/", h.HandleOverview)
	g.Post("/oauth/start", changeN0, h.HandleStart)
	g.Get("/oauth/status", h.HandleStatus)
	g.Delete("/connections/:id", changeN0, h.HandleDisconnect)
	g.Get("/bindings", h.HandleBindings)
	g.Get("/sessions/:sessionId", h.HandleGetSession)
	g.Put("/sessions/:sessionId", changeN0, h.HandleSetSession)
}

// HandleCallback is the public (https) OAuth redirect target, registered
// before auth middleware: the browser arriving from n0 may not carry an Eggify
// session cookie. It is protected by the single-use state + PKCE verifier.
func (h *N0Handler) HandleCallback(c *fiber.Ctx) error {
	st := h.m.CompleteAuthorization(c.UserContext(), c.Query("state"), c.Query("code"), c.Query("error"), c.Query("error_description"))
	w := &fiberResponseWriter{c: c, header: http.Header{}}
	n0.RenderCallbackPage(w, st)
	return nil
}

type fiberResponseWriter struct {
	c      *fiber.Ctx
	header http.Header
	status int
}

func (w *fiberResponseWriter) Header() http.Header { return w.header }
func (w *fiberResponseWriter) WriteHeader(code int) {
	w.status = code
}
func (w *fiberResponseWriter) Write(b []byte) (int, error) {
	for k, v := range w.header {
		for _, vv := range v {
			w.c.Set(k, vv)
		}
	}
	if w.status != 0 {
		w.c.Status(w.status)
	}
	w.header = http.Header{}
	return w.c.Write(b)
}

func connectionJSON(c *database.N0Connection) fiber.Map {
	var lastUsed interface{}
	if !c.LastUsedAt.IsZero() {
		lastUsed = c.LastUsedAt.UTC().Format(time.RFC3339)
	}
	scopes := []string{}
	if s := strings.TrimSpace(c.Scopes); s != "" {
		scopes = strings.Fields(s)
	}
	return fiber.Map{
		"id":                  c.ID,
		"host":                c.Host,
		"workspace_name":      c.WorkspaceName,
		"app_subdomain":       c.AppSubdomain,
		"app_name":            c.AppName,
		"status":              c.Status,
		"status_message":      c.StatusMessage,
		"scopes":              scopes,
		"last_used_at":        lastUsed,
		"created_at":          c.CreatedAt.UTC().Format(time.RFC3339),
		"connected_tools_url": n0.ConnectedToolsURL(c.Host),
	}
}

func requestHeaders(c *fiber.Ctx) http.Header {
	hdr := http.Header{}
	for k, vs := range c.GetReqHeaders() {
		for _, v := range vs {
			hdr.Add(k, v)
		}
	}
	return hdr
}

func (h *N0Handler) browserIsLocal(c *fiber.Ctx) bool {
	return n0.IsLoopbackRequest(c.Context().RemoteAddr().String(), requestHeaders(c))
}

// HandleOverview lists hosts, connections and the legacy API key state.
func (h *N0Handler) HandleOverview(c *fiber.Ctx) error {
	conns, err := h.m.Repo().ListN0Connections()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to list n0 connections"})
	}
	list := make([]fiber.Map, 0, len(conns))
	for _, conn := range conns {
		list = append(list, connectionJSON(conn))
	}
	legacy := fiber.Map{"configured": false}
	if uc, err := h.connectorRepo.GetConnectionBySlug(n0.ConnectorSlug); err == nil && uc != nil && uc.APIKey != nil && *uc.APIKey != "" {
		legacy["configured"] = true
		if dec, err := connectors.Decrypt(*uc.APIKey, h.key); err == nil && dec != "" {
			legacy["api_key_masked"] = connectors.MaskAPIKey(dec)
		}
		if uc.ExtraConfig != nil {
			var extra map[string]interface{}
			if json.Unmarshal([]byte(*uc.ExtraConfig), &extra) == nil {
				if d, ok := extra["domain"].(string); ok {
					legacy["domain"] = d
				}
			}
		}
	}
	return c.JSON(fiber.Map{
		"hosts":            n0.KnownHosts,
		"connections":      list,
		"legacy":           legacy,
		"browser_is_local": h.browserIsLocal(c),
	})
}

type n0StartBody struct {
	Host         string `json:"host"`
	ReconnectID  int64  `json:"reconnect_id"`
	RedirectMode string `json:"redirect_mode"` // auto | loopback | public
	ReturnTo     string `json:"return_to"`
}

// HandleStart begins an authorization and returns the n0 authorize URL.
func (h *N0Handler) HandleStart(c *fiber.Ctx) error {
	var body n0StartBody
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request body"})
	}
	scheme := requestScheme(c)
	origin := scheme + "://" + c.Hostname()
	req := n0.StartRequest{Host: body.Host, ReconnectID: body.ReconnectID, ReturnTo: h.safeReturnTo(body.ReturnTo, origin)}

	mode := body.RedirectMode
	if mode == "" || mode == "auto" {
		mode = "public"
		if h.browserIsLocal(c) || isLoopbackHostname(c.Hostname()) {
			mode = "loopback"
		}
	}
	switch mode {
	case "loopback":
		cb := ""
		if h.loopbackCallback != nil {
			cb = h.loopbackCallback()
		}
		if cb == "" {
			return c.Status(503).JSON(fiber.Map{"error": "Eggify's loopback OAuth callback is not running", "error_code": "loopback_unavailable"})
		}
		req.RedirectURI, req.RedirectKey = cb, n0.RedirectKeyLoopback
	case "public":
		redirect := origin + n0.CallbackPath
		if scheme != "https" {
			return c.Status(400).JSON(fiber.Map{
				"error":      "Eggify is reached over plain http at " + origin + ", and n0 only accepts loopback or allowlisted https callbacks.",
				"error_code": "invalid_redirect_uri",
				"hint":       "Open Eggify over https, or — if your browser runs on the same machine as Eggify — choose \"My browser runs on the same machine as Eggify\".",
			})
		}
		req.RedirectURI, req.RedirectKey = redirect, redirect
	default:
		return c.Status(400).JSON(fiber.Map{"error": "redirect_mode must be auto, loopback or public"})
	}

	res, err := h.m.StartAuthorization(c.UserContext(), req)
	if err != nil {
		var se *n0.StartError
		if errors.As(err, &se) {
			status := 400
			if se.Code == "discovery_failed" || se.Code == "registration_failed" {
				status = 502
			}
			return c.Status(status).JSON(se)
		}
		logging.Warning("n0 oauth start failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(res)
}

func isLoopbackHostname(h string) bool {
	return h == "localhost" || n0.IsLoopbackAddr(h) || strings.HasPrefix(h, "localhost:") || strings.HasPrefix(h, "127.")
}

// safeReturnTo only allows returning to this Eggify origin.
func (h *N0Handler) safeReturnTo(raw, origin string) string {
	def := origin + "/connectors"
	if raw == "" {
		return def
	}
	u, err := url.Parse(raw)
	if err != nil {
		return def
	}
	// The desktop app's own deep link: the consent page runs in the system
	// browser, which has no Eggify session on the app's loopback server, so
	// the result page hands back to the app instead (eggify://connectors).
	if u.Scheme == desktopScheme && u.Host == "connectors" && u.User == nil && u.Path == "" {
		return desktopScheme + "://connectors"
	}
	if u.Scheme+"://"+u.Host != origin {
		return def
	}
	return u.String()
}

// desktopScheme is the URL scheme the Eggify desktop app registers.
const desktopScheme = "eggify"

// HandleStatus reports an authorization's progress (polled by the UI).
func (h *N0Handler) HandleStatus(c *fiber.Ctx) error {
	return c.JSON(h.m.FlowStatus(c.Query("state")))
}

// HandleDisconnect revokes at n0 (best effort) and deletes locally.
func (h *N0Handler) HandleDisconnect(c *fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid connection id"})
	}
	revoked, err := h.m.Disconnect(c.UserContext(), id)
	if errors.Is(err, database.ErrN0ConnectionNotFound) {
		return c.Status(404).JSON(fiber.Map{"error": "n0 connection not found"})
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to disconnect: " + err.Error()})
	}
	return c.JSON(fiber.Map{"status": "success", "revoked_at_n0": revoked})
}

// HandleBindings maps session id → bound connection for sessions with n0 enabled.
func (h *N0Handler) HandleBindings(c *fiber.Ctx) error {
	out := fiber.Map{}
	repo := h.m.Repo()
	enabled, err := repo.SessionsWithConnectorEnabled(n0.ConnectorSlug)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to load bindings"})
	}
	if len(enabled) == 0 || !h.m.HasConnections() {
		return c.JSON(fiber.Map{"bindings": out})
	}
	bindings, err := repo.ListN0SessionBindings()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to load bindings"})
	}
	conns, _ := repo.ListN0Connections()
	byID := map[int64]*database.N0Connection{}
	for _, cn := range conns {
		byID[cn.ID] = cn
	}
	var def *database.N0Connection
	if d, err := h.m.DefaultConnection(); err == nil {
		def = d
	}
	for _, sid := range enabled {
		cn := byID[bindings[sid]]
		if cn == nil {
			cn = def // what ResolveSession will bind on first use
		}
		if cn == nil {
			continue
		}
		out[sid] = fiber.Map{
			"connection_id":  cn.ID,
			"host":           cn.Host,
			"app_subdomain":  cn.AppSubdomain,
			"app_name":       cn.AppName,
			"workspace_name": cn.WorkspaceName,
			"status":         cn.Status,
		}
	}
	return c.JSON(fiber.Map{"bindings": out})
}

func (h *N0Handler) sessionParam(c *fiber.Ctx) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params("sessionId"))
	if err != nil {
		return uuid.Nil, c.Status(400).JSON(fiber.Map{"error": "invalid session id"})
	}
	if h.authorizeSession != nil && !h.authorizeSession(c, id) {
		return uuid.Nil, nil
	}
	return id, nil
}

func (h *N0Handler) sessionState(c *fiber.Ctx, id uuid.UUID) error {
	row, err := h.m.ResolveSession(id.String())
	switch {
	case errors.Is(err, n0.ErrNotEnabled):
		return c.JSON(fiber.Map{"enabled": false, "connection_id": nil, "connection": nil})
	case errors.Is(err, n0.ErrNoConnection):
		return c.JSON(fiber.Map{"enabled": true, "connection_id": nil, "connection": nil})
	case err != nil:
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"enabled": true, "connection_id": row.ID, "connection": connectionJSON(row)})
}

// HandleGetSession returns the n0 state of a session.
func (h *N0Handler) HandleGetSession(c *fiber.Ctx) error {
	id, err := h.sessionParam(c)
	if id == uuid.Nil {
		return err
	}
	return h.sessionState(c, id)
}

// HandleSetSession binds a session to a connection (or disables n0 with null).
func (h *N0Handler) HandleSetSession(c *fiber.Ctx) error {
	id, err := h.sessionParam(c)
	if id == uuid.Nil {
		return err
	}
	var body struct {
		ConnectionID *int64 `json:"connection_id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request body"})
	}
	repo := h.m.Repo()
	sid := id.String()
	if body.ConnectionID == nil {
		if err := h.connectorRepo.DisableSessionConnector(sid, n0.ConnectorSlug); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		_ = repo.UnbindN0Session(sid)
	} else {
		if _, err := repo.GetN0Connection(*body.ConnectionID); err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "n0 connection not found"})
		}
		_ = repo.EnsureN0ConnectorMarker()
		if err := h.connectorRepo.EnableSessionConnector(sid, n0.ConnectorSlug); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		if err := repo.BindN0Session(sid, *body.ConnectionID); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
	}
	if h.toolsChanged != nil {
		h.toolsChanged(id)
	}
	return h.sessionState(c, id)
}
