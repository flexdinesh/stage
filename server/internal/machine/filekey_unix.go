//go:build unix

package machine

import (
	"os"
	"strconv"
	"syscall"
)

// fileKey identifies path's file on this machine by device and inode: what
// os.SameFile compares on Unix.
func fileKey(path string) (string, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false
	}
	return strconv.FormatUint(uint64(st.Dev), 10) + ":" + strconv.FormatUint(st.Ino, 10), true
}
