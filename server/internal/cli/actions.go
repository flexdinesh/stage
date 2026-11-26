package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func runAction(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "ls")
	c := newCmd(env, "action "+v)
	var connection, task, project, onConnection, binding int64
	c.fs.Int64Var(&connection, "connection", 0, "target connection ID")
	c.fs.Int64Var(&task, "task", 0, "target task ID")
	c.fs.Int64Var(&project, "project", 0, "target project ID")
	c.fs.Int64Var(&onConnection, "on-connection", 0, "connection ID for a project action")
	c.fs.Int64Var(&binding, "binding", 0, "workspace binding ID for a project action")
	var input string
	var confirm bool
	switch v {
	case "ls", "list":
	case "run":
		c.fs.StringVar(&input, "input", "{}", "action input JSON object (- = stdin)")
		c.fs.BoolVar(&confirm, "confirm", false, "confirm the action")
	default:
		return usagef("unknown action operation %q", v)
	}
	if err := c.parse(rest); err != nil {
		return err
	}
	scope := client.ActionScope{
		ConnectionID:   int64Flag(c, "connection", connection),
		TaskID:         int64Flag(c, "task", task),
		ProjectID:      int64Flag(c, "project", project),
		OnConnectionID: int64Flag(c, "on-connection", onConnection),
		BindingID:      int64Flag(c, "binding", binding),
	}
	if err := scope.Validate(); err != nil {
		return usagef("action %s: %v", v, err)
	}
	o := c.ops()
	if v != "run" {
		if len(c.pos) != 0 {
			return usagef("action ls: unexpected positional arguments")
		}
		caller, err := o.ActionCaller(ctx, agenttools.CallerArgs{})
		if err != nil {
			return err
		}
		actions, err := o.Client.ListActions(ctx, scope, caller)
		if err != nil {
			return err
		}
		return c.out(actions, func(w io.Writer) {
			if len(actions) == 0 {
				fmt.Fprintln(w, "No actions available.")
			}
			for _, a := range actions {
				printAction(w, a)
			}
		})
	}
	if len(c.pos) != 1 || strings.TrimSpace(c.pos[0]) == "" {
		return usagef("action run: want one action ID")
	}
	if input == "-" {
		body, err := io.ReadAll(env.Stdin)
		if err != nil {
			return err
		}
		input = string(body)
	}
	if !strings.HasPrefix(strings.TrimSpace(input), "{") || !json.Valid([]byte(input)) {
		return usagef("action input must be a JSON object")
	}
	caller, err := o.ActionCaller(ctx, agenttools.CallerArgs{})
	if err != nil {
		return err
	}
	caller.Confirmed = confirm
	result, err := o.Client.RunAction(ctx, scope, c.pos[0], json.RawMessage(input), caller)
	if err != nil {
		return err
	}
	if result.JournalError != "" && !c.json {
		warn(env.stderr(), "%s", result.JournalError)
	}
	return c.out(result, func(w io.Writer) { printActionResult(w, result) })
}

func printAction(w io.Writer, action execution.Action) {
	fmt.Fprintf(w, "%s  %s\n", action.ID, action.Label)
	if !action.Availability.Available {
		fmt.Fprintf(w, "  Unavailable (%s)\n", action.Availability.Reason)
	}
	if action.Confirm != "" {
		fmt.Fprintln(w, "  "+action.Confirm)
	}
	if action.Destructive {
		warning := "Destructive action"
		if !action.Allows(execution.CallerAgent) {
			warning += " (requires --confirm)"
		}
		fmt.Fprintln(w, "  "+warning)
	}
	for _, field := range action.Input {
		required := ""
		if field.Required {
			required = ", required"
		}
		fmt.Fprintf(w, "  %s  %s (%s%s)\n", field.ID, field.Label, field.Kind, required)
		for _, choice := range field.Choices {
			fmt.Fprintf(w, "    %s  %s\n", choice.ID, choice.Label)
		}
	}
}

func printActionResult(w io.Writer, result execution.ActionResult) {
	if result.Message != "" {
		fmt.Fprintln(w, result.Message)
	}
	if result.Link != "" {
		fmt.Fprintln(w, result.Link)
	}
	if result.Replaced != 0 {
		fmt.Fprintf(w, "Replaced connection #%d\n", result.Replaced)
	}
	for _, fact := range result.Facts {
		fmt.Fprintf(w, "%s  %s", fact.Label, fact.Value)
		if fact.Link != "" {
			fmt.Fprintf(w, "  %s", fact.Link)
		}
		fmt.Fprintln(w)
	}
}
