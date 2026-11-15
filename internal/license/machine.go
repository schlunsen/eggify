package license

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// DefaultMachineName is the name shown in the seat list: the Mac's computer
// name ("Dev's MacBook Pro"), else the host name.
func DefaultMachineName() string {
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--get", "ComputerName").Output(); err == nil {
			if name := strings.TrimSpace(string(out)); name != "" {
				return truncate(name, 100)
			}
		}
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return truncate(strings.TrimSuffix(h, ".local"), 100)
	}
	return "This computer"
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
