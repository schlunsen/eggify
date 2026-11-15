// Package machineproxy lets the desktop app show another of the owner's
// Eggify machines in its window (ADR 014). The other machine is reached only
// through the Eggify relay, with its key pinned; the window talks to a
// loopback proxy that adds the paired session token, so the token never
// reaches the page or any other local process.
package machineproxy

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/schlunsen/eggify/internal/relay"
)

// Machine is a paired machine: its relay address and the pin of its key.
type Machine struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"` // https://<id>.<relay suffix>[:port]
	Pin     string `json:"pin"`      // base64url SHA-256(SPKI), from the pair link
}

// Validate checks that the address is https and that its first host label
// is the install id the pin belongs to, like the iOS app (CertificatePins).
func (m Machine) Validate() error {
	u, err := url.Parse(m.BaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("machine address must be a plain https:// relay address")
	}
	id, err := relay.IDFromPin(m.Pin)
	if err != nil {
		return err
	}
	label, _, _ := strings.Cut(strings.ToLower(u.Hostname()), ".")
	if label != id {
		return errors.New("machine address does not belong to its pin")
	}
	return nil
}

// origin is the machine's https origin without a trailing slash.
func (m Machine) origin() string { return strings.TrimRight(m.BaseURL, "/") }

// PinnedTLSConfig accepts exactly the key with this pin for host, whatever
// the certificate's names, dates or issuer (relay certificates are
// self-signed and rebuilt per start).
func PinnedTLSConfig(host, pin string) *tls.Config {
	return &tls.Config{
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // replaced by VerifyConnection below
		// Resumed handshakes skip the key exchange work; VerifyConnection
		// still checks the pin of the certificate the session was made with.
		ClientSessionCache: tls.NewLRUClientSessionCache(16),
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("machine presented no certificate")
			}
			got := relay.PinFromSPKI(cs.PeerCertificates[0].RawSubjectPublicKeyInfo)
			if subtle.ConstantTimeCompare([]byte(got), []byte(pin)) != 1 {
				return errors.New("machine key does not match its pin")
			}
			return nil
		},
	}
}

