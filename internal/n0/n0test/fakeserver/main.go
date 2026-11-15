// Command fakeserver runs the in-process fake n0 (MCP + OAuth) on a local
// port, for developing and demoing Eggify's n0 connector without a real n0:
//
//	go run ./internal/n0/n0test/fakeserver
//
// Then pick "Custom URL…" in Eggify → Connectors → n0 and paste the printed
// URL. The fake's consent screen approves immediately (app "demo-app").
// POST <url>/__fake/revoke_all simulates disconnecting Eggify in n0.
package main

import (
	"fmt"
	"os"
	"os/signal"

	"github.com/schlunsen/eggify/internal/n0/n0test"
)

func main() {
	f := n0test.New()
	defer f.Close()
	fmt.Println(f.URL)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}
