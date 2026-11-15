//go:build windows

package agents

import "os/exec"

// configureVerifyProcess: on Windows the default CommandContext kill is used.
func configureVerifyProcess(cmd *exec.Cmd) {}
