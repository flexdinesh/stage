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

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/install"
	"github.com/flexdinesh/stage/server/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	// Which is a command's path on PATH (setup --check); nil: unknown.
	Which func(name string) (string, error)
	// Service is the `stage service` environment; nil is the real one.
	Service func() service.Deps
	// Install is the installer's environment; nil is the real one.
	Install func() install.Deps
	// Releases is where `stage update` finds releases; nil is GitHub.
	Releases install.Source
	// ReadPassword reads a terminal password without echo. Nil uses Stdin.
	ReadPassword func() ([]byte, error)
}

// cliName is this identity's installed CLI name, which agent guidance names.
func (e Env) cliName() string { return install.CLIName(e.Home, buildinfo.Identity) }

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
		Which:    exec.LookPath,
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
		fail(env.stderr(), "%s", u.msg)
		fmt.Fprintf(env.Stderr, "\n%s", usage)
		return 2
	case errors.Is(err, errTimeout):
		return 3
	default:
		fail(env.stderr(), "%s", err)
		var apiErr *client.Error
		if errors.As(err, &apiErr) && apiErr.Code == "authentication_required" {
			fmt.Fprintln(env.Stderr, "For the CLI, run stage auth login and pass --human. For agents, use the private agent socket or a configured agent credential.")
		}
		return 1
	}
}

