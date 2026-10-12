// Package fsx holds the small filesystem helpers shared by backup and apply.
package fsx

import (
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"time"
)

// TmpSuffix is appended to the target name for the temporary file.
const TmpSuffix = ".ccswitch-tmp"

// WriteAtomic writes data to path through "<path>.ccswitch-tmp" in the same
// directory: write, fsync, close, rename. On Windows the rename is retried
// (5 attempts, 50 ms doubling to 800 ms) because the app may hold the file
// open. The temp file is removed on any failure.
func WriteAtomic(path string, data []byte, mode fs.FileMode) error {
	tmp := path + TmpSuffix
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close temp file: %w", err)
	}
	_ = os.Chmod(tmp, mode.Perm()) // the umask may have narrowed it
	if err := rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func rename(from, to string) error {
	attempts := 1
	if runtime.GOOS == "windows" {
		attempts = 5
	}
	delay := 50 * time.Millisecond
	var err error
	for i := 0; i < attempts; i++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		if i < attempts-1 {
			time.Sleep(delay)
			delay *= 2
		}
	}
	return err
}
