//go:build !unix

package session

import "os"

func ownedByUser(os.FileInfo) bool { return false }
