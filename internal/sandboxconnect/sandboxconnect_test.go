package sandboxconnect

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"io"
	"net"
	"strings"
	"testing"
)

// fakeProxy replaces the dial with an in-memory proxy (the test sandbox may not
// bind ports): it records the CONNECT head, answers with status and, on 200,
// reads want bytes, answers them upper-cased and closes.
func fakeProxy(t *testing.T, status string, want int) <-chan string {
	t.Helper()
	ch := make(chan string, 1)
	old := dialProxy
	t.Cleanup(func() { dialProxy = old })
	dialProxy = func(addr string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			br := bufio.NewReader(server)
			var sb strings.Builder
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					return
				}
				sb.WriteString(line)
				if line == "\r\n" {
					break
				}
			}
			ch <- sb.String()
			io.WriteString(server, "HTTP/1.1 "+status+"\r\n\r\n")
			if !strings.HasPrefix(status, "200") {
				return
			}
			buf := make([]byte, want)
			if _, err := io.ReadFull(br, buf); err != nil {
				return
			}
			server.Write(bytes.ToUpper(buf))
		}()
		return client, nil
	}
	return ch
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestRunTunnelsBothWaysWithProxyAuth(t *testing.T) {
	head := fakeProxy(t, "200 Connection established", 9)
	// A password with characters that need percent-encoding.
	proxy := "http://user:p%40ss%3Aw@127.0.0.1:3128"
	var out, errOut bytes.Buffer
	code := Run([]string{"github.com", "22"}, env(map[string]string{"HTTPS_PROXY": proxy}), strings.NewReader("hello ssh"), &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if out.String() != "HELLO SSH" {
		t.Fatalf("relayed %q", out.String())
	}
	h := <-head
	if !strings.HasPrefix(h, "CONNECT github.com:22 HTTP/1.1\r\n") {
		t.Fatalf("head %q", h)
	}
	want := "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("user:p@ss:w"))
	if !strings.Contains(h, want) {
		t.Fatalf("auth not sent decoded: %q", h)
	}
}

func TestRunReadsLowercaseAndFallbackNames(t *testing.T) {
	head := fakeProxy(t, "200 OK", 1)
	var out, errOut bytes.Buffer
	code := Run([]string{"203.0.113.5", "2222"}, env(map[string]string{"http_proxy": "http://127.0.0.1:3128"}), strings.NewReader("x"), &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if h := <-head; strings.Contains(h, "Proxy-Authorization") {
		t.Fatalf("no credentials, no header: %q", h)
	}
}

func TestRunRefusedByProxyExitsNonZero(t *testing.T) {
	fakeProxy(t, "403 Forbidden", 0)
	var out, errOut bytes.Buffer
	code := Run([]string{"gitlab.com", "22"}, env(map[string]string{"HTTPS_PROXY": "http://127.0.0.1:3128"}), strings.NewReader(""), &out, &errOut)
	if code == 0 || !strings.Contains(errOut.String(), "403") {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout must stay clean: %q", out.String())
	}
}

func TestRunRejectsBadArguments(t *testing.T) {
	cases := [][]string{
		nil, {"github.com"}, {"a", "22", "extra"},
		{"", "22"}, {"bad host", "22"}, {"host\r\nX: y", "22"}, {"host/path", "22"},
		{"github.com", "0"}, {"github.com", "65536"}, {"github.com", "-1"}, {"github.com", "22a"}, {"github.com", "022"},
	}
	for _, args := range cases {
		var out, errOut bytes.Buffer
		if code := Run(args, env(map[string]string{"HTTPS_PROXY": "http://127.0.0.1:1"}), strings.NewReader(""), &out, &errOut); code == 0 {
			t.Errorf("%q accepted", args)
		}
	}
}

func TestRunWithoutProxyFails(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"github.com", "22"}, env(nil), strings.NewReader(""), &out, &errOut); code == 0 {
		t.Fatal("no proxy must fail")
	}
	for _, bad := range []string{"socks5://127.0.0.1:1", "http://127.0.0.1", "::"} {
		if code := Run([]string{"github.com", "22"}, env(map[string]string{"HTTPS_PROXY": bad}), strings.NewReader(""), &out, &errOut); code == 0 {
			t.Errorf("proxy %q accepted", bad)
		}
	}
}

func TestValidHost(t *testing.T) {
	for _, h := range []string{"github.com", "10.0.0.1", "::1", "my_host-1.example.com"} {
		if !ValidHost(h) {
			t.Errorf("%q refused", h)
		}
	}
	for _, h := range []string{"", "a b", "a;b", "a\nb", "a/b", "a@b"} {
		if ValidHost(h) {
			t.Errorf("%q accepted", h)
		}
	}
}
