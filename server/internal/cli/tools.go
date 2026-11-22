package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolSession connects an in-process MCP client to the agent tools server
// over ops: `stage tools` and `stage call` see exactly the MCP tools.
func toolSession(ctx context.Context, ops *agenttools.Ops) (*mcp.ClientSession, error) {
	server, client := mcp.NewInMemoryTransports()
	if _, err := agenttools.NewServer(ops).Connect(ctx, server, nil); err != nil {
		return nil, err
	}
	return mcp.NewClient(&mcp.Implementation{Name: "stage-cli", Version: agenttools.Version}, nil).Connect(ctx, client, nil)
}

// listTools is every MCP tool (sorted by name).
func listTools(ctx context.Context, cs *mcp.ClientSession) ([]*mcp.Tool, error) {
	var tools []*mcp.Tool
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

func findTool(tools []*mcp.Tool, name string) (*mcp.Tool, error) {
	for _, t := range tools {
		if t.Name == name {
			return t, nil
		}
	}
	return nil, usagef("unknown tool %q (stage tools lists them)", name)
}

// runTools is `stage tools [NAME] [--json]`: the MCP tools, or one tool's
// description and input schema. Static: it needs no server.
func runTools(ctx context.Context, env Env, args []string) error {
	c := newCmd(env, "tools")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.pos) > 1 {
		return usagef("tools: at most one tool name")
	}
	cs, err := toolSession(ctx, c.ops())
	if err != nil {
		return err
	}
	defer cs.Close()
	tools, err := listTools(ctx, cs)
	if err != nil {
		return err
	}
	if len(c.pos) == 0 {
		return c.out(tools, func(w io.Writer) {
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			for _, t := range tools {
				fmt.Fprintf(tw, "%s\t%s\n", t.Name, firstSentence(t.Description))
			}
			tw.Flush()
			fmt.Fprintf(w, "\n%s tools NAME: description and input schema; %s call NAME [JSON|-]: run it\n", env.cliName(), env.cliName())
		})
	}
	t, err := findTool(tools, c.pos[0])
	if err != nil {
		return err
	}
	return c.out(t, func(w io.Writer) {
		fmt.Fprintf(w, "%s%s\n\n%s\n\ninput schema:\n", t.Name, toolHints(t), t.Description)
		schema, _ := json.MarshalIndent(t.InputSchema, "", "  ")
		fmt.Fprintf(w, "%s\n", schema)
	})
}

// runCall is `stage call TOOL [JSON|-]`: one MCP tool call through the same
// server `stage mcp` runs, with the CLI's caller context. Prints the tool's
// result as JSON; a tool error exits 1.
func runCall(ctx context.Context, env Env, args []string) error {
	c := newCmd(env, "call")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.pos) == 0 || len(c.pos) > 2 {
		return usagef("call: want TOOL [JSON|-]")
	}
	input := "{}"
	if len(c.pos) == 2 {
		input = c.pos[1]
	}
	if input == "-" {
		b, err := io.ReadAll(env.Stdin)
		if err != nil {
			return err
		}
		input = string(b)
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(input), &arguments); err != nil || arguments == nil {
		return usagef("call: arguments must be a JSON object: %s", strings.TrimSpace(input))
	}
	cs, err := toolSession(ctx, c.ops())
	if err != nil {
		return err
	}
	defer cs.Close()
	tools, err := listTools(ctx, cs)
	if err != nil {
		return err
	}
	if _, err := findTool(tools, c.pos[0]); err != nil {
		return err
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.pos[0], Arguments: arguments})
	if err != nil {
		return err
	}
	if res.IsError {
		return errors.New(toolText(res))
	}
	if res.StructuredContent == nil {
		fmt.Fprintln(env.Stdout, toolText(res))
		return nil
	}
	enc := json.NewEncoder(env.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(res.StructuredContent)
}

func toolText(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func toolHints(t *mcp.Tool) string {
	if t.Annotations == nil {
		return ""
	}
	switch {
	case t.Annotations.ReadOnlyHint:
		return " (read-only)"
	case t.Annotations.DestructiveHint != nil && *t.Annotations.DestructiveHint:
		return " (destructive)"
	}
	return ""
}

// firstSentence is s up to its first ". " (or all of s).
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}
