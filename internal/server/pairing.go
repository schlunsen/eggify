package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/remoteaccess"
	"github.com/schlunsen/eggify/internal/server/principal"
)

// QR-code pairing of the iOS app.
//
// A signed-in user (principal.RequireUser: an MFA-complete session, never the
// API key) asks for a pairing code. The dashboard shows it as a QR code of
//
//	eggify://pair?u=<base URL>&c=<code>[&p=<pin>]
//
// p is present when base URL is this install's Eggify relay address: the
// phone then accepts only a server key whose SHA-256 (of the
// SubjectPublicKeyInfo, base64url) equals p, whatever the certificate says.
//
// The phone scans it (system Camera or the in-app scanner) and redeems the
// code at POST /api/auth/pairing/redeem, a public endpoint like login. The
// redeem atomically claims the code and mints an ordinary user session for
// the code's owner, marked created_via=pairing with the phone's name.
//
// Security:
//   - 32 random bytes per code; only database.HashToken(code) is stored.
//   - Codes expire after PairingTTL and work once (atomic UPDATE ... WHERE
//     used_at IS NULL). A new code revokes the user's earlier unused codes.
//   - Redeem is rate limited per IP and globally, answers every failure with
//     the same message, and never logs the code.
//   - Redeem is refused over plain http unless the peer is loopback (or the
//     request came through the remote-access tunnel, which is https).

// PairingTTL is how long a pairing code can be redeemed.
const PairingTTL = 5 * time.Minute

// PairingAction names pairing creation in user_required refusals.
const PairingAction = "Pairing a device"

// pairingInvalidMessage is the one answer for every failed redeem.
const pairingInvalidMessage = "This pairing code is invalid or has expired. Show a new code on your computer and scan it again."

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// normalizeBaseURL validates a server address for a pairing link: http(s),
// a host, no credentials, query or fragment; the path is dropped.
func normalizeBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("base_url must be an http(s) URL with a host")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("base_url must not contain credentials, a query or a fragment")
	}
	return u.Scheme + "://" + u.Host, nil
}

// isLoopbackHost reports whether host (optionally host:port) is loopback.
func isLoopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// pairingBaseURL picks the address the phone should use: the live remote
// access URL if it is connected, otherwise the address this request came to.
func (s *Server) pairingBaseURL(c *fiber.Ctx) string {
	if s.remoteAccess != nil {
		if st := s.remoteAccess.Status(); st.Status == remoteaccess.StateConnected && st.PublicURL != "" {
			if u, err := normalizeBaseURL(st.PublicURL); err == nil {
				return u
			}
		}
	}
	scheme := "http"
	if c.Context().IsTLS() || isTunnelRequest(c) {
		scheme = "https"
	}
	return scheme + "://" + string(c.Request().Host())
}

// relayPinFor is the certificate pin a phone must check for base: this
// install's relay key pin when base is the live relay address (TLS ends in
// Eggify there, with that key), "" otherwise (ngrok's edge or a local
// listener present other certificates).
func (s *Server) relayPinFor(base string) string {
	if s.remoteAccess == nil || s.remoteAccess.Mode() != remoteaccess.ModeRelay {
		return ""
	}
	st := s.remoteAccess.Status()
	if st.Status != remoteaccess.StateConnected {
		return ""
	}
	live, err := normalizeBaseURL(st.PublicURL)
	if err != nil || !strings.EqualFold(live, base) {
		return ""
	}
	return s.remoteAccess.RelayPin()
}

// clientIP is the TCP peer address (no proxy header is trusted, see
// server.go); kept as a helper with a fallback to the raw peer.
func clientIP(c *fiber.Ctx) string {
	if ip := c.IP(); ip != "" {
		return ip
	}
	return c.Context().RemoteIP().String()
}

// transportIsSecure: TLS, the (https) tunnel, or a loopback peer.
func transportIsSecure(c *fiber.Ctx) bool {
	if c.Context().IsTLS() || isTunnelRequest(c) {
		return true
	}
	ip := c.Context().RemoteIP()
	return ip != nil && ip.IsLoopback()
}

type createPairingRequest struct {
	BaseURL    string `json:"base_url"`
	DeviceName string `json:"device_name"`
}

// handleCreatePairing: POST /api/auth/pairing (signed-in user only).
func (s *Server) handleCreatePairing(c *fiber.Ctx) error {
	user, err := s.requestUser(c)
	if err != nil {
		return principal.Forbidden(c, PairingAction)
	}
	var req createPairingRequest
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}
	}
	base := s.pairingBaseURL(c)
	if strings.TrimSpace(req.BaseURL) != "" {
		if base, err = normalizeBaseURL(req.BaseURL); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
	}

	code := randomToken(32)
	now := time.Now()
	p := &database.PairingCode{
		ID:               randomHex(16),
		CodeHash:         database.HashToken(code),
		Username:         user.Username,
		CreatedBySession: s.userStore.sessionIDForToken(currentSessionToken(c, s.userStore)),
		DeviceNameHint:   clip(req.DeviceName, 64),
		BaseURL:          base,
		CreatedAt:        now,
		ExpiresAt:        now.Add(PairingTTL),
	}
	if err := database.NewPairingRepository(s.db).Create(p); err != nil {
		logging.Error("Failed to create pairing code: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create a pairing code"})
	}
	logging.Info("Pairing %s created by %s from %s (base %s)", p.ID, user.Username, clientIP(c), base)

	pairURL := "eggify://pair?u=" + url.QueryEscape(base) + "&c=" + code
	resp := fiber.Map{
		"id":          p.ID,
		"code":        code,
		"base_url":    base,
		"expires_at":  p.ExpiresAt.UTC(),
		"ttl_seconds": int(PairingTTL.Seconds()),
	}
	if pin := s.relayPinFor(base); pin != "" {
		pairURL += "&p=" + pin
		resp["cert_pin"] = pin
	}
	resp["pair_url"] = pairURL
	if u, err := url.Parse(base); err == nil && isLoopbackHost(u.Host) {
		resp["warning"] = "This address only works on this computer (or the iOS Simulator). Turn on Remote access to pair a phone."
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(resp)
}

