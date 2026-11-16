// Package cli is the stage command line: task, schedule and session
// commands over the REST API (agenttools operations), the stdio MCP server
// and agent setup helpers. `stage` with no args (or `serve`) runs the
// server instead (cmd/stage).
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
)

// URLEnv overrides the server address.
const URLEnv = "STAGE_URL"

// Env is the process environment (injectable for tests).
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	Caller         agentctx.Caller
	Home           string
	// Executable is this binary's path (registered as the stdio MCP command).
	Executable string
	// Exec runs an external command (claude / codex mcp add).
	Exec func(name string, args ...string) error
	// LookPath reports whether a command is installed.
	LookPath func(name string) error
}

// OSEnv is the real process environment.
func OSEnv() Env {
	home, _ := os.UserHomeDir()
	exe, err := os.Executable()
	if err != nil {
		exe = "stage"
	}
	return Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv,
		Caller: agentctx.FromEnv(), Home: home, Executable: exe,
		Exec: func(name string, args ...string) error {
			cmd := exec.Command(name, args...)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			return cmd.Run()
		},
		LookPath: func(name string) error { _, err := exec.LookPath(name); return err },
	}
}

// errUsage prints usage and exits 2.
type errUsage struct{ msg string }

func (e errUsage) Error() string { return e.msg }

func usagef(format string, a ...any) error { return errUsage{fmt.Sprintf(format, a...)} }

// errTimeout: task wait timed out (exit 3).
var errTimeout = errors.New("timed out")

// Run executes args (without the program name) and returns the exit code.
func Run(ctx context.Context, args []string, env Env) int {
	err := run(ctx, args, env)
	var u errUsage
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &u):
		fmt.Fprintf(env.Stderr, "error: %s\n\n%s", u.msg, usage)
		return 2
	case errors.Is(err, errTimeout):
		return 3
	default:
		fmt.Fprintf(env.Stderr, "error: %s\n", err)
		return 1
	}
}

const usage = `stage: stage server and agent CLI

Usage:
  stage serve [--listen ADDR]...      run the server (also: stage with no args); ADDR host:port,
                                      unix:/abs/path.sock or unix (private default socket); repeat or
                                      comma-separate for several (env STAGE_LISTEN, default 127.0.0.1:7380)
  stage status [--wait DUR]           the server this CLI reaches: version, readiness, T3 state
                                      (exit 1 unreachable or API mismatch, 3 --wait timeout)
  stage context                       your session, cwd and resolved project
  stage show REF                      the task or session a stage link, T3 thread link,
                                      #TASK_ID or thread id names (with its session/task)
  stage projects ls
  stage models ls
  stage task ls [--project P] [--state S,..] [--tag T] [-q TEXT] [--limit N]
  stage task show ID
  stage task add --title T --prompt TEXT|- [config] [--after-me|--after-task ID|--after-thread ID]
                   [--start-at YYYY-MM-DDTHH:mm --tz ZONE] [--start] [--handoff=false]
  stage task edit ID [config] [--start-at TIME --tz ZONE | --start-at ""] [--handoff=true|false]
  stage task rm|start|stop|dup|unlink ID
  stage task followup ID -m TEXT [--mode auto|queue|steer|restart] [--image PATH]...
  stage task link ID --after-me|--after-task ID|--after-thread ID [--handoff=false]
  stage task order ID ID...           link backlog tasks to run in this order
  stage task wait ID [--until S,..] [--timeout 10m]      exit 3 on timeout
  stage schedule ls [--project P]
  stage schedule show ID
  stage schedule add --title T --prompt TEXT --cron CRON --tz ZONE [config] [--paused]
  stage schedule edit ID [config] [--cron CRON] [--tz ZONE]
  stage schedule rm|run|pause|resume ID
  stage schedule preview --cron CRON --tz ZONE
  stage sessions ls [--project P] [--active] [-q TEXT] [--limit N]
  stage sessions search QUERY [--project P] [--limit N]   title, branch, PR, project, message text
  stage sessions show THREAD_ID
  stage sessions questions THREAD_ID  pending questions (read-only)
  stage mcp                           stdio MCP server
  stage setup mcp [--claude] [--codex] [--http]
  stage setup instructions [--claude] [--codex] [--file PATH]
  stage guide                         agent workflow guide

Config: --project P --provider ID --model ID --effort E --option K=V... --runtime MODE
        --mode default|plan --base-ref REF --branch NAME --tag T... --notes TEXT
        --prompt-file PATH --image PATH...
Global: --url URL (env STAGE_URL; unix:///abs/path.sock for a socket; default: the private default
        socket when a server owns it, else http://127.0.0.1:7380) --json
Inside a T3 session, T3CODE_THREAD_ID and cwd pick the project, record provenance
and enable --after-me.
`

