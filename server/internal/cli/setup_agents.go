package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/client"
)

// agentTarget is a coding agent `stage setup` configures. Paths are
// relative to the home directory.
type agentTarget struct {
	name, label string
	// command and home detect the agent: on PATH, or its config dir exists.
	command, home string
	// skills is the user skills directory the agent loads SKILL.md from.
	// Codex and OpenCode both load ~/.agents/skills (OpenCode also loads
	// ~/.claude/skills).
	skills string
	// instructions is its global instruction file ("" none).
	instructions string
	// mcp: `stage setup --mcp` can register the stdio server with it, and
	// mcpConfig is where that registration lives.
	mcp       bool
	mcpConfig string
}

var agentTargets = []agentTarget{
	{name: "claude", label: "Claude Code", command: "claude", home: ".claude", skills: ".claude/skills", instructions: ".claude/CLAUDE.md", mcp: true, mcpConfig: ".claude.json"},
	{name: "codex", label: "Codex", command: "codex", home: ".codex", skills: ".agents/skills", instructions: ".codex/AGENTS.md", mcp: true, mcpConfig: ".codex/config.toml"},
	{name: "opencode", label: "OpenCode", command: "opencode", home: ".config/opencode", skills: ".agents/skills"},
}

func (a agentTarget) skillPath(env Env) string {
	return filepath.Join(env.Home, a.skills, env.cliName(), "SKILL.md")
}

func (a agentTarget) detected(env Env) bool {
	if env.LookPath != nil && env.LookPath(a.command) == nil {
		return true
	}
	st, err := os.Stat(filepath.Join(env.Home, a.home))
	return err == nil && st.IsDir()
}

// agentFlags registers --claude, --codex and --opencode; the returned
// function is the chosen agents (none chosen: detectedOnly ones).
func agentFlags(c *cmd, env Env) func(detectedOnly bool) []agentTarget {
	chosen := make([]bool, len(agentTargets))
	for i, a := range agentTargets {
		c.fs.BoolVar(&chosen[i], a.name, false, a.label)
	}
	return func(detectedOnly bool) []agentTarget {
		var out []agentTarget
		for i, a := range agentTargets {
			if chosen[i] {
				out = append(out, a)
			}
		}
		if len(out) > 0 || !detectedOnly {
			return out
		}
		for _, a := range agentTargets {
			if a.detected(env) {
				out = append(out, a)
			}
		}
		return out
	}
}

