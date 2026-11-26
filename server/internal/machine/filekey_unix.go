//go:build unix

package machine

import (
	"os"
	"strconv"
	"syscall"
)

// inodeHint identifies the directory on this machine by device and inode.
func inodeHint(info os.FileInfo) string {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return strconv.FormatUint(uint64(st.Dev), 10) + ":" + strconv.FormatUint(uint64(st.Ino), 10)
}