const usage = `stage: stage server and agent CLI

Usage:
  stage serve [--listen ADDR]...      run the server (also: stage with no args); ADDR host:port,
                                      unix:/abs/path.sock or unix (private default socket); repeat or
                                      comma-separate for several (env STAGE_LISTEN, default 127.0.0.1:7380)
  stage serve --multi-user           enable shared access for this server (env STAGE_MULTI_USER=1)
  stage serve --managed               run as the per-user service (what "stage service" registers):
                                      config from service.env only, default data dir, unix required
  stage db copy --from PATH --to URL  copy a stopped server's SQLite stage.db into an empty Postgres
                                      database and verify it (Docker image only; stage db help)
  stage db invalidate-access         clear restored sessions and account links before startup
                                      (set STAGE_DATA_DIR explicitly; see stage db help)
  stage status [--wait DUR]          the server this CLI reaches: version, readiness, harness states
                                      (exit 1 unreachable or API mismatch, 3 --wait timeout)
  stage auth login --email EMAIL     sign in to the selected shared Stage server
  stage auth whoami | logout         show or revoke the saved CLI session for this address
  stage auth agent rotate --out FILE [--target URL]
                                      write a new 30-day agent credential to a private file
  stage auth agent revoke            revoke your agent credential
  stage harness [list]                the harnesses this server knows, its machine profile, and which
                                      ones can run here
  stage harness KIND [status]         KIND's connection setup on the server: state, settings, actions
  stage harness KIND set FIELD=VALUE... [--reset FIELD]...
                                      save connection settings
  stage harness KIND run ACTION [FIELD=VALUE...]
                                      run a setup action offered by KIND's status
  stage harness KIND disconnect       forget KIND's connection credentials (tasks stay)
  stage machine [ls]                  the machines that run harnesses: kind, status, connections
  stage machine pair [--label L]      a one-time code (10 min) and the stage worker join command
                                      that adds a machine; needs STAGE_PUBLIC_URL on the server
  stage machine rename ID LABEL
  stage machine rm ID                 remove a remote machine: revokes its token, keeps its
                                      connections as history (the local machine stays)
  stage machine ping ID               a round trip through the machine's worker channel
  stage worker join LINK [--label L]  join this machine to the server in LINK (from stage machine pair)
                                      as a remote worker; writes the worker config and token (0600)
  stage worker run [--listen ADDR] [--local-harnesses]
                                      run the worker in the foreground: keep a channel to the server
                                      open, reconnecting, and serve the agent gateway (local /api and
                                      /mcp forwarded to the server) on --listen (repeatable, env
                                      STAGE_WORKER_LISTEN, default and required: unix);
                                      --local-harnesses (env STAGE_LOCAL_HARNESSES=1) lets harnesses
                                      that run as local processes run here, as the
                                      worker service always does (exit 75 worker dir or gateway socket
                                      in use, 78 not joined, invalid config or machine removed)
  stage worker status                 the worker config, the local worker process and what the server
                                      reports (exit 0 online, 1 not online, 4 not joined, 7 revoked)
  stage worker leave [--local]        remove this machine on the server, then delete the worker config
                                      (--local: only delete it here)
                                      worker commands take --config PATH (env STAGE_WORKER_CONFIG;
                                      default worker/worker.json in the data dir) and --json
  stage worker service start|restart|stop|status|logs|enable|disable|uninstall
                                      the per-user worker service (stage worker run --managed), same
                                      flags and exit codes as stage service; a machine runs the server
                                      service or the worker service under one identity, not both
  stage version [--json]              this binary: version, identity, commit, API version, schema, engines,
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
  stage service status                service, definition, data dir holder, Stage health, harnesses, problems
  stage service logs [-f] [-n 100]
  stage service enable|disable        login startup on/off (does not start or stop it)
                                      service commands take --json; exit 0 ok, 1 failed, 3 wait
                                      expired, 4 not running, 5 data dir in use, 6 no service
                                      manager, 7 invalid config; --url/STAGE_URL do not apply
  stage context                       your session, cwd and resolved project
  stage show REF                      the task, tree, schedule, project, journal entry or session
                                      a stage link, harness session link or #TASK_ID names
  stage projects ls
  stage projects add --title T
  stage projects edit ID [--title T] [--archived true|false]
  stage models ls --connection ID [--workspace-id ID]
  stage action ls TARGET
  stage action run ACTION_ID TARGET [--input JSON|-] [--confirm]
                                      TARGET is --connection ID, --task ID, or --project ID --on-connection ID --workspace-id ID
  stage task ls [--project P] [--state S,..] [-q TEXT] [--closed true|all] [--limit N]
  stage task show ID
  stage task add --title T --prompt TEXT|- [config] [--needs ID,..] [--after-me] [--tree ID]
                   [--start-mode auto|manual|inherit]
                   [--start-at YYYY-MM-DDTHH:mm --tz ZONE] [--start] [--handoff=false]
  stage task edit ID [config] [--start-at TIME --tz ZONE | --start-at ""] [--handoff=true|false]
                   [--start-mode auto|manual|inherit]
  stage task needs ID [NEED...]       replace a backlog task's needs (none clears them)
  stage task rm|start|stop|dup ID     start requires completed prerequisites;
                                      rm only for a task that never started
  stage task archive ID [--undo]      archive a standalone task (--undo unarchives)
  stage task wait ID [--until S,..] [--timeout 10m]      exit 3 on timeout
  stage task preview ID               completion preview: revision, evidence version, dependents
  stage task complete ID --revision N --evidence-version V [--note TEXT] [--request-id KEY]
  stage task reopen ID --revision N [--request-id KEY]
  stage task requests ID              what the task's session waits on
  stage task respond ID RID [--option O] [--text T] [--answers JSON]
  stage task send ID --text TEXT      message the task's session
  stage task opening-prompt ID [--full]
                                      complete, reopen, respond and send: never your own task or session
  stage tree ls [--status active|done|archived|all] [--all] [--project P]
  stage tree show ID                  levels, states, needs, readiness
  stage tree new --title T [--start-mode auto|manual] ID[:NEED,..]...
  stage tree add TREE_ID TASK_ID [--needs ID,..]   a task in another tree moves
  stage tree rm TASK_ID               take a task out of its tree (dependents take its needs)
  stage tree edit ID [--title T] [--start-mode auto|manual]
  stage tree start ID                 start every ready member
  stage tree done|archive ID [--undo] mark done / archive (--undo reopens)
  stage tree rm-tree ID               delete a tree and its tasks (none started)
  stage tree dissolve ID              detach every task (needs go) and archive the tree
  stage schedule ls [--project P]
  stage schedule show ID
  stage schedule add --title T --prompt TEXT --cron CRON --tz ZONE [config] [--paused]
  stage schedule edit ID [config] [--cron CRON] [--tz ZONE]
  stage schedule rm|run|pause|resume ID
  stage schedule preview --cron CRON --tz ZONE
  stage sessions adopt [--target-connection ID --target-session ID] [--title T] [--initial-note TEXT]
                       [--tree ID]   track a session as a task (default: your session)
  stage tools [NAME]                  the agent tools (MCP tools); NAME: its description and input schema
  stage call TOOL [JSON|-]            run one agent tool with JSON arguments (- reads stdin); prints
                                      its result as JSON, exactly as over MCP (exit 1 tool error)
  stage mcp                           the agent tools over stdio MCP
` + setupUsage + `  stage guide                         agent workflow guide

Config: --connection ID --workspace-id ID from projects ls (optional --project P)
        --workspace-id "" clears the workspace on a backlog task.
        Planning backlog: --connection ID --project P
        --execution-config JSON (models ls shows the choices,
        including launch options such as a worktree, its base ref or branch)
        --prompt-file PATH --image PATH...
Notes: task add and sessions adopt accept --initial-note TEXT; schedules accept --notes TEXT.
Trees: joining a tree without --needs makes a parallel root; --needs ID runs it after
       that task (joining its tree); tasks that need the same task run in parallel.
Start mode: tasks inherit their tree by default (auto: roots wait for a start, other
            members launch once their needs are completed). Set shared policy on the tree;
            task --start-mode auto|manual overrides it; inherit clears the override.
  stage task|tree notes ID [list] [--kind notes|events|all] [--category C] [--q TEXT]
                          [--task ID (tree only)] [--cursor CURSOR] [--limit N]
  stage task|tree notes ID add --text TEXT [--category C] [--request-id KEY] [--source-task ID]
  stage task|tree notes ID get ENTRY_ID
Global: --url URL (env STAGE_URL; unix:///abs/path.sock for a socket; default: the private default
        socket when a server owns it, else http://127.0.0.1:7380) --json
Shared: --human uses the saved CLI session for the selected address.
        Agents can set STAGE_AGENT_URL and, for network access, STAGE_AGENT_CREDENTIAL_FILE.
Caller: context, creation, notes and adoption accept --caller-connection ID --caller-session ID,
        else read STAGE_CALLER_SESSION=<connectionId>:<externalSessionId>, else the harness's session
        variable (stage mcp too) on its connection on this machine. Harness MCP metadata and the
        Stage-Caller-Session header on HTTP MCP name the caller per call.
References/adoption: --target-connection ID --target-session ID names a scoped session.
Creator filter: task ls --origin-connection ID --origin-session ID.
`

