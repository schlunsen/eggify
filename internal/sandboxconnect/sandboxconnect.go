// Package sandboxconnect implements `eggify sandbox-connect <host> <port>`, an
// ssh ProxyCommand for sandboxed sessions with the ssh grant. The sandbox lets a
// command reach the network only through its HTTP proxy (HTTPS_PROXY, with
// credentials in the URL), so this tunnels one TCP connection through it with
// CONNECT and relays stdin/stdout. The proxy still refuses hosts that are not on
// the session's egress allowlist.
package sandboxconnect

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// proxyEnvNames are read in this order, like the reference script.
var proxyEnvNames = []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"}

// ValidHost reports whether host is a host name or an IP literal: letters,
// digits, dots, hyphens, underscores, and colons/brackets for IPv6. Anything
// that could break the CONNECT line (spaces, CR/LF, slashes) is refused.
func ValidHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_', r == ':', r == '%':
		default:
			return false
		}
	}
	return true
}

// ValidPort parses a TCP port, 1-65535.
func ValidPort(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != s {
		return 0, false
	}
	return n, true
}

// proxyFromEnv returns the proxy's host:port and the Proxy-Authorization value
// ("" when the URL has no credentials).
func proxyFromEnv(getenv func(string) string) (addr, auth string, err error) {
	var raw string
	for _, n := range proxyEnvNames {
		if v := strings.TrimSpace(getenv(n)); v != "" {
			raw = v
			break
		}
	}
	if raw == "" {
		return "", "", errors.New("no proxy in the environment (HTTPS_PROXY)")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", "", errors.New("bad proxy url")
	}
	port := u.Port()
	if port == "" {
		return "", "", errors.New("bad proxy url: no port")
	}
	if u.User != nil {
		// url.Userinfo is already percent-decoded.
		pass, _ := u.User.Password()
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+pass))
	}
	return net.JoinHostPort(u.Hostname(), port), auth, nil
}

// dialProxy opens the connection to the proxy; tests replace it.
var dialProxy = func(addr string) (net.Conn, error) { return net.DialTimeout("tcp", addr, 15*time.Second) }

// Run tunnels host:port through the proxy named by the environment and relays
// in/out until either side closes. It returns the process exit code; errors go
// to errOut.
func Run(args []string, getenv func(string) string, in io.Reader, out, errOut io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(errOut, "usage: sandbox-connect <host> <port>")
		return 2
	}
	host := args[0]
	port, ok := ValidPort(args[1])
	if !ValidHost(host) {
		fmt.Fprintln(errOut, "sandbox-connect: invalid host")
		return 2
	}
	if !ok {
		fmt.Fprintln(errOut, "sandbox-connect: invalid port")
		return 2
	}
	addr, auth, err := proxyFromEnv(getenv)
	if err != nil {
		fmt.Fprintln(errOut, "sandbox-connect:", err)
		return 1
	}
	conn, err := dialProxy(addr)
	if err != nil {
		fmt.Fprintf(errOut, "sandbox-connect: proxy connect: %v\n", err)
		return 1
	}
	defer conn.Close()

	target := net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
	var req strings.Builder
	fmt.Fprintf(&req, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	if auth != "" {
		fmt.Fprintf(&req, "Proxy-Authorization: %s\r\n", auth)
	}
	req.WriteString("\r\n")
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := io.WriteString(conn, req.String()); err != nil {
		fmt.Fprintf(errOut, "sandbox-connect: proxy write: %v\n", err)
		return 1
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		fmt.Fprintf(errOut, "sandbox-connect: proxy closed: %v\n", err)
		return 1
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(errOut, "sandbox-connect: proxy refused: %s\n", resp.Status)
		return 1
	}
	_ = conn.SetDeadline(time.Time{})

	// socket -> stdout in the background; stdin -> socket here. Either side
	// ending ends the tunnel.
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(out, br)
		close(done)
	}()
	stdinDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(conn, in)
		// stdin closed: tell the server we are done writing, then let the
		// remaining reply drain.
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		close(stdinDone)
	}()
	select {
	case <-done:
	case <-stdinDone:
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	return 0
}