// pinnedTransport is an HTTP/1.1 transport (WebSocket upgrades go through
// it too) that only talks to the machine's pinned key.
func pinnedTransport(m Machine) (*http.Transport, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	u, _ := url.Parse(m.BaseURL)
	return &http.Transport{
		Proxy:                 nil, // never through a system proxy
		DialContext:           dialWithDNSFallback((&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext),
		TLSClientConfig:       PinnedTLSConfig(u.Hostname(), m.Pin),
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   8,
	}, nil
}

// newTransport is replaced by tests to dial a test relay.
var newTransport = pinnedTransport

// Client is an HTTP client for the machine as the paired user (pinned
// transport, Bearer token, no redirects), for the app's own calls.
func Client(m Machine, token string) (*http.Client, error) {
	tr, err := newTransport(m)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Transport:     bearer{tr, token},
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

type bearer struct {
	rt    http.RoundTripper
	token string
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.rt.RoundTrip(r)
}

// Hostname asks the machine for its host name ("Studio.local" → "Studio"),
// "" when it doesn't say.
func Hostname(ctx context.Context, m Machine, token string) string {
	c, err := Client(m, token)
	if err != nil {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.origin()+"/api/system-info", nil)
	if err != nil {
		return ""
	}
	resp, err := c.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out struct {
		System struct {
			Hostname string `json:"hostname"`
		} `json:"system"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&out) != nil {
		return ""
	}
	name := strings.TrimSuffix(strings.TrimSpace(out.System.Hostname), ".local")
	if len(name) > 40 {
		name = name[:40]
	}
	return name
}

// SignOut ends the paired session on the machine (best effort): removing a
// machine here also removes this Mac from its Signed-in devices.
func SignOut(ctx context.Context, m Machine, token string) error {
	c, err := Client(m, token)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.origin()+"/api/auth/logout", nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Pairing is the result of redeeming a pair link.
type Pairing struct {
	Machine   Machine   `json:"machine"`
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	SessionID string    `json:"session_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ParsePairLink reads eggify://pair?u=<base URL>&c=<code>&p=<pin>. Only relay
// links (with a pin) are accepted.
func ParsePairLink(raw string) (Machine, string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "eggify" || u.Host != "pair" {
		return Machine{}, "", errors.New("not an eggify://pair link")
	}
	q := u.Query()
	m := Machine{BaseURL: strings.TrimRight(q.Get("u"), "/"), Pin: q.Get("p")}
	code := q.Get("c")
	if code == "" {
		return Machine{}, "", errors.New("pair link has no code")
	}
	if m.Pin == "" {
		return Machine{}, "", errors.New("pair link has no key pin: turn on remote access in Eggify relay mode on the other machine")
	}
	if err := m.Validate(); err != nil {
		return Machine{}, "", err
	}
	return m, code, nil
}

// Redeem claims a pair link's code at the other machine (over the pinned
// connection) and returns the session it minted for this Mac.
func Redeem(ctx context.Context, link, deviceName string) (*Pairing, error) {
	m, code, err := ParsePairLink(link)
	if err != nil {
		return nil, err
	}
	tr, err := newTransport(m)
	if err != nil {
		return nil, err
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport:     tr,
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	body, _ := json.Marshal(map[string]string{"code": code, "device_name": deviceName, "device_model": "Mac (Eggify desktop)"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.origin()+"/api/auth/pairing/redeem", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the other machine: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Token     string    `json:"token"`
		Username  string    `json:"username"`
		SessionID string    `json:"session_id"`
		ExpiresAt time.Time `json:"expires_at"`
		Error     string    `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return nil, fmt.Errorf("unexpected answer from the other machine (%d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || out.Token == "" {
		if out.Error == "" {
			out.Error = resp.Status
		}
		return nil, errors.New(out.Error)
	}
	return &Pairing{Machine: m, Token: out.Token, Username: out.Username, SessionID: out.SessionID, ExpiresAt: out.ExpiresAt}, nil
}

// ErrSignedOut is returned when the machine refuses the paired token (this
// Mac was signed out there, or the session expired).
var ErrSignedOut = errors.New("the other Mac signed this Mac out")

// PairCode is a fresh pair link minted on a paired machine for another of
// the owner's devices (the iPhone, ADR 014): the device redeems it at the
// machine itself and gets its own session there.
type PairCode struct {
	PairURL   string    `json:"pair_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CreatePairing asks the machine, as the paired user, for a pair link for
// another device (POST /api/auth/pairing with base_url = its relay address,
// so the link carries its key pin). The link is checked to point at this
// machine's address and key before it is returned. deviceName is only a
// hint stored with the code; the device names itself when it redeems.
func CreatePairing(ctx context.Context, m Machine, token, deviceName string) (*PairCode, error) {
	c, err := Client(m, token)
	if err != nil {
		return nil, err
	}
	if t, ok := c.Transport.(bearer); ok {
		if tr, ok := t.rt.(*http.Transport); ok {
			defer tr.CloseIdleConnections()
		}
	}
	body, _ := json.Marshal(map[string]string{"base_url": m.origin(), "device_name": deviceName})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.origin()+"/api/auth/pairing", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the other Mac: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrSignedOut
	}
	var out struct {
		PairURL   string    `json:"pair_url"`
		ExpiresAt time.Time `json:"expires_at"`
		Error     string    `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return nil, fmt.Errorf("unexpected answer from the other Mac (%d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || out.PairURL == "" {
		if out.Error == "" {
			out.Error = resp.Status
		}
		return nil, errors.New(out.Error)
	}
	got, _, err := ParsePairLink(out.PairURL)
	if err != nil {
		// No pin: the other Mac's relay is not connected (or it's on ngrok).
		return nil, errors.New("the other Mac's remote access is not connected in Eggify relay mode")
	}
	if !strings.EqualFold(got.BaseURL, m.origin()) || got.Pin != m.Pin {
		return nil, errors.New("the other Mac answered with a pair link for a different address or key")
	}
	return &PairCode{PairURL: out.PairURL, ExpiresAt: out.ExpiresAt}, nil
}
