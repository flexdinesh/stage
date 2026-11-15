// Package procnet reports the TCP sockets a process listens on, as
// evidence that the desktop bridge opens no TCP listener.
package procnet

import "errors"

// ErrUnsupported is returned on platforms without an implementation.
var ErrUnsupported = errors.New("procnet: unsupported platform")

// ListeningTCP returns the local addresses of the TCP sockets in LISTEN
// state that process pid owns; empty means none.
func ListeningTCP(pid int) ([]string, error) { return listeningTCP(pid) }
