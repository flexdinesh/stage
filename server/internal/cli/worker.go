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
	"github.com/flexdinesh/stage/server/internal/harnesses"
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
	var managed, local, localHarnesses bool
	var listenFlags listFlag
	switch verb {
	case "join":
		fs.StringVar(&label, "label", "", "name this machine suggests (default: its host name)")
	case "run":
		fs.BoolVar(&managed, "managed", false, "run as the worker service (`stage worker service`): config path and gateway listeners from worker.env only")
		fs.Var(&listenFlags, "listen", "agent gateway listen address (repeatable or a comma list; env "+config.WorkerListenEnv+"; default and required: unix)")
		fs.BoolVar(&localHarnesses, "local-harnesses", false, "let harnesses that run as local processes run on this machine (the worker service always may; a container never); also "+harness.LocalHarnessesEnv+"=1")
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
		return runWorkerRun(ctx, env, *cfgFlag, managed, localHarnesses, strings.Join(listenFlags, ","))
	}
	path, err := worker.ResolveConfigPath(*cfgFlag, env.Getenv)
	if err != nil {
		return err
	}
	out := func(v any, human func(io.Writer)) error {
		if *asJSON {
			return writeJSON(env.Stdout, v)
		}
		human(env.stdout())
		return nil
	}
	switch verb {
	case "join":
		c, err := worker.Join(ctx, worker.JoinOptions{Link: pos[0], ConfigPath: path, Label: label})
		if err != nil {
			return err
		}
		return out(map[string]any{"machineId": c.MachineID, "label": c.Label, "url": c.URL, "config": path}, func(w io.Writer) {
			cli := env.cliName()
			success(w, "Joined %s as machine %d (%s)", c.URL, c.MachineID, c.Label)
			kv(w, "config", "%s", path)
			nextSteps(w,
				step{cli + " worker service start", "run in the background, restarted by the OS"},
				step{cli + " worker run", "run in the foreground"})
		})
	case "status":
		st, code := worker.ReadStatus(ctx, path, nil)
		if *asJSON {
			if err := writeJSON(env.Stdout, st); err != nil {
				return err
			}
		} else {
			printWorkerStatus(env.stdout(), st, env.cliName())
		}
		return codeErr(code)
	default: // leave
		res, err := worker.Leave(ctx, worker.LeaveOptions{ConfigPath: path, Local: local})
		if err != nil {
			return err
		}
		svc := workerServiceInstalled(ctx, env)
		return out(map[string]any{"removed": res.Removed, "alreadyRemoved": res.AlreadyRemoved, "running": res.Running, "serviceInstalled": svc, "config": path}, func(w io.Writer) {
			cli := env.cliName()
			switch {
			case res.Removed:
				success(w, "Left %s: machine %d removed there", res.Config.URL, res.Config.MachineID)
				kv(w, "deleted", "%s", path)
			case res.AlreadyRemoved:
				success(w, "The server had already removed machine %d", res.Config.MachineID)
				kv(w, "deleted", "%s", path)
			default:
				warn(w, "Deleted %s, but the server still lists machine %d", path, res.Config.MachineID)
				fixLine(w, fmt.Sprintf("remove it in Settings > Machines, or %s", cmdText(fmt.Sprintf("%s machine rm %d", cli, res.Config.MachineID))))
			}
			if res.Running {
				warn(w, "A worker process still runs here; it stops at its next request (its token is revoked)")
			}
			if svc {
				warn(w, "The worker service is still installed: %s", cmdText(cli+" worker service uninstall"))
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
// setup and adapters once the main server is reachable, and its fs and
// process work (machine.LocalOps), over the worker channel. harnessEnv is
// the machine's harness environment; credentials stay in the worker dir
// of configPath.
func workerHost(profile harness.Profile, harnessEnv func(string) string, configPath string, logger *slog.Logger) func(int64, string, workerchan.Upcall) (worker.Host, func(context.Context) (bool, error)) {
	return func(machineID int64, identity string, call workerchan.Upcall) (worker.Host, func(context.Context) (bool, error)) {
		h := remote.NewHost(machine.NewLocal(machineID, profile, machine.LocalOps{}))
		setup := workersetup.Attach(workersetup.Options{
			Host:   h,
			Ports:  workersetup.Ports{Call: call, Creds: worker.NewCredentialStore(configPath, harnesses.LegacySecrets()...)},
			Getenv: harnessEnv,
			// The worker dir of configPath (beside its credentials).
			StateDir:      worker.Dir(configPath),
			Logger:        logger,
			CacheIdentity: identity,
		})
		return h, func(ctx context.Context) (bool, error) {
			err := setup.Restore(ctx)
			if errors.Is(err, workersetup.ErrNoCache) {
				return false, nil
			}
			return err == nil, err
		}
	}
}

// runWorkerRun is `stage worker run [--managed] [--listen ADDR]
// [--local-harnesses]`. Exit: 0
// on SIGINT or SIGTERM, 75 when another worker holds the worker dir or
// another process holds a gateway socket, 78 when not joined, the config
// is invalid or the machine was removed, 1 otherwise. A managed
// worker on macOS exits 0 instead of 75 and 78 (launchd cannot exclude exit
// codes from KeepAlive).
func runWorkerRun(ctx context.Context, env Env, cfgFlag string, managed, localHarnesses bool, listenFlag string) error {
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
		// The worker service always hosts local harnesses; a foreground
		// worker only when asked (flag or env); a container never.
		profile := harness.ResolveProfile(managed, localHarnesses, env.Getenv)
		onOff := "off"
		if profile.LocalProcesses {
			onOff = "on"
		}
		opts.Logger.Info("machine profile", "profile", profile.Kind, "localHarnesses", onOff)
		opts.ScopedHost = workerHost(profile, harnessEnv, opts.ConfigPath, opts.Logger)
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
	vals, err := config.ReadWorkerEnv(envPath, harnesses.Env())
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

func printWorkerStatus(w io.Writer, st worker.Status, cli string) {
	c := st.Config
	if !c.Joined {
		t := toneWarn
		if c.Error != "" {
			t = toneBad
		}
		line(w, t, "Not joined (%s)", c.Path)
		if c.Error != "" {
			kv(w, "error", "%s", toneBad.paint(c.Error))
		} else {
			kv(w, "join with", "%s %s", cmdText(cli+" worker join <link>"), dim("(Settings > Machines > Add, or "+cli+" machine pair)"))
		}
	} else {
		line(w, workerTone(st), "Machine %d (%s) of %s", c.MachineID, c.Label, c.URL)
		kv(w, "config", "%s", c.Path)
	}
	if s := st.Server; s != nil {
		switch {
		case s.Status != "":
			seen := ""
			if s.LastSeenAt != nil {
				seen = dim(" (last seen " + s.LastSeenAt.Local().Format(time.DateTime) + ")")
			}
			kv(w, "server says", "%s%s", stateTone(s.Status).paint(s.Status), seen)
		default:
			kv(w, "server", "%s %s", toneBad.paint("unreachable:"), s.Error)
		}
	}
	if p := st.Process; p != nil {
		mode := "foreground"
		if p.Managed {
			mode = "worker service"
		}
		state := orNone(p.State)
		kv(w, "process", "pid %d (%s, %s), channel %s", p.PID, mode, p.Version, stateTone(state).paint(state))
		if len(p.Gateway) > 0 {
			kv(w, "gateway", "%s", strings.Join(p.Gateway, ", "))
		}
		if p.LastError != "" {
			kv(w, "last error", "%s", toneBad.paint(p.LastError))
		}
		if p.Observation != "" {
			kv(w, "observation", "%s", strings.ReplaceAll(p.Observation, "_", " "))
		}
		if p.ObservationLost > 0 {
			kv(w, "observations lost", "%d", p.ObservationLost)
		}
		if p.ObservationError != "" {
			kv(w, "observation error", "%s", toneBad.paint(p.ObservationError))
		}
	} else if c.Joined {
		kv(w, "process", "%s %s", toneWarn.paint("not running"), dim("("+cli+" worker service start, or "+cli+" worker run)"))
	}
}

// workerTone is a joined worker's heading tone: ok when the server reports
// it online, warn while connecting or not running, bad when unreachable,
// offline or revoked.
func workerTone(st worker.Status) tone {
	if s := st.Server; s != nil {
		if s.Status == "" {
			return toneBad
		}
		return stateTone(s.Status)
	}
	if st.Process == nil {
		return toneWarn
	}
	return stateTone(st.Process.State)
}
