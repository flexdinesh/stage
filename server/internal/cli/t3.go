package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/client"
)

// runT3 is `stage t3 status|pair|url`: the server's T3 connection.
func runT3(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "status")
	c := newCmd(env, "t3 "+v)
	var code string
	var reset bool
	switch v {
	case "status":
	case "pair":
		c.fs.StringVar(&code, "code", "", "pairing code from `t3 pair` (default: auto-pair with the t3 CLI on the server's machine)")
	case "url":
		c.fs.BoolVar(&reset, "reset", false, "go back to the default T3 URL")
	default:
		return usagef("t3 %s: unknown (want status, pair or url)", v)
	}
	if err := c.parse(rest); err != nil {
		return err
	}
	cl := client.New(client.Resolve(c.url, env.Getenv).URL)
	var st client.T3Status
	var err error
	switch v {
	case "status":
		if len(c.pos) > 0 {
			return usagef("t3 status: unexpected argument %q", c.pos[0])
		}
		st, err = cl.T3Status(ctx)
	case "pair":
		if len(c.pos) > 0 {
			return usagef("t3 pair: unexpected argument %q (use --code CODE)", c.pos[0])
		}
		if c.set("code") {
			if strings.TrimSpace(code) == "" {
				return usagef("t3 pair: --code is empty")
			}
			st, err = cl.PairT3(ctx, code)
		} else {
			st, err = cl.AutoPairT3(ctx)
		}
	case "url":
		switch {
		case len(c.pos) > 1 || (reset && len(c.pos) == 1):
			return usagef("t3 url: want one URL or --reset")
		case reset:
			st, err = cl.SetT3URL(ctx, nil)
		case len(c.pos) == 1:
			u := c.pos[0]
			st, err = cl.SetT3URL(ctx, &u)
		default:
			st, err = cl.T3Status(ctx)
		}
	}
	if err != nil {
		return err
	}
	return c.out(st, func(w io.Writer) { printT3Status(w, st) })
}

func printT3Status(w io.Writer, s client.T3Status) {
	fmt.Fprintf(w, "t3 %s at %s (%s)\n", s.State, s.T3URL, urlSource(s.T3URLSource))
	if s.PublicURL != s.T3URL {
		fmt.Fprintf(w, "  public    %s\n", s.PublicURL)
	}
	if s.T3 != nil {
		label := s.T3.Label
		if label == "" {
			label = s.T3.EnvironmentID
		}
		fmt.Fprintf(w, "  server    %s v%s (environment %s)\n", label, s.T3.ServerVersion, s.T3.EnvironmentID)
	} else if s.ProbeError != nil {
		fmt.Fprintf(w, "  server    not answering: %s\n", *s.ProbeError)
	}
	if s.TokenExpiresAt != nil {
		fmt.Fprintf(w, "  token     expires %s\n", s.TokenExpiresAt.Local().Format(time.RFC3339))
	}
	switch {
	case s.Sync.Running:
		fmt.Fprintf(w, "  sync      running: %d projects, %d threads so far\n", s.Sync.Projects, s.Sync.Threads)
	case s.LastFullSyncAt != nil:
		fmt.Fprintf(w, "  sync      last full sync %s\n", s.LastFullSyncAt.Local().Format(time.RFC3339))
	default:
		fmt.Fprintln(w, "  sync      never")
	}
	if s.AutoPair.Available {
		fmt.Fprintln(w, "  auto-pair available")
	} else if s.AutoPair.Reason != nil {
		fmt.Fprintf(w, "  auto-pair unavailable (%s)\n", autoPairReason(*s.AutoPair.Reason))
	}
	if s.LastError != nil && s.State != "connected" {
		fmt.Fprintf(w, "  error     %s\n", *s.LastError)
	}
	switch s.State {
	case "unreachable":
		fmt.Fprintf(w, "next: start T3 (`t3 serve`) on that machine, or point stage at it: stage t3 url URL\n")
	case "unpaired", "expired", "rejected":
		if s.AutoPair.Available {
			fmt.Fprintln(w, "next: stage t3 pair")
		} else {
			fmt.Fprintln(w, "next: run `t3 pair` on the T3 host, then: stage t3 pair --code CODE")
		}
	}
}

func urlSource(src string) string {
	switch src {
	case "env":
		return "set by T3_URL"
	case "settings":
		return "saved setting"
	}
	return "default"
}

func autoPairReason(r string) string {
	switch r {
	case "not_local":
		return "T3 is not on the server's machine"
	case "cli_not_found":
		return "t3 CLI not found on the server's machine"
	case "fake":
		return "fake T3"
	}
	return r
}
