package claudecode

import (
	"context"
	"errors"
	"strings"
)

// MCPServer is `claude mcp get NAME`'s answer for one configured server.
type MCPServer struct {
	// Found: a server of that name is configured.
	Found bool
	// Status is Claude's status text without its mark ("Connected",
	// "Failed to connect"); Issue is the failure detail, if any.
	Status string
	Issue  string
}

// ReadMCPServer runs `claude mcp get name` in dir. Claude health-checks
// only that server (unlike `mcp list`, which checks every server). An
// unknown name exits 1 with "No MCP server named" on stderr.
func ReadMCPServer(ctx context.Context, cli *CLI, dir, name string) (MCPServer, error) {
	res, err := cli.Run(ctx, dir, "mcp", "get", name)
	if err != nil {
		return MCPServer{}, err
	}
	if res.Code != 0 {
		if strings.Contains(res.Stderr+res.Stdout, "No MCP server named") {
			return MCPServer{}, nil
		}
		return MCPServer{}, exitError("mcp get", res)
	}
	return ParseMCPServer(res.Stdout)
}

// ParseMCPServer reads the Status and Issue lines of `claude mcp get`.
func ParseMCPServer(out string) (MCPServer, error) {
	s := MCPServer{Found: true}
	for line := range strings.Lines(out) {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "Status":
			// Drop the leading mark (✔, ✘, ⏸ ...).
			if mark, rest, ok := strings.Cut(value, " "); ok && !strings.ContainsAny(mark, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
				value = strings.TrimSpace(rest)
			}
			s.Status = value
		case "Issue":
			s.Issue = value
		}
	}
	if s.Status == "" {
		return MCPServer{}, errors.New("claude mcp get: no status in its output")
	}
	return s, nil
}
