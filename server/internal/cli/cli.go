// Package cli is the stage command line: task, tree, schedule and session
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
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/install"
	"github.com/flexdinesh/stage/server/internal/service"
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
	// Service is the `stage service` environment; nil is the real one.
	Service func() service.Deps
	// Install is the installer's environment; nil is the real one.
	Install func() install.Deps
	// Releases is where `stage update` finds releases; nil is GitHub.
	Releases install.Source
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
	var x exitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &x):
		return x.code
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
  stage serve --managed               run as the per-user service (what "stage service" registers):
                                      config from service.env only, default data dir, unix required
  stage status [--wait DUR]           the server this CLI reaches: version, readiness, T3 state
                                      (exit 1 unreachable or API mismatch, 3 --wait timeout)
  stage t3 [status]                   the server's T3: URL, reachability, pairing, sync, auto-pair
  stage t3 pair [--code CODE]         pair the server with T3: auto-pair with the t3 CLI on the server's
                                      machine, or a code from "t3 pair" on the T3 host
  stage t3 url [URL | --reset]        show or set the server's T3 URL (--reset: default; T3_URL wins)
  stage version [--json]              this binary: version, identity, commit, API version, schema,
                                      and its installation (mode, location) when installed
  stage update [--check] [--version X] [--from DIR]
                                      install the latest stable release (or X) in this installation's
                                      mode; restarts the service and desktop as needed (docs/install.md)
  stage uninstall                     remove this installation; keeps data, config, logs and backups
  stage install --from DIR [--headless|--desktop] [--cli-name NAME]
                                      install a downloaded, verified release (run by scripts/install.sh
                                      and stage update with the release's own CLI)
                                      install commands take --json; exit 0 ok, 1 failed, 3 another
                                      install running, 7 unsupported/collision, 10 network, 11 failed
                                      verification, 12 rolled back, 13 failed and kept (see docs)
  stage service start [--exe PATH] [--wait 30s]
                                      register/update the per-user service (systemd --user, launchd)
                                      and start it; waits for readiness; never restarts a running one
  stage service restart [--exe PATH] [--wait 150s]
  stage service stop|uninstall [--wait 2m]   uninstall keeps data and service.env
  stage service status                service, definition, data dir holder, Stage health, T3, problems
  stage service logs [-f] [-n 100]
  stage service enable|disable        login startup on/off (does not start or stop it)
                                      service commands take --json; exit 0 ok, 1 failed, 3 wait
                                      expired, 4 not running, 5 data dir in use, 6 no service
                                      manager, 7 invalid config; --url/STAGE_URL do not apply
  stage context                       your session, cwd and resolved project
  stage show REF                      the task or session a stage link, T3 thread link,
                                      #TASK_ID or thread id names (with its session/task)
  stage projects ls
  stage models ls [--connection ID]
  stage task ls [--project P] [--state S,..] [--tag T] [-q TEXT] [--closed true|all] [--limit N]
  stage task show ID
  stage task add --title T --prompt TEXT|- [config] [--needs ID,..] [--after-me] [--tree ID]
                   [--start-mode auto|manual|inherit] [--base-task ID]
                   [--start-at YYYY-MM-DDTHH:mm --tz ZONE] [--start] [--handoff=false]
  stage task edit ID [config] [--start-at TIME --tz ZONE | --start-at ""] [--handoff=true|false]
                   [--start-mode auto|manual|inherit] [--base-task ID]
  stage task needs ID [NEED...]       replace a backlog task's needs (none clears them)
  stage task rm|start|stop|dup ID     start requires completed prerequisites;
                                      rm only for a task that never started
  stage task archive ID [--undo]      archive a standalone task (--undo unarchives)
  stage task wait ID [--until S,..] [--timeout 10m]      exit 3 on timeout
  stage tree ls [--status active|done|archived|all] [--all] [--project P]
  stage tree show ID                  levels, states, needs, readiness
  stage tree new --title T [--start-mode auto|manual] ID[:NEED,..]...
  stage tree add TREE_ID TASK_ID [--needs ID,..]   a task in another tree moves
  stage tree rm TASK_ID               take a task out of its tree (dependents take its needs)
  stage tree edit ID [--title T] [--start-mode auto|manual]
  stage tree start ID                 start every ready member
  stage tree done|archive ID [--undo] mark done / archive (--undo reopens)
  stage tree rm-tree ID               delete a tree and its tasks (none started)
  stage schedule ls [--project P]
  stage schedule show ID
  stage schedule add --title T --prompt TEXT --cron CRON --tz ZONE [config] [--paused]
  stage schedule edit ID [config] [--cron CRON] [--tz ZONE]
  stage schedule rm|run|pause|resume ID
  stage schedule preview --cron CRON --tz ZONE
  stage sessions adopt [THREAD_ID] [--title T] [--initial-note TEXT] [--tag T]... [--tree ID]
                                      track a session as a task (default: your session)
  stage mcp [--t3-session-env]        opt in only for a dedicated T3 stdio process
  stage setup mcp [--claude] [--codex] [--http|--t3-session-env]
  stage setup instructions [--claude] [--codex] [--file PATH]
  stage guide                         agent workflow guide

Config: --project P --provider ID --model ID --effort E --option K=V... --runtime MODE
        --mode default|plan --base-ref REF --branch NAME --tag T...
        --prompt-file PATH --image PATH...
Notes: task add and sessions adopt accept --initial-note TEXT; schedules accept --notes TEXT.
Start mode: tasks inherit their tree by default. Set shared policy on the tree;
            task --start-mode auto|manual overrides it; inherit clears the override.
  stage task|tree notes ID [list] [--kind notes|events|all] [--category C] [--q TEXT]
                          [--task ID (tree only)] [--cursor CURSOR] [--limit N]
  stage task|tree notes ID add --text TEXT [--category C] [--request-id KEY] [--source-task ID]
  stage task|tree notes ID get ENTRY_ID
Global: --url URL (env STAGE_URL; unix:///abs/path.sock for a socket; default: the private default
        socket when a server owns it, else http://127.0.0.1:7380) --json
Caller: context, creation, notes and adoption accept --caller-connection ID --caller-session ID.
The CLI also reads T3CODE_THREAD_ID. OpenCode MCP sends per-call session metadata.
Creation: --connection ID selects the destination. --execution-config JSON sets harness options.
References/adoption: --target-connection ID --target-session ID names a scoped session.
Creator filter: task ls --origin-connection ID --origin-session ID.
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

// sessionFlags keeps the external ID and its connection together.
func (c *cmd) sessionFlags(prefix string) func() (*db.SessionRef, error) {
	var ref db.SessionRef
	c.fs.Int64Var(&ref.ConnectionID, prefix+"-connection", 0, "Stage harness connection ID")
	c.fs.StringVar(&ref.ExternalSessionID, prefix+"-session", "", "external session ID")
	return func() (*db.SessionRef, error) {
		if !c.set(prefix+"-connection") && !c.set(prefix+"-session") {
			return nil, nil
		}
		if ref.ConnectionID <= 0 || strings.TrimSpace(ref.ExternalSessionID) == "" {
			return nil, usagef("supply --%s-connection and --%s-session together", prefix, prefix)
		}
		return &ref, nil
	}
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
	case "t3":
		return runT3(ctx, env, sub)
	case "version":
		return runVersion(env, sub)
	case "service":
		return runService(ctx, env, sub)
	case "install":
		return runInstall(ctx, env, sub)
	case "update":
		return runUpdate(ctx, env, sub)
	case "uninstall":
		return runUninstall(ctx, env, sub)
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
	case "tree", "trees":
		return runTree(ctx, env, sub)
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
	caller := c.sessionFlags("caller")
	c.fs.StringVar(&a.CallerThreadID, "thread", "", "session id")
	c.fs.StringVar(&a.Cwd, "cwd", "", "working directory")
	if err := c.parse(args); err != nil {
		return err
	}
	var err error
	a.CallerSession, err = caller()
	if err != nil {
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
	target := c.sessionFlags("target")
	if err := c.parse(args); err != nil {
		return err
	}
	ref, err := target()
	if err != nil {
		return err
	}
	a := agenttools.RefArgs{Session: ref}
	switch {
	case len(c.pos) == 1 && ref == nil:
		a.Ref = c.pos[0]
	case len(c.pos) == 0 && ref != nil:
	default:
		return usagef("show: pass one reference or --target-connection and --target-session")
	}
	res, err := c.ops().ReadRef(ctx, a)
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
	return c.out(res, func(w io.Writer) {
		printProjects(w, res.Projects)
		for _, v := range res.Connections {
			fmt.Fprintf(w, "connection %d  %s\n", v.ID, v.Harness)
		}
	})
}

func runModels(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "ls")
	if v != "ls" {
		return usagef("models %s: unknown", v)
	}
	c := newCmd(env, "models ls")
	var a agenttools.ModelsArgs
	c.fs.Int64Var(&a.ConnectionID, "connection", 0, "show this connection’s capabilities")
	if err := c.parse(rest); err != nil {
		return err
	}
	res, err := c.ops().ListModels(ctx, a)
	if err != nil {
		return err
	}
	return c.out(res, func(w io.Writer) {
		if res.Capabilities != nil {
			printCapabilities(w, *res.Capabilities)
		} else {
			printCatalog(w, res.Catalog)
		}
	})
}

func runMCP(ctx context.Context, env Env, args []string) error {
	c := newCmd(env, "mcp")
	dedicated := c.fs.Bool("t3-session-env", false, "dedicated T3 process: read T3CODE_THREAD_ID")
	if err := c.parse(args); err != nil {
		return err
	}
	ops := c.ops()
	if !*dedicated {
		ops.Caller = agentctx.Caller{}
	}
	return agenttools.NewServer(ops).Run(ctx, &mcp.StdioTransport{})
}
