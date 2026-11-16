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
// and whether it is ready. Exit 0 when ready (T3 offline included), 1 when
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
				fmt.Fprintf(env.Stderr, "error: not ready after %s: %s\n", wait, err)
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
		fmt.Fprintf(w, "stage not reachable at %s (%s)\n", s.URL, s.Source)
		return
	}
	state := "ready"
	if !s.Ready {
		state = "not ready"
	}
	fmt.Fprintf(w, "stage %s %s at %s (%s)\n", h.Version, state, s.URL, s.Source)
	fmt.Fprintf(w, "  identity  %s\n", h.Identity)
	if h.Commit != "" {
		fmt.Fprintf(w, "  commit    %s\n", h.Commit)
	}
	fmt.Fprintf(w, "  api       v%d\n", h.APIVersion)
	if h.InstanceID != "" && h.StartedAt != nil {
		fmt.Fprintf(w, "  instance  %s since %s\n", h.InstanceID, h.StartedAt.Local().Format(time.RFC3339))
	}
	fmt.Fprintf(w, "  database  %s\n", h.DB)
	switch {
	case h.T3.Connected:
		fmt.Fprintln(w, "  t3        connected")
	case h.T3.LastError != nil:
		fmt.Fprintf(w, "  t3        offline: %s\n", *h.T3.LastError)
	default:
		fmt.Fprintln(w, "  t3        offline")
	}
	if h.Version != s.ClientVersion {
		fmt.Fprintf(w, "note: server version %s differs from this CLI (%s)\n", h.Version, s.ClientVersion)
	}
}
