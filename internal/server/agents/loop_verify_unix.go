//go:build !windows

package agents

import (
	"os/exec"
	"syscall"
)

// configureVerifyProcess runs the verify command in its own process group so
// a timeout kills everything it started (test runners, dev servers…), not
// just the bash wrapper.
func configureVerifyProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
