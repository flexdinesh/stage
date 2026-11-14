package api

import "time"

// SetStreamPing overrides the event stream ping interval; the returned func restores it.
func SetStreamPing(d time.Duration) func() {
	old := streamPing
	streamPing = d
	return func() { streamPing = old }
}
