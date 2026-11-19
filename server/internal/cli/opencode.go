package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/client"
)

// runOpenCode is `stage opencode status|connect|pair LINK|disconnect`: the
// server's OpenCode connection.
func runOpenCode(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "status")
	c := newCmd(env, "opencode "+v)
	switch v {
	case "status", "connect", "pair", "disconnect":
	default:
		return usagef("opencode %s: unknown (want status, connect, pair or disconnect)", v)
	}
	newInstance := false
	if v == "connect" || v == "pair" {
		c.fs.BoolVar(&newInstance, "new-instance", false, "connect a different (or earlier) OpenCode service as a new connection; the current one becomes read-only")
	}
	if err := c.parse(rest); err != nil {
		return err
	}
	want := 0
	if v == "pair" {
		want = 1
	}
	switch {
	case v == "pair" && len(c.pos) == 0:
		return usagef("opencode pair: want the link printed by `opencode pair`")
	case len(c.pos) > want:
		return usagef("opencode %s: unexpected argument %q", v, c.pos[want])
	}
	cl := client.New(client.Resolve(c.url, env.Getenv).URL)
	var err error
	switch v {
	case "connect":
		err = cl.ConnectLocalOpenCode(ctx, newInstance)
	case "pair":
		if strings.TrimSpace(c.pos[0]) == "" {
			return usagef("opencode pair: the link is empty")
		}
		err = cl.PairOpenCode(ctx, c.pos[0], newInstance)
	case "disconnect":
		err = cl.DisconnectOpenCode(ctx)
	}
	if err != nil {
		return err
	}
	st, err := cl.OpenCodeStatus(ctx)
	if err != nil {
		return err
	}
	return c.out(st, func(w io.Writer) { printOpenCodeStatus(w, st) })
}

func printOpenCodeStatus(w io.Writer, s client.OpenCodeStatus) {
	if s.State == "unconfigured" {
		fmt.Fprintln(w, "opencode not connected")
	} else {
		fmt.Fprintf(w, "opencode %s at %s (%s)\n", s.State, s.Endpoint, openCodeAuth(s.Auth))
	}
	if s.BrowserEndpoint != "" {
		fmt.Fprintf(w, "  browser   %s\n", s.BrowserEndpoint)
	}
	if s.WebClient != "" {
		fmt.Fprintf(w, "  web       %s\n", s.WebClient)
	}
	if len(s.EnvironmentFields) > 0 {
		fmt.Fprintf(w, "  managed   by the server environment: %s\n", strings.Join(s.EnvironmentFields, ", "))
	}
	if s.ExpiresAt != nil {
		fmt.Fprintf(w, "  token     expires %s (renewed automatically)\n", s.ExpiresAt.Local().Format(time.RFC3339))
	}
	switch l := s.Local; {
	case l.Available:
		fmt.Fprintf(w, "  local     OpenCode %s at %s\n", l.Version, l.URL)
	case l.Reason == "missing":
		fmt.Fprintln(w, "  local     no OpenCode service registered on the server's machine")
	case l.Reason == "invalid":
		fmt.Fprintln(w, "  local     the OpenCode service file on the server's machine is invalid")
	case l.Reason == "unreachable":
		fmt.Fprintf(w, "  local     OpenCode registered at %s is not answering\n", l.URL)
	}
	if s.Reason != "" && s.State != "connected" {
		fmt.Fprintf(w, "  error     %s\n", s.Reason)
	}
	pair := "next: run `opencode pair` on the OpenCode host, then: stage opencode pair LINK"
	switch {
	case s.State == "unconfigured" && s.Local.Available:
		fmt.Fprintln(w, "next: stage opencode connect (or pair a remote service: stage opencode pair LINK)")
	case s.State == "unconfigured", s.State == "rejected" && s.Auth == "paired":
		fmt.Fprintln(w, pair)
	case s.State == "rejected" && s.Auth == "local":
		fmt.Fprintln(w, "next: stage opencode connect")
	case s.State == "rejected":
		fmt.Fprintln(w, "next: update the OpenCode credentials in Settings, or: stage opencode pair LINK")
	}
}

func openCodeAuth(a string) string {
	switch a {
	case "local":
		return "the server machine's service"
	case "paired":
		return "paired"
	case "basic":
		return "basic credentials"
	}
	return "no credentials"
}
