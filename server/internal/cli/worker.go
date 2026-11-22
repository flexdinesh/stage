package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/logfile"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/service"
	"github.com/flexdinesh/stage/server/internal/worker"
	"github.com/flexdinesh/stage/server/internal/workerchan"
	"github.com/flexdinesh/stage/server/internal/workersetup"
)

// runWorker is `stage worker join|run|status|leave|service`: this machine
// as a remote worker of a Stage server (docs/runtime.md "Worker process").
// Worker commands act on the local worker config, never on --url/STAGE_URL.
func runWorker(ctx context.Context, env Env, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		return usagef("worker: want join|run|status|leave|service")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "service":
		return runRoleService(ctx, env, rest, service.RoleWorker)
	case "join", "run", "status", "leave":
	default:
		return usagef("worker %s: unknown (want join, run, status, leave or service)", verb)
	}
	fs := flag.NewFlagSet("worker "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgFlag := fs.String("config", "", "worker config file (env "+config.WorkerConfigEnv+"; default worker/worker.json in the data dir)")
	asJSON := fs.Bool("json", false, "JSON output")
	var label string
	var managed, local bool
	var listenFlags listFlag
	switch verb {
	case "join":
		fs.StringVar(&label, "label", "", "name this machine suggests (default: its host name)")
	case "run":
		fs.BoolVar(&managed, "managed", false, "run as the worker service (`stage worker service`): config path and gateway listeners from worker.env only")
		fs.Var(&listenFlags, "listen", "agent gateway listen address (repeatable or a comma list; env "+config.WorkerListenEnv+"; default and required: unix)")
	case "leave":
		fs.BoolVar(&local, "local", false, "only delete the local worker config (the server is unreachable or already removed the machine)")
	}
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(env.Stdout, usage)
			return err
		}
		return usagef("worker %s: %v", verb, err)
	}
	pos := fs.Args()
	if verb == "join" {
		if len(pos) != 1 {
			return usagef("worker join: want exactly one pairing link")
		}
	} else if len(pos) > 0 {
		return usagef("worker %s: unexpected argument %q", verb, pos[0])
	}
	if verb == "run" {
		if managed && *cfgFlag != "" {
			return usagef("worker run: --managed takes the worker config from worker.env (%s); drop --config", config.WorkerConfigEnv)
		}
		if managed && len(listenFlags) > 0 {
			return usagef("worker run: --managed takes the gateway listeners from worker.env (%s); drop --listen", config.WorkerListenEnv)
		}
		return runWorkerRun(ctx, env, *cfgFlag, managed, strings.Join(listenFlags, ","))
	}
	path, err := worker.ResolveConfigPath(*cfgFlag, env.Getenv)
	if err != nil {
		return err
	}
	out := func(v any, human func(io.Writer)) error {
		if *asJSON {
			return writeJSON(env.Stdout, v)
		}
		human(env.Stdout)
		return nil
	}
	switch verb {
	case "join":
		c, err := worker.Join(ctx, worker.JoinOptions{Link: pos[0], ConfigPath: path, Label: label})
		if err != nil {
			return err
		}
		return out(map[string]any{"machineId": c.MachineID, "label": c.Label, "url": c.URL, "config": path}, func(w io.Writer) {
			fmt.Fprintf(w, "joined %s as machine %d (%s); config %s\n", c.URL, c.MachineID, c.Label, path)
			fmt.Fprintln(w, "next: `stage worker service start` (runs it in the background, restarted by the OS), or `stage worker run` in the foreground")
		})
	case "status":
		st, code := worker.ReadStatus(ctx, path, nil)
		if *asJSON {
			if err := writeJSON(env.Stdout, st); err != nil {
				return err
			}
		} else {
			printWorkerStatus(env.Stdout, st)
		}
		return codeErr(code)
	default: // leave
		res, err := worker.Leave(ctx, worker.LeaveOptions{ConfigPath: path, Local: local})
		if err != nil {
			return err
		}
		svc := workerServiceInstalled(ctx, env)
		return out(map[string]any{"removed": res.Removed, "alreadyRemoved": res.AlreadyRemoved, "running": res.Running, "serviceInstalled": svc, "config": path}, func(w io.Writer) {
			switch {
			case res.Removed:
				fmt.Fprintf(w, "left %s: machine %d removed there; deleted %s\n", res.Config.URL, res.Config.MachineID, path)
			case res.AlreadyRemoved:
				fmt.Fprintf(w, "the server had already removed machine %d; deleted %s\n", res.Config.MachineID, path)
			default:
				fmt.Fprintf(w, "deleted %s (the server still lists the machine: remove it in Settings > Machines or `stage machine rm`)\n", path)
			}
			if res.Running {
				fmt.Fprintln(w, "note: a worker process still runs here; it stops at its next request (its token is revoked)")
			}
			if svc {
				fmt.Fprintln(w, "note: the worker service is still installed: `stage worker service uninstall`")
			}
		})
	}
}

