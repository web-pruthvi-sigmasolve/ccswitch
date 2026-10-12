package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ccswitch/ccswitch/internal/config"
)

// staleAfter is how old a lock file must be to be replaced regardless of pid.
const staleAfter = time.Hour

// Lock takes the exclusive ccswitch lock (a file in the config folder holding
// the pid). A lock whose process is gone, or that is older than an hour, is
// replaced. The returned release function removes the lock and is safe to call
// more than once.
func (a *App) Lock() (release func(), err error) {
	path := config.LockPath(a.ConfigDir)
	if err := os.MkdirAll(a.ConfigDir, 0o700); err != nil {
		return nil, &HintError{Msg: "Could not create the ccswitch config folder.", Hint: a.ConfigDir, Err: err}
	}
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, werr := fmt.Fprintf(f, "%d\n", os.Getpid())
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(path)
				return nil, &HintError{Msg: "Could not write the lock file.", Hint: path}
			}
			done := false
			return func() {
				if !done {
					done = true
					_ = os.Remove(path)
				}
			}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, &HintError{Msg: "Could not create the lock file.", Hint: path, Err: err}
		}
		if !lockIsStale(path) {
			break
		}
		if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			break
		}
	}
	return nil, &HintError{
		Msg:  "Another ccswitch run seems to be in progress.",
		Hint: "If that is not the case, delete " + path + " and try again.",
	}
}

func lockIsStale(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	if time.Since(info.ModTime()) > staleAfter {
		return true
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return true
	}
	return !processAlive(pid)
}
