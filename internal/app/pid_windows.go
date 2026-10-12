//go:build windows

package app

import "syscall"

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

// processAlive reports whether a process with the pid exists and has not exited.
func processAlive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