// cmd holds a command's parsed global flags.
type cmd struct {
	env   Env
	fs    *flag.FlagSet
	url   string
	json  bool
	human bool
	pos   []string
}

func newCmd(env Env, name string) *cmd {
	c := &cmd{env: env, fs: flag.NewFlagSet(name, flag.ContinueOnError)}
	c.fs.SetOutput(io.Discard)
	c.fs.StringVar(&c.url, "url", "", "server URL")
	c.fs.BoolVar(&c.json, "json", false, "JSON output")
	group, _, _ := strings.Cut(name, " ")
	switch group {
	case "auth", "setup", "mcp", "call", "tools", "context":
	default:
		c.fs.BoolVar(&c.human, "human", false, "use the saved human CLI session for this address")
	}
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
	return &agenttools.Ops{Client: c.apiClient(), Caller: c.env.Caller, HarnessEnv: c.env.Getenv, LocalFiles: true, CLI: c.env.cliName()}
}

// out prints v as JSON (--json) or with human.
func (c *cmd) out(v any, human func(io.Writer)) error {
	if c.json {
		enc := json.NewEncoder(c.env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	human(c.env.stdout())
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
		fmt.Fprintln(env.Stdout, agenttools.Guide(env.cliName()))
		return nil
	case "context":
		return runContext(ctx, env, sub)
	case "status":
		return runStatus(ctx, env, sub)
	case "auth":
		return runAuth(ctx, env, sub)
	case "harness":
		return runHarness(ctx, env, sub)
	case "machine", "machines":
		return runMachine(ctx, env, sub)
	case "worker":
		return runWorker(ctx, env, sub)
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
	case "action":
		return runAction(ctx, env, sub)
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
	case "tools":
		return runTools(ctx, env, sub)
	case "call":
		return runCall(ctx, env, sub)
	case "setup":
		return runSetup(ctx, env, sub)
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

func runModels(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "ls")
	if v != "ls" {
		return usagef("models %s: unknown", v)
	}
	c := newCmd(env, "models ls")
	var a agenttools.ModelsArgs
	var workspace string
	retireDestinationFlag(c, "project")
	c.fs.Int64Var(&a.ConnectionID, "connection", 0, "show this connection’s capabilities")
	c.fs.StringVar(&workspace, "workspace-id", "", "exact workspace ID from projects ls")
	retireDestinationFlag(c, "binding")
	if err := c.parse(rest); err != nil {
		return err
	}
	a.WorkspaceID = strFlag(c, "workspace-id", workspace)
	res, err := c.ops().ListModels(ctx, a)
	if err != nil {
		return err
	}
	return c.out(res, func(w io.Writer) {
		if res.Capabilities != nil {
			printCapabilities(w, *res.Capabilities)
		}
	})
}

func runMCP(ctx context.Context, env Env, args []string) error {
	c := newCmd(env, "mcp")
	if err := c.parse(args); err != nil {
		return err
	}
	// A stdio MCP process may serve several sessions: callers come per call
	// (harness metadata or callerSession), never from STAGE_CALLER_SESSION.
	// A harness session variable (T3CODE_THREAD_ID) in its environment, set
	// by the harness for the agent that spawned it, is the fallback.
	ops := c.ops()
	ops.Caller = agentctx.Caller{}
	ops.Client = ops.Client.AsToolTransport()
	return agenttools.NewServer(ops).Run(ctx, &mcp.StdioTransport{})
}