// cmd holds a command's parsed global flags.
type cmd struct {
	env  Env
	fs   *flag.FlagSet
	url  string
	json bool
	pos  []string
}

func newCmd(env Env, name string) *cmd {
	c := &cmd{env: env, fs: flag.NewFlagSet(name, flag.ContinueOnError)}
	c.fs.SetOutput(io.Discard)
	c.fs.StringVar(&c.url, "url", "", "server URL")
	c.fs.BoolVar(&c.json, "json", false, "JSON output")
	return c
}

// parse parses flags interspersed with positional args.
func (c *cmd) parse(args []string) error {
	for {
		if err := c.fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprint(c.env.Stdout, usage)
				return err
			}
			return usagef("%s: %v", c.fs.Name(), err)
		}
		rest := c.fs.Args()
		if len(rest) == 0 {
			return nil
		}
		c.pos = append(c.pos, rest[0])
		args = rest[1:]
	}
}

func (c *cmd) set(name string) bool {
	found := false
	c.fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func (c *cmd) ops() *agenttools.Ops {
	u := client.Resolve(c.url, c.env.Getenv).URL
	return &agenttools.Ops{Client: client.New(u), Caller: c.env.Caller, LocalFiles: true}
}

// out prints v as JSON (--json) or with human.
func (c *cmd) out(v any, human func(io.Writer)) error {
	if c.json {
		enc := json.NewEncoder(c.env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	human(c.env.Stdout)
	return nil
}

func run(ctx context.Context, args []string, env Env) error {
	if len(args) == 0 {
		fmt.Fprint(env.Stdout, usage)
		return nil
	}
	sub := args[1:]
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(env.Stdout, usage)
		return nil
	case "guide":
		fmt.Fprintln(env.Stdout, agenttools.Guide)
		return nil
	case "context":
		return runContext(ctx, env, sub)
	case "status":
		return runStatus(ctx, env, sub)
	case "show":
		return runShow(ctx, env, sub)
	case "projects":
		return runProjects(ctx, env, sub)
	case "models":
		return runModels(ctx, env, sub)
	case "task", "tasks":
		return runTask(ctx, env, sub)
	case "schedule", "schedules":
		return runSchedule(ctx, env, sub)
	case "sessions", "session":
		return runSessions(ctx, env, sub)
	case "mcp":
		return runMCP(ctx, env, sub)
	case "setup":
		return runSetup(env, sub)
	}
	return usagef("unknown command %q", args[0])
}

func verb(args []string, def string) (string, []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return def, args
	}
	return args[0], args[1:]
}

func runContext(ctx context.Context, env Env, args []string) error {
	c := newCmd(env, "context")
	var a agenttools.CallerArgs
	c.fs.StringVar(&a.CallerThreadID, "thread", "", "session id")
	c.fs.StringVar(&a.Cwd, "cwd", "", "working directory")
	if err := c.parse(args); err != nil {
		return err
	}
	res, err := c.ops().Context(ctx, a)
	if err != nil {
		return err
	}
	return c.out(res, func(w io.Writer) { printContext(w, res) })
}

func runShow(ctx context.Context, env Env, args []string) error {
	c := newCmd(env, "show")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.pos) != 1 {
		return usagef("show: want one reference (stage link, T3 thread link, #TASK_ID or thread id)")
	}
	res, err := c.ops().ReadRef(ctx, agenttools.RefArgs{Ref: c.pos[0]})
	if err != nil {
		return err
	}
	return c.out(res, func(w io.Writer) { printRef(w, res) })
}

func runProjects(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "ls")
	if v != "ls" {
		return usagef("projects %s: unknown", v)
	}
	c := newCmd(env, "projects ls")
	if err := c.parse(rest); err != nil {
		return err
	}
	res, err := c.ops().ListProjects(ctx, struct{}{})
	if err != nil {
		return err
	}
	return c.out(res, func(w io.Writer) { printProjects(w, res.Projects) })
}

func runModels(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "ls")
	if v != "ls" {
		return usagef("models %s: unknown", v)
	}
	c := newCmd(env, "models ls")
	if err := c.parse(rest); err != nil {
		return err
	}
	res, err := c.ops().ListModels(ctx, struct{}{})
	if err != nil {
		return err
	}
	return c.out(res, func(w io.Writer) { printCatalog(w, res) })
}

func runMCP(ctx context.Context, env Env, args []string) error {
	c := newCmd(env, "mcp")
	if err := c.parse(args); err != nil {
		return err
	}
	return agenttools.NewServer(c.ops()).Run(ctx, &mcp.StdioTransport{})
}
