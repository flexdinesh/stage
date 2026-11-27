package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/client"
)

// statusOut is `stage status --json`.
type statusOut struct {
	URL              string         `json:"url"`
	Source           string         `json:"source"` // --url | STAGE_URL | default socket | default
	Ready            bool           `json:"ready"`
	Error            string         `json:"error,omitempty"`
	Health           *client.Health `json:"health"`
	ClientVersion    string         `json:"clientVersion"`
	ClientAPIVersion int            `json:"clientApiVersion"`
}

// runStatus is `stage status [--wait DUR]`: which server this CLI reaches
// and whether it is ready. Exit 0 when ready (even with offline connections), 1 when
// unreachable, unhealthy or on another API version, 3 when --wait expires.
func runStatus(ctx context.Context, env Env, args []string) error {
	c := newCmd(env, "status")
	var wait time.Duration
	c.fs.DurationVar(&wait, "wait", 0, "wait up to this long for the server to be ready")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.pos) > 0 {
		return usagef("status: unexpected argument %q", c.pos[0])
	}
	res := client.Resolve(c.url, env.Getenv)
	cl := client.New(res.URL)
	deadline := time.Now().Add(wait)
	out := statusOut{URL: res.URL, Source: res.Source, ClientVersion: buildinfo.Version, ClientAPIVersion: buildinfo.APIVersion}
	for {
		h, err := cl.Health(ctx)
		if err == nil && h.APIVersion != 0 && h.APIVersion != buildinfo.APIVersion {
			err = &client.VersionError{BaseURL: res.URL, Server: fmt.Sprint(h.APIVersion)}
		}
		var verr *client.VersionError
		if err == nil || errors.As(err, &verr) || time.Now().After(deadline) || ctx.Err() != nil {
			if h.Service != "" || h.Status != "" {
				out.Health = &h
			}
			out.Ready = err == nil
			if err != nil {
				out.Error = err.Error()
			}
			if perr := c.out(out, func(w io.Writer) { printStatus(w, out) }); perr != nil {
				return perr
			}
			switch {
			case err == nil:
				return nil
			case wait > 0 && verr == nil:
				fail(env.stderr(), "not ready after %s: %s", wait, err)
				return errTimeout
			}
			return err
		}
		select {
		case <-ctx.Done():
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func printStatus(w io.Writer, s statusOut) {
	h := s.Health
	if !s.Ready && h == nil {
		line(w, toneBad, "stage not reachable at %s %s", s.URL, dim("("+s.Source+")"))
		return
	}
	t, state := toneOK, "ready"
	if !s.Ready {
		t, state = toneWarn, "not ready"
	}
	line(w, t, "stage %s %s at %s %s", h.Version, t.paint(state), s.URL, dim("("+s.Source+")"))
	kv(w, "identity", "%s", h.Identity)
	if h.Commit != "" {
		kv(w, "commit", "%s", h.Commit)
	}
	kv(w, "api", "v%d", h.APIVersion)
	if h.InstanceID != "" && h.StartedAt != nil {
		kv(w, "instance", "%s %s", h.InstanceID, dim("since "+h.StartedAt.Local().Format(time.RFC3339)))
	}
	if h.Engine != "" {
		kv(w, "database", "%s %s", h.DB, dim("("+h.Engine+")"))
	} else {
		kv(w, "database", "%s", h.DB)
	}
	for _, hh := range h.Harnesses {
		printHarnessState(w, hh.Kind, hh.Connected, hh.Configured, hh.LastError)
	}
	if h.Version != s.ClientVersion {
		warn(w, "Server version %s differs from this CLI (%s)", h.Version, s.ClientVersion)
	}
}
