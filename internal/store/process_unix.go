//go:build !windows

package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ClaudeDesktopRunning reports whether a Claude Desktop process appears to be
// running. It only inspects processes and never signals or kills anything.
// Note that the Claude Code CLI binary is also named "claude" and matches.
func ClaudeDesktopRunning() bool {
	if matches, err := filepath.Glob("/proc/[0-9]*/comm"); err == nil && len(matches) > 0 {
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil {
				continue
			}
			if isDesktopProcessName(strings.TrimSpace(string(b))) {
				return true
			}
		}
		return false
	}
	for _, name := range desktopProcessNames {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := exec.CommandContext(ctx, "pgrep", "-x", name).Run()
		cancel()
		if err == nil {
			return true
		}
	}
	return false
}