// workerServiceInstalled reports whether the worker service's definition
// exists (best effort; false when the manager is unavailable).
func workerServiceInstalled(ctx context.Context, env Env) bool {
	st, _ := service.New(env.serviceDeps(service.RoleWorker)).Status(ctx)
	return st.Definition.Present
}

// workerHost is what `stage worker run` serves: this machine's connection
// setup (T3 and OpenCode, composed once the main server is reachable), the
// adapters it installs, and its fs and process work (machine.LocalOps),
// over the worker channel. harnessEnv is the machine's harness settings
// (config.HarnessEnvKeys); credentials stay in the worker dir of
// configPath.
func workerHost(managed bool, getenv, harnessEnv func(string) string, configPath string, logger *slog.Logger) func(int64, workerchan.Upcall) worker.Host {
	return func(machineID int64, call workerchan.Upcall) worker.Host {
		h := remote.NewHost(machine.NewLocal(machineID, harness.ResolveProfile(managed, false, getenv), machine.LocalOps{}))
		workersetup.Attach(workersetup.Options{
			Host:   h,
			Ports:  workersetup.Ports{Call: call, Creds: worker.NewCredentialStore(configPath)},
			Config: config.HarnessFrom(harnessEnv),
			Logger: logger,
		})
		return h
	}
}

// runWorkerRun is `stage worker run [--managed] [--listen ADDR]`. Exit: 0
// on SIGINT or SIGTERM, 75 when another worker holds the worker dir or
// another process holds a gateway socket, 78 when not joined, the config
// is invalid or the machine was removed, 1 otherwise. A managed
// worker on macOS exits 0 instead of 75 and 78 (launchd cannot exclude exit
// codes from KeepAlive).
func runWorkerRun(ctx context.Context, env Env, cfgFlag string, managed bool, listenFlag string) error {
	goos := runtime.GOOS
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))
	opts := worker.RunOptions{Logger: logger, Managed: managed, Getenv: env.Getenv}
	var err error
	listenList := listenFlag
	// A foreground worker takes its harness settings from its environment;
	// the worker service only from worker.env.
	harnessEnv := env.Getenv
	if managed {
		opts.Manager, opts.Service = service.ManagerKind(goos), service.NameFor(goos, service.RoleWorker)
		var vals map[string]string
		opts.ConfigPath, vals, err = managedWorkerConfig(env.Getenv, goos)
		listenList = vals[config.WorkerListenEnv]
		harnessEnv = func(k string) string {
			if k == "STAGE_ENV" {
				return env.Getenv(k)
			}
			return vals[k]
		}
		// The gateway socket is the identity default data dir's, like the
		// managed server's: STAGE_DATA_DIR in the inherited environment is
		// ignored.
		opts.Getenv = func(k string) string {
			if k == "STAGE_DATA_DIR" {
				return ""
			}
			return env.Getenv(k)
		}
		if err == nil && goos == "darwin" {
			if l := managedWorkerLog(env.Getenv, env.Stderr); l != nil {
				opts.Logger, logger = l, l
			}
		}
	} else {
		opts.ConfigPath, err = worker.ResolveConfigPath(cfgFlag, env.Getenv)
		if listenList == "" {
			listenList = env.Getenv(config.WorkerListenEnv)
		}
	}
	if err == nil {
		if opts.Listen, err = config.ParseWorkerListen(listenList); err != nil {
			err = config.Invalid(err)
		}
	}
	if err == nil {
		opts.Host = workerHost(managed, env.Getenv, harnessEnv, opts.ConfigPath, opts.Logger)
		err = worker.Run(ctx, opts)
	}
	if err == nil {
		return nil
	}
	var le *worker.LockedError
	var ie *config.InvalidError
	var ue *listen.InUseError
	code := 1
	switch {
	case errors.As(err, &le), errors.As(err, &ue):
		code = service.ExitConflict
	case errors.As(err, &ie):
		code = service.ExitConfig
	}
	logger.Error("worker", "err", err, "exit", code)
	if managed && goos == "darwin" && code != 1 {
		code = 0
	}
	return exitError{code}
}

