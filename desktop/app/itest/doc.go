// Package itest is the desktop app's headless integration test (no
// webview): it builds the real stage CLI from ../../server with a test
// identity, runs `stage serve` on a temp Unix socket with the fake T3, and
// drives the desktop's bridge B handler and launcher against it. Normal
// `go test ./...` runs it (mise run check:desktop); it skips without a Go
// toolchain.
package itest
