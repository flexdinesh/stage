//go:build !unix

package machine

import "os"

func inodeHint(os.FileInfo) string { return "" }
