package relay

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveRelay connects to the deployed relay (EGGIFY_LIVE_RELAY=1;
// EGGIFY_RELAY_ADDR names the relay, e.g. relay.example.com:8443), serves one request
// through it and fetches it with a pinned client over real DNS.
func TestLiveRelay(t *testing.T) {
	if os.Getenv("EGGIFY_LIVE_RELAY") != "1" {
		t.Skip("set EGGIFY_LIVE_RELAY=1 to test the deployed relay")
	}
	addr := os.Getenv("EGGIFY_RELAY_ADDR")
	if addr == "" {
		t.Skip("set EGGIFY_RELAY_ADDR to the relay's host:port")
	}
	id := newIdentity(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	l, err := Dial(ctx, ClientConfig{Addr: addr, Identity: id})
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer l.Close()
	t.Logf("install address: %s", l.URL())
	go serveEcho(l)

	host := strings.TrimPrefix(l.URL(), "https://")
	c := pinnedClient(host, id.Pin())
	resp, err := c.Get(l.URL() + "/live")
	if err != nil {
		t.Fatalf("get through the relay: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(string(body), "hello /live from ") {
		t.Fatalf("body = %q", body)
	}
	t.Logf("reply: %s", body)
}
