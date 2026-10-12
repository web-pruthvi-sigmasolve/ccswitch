package store

// desktopProcessNames are the process names that indicate Claude Desktop is
// running (Linux/macOS names; Windows uses Claude.exe).
var desktopProcessNames = []string{"claude", "Claude", "claude-desktop"}

// isDesktopProcessName reports whether a process name is one of the known
// Claude Desktop names.
func isDesktopProcessName(name string) bool {
	for _, n := range desktopProcessNames {
		if name == n {
			return true
		}
	}
	return false
}
