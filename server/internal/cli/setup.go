package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
)

// Instruction block markers (setup instructions replaces between them).
const (
	blockBegin = "<!-- stage:begin -->"
	blockEnd   = "<!-- stage:end -->"
)

// mcpName is the MCP server name registered with agents.
const mcpName = "stage"

func runSetup(env Env, args []string) error {
	v, rest := verb(args, "")
	switch v {
	case "mcp":
		return setupMCP(env, rest)
	case "instructions":
		return setupInstructions(env, rest)
	}
	return usagef("setup: want mcp or instructions")
}

// setupMCP registers the stdio server (or the HTTP endpoint with --http) in
// Claude Code (user scope) and Codex. Default: both.
func setupMCP(env Env, args []string) error {
	c := newCmd(env, "setup mcp")
	var claude, codex, http, dedicated bool
	c.fs.BoolVar(&claude, "claude", false, "Claude Code")
	c.fs.BoolVar(&codex, "codex", false, "Codex")
	c.fs.BoolVar(&dedicated, "t3-session-env", false, "register dedicated T3 stdio with caller environment enabled")
	c.fs.BoolVar(&http, "http", false, "register the server's HTTP /mcp endpoint instead of stdio")
	if err := c.parse(args); err != nil {
		return err
	}
	if dedicated && http {
		return usagef("--t3-session-env requires dedicated stdio")
	}
	if !claude && !codex {
		claude, codex = true, true
	}
	base := c.url
	if base == "" {
		base = env.Getenv(URLEnv)
	}
	// Pass an explicit non-default server URL to the stdio server; without
	// one it resolves the server itself (default socket, else DefaultURL).
	var urlEnv []string
	if base != "" && strings.TrimRight(base, "/") != client.DefaultURL {
		urlEnv = []string{URLEnv + "=" + base}
	}
	if base == "" && http {
		if r := client.Resolve("", env.Getenv); r.Source == "default socket" {
			return usagef("setup mcp --http: the server is on a unix socket (%s); agents need an http(s) URL: pass --url, or use stdio", r.URL)
		}
	}
	if base == "" {
		base = client.DefaultURL
	}
	if http && strings.HasPrefix(base, "unix:") {
		return usagef("setup mcp --http: %s is a unix socket; agents need an http(s) URL (use stdio)", base)
	}
	endpoint := strings.TrimRight(base, "/") + "/mcp"

	stdio := []string{env.Executable, "mcp"}
	if dedicated {
		stdio = append(stdio, "--t3-session-env")
	}
	var errs []error
	if claude {
		_ = env.Exec("claude", "mcp", "remove", "--scope", "user", mcpName) // re-register
		args := []string{"mcp", "add", "--scope", "user"}
		if http {
			args = append(args, "--transport", "http", mcpName, endpoint)
		} else {
			for _, e := range urlEnv {
				args = append(args, "-e", e)
			}
			args = append(args, mcpName, "--")
			args = append(args, stdio...)
		}
		errs = append(errs, register(env, "claude", args))
	}
	if codex {
		_ = env.Exec("codex", "mcp", "remove", mcpName)
		args := []string{"mcp", "add", mcpName}
		if http {
			args = append(args, "--url", endpoint)
		} else {
			for _, e := range urlEnv {
				args = append(args, "--env", e)
			}
			args = append(args, "--")
			args = append(args, stdio...)
		}
		errs = append(errs, register(env, "codex", args))
	}
	return errors.Join(errs...)
}

func register(env Env, tool string, args []string) error {
	if err := env.LookPath(tool); err != nil {
		fmt.Fprintf(env.Stdout, "skipped %s: not installed\n", tool)
		return nil
	}
	if err := env.Exec(tool, args...); err != nil {
		return fmt.Errorf("%s %s: %w", tool, strings.Join(args, " "), err)
	}
	fmt.Fprintf(env.Stdout, "registered %s MCP server with %s\n", mcpName, tool)
	return nil
}

// instructionsBlock is the marked block written to agent instruction files.
func instructionsBlock() string {
	return blockBegin + "\n## stage (task queue)\n\n" + agenttools.Guide + "\n" + blockEnd + "\n"
}

// upsertBlock replaces the marked block in content, or appends it.
func upsertBlock(content string) string {
	block := instructionsBlock()
	if i := strings.Index(content, blockBegin); i >= 0 {
		if j := strings.Index(content[i:], blockEnd); j >= 0 {
			end := i + j + len(blockEnd)
			if end < len(content) && content[end] == '\n' {
				end++
			}
			return content[:i] + block + content[end:]
		}
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if content != "" {
		content += "\n"
	}
	return content + block
}

// setupInstructions writes the guide block into the global agent
// instruction files (~/.claude/CLAUDE.md, ~/.codex/AGENTS.md; default both)
// or --file. A target whose directory does not exist is skipped.
func setupInstructions(env Env, args []string) error {
	c := newCmd(env, "setup instructions")
	var claude, codex bool
	var file string
	c.fs.BoolVar(&claude, "claude", false, "~/.claude/CLAUDE.md")
	c.fs.BoolVar(&codex, "codex", false, "~/.codex/AGENTS.md")
	c.fs.StringVar(&file, "file", "", "write this file instead")
	if err := c.parse(args); err != nil {
		return err
	}
	var targets []string
	if file != "" {
		targets = []string{file}
	} else {
		if !claude && !codex {
			claude, codex = true, true
		}
		if claude {
			targets = append(targets, filepath.Join(env.Home, ".claude", "CLAUDE.md"))
		}
		if codex {
			targets = append(targets, filepath.Join(env.Home, ".codex", "AGENTS.md"))
		}
	}
	for _, path := range targets {
		if st, err := os.Stat(filepath.Dir(path)); err != nil || !st.IsDir() {
			fmt.Fprintf(env.Stdout, "skipped %s: directory missing\n", path)
			continue
		}
		cur, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		next := upsertBlock(string(cur))
		if next == string(cur) {
			fmt.Fprintf(env.Stdout, "unchanged %s\n", path)
			continue
		}
		if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "wrote %s\n", path)
	}
	return nil
}
