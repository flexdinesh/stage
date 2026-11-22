package serve

import (
	"errors"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
)

func TestDefaultLocalLabel(t *testing.T) {
	container := harness.Profile{Kind: harness.ProfileContainer}
	host := harness.Profile{Kind: harness.ProfileServer}
	desktop := harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}
	long := strings.Repeat("h", db.MaxMachineLabel+20)
	name := func(h string, err error) func() (string, error) { return func() (string, error) { return h, err } }
	for _, tc := range []struct {
		desc      string
		profile   harness.Profile
		publicURL string
		hostname  func() (string, error)
		want      string
	}{
		{"container uses the public URL host", container, "https://stage.local.deebox.xyz", name("3f2a9c0d1b7e", nil), "stage.local.deebox.xyz"},
		{"container drops port and path", container, "http://10.0.0.5:7380/app", name("3f2a9c0d1b7e", nil), "10.0.0.5"},
		{"container never uses its hostname", container, "", name("3f2a9c0d1b7e", nil), fallbackLocalLabel},
		{"container with an unparseable URL", container, "://bad", name("x", nil), fallbackLocalLabel},
		{"foreground server uses the hostname", host, "https://stage.example", name("deebox", nil), "deebox"},
		{"desktop uses the hostname", desktop, "", name(" laptop \n", nil), "laptop"},
		{"empty hostname", host, "", name("", nil), fallbackLocalLabel},
		{"hostname error", host, "", name("ignored", errors.New("no name")), fallbackLocalLabel},
		{"capped length", host, "", name(long, nil), long[:db.MaxMachineLabel]},
	} {
		if got := defaultLocalLabel(tc.profile, tc.publicURL, tc.hostname); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.desc, got, tc.want)
		}
	}
}