// writeFile writes content to path (creating its directory) unless it is
// already current; reports what it did.
func writeFile(env Env, path, content string) error {
	cur, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if string(cur) == content {
		unchanged(env.stdout(), path)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	success(env.stdout(), "Wrote %s", path)
	return nil
}

// installSkills writes the Stage skill for agents (once per shared
// directory).
func installSkills(env Env, agents []agentTarget) error {
	content := agenttools.Skill(env.cliName())
	var done []string
	for _, a := range agents {
		path := a.skillPath(env)
		if slices.Contains(done, path) {
			continue
		}
		done = append(done, path)
		if err := writeFile(env, path, content); err != nil {
			return err
		}
	}
	return nil
}

// setupSkill is `stage setup skill [--claude] [--codex] [--opencode]`:
// the Stage skill only (default: every detected agent).
func setupSkill(env Env, args []string) error {
	c := newCmd(env, "setup skill")
	agents := agentFlags(c, env)
	if err := c.parse(args); err != nil {
		return err
	}
	chosen := agents(true)
	if len(chosen) == 0 {
		warn(env.stdout(), "No agents found (Claude Code, Codex, OpenCode); pass --claude, --codex or --opencode")
		return nil
	}
	return installSkills(env, chosen)
}

// setupAgents is `stage setup [--claude] [--codex] [--opencode] [--mcp]
// [--check]`: for each chosen (default: detected) agent, the Stage skill and
// the instruction pointer, the stdio MCP server with --mcp; then the check.
// --check only reports (exit 1 on problems).
func setupAgents(ctx context.Context, env Env, args []string) error {
	c := newCmd(env, "setup")
	agents := agentFlags(c, env)
	var mcp, check bool
	c.fs.BoolVar(&mcp, "mcp", false, "also register the stdio MCP server (Claude Code, Codex)")
	c.fs.BoolVar(&check, "check", false, "report only: write nothing")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.pos) > 0 {
		return usagef("setup: unexpected argument %q", c.pos[0])
	}
	chosen := agents(true)
	if !check {
		if len(chosen) == 0 {
			warn(env.stdout(), "No agents found (Claude Code, Codex, OpenCode); pass --claude, --codex or --opencode")
		}
		if err := installSkills(env, chosen); err != nil {
			return err
		}
		for _, a := range chosen {
			if a.instructions == "" {
				continue
			}
			path := filepath.Join(env.Home, a.instructions)
			if st, err := os.Stat(filepath.Dir(path)); err != nil || !st.IsDir() {
				continue
			}
			if err := upsertInstructions(env, path); err != nil {
				return err
			}
		}
		if mcp {
			var mcpArgs []string
			for _, a := range chosen {
				if a.mcp {
					mcpArgs = append(mcpArgs, "--"+a.name)
				} else {
					warn(env.stdout(), "Skipped %s MCP: register %s as a local MCP server named %s in its config yourself", a.label, cmdText(env.Executable+" mcp"), mcpName)
				}
			}
			if len(mcpArgs) > 0 {
				if c.url != "" {
					mcpArgs = append(mcpArgs, "--url", c.url)
				}
				if err := setupMCP(env, mcpArgs); err != nil {
					return err
				}
			}
		}
		fmt.Fprintln(env.Stdout)
	}
	report := setupCheck(ctx, env, c.url, chosen)
	if err := c.out(report, func(w io.Writer) { printSetupReport(w, report) }); err != nil {
		return err
	}
	if check && report.Problems > 0 {
		return exitError{1}
	}
	return nil
}

// setupReport is what `stage setup` checks.
type setupReport struct {
	CLI      checkLine    `json:"cli"`
	Server   checkLine    `json:"server"`
	Caller   checkLine    `json:"caller"`
	Agents   []agentCheck `json:"agents"`
	Problems int          `json:"problems"`
}

// checkLine is one checked item: ok, note (fine, but worth knowing) or
// problem.
type checkLine struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type agentCheck struct {
	Agent        string    `json:"agent"`
	Skill        checkLine `json:"skill"`
	Instructions checkLine `json:"instructions"`
	MCP          checkLine `json:"mcp"`
}

func ok(format string, a ...any) checkLine   { return checkLine{"ok", fmt.Sprintf(format, a...)} }
func note(format string, a ...any) checkLine { return checkLine{"note", fmt.Sprintf(format, a...)} }
func problem(format string, a ...any) checkLine {
	return checkLine{"problem", fmt.Sprintf(format, a...)}
}

func setupCheck(ctx context.Context, env Env, url string, agents []agentTarget) setupReport {
	var r setupReport
	cli := env.cliName()
	switch path, err := which(env, cli); {
	case err != nil:
		r.CLI = problem("%s is not on PATH; agents run `%s`: add %s to PATH", cli, cli, filepath.Dir(env.Executable))
	case path != "" && !sameFile(path, env.Executable):
		r.CLI = note("%s on PATH is %s, not this binary (%s)", cli, path, env.Executable)
	default:
		r.CLI = ok("%s on PATH (%s)", cli, env.Executable)
	}

	res := client.Resolve(url, env.Getenv)
	h, err := client.New(res.URL).Health(ctx)
	switch {
	case err != nil:
		r.Server = problem("%s (%s): %v", res.URL, res.Source, err)
	case h.APIVersion != 0 && h.APIVersion != buildinfo.APIVersion:
		r.Server = problem("%s: server API %d, this CLI %d: update one of them", res.URL, h.APIVersion, buildinfo.APIVersion)
	default:
		r.Server = ok("%s (%s), Stage %s", res.URL, res.Source, h.Version)
	}

	if err == nil {
		ops := (&cmd{env: env, url: url}).ops()
		cx, cerr := ops.Context(ctx, agenttools.CallerArgs{})
		switch {
		case cerr != nil:
			r.Caller = problem("%v", cerr)
		case cx.Session != nil:
			r.Caller = ok("session %s on connection %d (from %s)", cx.Session.ExternalSessionID, cx.Session.ConnectionID, cx.SessionSource)
		case cx.CallerWarning != "":
			r.Caller = problem("%s", cx.CallerWarning)
		default:
			r.Caller = note("no agent session here (expected outside an agent; agents get it from their harness)")
		}
	} else {
		r.Caller = note("not checked: no server")
	}

	skill := agenttools.Skill(cli)
	for _, a := range agents {
		ac := agentCheck{Agent: a.label}
		path := a.skillPath(env)
		switch cur, err := os.ReadFile(path); {
		case err != nil:
			ac.Skill = problem("missing %s: run %s setup", path, cli)
		case string(cur) != skill:
			ac.Skill = problem("outdated %s: run %s setup", path, cli)
		default:
			ac.Skill = ok("%s", path)
		}
		ac.Instructions = instructionsCheck(env, a)
		ac.MCP = mcpCheck(env, a)
		r.Agents = append(r.Agents, ac)
	}
	for _, l := range r.lines() {
		if l.Status == "problem" {
			r.Problems++
		}
	}
	return r
}

