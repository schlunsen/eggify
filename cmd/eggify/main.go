// Package main is the entry point for the Eggify application.
// Eggify is a high-performance Go control center for Claude Code environments,
// providing component templates, analytics dashboards, and real-time monitoring
// for Claude Code projects.
package main

import (
	"fmt"
	"os"

	"github.com/schlunsen/eggify/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
