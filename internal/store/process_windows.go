//go:build windows

package store

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// ClaudeDesktopRunning reports whether Claude.exe appears in the task list. It
// never terminates anything.
func ClaudeDesktopRunning() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tasklist", "/FI", "IMAGENAME eq Claude.exe", "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "claude.exe")
}
