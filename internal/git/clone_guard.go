package git

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Clone SSRF guard, shared by POST /api/git/clone and the new-project flow
// (/api/projects/setup, handlers/project_setup.go).
//
// A clone makes the Eggify server fetch a URL of the caller's choosing, so:
//   - only https (no file://, ssh://, ext::, local paths), no credentials in
//     the URL;
//   - the host must not be localhost or a loopback/private/link-local/
//     unspecified address, written as a literal in ANY form git's resolver
//     accepts (127.1, 2130706433, 0x7f000001, 0177.0.0.1, [::ffff:127.0.0.1])
//     or reached through DNS (a name resolving to 127.0.0.1);
//   - git is pinned to the address that was checked (http.curloptResolve),
//     never follows redirects, may use https only, and never prompts.

// ErrCloneHostBlocked is returned for clone targets on this machine or the
// local network.
var ErrCloneHostBlocked = errors.New("cloning from localhost, private or loopback addresses is not allowed")

// LookupIP resolves a clone host. Tests replace it with a stub resolver.
var LookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.IP)
	}
	return out, nil
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
var thisNetwork = &net.IPNet{IP: net.IPv4(0, 0, 0, 0), Mask: net.CIDRMask(8, 32)}

// BlockedCloneIP reports whether ip is on this machine or a local network:
// loopback, private (RFC 1918, fc00::/7), link-local, unspecified, 0.0.0.0/8,
// carrier-grade NAT (100.64.0.0/10) or multicast.
func BlockedCloneIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
		if thisNetwork.Contains(ip) || cgnat.Contains(ip) {
			return true
		}
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

// parseLooseIPv4 parses the IPv4 forms inet_aton accepts (and so curl/git):
// 1 to 4 dot-separated parts, each decimal, octal (leading 0) or hex (0x).
func parseLooseIPv4(h string) (net.IP, bool) {
	if h == "" || strings.ContainsAny(h, ":") {
		return nil, false
	}
	parts := strings.Split(strings.TrimSuffix(h, "."), ".")
	if len(parts) > 4 {
		return nil, false
	}
	vals := make([]uint64, len(parts))
	for i, p := range parts {
		if p == "" {
			return nil, false
		}
		base := 10
		switch {
		case strings.HasPrefix(p, "0x") || strings.HasPrefix(p, "0X"):
			base, p = 16, p[2:]
			if p == "" {
				p = "0"
			}
		case len(p) > 1 && p[0] == '0':
			base, p = 8, p[1:]
		}
		v, err := strconv.ParseUint(p, base, 32)
		if err != nil {
			return nil, false
		}
		vals[i] = v
	}
	var n uint64
	switch len(vals) {
	case 1:
		n = vals[0]
	case 2:
		if vals[0] > 0xff || vals[1] > 0xffffff {
			return nil, false
		}
		n = vals[0]<<24 | vals[1]
	case 3:
		if vals[0] > 0xff || vals[1] > 0xff || vals[2] > 0xffff {
			return nil, false
		}
		n = vals[0]<<24 | vals[1]<<16 | vals[2]
	case 4:
		for _, v := range vals {
			if v > 0xff {
				return nil, false
			}
		}
		n = vals[0]<<24 | vals[1]<<16 | vals[2]<<8 | vals[3]
	}
	if n > 0xffffffff {
		return nil, false
	}
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n)).To4(), true
}

// CheckCloneHostLiteral refuses localhost names and blocked address literals
// in any notation, without DNS. It returns the literal IP when host is one.
func CheckCloneHostLiteral(host string) (net.IP, error) {
	h := strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if h == "" {
		return nil, errors.New("the URL has no host")
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return nil, ErrCloneHostBlocked
	}
	if i := strings.IndexByte(h, '%'); i >= 0 { // IPv6 zone
		h = h[:i]
	}
	ip := net.ParseIP(h)
	if ip == nil {
		if v4, ok := parseLooseIPv4(h); ok {
			ip = v4
		}
	}
	if ip != nil {
		if BlockedCloneIP(ip) {
			return nil, ErrCloneHostBlocked
		}
		return ip, nil
	}
	return nil, nil
}

// ResolveCloneHost checks host (literal forms, then every DNS answer) and
// returns the address git must connect to.
func ResolveCloneHost(ctx context.Context, host string) (net.IP, error) {
	ip, err := CheckCloneHostLiteral(host)
	if err != nil {
		return nil, err
	}
	if ip != nil {
		return ip, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ips, err := LookupIP(ctx, strings.TrimSuffix(strings.ToLower(host), "."))
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("could not resolve %s", host)
	}
	for _, a := range ips {
		if BlockedCloneIP(a) {
			return nil, ErrCloneHostBlocked
		}
	}
	return ips[0], nil
}

// CloneEnv is the environment for a git clone: https only, no prompts.
func CloneEnv() []string {
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=https", "GCM_INTERACTIVE=never")
}

// PrepareClone validates an https clone URL (no credentials, host checked
// including DNS) and returns the git config arguments ("-c ..." pairs, to go
// before "clone") that keep git on https, off redirects and on the checked
// address.
func PrepareClone(ctx context.Context, cloneURL string) ([]string, error) {
	u, err := url.Parse(strings.TrimSpace(cloneURL))
	if err != nil {
		return nil, errors.New("invalid clone URL")
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return nil, errors.New("only HTTPS clone URLs are allowed")
	}
	if u.User != nil {
		return nil, errors.New("remove credentials from the clone URL")
	}
	ip, err := ResolveCloneHost(ctx, u.Hostname())
	if err != nil {
		return nil, err
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	args := []string{
		"-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always",
		"-c", "http.followRedirects=false",
	}
	if net.ParseIP(strings.Trim(u.Hostname(), "[]")) == nil {
		addr := ip.String()
		if ip.To4() == nil {
			addr = "[" + addr + "]"
		}
		// Pin the connection to the address checked above (DNS rebinding).
		args = append(args, "-c", "http.curloptResolve="+u.Hostname()+":"+port+":"+addr)
	}
	return args, nil
}
