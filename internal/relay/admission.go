package relay

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
)

// Who may use a relay: any install, or, when the relay is started with an
// allowlist file, only the installs listed in it.
//
// Admission is not forever for allowlisted installs: they are re-checked
// against the file and their session is closed when they are taken off it
// (Server.watchAdmissions).

// Refusal codes (resultMsg.Code). Old apps ignore the code and read only the
// message and the permanent flag.
const (
	// CodeNotAllowed: the relay has an allowlist and this install is not on it.
	CodeNotAllowed = "not_allowed"
	// CodeBadVersion / CodeBadProof / CodeRelayFull: protocol-level refusals.
	CodeBadVersion = "bad_version"
	CodeBadProof   = "bad_proof"
	CodeRelayFull  = "relay_full"
)

// NotAllowedMessage is the permanent refusal an install not on the allowlist gets.
const NotAllowedMessage = "This relay is private: this install is not on its allowlist. Use ngrok in Settings → Remote access, or run your own relay."

var b64url = base64.RawURLEncoding

// Allowlist is a file of relay install ids, one per line (# comments). The
// file is read on every lookup and parsed again only when its content hash
// changed (modification time and size miss same-size edits within one
// timestamp tick).
type Allowlist struct {
	Path string

	mu   sync.Mutex
	hash [sha256.Size]byte
	ids  map[string]bool
}

// load returns the listed ids. An error (missing, unreadable) is never
// cached: the next call reads the file again.
func (a *Allowlist) load() (map[string]bool, error) {
	if a == nil || a.Path == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(a.Path)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.ids, a.hash = nil, [sha256.Size]byte{}
		return nil, err
	}
	sum := sha256.Sum256(data)
	if a.ids == nil || sum != a.hash {
		a.ids, a.hash = parseAllowlist(data), sum
	}
	return a.ids, nil
}

// Has reports whether id is listed. A missing or unreadable file lists nobody.
func (a *Allowlist) Has(id string) bool {
	ids, err := a.load()
	return err == nil && ids[strings.ToLower(id)]
}

func parseAllowlist(data []byte) map[string]bool {
	ids := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if line = strings.ToLower(strings.TrimSpace(line)); line != "" {
			ids[line] = true
		}
	}
	return ids
}

// admission is the outcome of the admission check.
type admission struct {
	ok      bool
	reason  string // for the log
	code    string
	message string // sent to the install
	// viaAllowlist: admitted by the allowlist, re-checked against the file.
	viaAllowlist bool
}

// admit decides whether the install may connect.
func (s *Server) admit(id string) admission {
	if s.cfg.Allowlist == nil {
		return admission{ok: true}
	}
	if s.cfg.Allowlist.Has(id) {
		return admission{ok: true, viaAllowlist: true}
	}
	return admission{reason: "not on the allowlist", code: CodeNotAllowed, message: NotAllowedMessage}
}

// watchAdmissions closes installs whose admission has ended:
// an allowlisted install that was taken off the file. They reconnect and are
// admitted (or refused) again.
func (s *Server) watchAdmissions(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.dropEnded()
		}
	}
}

func (s *Server) dropEnded() {
	var ended []*install
	var listed map[string]bool
	var listErr error
	loaded := false
	s.mu.Lock()
	for id, in := range s.installs {
		switch {
		case in.viaAllowlist:
			if !loaded {
				listed, listErr = s.cfg.Allowlist.load()
				loaded = true
			}
			// A read error other than "no file" is not a reason to cut anyone off.
			if (listErr == nil && !listed[id]) || errors.Is(listErr, os.ErrNotExist) {
				ended = append(ended, in)
			}
		}
	}
	s.mu.Unlock()
	for _, in := range ended {
		s.cfg.Logf("relay: install %s no longer admitted; closing", in.id)
		_ = in.session.Close()
	}
}
