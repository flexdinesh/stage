package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/agenttools"
)

// runWorkspaces runs workspaces sources|add: adding a workspace to a
// harness through its declared sources (ADR 0012).
func runWorkspaces(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "sources")
	c := newCmd(env, "workspaces "+v)
	var connection int64
	c.fs.Int64Var(&connection, "connection", 0, "harness connection ID")
	switch v {
	case "sources":
		if err := c.parse(rest); err != nil {
			return err
		}
		if len(c.pos) != 0 || connection <= 0 {
			return usagef("workspaces sources: want --connection ID")
		}
		res, err := c.ops().ListWorkspaceSources(ctx, agenttools.WorkspaceSourcesArgs{ConnectionID: connection})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) {
			if !res.Availability.Available {
				fmt.Fprintf(w, "Unavailable (%s)\n", res.Availability.Reason)
			}
			for _, s := range res.Sources {
				fmt.Fprintf(w, "%s  %s\n", s.ID, s.Label)
				if s.Help != "" {
					fmt.Fprintln(w, "  "+s.Help)
				}
				if !s.Availability.Available {
					fmt.Fprintf(w, "  Unavailable (%s)\n", s.Availability.Reason)
				}
				if s.Confirm != "" {
					fmt.Fprintf(w, "  %s (requires --confirm)\n", s.Confirm)
				}
				for _, f := range s.Input {
					required := ""
					if f.Required {
						required = ", required"
					}
					fmt.Fprintf(w, "  %s  %s (%s%s)\n", f.ID, f.Label, f.Kind, required)
				}
			}
		})
	case "add":
		var source, input, requestID string
		var project int64
		var confirm bool
		caller := c.sessionFlags("caller")
		c.fs.StringVar(&source, "source", "", "source ID from workspaces sources")
		c.fs.StringVar(&input, "input", "{}", "source input JSON object (- = stdin)")
		c.fs.StringVar(&requestID, "request-id", "", "retry key (default: derived from the connection, source, input and project)")
		c.fs.Int64Var(&project, "project", 0, "Stage project group to put the workspace in")
		c.fs.BoolVar(&confirm, "confirm", false, "confirm the source's confirmation text")
		if err := c.parse(rest); err != nil {
			return err
		}
		if len(c.pos) != 0 || connection <= 0 || strings.TrimSpace(source) == "" {
			return usagef("workspaces add: want --connection ID --source S")
		}
		if input == "-" {
			body, err := io.ReadAll(env.Stdin)
			if err != nil {
				return err
			}
			input = string(body)
		}
		var fields map[string]any
		if !strings.HasPrefix(strings.TrimSpace(input), "{") || json.Unmarshal([]byte(input), &fields) != nil {
			return usagef("workspaces add: --input must be a JSON object")
		}
		projectID := int64Flag(c, "project", project)
		if requestID == "" {
			requestID = workspaceRequestID(connection, source, fields, projectID)
		}
		ref, err := caller()
		if err != nil {
			return err
		}
		res, err := c.ops().AddWorkspace(ctx, agenttools.AddWorkspaceArgs{
			CallerArgs: agenttools.CallerArgs{CallerSession: ref}, ConnectionID: connection, SourceID: source,
			Input: fields, RequestID: requestID, ProjectID: projectID, Confirmed: confirm,
		})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) {
			verbText := "Added"
			if res.Existing {
				verbText = "Already in the harness:"
			}
			fmt.Fprintf(w, "%s %s  %s\n", verbText, res.Workspace.WorkspaceID, res.Workspace.Title)
			if res.Workspace.Path != "" {
				fmt.Fprintln(w, "  "+res.Workspace.Path)
			}
			fmt.Fprintf(w, "  project #%d %s\n", res.Group.ID, res.Group.Title)
			if res.Message != "" {
				fmt.Fprintln(w, "  "+res.Message)
			}
		})
	default:
		return usagef("workspaces %s: unknown", v)
	}
}

// workspaceRequestID derives a stable key so a repeated command retries the
// same request.
func workspaceRequestID(connection int64, source string, input map[string]any, project *int64) string {
	canonical, _ := json.Marshal(input) // map keys marshal sorted
	var p string
	if project != nil {
		p = strconv.FormatInt(*project, 10)
	}
	sum := sha256.Sum256(bytes.Join([][]byte{[]byte(strconv.FormatInt(connection, 10)), []byte(source), canonical, []byte(p)}, []byte{0}))
	return "workspace-add-" + hex.EncodeToString(sum[:12])
}
