package client

import (
	"context"
	"strings"
	"testing"
)

// STAGE_URL takes the same unix syntax as STAGE_LISTEN: a relative socket
// path is an error, never dialed.
func TestNewUnixURL(t *testing.T) {
	for _, u := range []string{"unix://run/stage.sock", "unix:rel.sock", "unix:"} {
		_, err := New(u).Projects(context.Background())
		if err == nil || !strings.Contains(err.Error(), "absolute") {
			t.Errorf("%s: err %v, want absolute-path error", u, err)
		}
	}
	for _, u := range []string{"unix:///run/stage.sock", "unix:/run/stage.sock"} {
		if c := New(u); c.err != nil || c.base != socketBase {
			t.Errorf("%s: %+v", u, c)
		}
	}
}
