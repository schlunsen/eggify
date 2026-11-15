package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// SessionMeta describes the device a new user session belongs to and how it
// was created.
type SessionMeta struct {
	DeviceName  string
	DeviceModel string
	CreatedVia  string // password | oauth | setup | pairing
	MintedBy    string // pairing: public id of the session that made the code
}

// sessionTouchInterval limits how often last_used_at is written to the DB.
const sessionTouchInterval = 5 * time.Minute

// newSessionID returns a public, random session identifier.
func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// clip trims s and caps it at n runes.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	// Drop control characters (log and UI hygiene).
	out := make([]rune, 0, len(r))
	for _, c := range r {
		if c >= 0x20 && c != 0x7f {
			out = append(out, c)
		}
	}
	return string(out)
}

// sessionMetaFromRequest labels a browser/password session from its
// User-Agent ("Safari on macOS").
func sessionMetaFromRequest(c *fiber.Ctx, via string) SessionMeta {
	ua := c.Get("User-Agent")
	return SessionMeta{DeviceName: describeUserAgent(ua), DeviceModel: clip(ua, 200), CreatedVia: via}
}

// describeUserAgent turns a User-Agent into a short "Browser on OS" label.
func describeUserAgent(ua string) string {
	if ua == "" {
		return ""
	}
	l := strings.ToLower(ua)
	app := ""
	switch {
	case strings.Contains(l, "eggify") && (strings.Contains(l, "cfnetwork") || strings.Contains(l, "darwin")):
		app = "Eggify app"
	case strings.Contains(l, "edg/"):
		app = "Edge"
	case strings.Contains(l, "firefox/"):
		app = "Firefox"
	case strings.Contains(l, "chrome/") || strings.Contains(l, "crios/"):
		app = "Chrome"
	case strings.Contains(l, "safari/"):
		app = "Safari"
	case strings.Contains(l, "curl/"):
		app = "curl"
	}
	osName := ""
	switch {
	case strings.Contains(l, "iphone"):
		osName = "iPhone"
	case strings.Contains(l, "ipad"):
		osName = "iPad"
	case strings.Contains(l, "android"):
		osName = "Android"
	case strings.Contains(l, "mac os") || strings.Contains(l, "macintosh"):
		osName = "macOS"
	case strings.Contains(l, "windows"):
		osName = "Windows"
	case strings.Contains(l, "linux"):
		osName = "Linux"
	}
	switch {
	case app != "" && osName != "":
		return app + " on " + osName
	case app != "":
		return app
	case osName != "":
		return osName
	}
	return "Unknown device"
}

// touchSession records that the session with this token hash was just used.
func (us *UserStore) touchSession(key string) {
	now := time.Now()
	us.mu.Lock()
	s, ok := us.sessions[key]
	if !ok {
		us.mu.Unlock()
		return
	}
	t := now
	s.LastUsedAt = &t
	persist := now.Sub(s.lastPersisted) >= sessionTouchInterval && s.SessionID != ""
	if persist {
		s.lastPersisted = now
	}
	us.mu.Unlock()
	if persist && us.db != nil {
		if err := database.NewSessionRepository(us.db).TouchSession(key, now); err != nil {
			logging.Debug("failed to record session use: %v", err)
		}
	}
}

