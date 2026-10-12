package cli

import "os"

// IsTerminal reports whether f is attached to a terminal (a character device).
// It is shared with the TUI entry point.
func IsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
