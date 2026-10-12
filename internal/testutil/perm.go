package testutil

import (
	"io/fs"
	"os"
	"runtime"
	"testing"
)

// WantPerm fails t unless path has exactly the permission bits want. It does
// nothing on Windows, which has no Unix permission bits: os.Stat reports
// -rw-rw-rw- for files and 0777 for directories there.
func WantPerm(t testing.TB, path string, want fs.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Errorf("stat %s: %v", path, err)
		return
	}
	if got := st.Mode().Perm(); got != want {
		t.Errorf("%s mode = %v, want %v", path, got, want)
	}
}