// managedWorkerConfig is the worker service's config path and worker.env
// (gateway listen list, harness settings; the shell environment is never
// used).
func managedWorkerConfig(getenv func(string) string, goos string) (string, map[string]string, error) {
	envPath, err := config.WorkerEnvPath(getenv, goos)
	if err != nil {
		return "", nil, config.Invalid(err)
	}
	vals, err := config.ReadWorkerEnv(envPath)
	if err != nil {
		return "", nil, config.Invalid(err)
	}
	p, err := worker.ManagedConfigPath(getenv, goos, vals)
	if err != nil {
		return "", nil, config.Invalid(err)
	}
	return p, vals, nil
}

// managedWorkerLog (macOS) logs to ~/Library/Logs/<id>/worker.log, rotated,
// and trims launchd's stderr file past 1 MiB. nil: keep stderr.
func managedWorkerLog(getenv func(string) string, stderr io.Writer) *slog.Logger {
	dir := config.ServiceLogDir(getenv, "darwin")
	if dir == "" {
		return nil
	}
	if f, ok := stderr.(*os.File); ok {
		if st, err := f.Stat(); err == nil && st.Mode().IsRegular() && st.Size() > 1<<20 {
			if f.Truncate(0) == nil {
				_, _ = f.Seek(0, 0)
			}
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	r, err := logfile.Open(filepath.Join(dir, service.LogFileName(service.RoleWorker)), logfile.DefaultMaxBytes, logfile.DefaultKeep)
	if err != nil {
		return nil
	}
	return slog.New(slog.NewTextHandler(r, nil))
}

func printWorkerStatus(w io.Writer, st worker.Status) {
	c := st.Config
	if !c.Joined {
		fmt.Fprintf(w, "not joined (%s)\n", c.Path)
		if c.Error != "" {
			fmt.Fprintf(w, "  error       %s\n", c.Error)
		} else {
			fmt.Fprintln(w, "  join with   stage worker join <link> (Settings > Machines > Add, or `stage machine pair`)")
		}
	} else {
		fmt.Fprintf(w, "machine %d (%s) of %s\n", c.MachineID, c.Label, c.URL)
		fmt.Fprintf(w, "  config      %s\n", c.Path)
	}
	if s := st.Server; s != nil {
		switch {
		case s.Status != "":
			fmt.Fprintf(w, "  server says %s", s.Status)
			if s.LastSeenAt != nil {
				fmt.Fprintf(w, " (last seen %s)", s.LastSeenAt.Local().Format(time.DateTime))
			}
			fmt.Fprintln(w)
		default:
			fmt.Fprintf(w, "  server      unreachable: %s\n", s.Error)
		}
	}
	if p := st.Process; p != nil {
		mode := "foreground"
		if p.Managed {
			mode = "worker service"
		}
		fmt.Fprintf(w, "  process     pid %d (%s, %s), channel %s\n", p.PID, mode, p.Version, orNone(p.State))
		if len(p.Gateway) > 0 {
			fmt.Fprintf(w, "  gateway     %s\n", strings.Join(p.Gateway, ", "))
		}
		if p.LastError != "" {
			fmt.Fprintf(w, "  last error  %s\n", p.LastError)
		}
	} else if c.Joined {
		fmt.Fprintln(w, "  process     not running (`stage worker service start` or `stage worker run`)")
	}
}
