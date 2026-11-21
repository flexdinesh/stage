package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/client"
)

// runMachine is `stage machine ls|rename|rm|pair`: the machines that run
// harnesses for the server (docs/api.md "Machines").
func runMachine(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "ls")
	c := newCmd(env, "machine "+v)
	var label string
	switch v {
	case "ls", "list", "rename", "rm", "ping":
	case "pair":
		c.fs.StringVar(&label, "label", "", "name for the machine it adds (default: the worker's host name)")
	default:
		return usagef("machine %s: unknown (want ls, rename, rm, ping or pair)", v)
	}
	if err := c.parse(rest); err != nil {
		return err
	}
	cl := client.New(client.Resolve(c.url, env.Getenv).URL)
	switch v {
	case "ls", "list":
		if len(c.pos) > 0 {
			return usagef("machine ls: unexpected argument %q", c.pos[0])
		}
		ms, err := cl.Machines(ctx)
		if err != nil {
			return err
		}
		return c.out(ms, func(w io.Writer) { printMachines(w, ms) })
	case "rename":
		if len(c.pos) < 2 {
			return usagef("machine rename: want ID LABEL")
		}
		id, err := parseID(c.pos[0])
		if err != nil {
			return err
		}
		m, err := cl.RenameMachine(ctx, id, strings.Join(c.pos[1:], " "))
		if err != nil {
			return err
		}
		return c.out(m, func(w io.Writer) { fmt.Fprintf(w, "machine %d renamed to %s\n", m.ID, m.Label) })
	case "rm":
		id, err := c.oneID()
		if err != nil {
			return err
		}
		if err := cl.RemoveMachine(ctx, id); err != nil {
			return err
		}
		return c.out(map[string]any{"id": id, "removed": true}, func(w io.Writer) {
			fmt.Fprintf(w, "machine %d removed: its token is revoked and its connections retire as history\n", id)
		})
	case "ping":
		id, err := c.oneID()
		if err != nil {
			return err
		}
		p, err := cl.PingMachine(ctx, id)
		if err != nil {
			return err
		}
		return c.out(p, func(w io.Writer) {
			fmt.Fprintf(w, "machine %d answered in %d ms (stage %s)\n", p.MachineID, p.LatencyMs, p.Version)
		})
	default: // pair
		if len(c.pos) > 0 {
			return usagef("machine pair: unexpected argument %q (name it with --label)", c.pos[0])
		}
		p, err := cl.PairMachine(ctx, label)
		if err != nil {
			return err
		}
		return c.out(p, func(w io.Writer) { printPairing(w, p) })
	}
}

func printMachines(w io.Writer, ms []client.Machine) {
	table(w, "ID\tLABEL\tKIND\tSTATUS\tVERSION\tLAST SEEN\tCONNECTIONS", func(tw io.Writer) {
		for _, m := range ms {
			status := strings.ReplaceAll(m.Status, "_", " ")
			if status == "" {
				status = "offline"
				if m.Online {
					status = "online"
				}
			}
			conns := make([]string, len(m.Connections))
			for i, c := range m.Connections {
				conns[i] = fmt.Sprintf("%s #%d", c.Harness, c.ID)
			}
			list := strings.Join(conns, ", ")
			if list == "" {
				list = "-"
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", m.ID, m.Label, m.Kind, status, orDash(m.Version), ts(m.LastSeenAt), list)
		}
	})
}

func printPairing(w io.Writer, p client.Pairing) {
	fmt.Fprintf(w, "On the machine to add, run (expires %s, works once):\n\n  %s\n\n", p.ExpiresAt.Local().Format(time.Kitchen), p.Command)
	fmt.Fprintf(w, "link  %s\n", p.Link)
	if p.Label != "" {
		fmt.Fprintf(w, "name  %s\n", p.Label)
	}
}
