package git

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// stubResolver answers every lookup from table (missing names fail).
func stubResolver(t *testing.T, table map[string][]string) {
	t.Helper()
	prev := LookupIP
	LookupIP = func(_ context.Context, host string) ([]net.IP, error) {
		addrs, ok := table[host]
		if !ok {
			return nil, errors.New("no such host")
		}
		var out []net.IP
		for _, a := range addrs {
			out = append(out, net.ParseIP(a))
		}
		return out, nil
	}
	t.Cleanup(func() { LookupIP = prev })
}

// Attack: make the server clone from itself or the LAN (SSRF) with address
// forms a naive net.ParseIP check misses, or a name that resolves locally.
func TestRedTeam_CloneSSRF(t *testing.T) {
	stubResolver(t, map[string][]string{
		"evil.example":   {"127.0.0.1"},
		"mixed.example":  {"93.184.216.34", "10.0.0.5"},
		"github.com":     {"140.82.121.4"},
		"rebind.example": {"169.254.169.254"},
	})
	for _, u := range []string{
		"https://127.1/x/y",
		"https://2130706433/x/y",
		"https://0x7f000001/x/y",
		"https://0177.0.0.1/x/y",
		"https://017700000001/x/y",
		"https://[::1]/x/y",
		"https://[::ffff:127.0.0.1]/x/y",
		"https://0/x/y",
		"https://localhost/x/y",
		"https://foo.localhost/x/y",
		"https://evil.example/x/y",
		"https://mixed.example/x/y",
		"https://rebind.example/x/y",
		"https://10.1.2.3/x/y",
		"https://100.64.0.1/x/y",
		"https://unresolvable.example/x/y",
		"http://github.com/x/y",
		"file:///etc/passwd",
		"https://user:pw@github.com/x/y",
	} {
		if args, err := PrepareClone(context.Background(), u); err == nil {
			t.Errorf("PrepareClone(%q) allowed: %v", u, args)
		}
	}

	args, err := PrepareClone(context.Background(), "https://github.com/owner/repo.git")
	if err != nil {
		t.Fatalf("github.com refused: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"http.followRedirects=false", "protocol.allow=never", "protocol.https.allow=always", "http.curloptResolve=github.com:443:140.82.121.4"} {
		if !strings.Contains(joined, want) {
			t.Errorf("clone args %q lack %q", joined, want)
		}
	}
	env := strings.Join(CloneEnv(), "\n")
	for _, want := range []string{"GIT_ALLOW_PROTOCOL=https", "GIT_TERMINAL_PROMPT=0"} {
		if !strings.Contains(env, want) {
			t.Errorf("clone env lacks %s", want)
		}
	}
}

func TestParseLooseIPv4(t *testing.T) {
	for in, want := range map[string]string{
		"127.1":        "127.0.0.1",
		"2130706433":   "127.0.0.1",
		"0x7f.1":       "127.0.0.1",
		"0177.0.0.01":  "127.0.0.1",
		"10.1.65535":   "10.1.255.255",
		"8.8.8.8":      "8.8.8.8",
		"1.2.3.4.5":    "",
		"256.1.1.1":    "",
		"github.com":   "",
		"4294967296":   "",
		"0x":           "0.0.0.0",
		"example.1com": "",
	} {
		ip, ok := parseLooseIPv4(in)
		got := ""
		if ok {
			got = ip.String()
		}
		if got != want {
			t.Errorf("parseLooseIPv4(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCloneRepositoryRefusesBlockedHostBeforeRunningGit(t *testing.T) {
	stubResolver(t, map[string][]string{"evil.example": {"127.0.0.1"}})
	dir := t.TempDir()
	s := NewService(dir)
	if _, err := s.CloneRepository("https://evil.example/o/r.git", "o", "r", nil); !errors.Is(err, ErrCloneHostBlocked) {
		t.Fatalf("CloneRepository = %v, want ErrCloneHostBlocked", err)
	}
}
