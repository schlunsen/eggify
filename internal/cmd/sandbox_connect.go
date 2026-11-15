package cmd

import (
	"os"

	"github.com/schlunsen/eggify/internal/sandboxconnect"
	"github.com/spf13/cobra"
)

// sandboxConnectCmd is the ssh ProxyCommand of sandboxed sessions that have the
// ssh grant (GIT_SSH_COMMAND names it). The desktop binary dispatches
// subcommands to this root as well (HasSubcommand), so both binaries have it.
var sandboxConnectCmd = &cobra.Command{
	Use:                "sandbox-connect <host> <port>",
	Short:              "Tunnel a TCP connection through the sandbox's HTTP proxy (ssh ProxyCommand)",
	Hidden:             true,
	SilenceUsage:       true,
	SilenceErrors:      true,
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		// Not RunE: the exit code and stderr text are ours, and stdout must
		// carry nothing but the tunnelled bytes.
		os.Exit(sandboxconnect.Run(args, os.Getenv, os.Stdin, os.Stdout, os.Stderr))
	},
}

func init() {
	rootCmd.AddCommand(sandboxConnectCmd)
}
