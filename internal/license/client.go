package license

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultBaseURL is the license server of a release build. Empty in the source;
// release builds inject it with
// -ldflags "-X github.com/schlunsen/eggify/internal/license.DefaultBaseURL=https://host"
// (scripts/build-desktop-wails.sh, EGGIFY_BUILD_LICENSE_URL). EGGIFY_LICENSE_URL
// overrides it. With neither, activation fails with CodeNoServer.
var DefaultBaseURL = ""

// CodeNoServer: no license server address is configured (not from the server).
const CodeNoServer = "no_server"

// errNoServer is what activate/renew/deactivate return without a server address.
func errNoServer() error {
	return &APIError{Code: CodeNoServer, ServerMsg: "This build has no license server configured. Set EGGIFY_LICENSE_URL to the license server's address and try again."}
}

// BaseURLFromEnv returns EGGIFY_LICENSE_URL, or DefaultBaseURL (possibly empty).
func BaseURLFromEnv() string {
	if u := strings.TrimSpace(os.Getenv("EGGIFY_LICENSE_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return DefaultBaseURL
}

// Error codes of the license API (docs/licensing-protocol.md), plus
// CodeOffline for a server that couldn't be reached.
const (
	CodeInvalidSerial  = "invalid_serial"
	CodeBadRequest     = "bad_request"
	CodeStaleTimestamp = "stale_timestamp"
	CodeBadSignature   = "bad_signature"
	CodeUnknownSerial  = "unknown_serial"
	CodeSerialRevoked  = "serial_revoked"
	CodeSeatsFull      = "seats_full"
	CodeRateLimited    = "rate_limited"
	CodeNotActivated   = "not_activated"
	CodeOffline        = "offline"
	CodeServerError    = "server_error"
)

// Activation is one machine holding a seat (listed with seats_full).
type Activation struct {
	Machine     string `json:"machine"`
	ActivatedAt int64  `json:"activated_at"`
	LastSeenAt  int64  `json:"last_seen_at"`
}

// APIError is a refusal from the license server, or CodeOffline when it
// couldn't be reached. Error() is plain words for the user.
type APIError struct {
	Status      int          `json:"-"`
	Code        string       `json:"error"`
	ServerMsg   string       `json:"message"`
	Activations []Activation `json:"activations,omitempty"`
}

func (e *APIError) Error() string { return userMessage(e) }

// userMessage maps an error code to plain words. Unknown codes use the
// server's own message.
func userMessage(e *APIError) string {
	switch e.Code {
	case CodeInvalidSerial:
		return ErrInvalidSerial.Error()
	case CodeUnknownSerial:
		return "That serial number doesn't exist. Check it for typos."
	case CodeSerialRevoked:
		return "This serial number has been revoked. Contact support if you think this is a mistake."
	case CodeSeatsFull:
		var names []string
		for _, a := range e.Activations {
			if a.Machine != "" {
				names = append(names, a.Machine)
			}
		}
		msg := "This serial number is already active on the most computers it allows"
		if len(names) > 0 {
			msg += ": " + strings.Join(names, ", ")
		}
		return msg + ". Deactivate it on one of them (Settings → License on that Mac) and try again."
	case CodeStaleTimestamp:
		return "Your computer's clock is off by more than 5 minutes. Set the date and time automatically, then try again."
	case CodeBadSignature:
		return "The license server didn't accept this computer's signature. Try again; if it keeps happening, contact support."
	case CodeRateLimited:
		return "Too many attempts. Wait a minute and try again."
	case CodeNotActivated:
		return "This computer isn't activated any more. Enter your serial number again."
	case CodeOffline:
		return "Couldn't reach the license server. Check your internet connection and try again."
	}
	if e.ServerMsg != "" {
		return e.ServerMsg
	}
	if e.Status >= 500 {
		return "The license server had a problem. Try again in a few minutes."
	}
	return "The license server refused the request."
}

// IsOffline reports whether err means the server couldn't be reached (or
// answered with a server error), so a retry later may succeed.
func IsOffline(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Code == CodeOffline || ae.Code == CodeServerError)
}

// Client calls the license API with signed requests.
type Client struct {
	BaseURL    string
	HTTP       *http.Client
	AppVersion string
	Now        func() time.Time
}

// ActivateResult is a successful activate.
type ActivateResult struct {
	Token      string `json:"token"`
	ExpiresAt  int64  `json:"expires_at"`
	SerialHint string `json:"serial_hint"`
	Seats      int    `json:"seats"`
	SeatsUsed  int    `json:"seats_used"`
}

// RenewResult is a successful renew.
type RenewResult struct {
	Token      string `json:"token"`
	ExpiresAt  int64  `json:"expires_at"`
	SerialHint string `json:"serial_hint"`
}

type signedFields struct {
	InstallKey string `json:"install_key"`
	Timestamp  int64  `json:"timestamp"`
	Signature  string `json:"signature"`
}

func (c *Client) sign(priv ed25519.PrivateKey, action, serial string) signedFields {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	ts := now().Unix()
	return signedFields{
		InstallKey: InstallKeyString(priv.Public().(ed25519.PublicKey)),
		Timestamp:  ts,
		Signature:  SignRequest(priv, action, ts, serial),
	}
}

// Activate activates serial (normalised) on this install.
func (c *Client) Activate(ctx context.Context, priv ed25519.PrivateKey, serial, machine string) (*ActivateResult, error) {
	body := struct {
		Serial string `json:"serial"`
		signedFields
		Machine    string `json:"machine,omitempty"`
		AppVersion string `json:"app_version,omitempty"`
	}{serial, c.sign(priv, "activate", serial), machine, c.AppVersion}
	var out ActivateResult
	if err := c.post(ctx, "/v1/activate", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Renew asks for a fresh token for this install.
func (c *Client) Renew(ctx context.Context, priv ed25519.PrivateKey) (*RenewResult, error) {
	body := struct {
		signedFields
		AppVersion string `json:"app_version,omitempty"`
	}{c.sign(priv, "renew", ""), c.AppVersion}
	var out RenewResult
	if err := c.post(ctx, "/v1/renew", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Deactivate frees this install's seat.
func (c *Client) Deactivate(ctx context.Context, priv ed25519.PrivateKey) error {
	return c.post(ctx, "/v1/deactivate", c.sign(priv, "deactivate", ""), nil)
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	if base == "" {
		return errNoServer()
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return &APIError{Code: CodeOffline}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		ae := &APIError{Status: resp.StatusCode}
		_ = json.Unmarshal(raw, ae)
		if ae.Code == "" {
			// Not the license server's JSON (a proxy's error page, a 502
			// while it restarts): treat as a server problem, retried later.
			if resp.StatusCode >= 500 || resp.StatusCode == http.StatusNotFound {
				ae.Code = CodeServerError
			} else {
				ae.Code = fmt.Sprintf("http_%d", resp.StatusCode)
			}
		}
		return ae
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &APIError{Status: resp.StatusCode, Code: CodeServerError}
	}
	return nil
}
