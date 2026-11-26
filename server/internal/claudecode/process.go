package claudecode

import (
	"errors"
	"syscall"
)

// processAlive reports whether process pid exists (signal 0; a process of
// another user still counts).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
