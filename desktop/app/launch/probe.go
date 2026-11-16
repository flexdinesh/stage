package launch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// probeTimeout bounds a server test.
const probeTimeout = 8 * time.Second

// ServerTest is the result of testing a server URL before saving it
// (Settings, Add server): whether a Stage server answered, what it is,
// and what to do when it is not usable.
type ServerTest struct {
	// Reachable: something answered at the URL (even a refusal).
	Reachable bool `json:"reachable"`
	// APIVersion, Identity and Version are its /api/health; zero values
	// when it did not answer as a Stage server.
	APIVersion int    `json:"apiVersion"`
	Identity   string `json:"identity"`
	Version    string `json:"version"`
	// Error is why the server is not usable, "" when it is.
	Error string `json:"error"`
	// Hint says what to do about Error, "".
	Hint string `json:"hint"`
	// Command is a setting or command that fixes it, to copy (for a
	// refused hostname: STAGE_ALLOWED_ORIGINS=<url>), or "".
	Command string `json:"command"`
}

// TestServer tests rawURL as a remote server for this app (the bundled
// CLI's API version, once read).
func (l *Launcher) TestServer(ctx context.Context, rawURL string) ServerTest {
	want := 0
	l.mu.Lock()
	if l.info != nil {
		want = l.info.APIVersion
	}
	l.mu.Unlock()
	return Probe(ctx, l.cfg.Get, rawURL, want)
}

// Probe GETs /api/health at rawURL as the bridge would (its Host, no
// Origin, no proxy) and explains the outcome; wantAPI 0 skips the API
// check.
func Probe(ctx context.Context, get Getter, rawURL string, wantAPI int) ServerTest {
	u, err := target.NormalizeURL(rawURL)
	if err != nil {
		return ServerTest{Error: err.Error(), Hint: "Use the address Stage listens on, e.g. http://192.168.1.20:7380."}
	}
	t := target.Target{ID: "test", Kind: target.KindRemote, URL: u}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	var h Health
	err = get(ctx, t, "/api/health", &h)
	var he *HTTPError
	switch {
	case errors.As(err, &he) && he.Status == 403:
		msg := he.Message
		if msg == "" {
			msg = err.Error()
		}
		return ServerTest{Reachable: true, Error: msg,
			Hint:    "That server does not accept this hostname. Add it to STAGE_ALLOWED_ORIGINS on that server (its service.env or environment), restart it, then test again.",
			Command: "STAGE_ALLOWED_ORIGINS=" + u}
	case errors.As(err, &he):
		return ServerTest{Reachable: true, Error: err.Error(), Hint: "Something answered, but not as a Stage server. Check the address and port."}
	case err != nil:
		return ServerTest{Error: err.Error(),
			Hint: "Check the address, and that Stage on that machine listens on the network (stage serve --listen 0.0.0.0:7380, or STAGE_LISTEN in its service.env) and no firewall blocks the port."}
	case h.Service != "stage":
		return ServerTest{Reachable: true, Error: fmt.Sprintf("%s is not a Stage server", u), Hint: "Check the address and port."}
	}
	r := ServerTest{Reachable: true, APIVersion: h.APIVersion, Identity: h.Identity, Version: h.Version}
	if wantAPI != 0 && h.APIVersion != wantAPI {
		r.Error = fmt.Sprintf("it speaks API v%d; this app needs v%d", h.APIVersion, wantAPI)
		r.Hint = "Update Stage on that machine (or this app) so both match."
	}
	return r
}
