package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/flexdinesh/stage/server/internal/client"
)

// listFlag is a repeatable string flag.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// runHarness is `stage harness [list]` and `stage harness KIND
// status|set|run|disconnect`: the server's harnesses and their connection
// setup, described by each harness's setup schema (GET /api/harnesses).
func runHarness(ctx context.Context, env Env, args []string) error {
	kind, rest := verb(args, "list")
	if kind == "list" {
		c := newCmd(env, "harness list")
		if err := c.parse(rest); err != nil {
			return err
		}
		if len(c.pos) > 0 {
			return usagef("harness list: unexpected argument %q", c.pos[0])
		}
		hs, err := client.New(client.Resolve(c.url, env.Getenv).URL).Harnesses(ctx)
		if err != nil {
			return err
		}
		return c.out(hs, func(w io.Writer) { printHarnesses(w, hs) })
	}
	v, rest := verb(rest, "status")
	c := newCmd(env, "harness "+kind+" "+v)
	var reset listFlag
	switch v {
	case "status", "disconnect", "run":
	case "set":
		c.fs.Var(&reset, "reset", "reset FIELD to its default (repeatable)")
	default:
		return usagef("harness %s %s: unknown (want status, set, run or disconnect)", kind, v)
	}
	if err := c.parse(rest); err != nil {
		return err
	}
	cl := client.New(client.Resolve(c.url, env.Getenv).URL)
	hs, err := cl.Harnesses(ctx)
	if err != nil {
		return err
	}
	d, ok := hs.Lookup(kind)
	switch {
	case !ok:
		return usagef("harness %s: unknown harness (want one of %s)", kind, strings.Join(kinds(hs), ", "))
	case !d.Connectable:
		return usagef("harness %s: %s has no connection setup on this server", kind, d.Name)
	}
	var st client.HarnessStatus
	switch v {
	case "status", "disconnect":
		if len(c.pos) > 0 {
			return usagef("harness %s %s: unexpected argument %q", kind, v, c.pos[0])
		}
		if v == "disconnect" {
			st, err = cl.DisconnectHarness(ctx, kind)
		} else {
			st, err = cl.HarnessStatus(ctx, kind)
		}
	case "set":
		if len(c.pos) == 0 && len(reset) == 0 {
			return usagef("harness %s set: want FIELD=VALUE or --reset FIELD (fields: %s)", kind, fieldIDs(d.Setup.Fields))
		}
		settings, perr := assignments(d.Setup.Fields, c.pos)
		if perr != nil {
			return usagef("harness %s set: %v", kind, perr)
		}
		for _, f := range reset {
			if _, ok := field(d.Setup.Fields, f); !ok {
				return usagef("harness %s set: unknown field %q (want %s)", kind, f, fieldIDs(d.Setup.Fields))
			}
			settings[f] = nil
		}
		st, err = cl.ConfigureHarness(ctx, kind, settings)
	case "run":
		if len(c.pos) == 0 {
			return usagef("harness %s run: want an action (%s)", kind, actionIDs(d.Setup.Actions))
		}
		var a client.SetupAction
		for _, x := range d.Setup.Actions {
			if x.ID == c.pos[0] {
				a, ok = x, true
			}
		}
		if a.ID == "" {
			return usagef("harness %s run: unknown action %q (want %s)", kind, c.pos[0], actionIDs(d.Setup.Actions))
		}
		input, perr := assignments(a.Input, c.pos[1:])
		if perr != nil {
			return usagef("harness %s run %s: %v", kind, a.ID, perr)
		}
		st, err = cl.RunHarnessAction(ctx, kind, a.ID, input)
	}
	if err != nil {
		return err
	}
	return c.out(st, func(w io.Writer) { printHarnessStatus(w, d, st, env.cliName()) })
}

func kinds(hs client.Harnesses) []string {
	out := make([]string, len(hs.Harnesses))
	for i, d := range hs.Harnesses {
		out[i] = d.Kind
	}
	return out
}

func field(fs []client.SetupField, id string) (client.SetupField, bool) {
	for _, f := range fs {
		if f.ID == id {
			return f, true
		}
	}
	return client.SetupField{}, false
}

func fieldIDs(fs []client.SetupField) string {
	ids := make([]string, len(fs))
	for i, f := range fs {
		ids[i] = f.ID
	}
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

func actionIDs(as []client.SetupAction) string {
	ids := make([]string, len(as))
	for i, a := range as {
		ids[i] = a.ID
	}
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

// assignments parses FIELD=VALUE arguments against the schema; a bool
// field takes true or false.
func assignments(fs []client.SetupField, args []string) (map[string]any, error) {
	out := map[string]any{}
	for _, arg := range args {
		k, val, ok := strings.Cut(arg, "=")
		if !ok {
			return nil, fmt.Errorf("want FIELD=VALUE, got %q (fields: %s)", arg, fieldIDs(fs))
		}
		f, ok := field(fs, k)
		if !ok {
			return nil, fmt.Errorf("unknown field %q (want %s)", k, fieldIDs(fs))
		}
		if f.Kind != "bool" {
			out[k] = val
			continue
		}
		switch val {
		case "true":
			out[k] = true
		case "false":
			out[k] = false
		default:
			return nil, fmt.Errorf("%s takes true or false", k)
		}
	}
	return out, nil
}

func printHarnesses(w io.Writer, hs client.Harnesses) {
	p := hs.Machine.Profile
	local := "no local harness processes"
	if p.LocalProcesses {
		local = "local harness processes allowed"
	}
	fmt.Fprintf(w, "machine %s (%s; %s)\n", hs.Machine.Label, p.Kind, local)
	for _, d := range hs.Harnesses {
		state := "available"
		if !d.Available {
			state = "unavailable: " + d.Reason
		}
		setup := ""
		if d.Connectable {
			setup = "; stage harness " + d.Kind + " status"
		}
		fmt.Fprintf(w, "  %-9s %s, %s, %s%s\n", d.Kind, d.Name, d.Hosting, state, setup)
	}
}

// printHarnessStatus prints a registered panel for the kind (slots), else
// the generic status from the setup schema.
func printHarnessStatus(w io.Writer, d client.HarnessDescriptor, st client.HarnessStatus, cli string) {
	if p, ok := statusPrinters[d.Kind]; ok && len(st.Details) > 0 {
		if p(w, st, cli) == nil {
			return
		}
	}
	fmt.Fprintf(w, "%s %s\n", d.Kind, st.State)
	for _, f := range st.Fields {
		label := f.ID
		if sf, ok := field(d.Setup.Fields, f.ID); ok {
			label = sf.Label
		}
		val := f.Value
		if val == "" && f.Set {
			val = "(set)"
		}
		fmt.Fprintf(w, "  %-20s %s\n", label, val)
	}
	for _, a := range st.Actions {
		if !a.Available && a.Reason != "" {
			fmt.Fprintf(w, "  %-20s unavailable (%s)\n", a.ID, a.Reason)
		}
	}
	if st.Reason != "" && !st.Connected {
		fmt.Fprintf(w, "  error                %s\n", st.Reason)
	}
}

// statusPrinters are harness-specific status panels (slots_*.go); an error
// falls back to the generic print.
var statusPrinters = map[string]func(io.Writer, client.HarnessStatus, string) error{}