func (r setupReport) lines() []checkLine {
	out := []checkLine{r.CLI, r.Server, r.Caller}
	for _, a := range r.Agents {
		out = append(out, a.Skill, a.Instructions, a.MCP)
	}
	return out
}

func instructionsCheck(env Env, a agentTarget) checkLine {
	if a.instructions == "" {
		return note("none (the skill is enough)")
	}
	path := filepath.Join(env.Home, a.instructions)
	cur, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(cur), blockBegin) {
		return note("no Stage pointer in %s (optional)", path)
	}
	if upsertBlock(string(cur), env.cliName()) != string(cur) {
		return problem("old Stage block in %s: run %s setup to shrink it", path, env.cliName())
	}
	return ok("pointer in %s", path)
}

// mcpCheck reads the agent's MCP config for a server named mcpName.
func mcpCheck(env Env, a agentTarget) checkLine {
	if !a.mcp {
		return note("not managed by %s setup", env.cliName())
	}
	path := filepath.Join(env.Home, a.mcpConfig)
	b, err := os.ReadFile(path)
	registered := false
	if err == nil {
		if a.name == "claude" {
			var cfg struct {
				MCPServers map[string]json.RawMessage `json:"mcpServers"`
			}
			if json.Unmarshal(b, &cfg) == nil {
				_, registered = cfg.MCPServers[mcpName]
			}
		} else {
			registered = strings.Contains(string(b), "[mcp_servers."+mcpName+"]")
		}
	}
	if registered {
		return ok("MCP server %s registered", mcpName)
	}
	return note("MCP not registered (optional: %s setup --mcp)", env.cliName())
}

func which(env Env, name string) (string, error) {
	if env.Which != nil {
		return env.Which(name)
	}
	if env.LookPath == nil {
		return "", errors.New("unknown")
	}
	return "", env.LookPath(name)
}

func sameFile(a, b string) bool {
	sa, errA := os.Stat(a)
	sb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(sa, sb)
}

func printSetupReport(w io.Writer, r setupReport) {
	row := func(label string, l checkLine) {
		t := toneOK
		switch l.Status {
		case "note":
			t = toneWarn
		case "problem":
			t = toneBad
		}
		fmt.Fprintf(w, "%s %s %s\n", t.glyph(), dim(fmt.Sprintf("%-13s", label)), l.Detail)
	}
	row("cli", r.CLI)
	row("server", r.Server)
	row("caller", r.Caller)
	for _, a := range r.Agents {
		row(a.Agent, a.Skill)
		row("", a.Instructions)
		row("", a.MCP)
	}
	if len(r.Agents) == 0 {
		row("agents", note("none found (Claude Code, Codex, OpenCode)"))
	}
	if r.Problems > 0 {
		fmt.Fprintln(w)
		line(w, toneBad, "%d problem(s)", r.Problems)
	}
}
