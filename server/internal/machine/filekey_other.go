//go:build !unix

package machine

import (
	"os"
	"path/filepath"
)

// fileKey identifies path's file by its canonical path where device and
// inode are unavailable (not a release target).
func fileKey(path string) (string, bool) {
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	return p, true
}