// handlePairingStatus: GET /api/auth/pairing/:id/status (owner only).
func (s *Server) handlePairingStatus(c *fiber.Ctx) error {
	user, err := s.requestUser(c)
	if err != nil {
		return principal.Forbidden(c, PairingAction)
	}
	p, err := database.NewPairingRepository(s.db).Get(c.Params("id"))
	if err != nil || p == nil || p.Username != user.Username {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pairing not found"})
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{
		"id":          p.ID,
		"status":      p.Status(time.Now()),
		"device_name": p.RedeemedDeviceName,
		"expires_at":  p.ExpiresAt.UTC(),
	})
}

// handleCancelPairing: DELETE /api/auth/pairing/:id (owner only).
func (s *Server) handleCancelPairing(c *fiber.Ctx) error {
	user, err := s.requestUser(c)
	if err != nil {
		return principal.Forbidden(c, PairingAction)
	}
	repo := database.NewPairingRepository(s.db)
	p, err := repo.Get(c.Params("id"))
	if err != nil || p == nil || p.Username != user.Username {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Pairing not found"})
	}
	if err := repo.Revoke(p.ID); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to cancel the pairing"})
	}
	return c.JSON(fiber.Map{"cancelled": true})
}

type redeemPairingRequest struct {
	Code        string `json:"code"`
	DeviceName  string `json:"device_name"`
	DeviceModel string `json:"device_model"`
}

func pairingInvalid(c *fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error": pairingInvalidMessage,
		"code":  "pairing_invalid",
	})
}

// handleRedeemPairing: POST /api/auth/pairing/redeem (public, rate limited).
func (s *Server) handleRedeemPairing(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	if !transportIsSecure(c) {
		logging.Warning("SECURITY: pairing redeem over plain http from %s refused", clientIP(c))
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "Pairing needs an https connection to Eggify.",
			"code":  "https_required",
		})
	}
	var req redeemPairingRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	code := strings.TrimSpace(req.Code)
	if code == "" || len(code) > 128 {
		logging.Warning("Pairing redeem with a malformed code from %s", clientIP(c))
		return pairingInvalid(c)
	}
	deviceName := clip(req.DeviceName, 64)
	if deviceName == "" {
		deviceName = "iOS device"
	}

	repo := database.NewPairingRepository(s.db)
	p, err := repo.Claim(database.HashToken(code), deviceName, time.Now())
	if err != nil {
		if !errors.Is(err, database.ErrPairingCodeInvalid) {
			logging.Error("Pairing redeem failed: %v", err)
		}
		logging.Warning("Pairing redeem refused (invalid, used or expired code) from %s", clientIP(c))
		return pairingInvalid(c)
	}

	s.userStore.mu.RLock()
	user := s.userStore.users[p.Username]
	s.userStore.mu.RUnlock()
	if user == nil {
		logging.Warning("Pairing %s redeem refused: user no longer exists", p.ID)
		return pairingInvalid(c)
	}
	if s.config != nil {
		validator := NewAccessValidator(s.config.Auth.AccessControl, s.db)
		if err := validator.ValidateUser(user.Username, user.Email); err != nil {
			_ = validator.LogAccessDenied(user.Username, user.Email, "pairing_denied", err.Error(), clientIP(c), c.Get("User-Agent"))
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": validator.GetDenyMessage()})
		}
	}

	// SECURITY: a code made by a session that has since been signed out,
	// revoked or expired is dead: check that the minter is alive and create
	// the session under the lock that revocation takes.
	s.userStore.mintMu.Lock()
	if p.CreatedBySession != "" && !s.userStore.sessionAlive(p.CreatedBySession) {
		s.userStore.mintMu.Unlock()
		logging.Warning("Pairing %s redeem refused: the session that made the code is gone", p.ID)
		return pairingInvalid(c)
	}
	token, err := s.userStore.createUserSession(user.Username, SessionMeta{
		DeviceName:  deviceName,
		DeviceModel: clip(req.DeviceModel, 64),
		CreatedVia:  "pairing",
		MintedBy:    p.CreatedBySession,
	})
	s.userStore.mintMu.Unlock()
	if err != nil {
		logging.Error("Pairing %s: failed to create session: %v", p.ID, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create a session"})
	}
	sessionID := s.userStore.sessionIDForToken(token)
	_ = repo.SetRedeemedSession(p.ID, sessionID)
	logging.Info("Pairing %s redeemed: %s signed in on %q from %s", p.ID, user.Username, deviceName, clientIP(c))

	return c.JSON(fiber.Map{
		"token":       token,
		"token_type":  "Bearer",
		"expires_at":  time.Now().Add(s.userStore.SessionLifetime()).UTC(),
		"username":    user.Username,
		"is_admin":    user.IsAdmin,
		"session_id":  sessionID,
		"device_name": deviceName,
	})
}