// SessionInfo is a user session as shown in "Signed-in devices".
type SessionInfo struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DeviceName  string     `json:"device_name"`
	DeviceModel string     `json:"device_model,omitempty"`
	CreatedVia  string     `json:"created_via"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   time.Time  `json:"expires_at"`
	Current     bool       `json:"current"`
}

// ListSessions returns the valid, MFA-complete sessions of username (or of
// every user when username is ""), newest first. currentKey marks the
// caller's own session.
func (us *UserStore) ListSessions(username, currentKey string) []SessionInfo {
	now := time.Now()
	us.mu.RLock()
	defer us.mu.RUnlock()
	out := []SessionInfo{}
	for key, s := range us.sessions {
		if s.SessionID == "" || now.After(s.ExpiresAt) || (s.MFARequired && !s.MFAVerified) {
			continue
		}
		if username != "" && s.Username != username {
			continue
		}
		via := s.CreatedVia
		if via == "" {
			via = "password"
		}
		name := s.DeviceName
		if name == "" {
			name = "Browser session"
		}
		out = append(out, SessionInfo{
			ID: s.SessionID, Username: s.Username, DeviceName: name, DeviceModel: s.DeviceModel,
			CreatedVia: via, CreatedAt: s.CreatedAt, LastUsedAt: s.LastUsedAt, ExpiresAt: s.ExpiresAt,
			Current: key == currentKey,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// sessionByID returns the token hash and session with this public id.
func (us *UserStore) sessionByID(id string) (string, *Session) {
	if id == "" {
		return "", nil
	}
	us.mu.RLock()
	defer us.mu.RUnlock()
	for key, s := range us.sessions {
		if s.SessionID == id {
			return key, s
		}
	}
	return "", nil
}

// sessionIDForToken returns the public id of the session behind token.
func (us *UserStore) sessionIDForToken(token string) string {
	if s := us.GetSession(token); s != nil {
		return s.SessionID
	}
	return ""
}

// revokeSessionKey removes the session with this token hash.
func (us *UserStore) revokeSessionKey(key string) error {
	us.mintMu.Lock()
	defer us.mintMu.Unlock()
	us.mu.Lock()
	minter := ""
	if sess := us.sessions[key]; sess != nil {
		minter = sess.SessionID
	}
	delete(us.sessions, key)
	us.mu.Unlock()
	if us.db == nil {
		us.revokeMinted(minter)
		return nil
	}
	err := database.NewSessionRepository(us.db).DeleteSession(key)
	us.revokeMinted(minter)
	return err
}

// sessionAlive reports whether a session with this public id exists, has not
// expired and is signed in completely.
func (us *UserStore) sessionAlive(id string) bool {
	_, s := us.sessionByID(id)
	return s != nil && time.Now().Before(s.ExpiresAt) && (!s.MFARequired || s.MFAVerified)
}

// revokeMinted revokes the sessions created by redeeming a pairing code that
// the session with public id minter made, and, transitively, the ones those
// minted. A phone paired from a Mac must not outlive the Mac's sign-in. Each
// revoked session's sockets are closed through onMintedRevoked.
func (us *UserStore) revokeMinted(minter string) {
	var revoked []string
	queue := []string{}
	if minter != "" {
		queue = append(queue, minter)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		// Codes the session made but nobody redeemed yet die with it.
		if us.db != nil {
			_ = database.NewPairingRepository(us.db).RevokeUnusedBySession(id)
		}
		us.mu.Lock()
		for key, s := range us.sessions {
			if s.MintedBy == id && s.SessionID != id {
				delete(us.sessions, key)
				revoked = append(revoked, key)
				queue = append(queue, s.SessionID)
			}
		}
		us.mu.Unlock()
	}
	for _, key := range revoked {
		if us.db != nil {
			_ = database.NewSessionRepository(us.db).DeleteSession(key)
		}
		if us.onMintedRevoked != nil {
			us.onMintedRevoked(key)
		}
		logging.Info("Signed out a paired session because the session that paired it was signed out")
	}
}

// currentSessionToken returns the caller's session token, if any.
func currentSessionToken(c *fiber.Ctx, us *UserStore) string {
	for _, tok := range sessionTokenCandidates(c) {
		if us.userForSessionToken(tok) != nil {
			return tok
		}
	}
	return ""
}

var errNotSignedIn = errors.New("not signed in")

// requestUser returns the signed-in user of a request.
func (s *Server) requestUser(c *fiber.Ctx) (*User, error) {
	name, ok := principal.Username(c)
	if !ok {
		return nil, errNotSignedIn
	}
	if u, ok := c.Locals("user").(*User); ok && u != nil && u.Username == name {
		return u, nil
	}
	s.userStore.mu.RLock()
	defer s.userStore.mu.RUnlock()
	if u, ok := s.userStore.users[name]; ok {
		return u, nil
	}
	return nil, errNotSignedIn
}

// handleListSessions: GET /api/auth/sessions — the caller's signed-in
// devices. Admins may pass ?all=true for every user's sessions.
func (s *Server) handleListSessions(c *fiber.Ctx) error {
	user, err := s.requestUser(c)
	if err != nil {
		return principal.Forbidden(c, SessionsAction)
	}
	username := user.Username
	if c.Query("all") == "true" && user.IsAdmin {
		username = ""
	}
	currentKey := ""
	if tok := currentSessionToken(c, s.userStore); tok != "" {
		currentKey = database.HashToken(tok)
	}
	sessions := s.userStore.ListSessions(username, currentKey)
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"sessions": sessions, "count": len(sessions)})
}

// handleRevokeSession: DELETE /api/auth/sessions/:id — sign a device out.
// Users may revoke their own sessions; admins any session.
func (s *Server) handleRevokeSession(c *fiber.Ctx) error {
	user, err := s.requestUser(c)
	if err != nil {
		return principal.Forbidden(c, SessionsAction)
	}
	key, sess := s.userStore.sessionByID(c.Params("id"))
	// Someone else's session looks exactly like a missing one to non-admins.
	if sess == nil || (sess.Username != user.Username && !user.IsAdmin) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Session not found"})
	}
	if err := s.userStore.revokeSessionKey(key); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to sign out the device"})
	}
	s.closeSocketsForToken(key)
	logging.Info("Session %s of %s (%s) signed out by %s from %s", sess.SessionID, sess.Username, sess.DeviceName, user.Username, clientIP(c))
	return c.JSON(fiber.Map{"revoked": true, "id": sess.SessionID})
}

// SessionsAction names the session-management actions in user_required refusals.
const SessionsAction = "Managing signed-in devices"
