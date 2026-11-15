//go:build e2e

package main

// The automation plugin lets e2e/ drive the built app over the socket
// MYGO_AUTOMATION names. Release builds leave it out.
import _ "github.com/egoist/mygo/plugins/automation"
