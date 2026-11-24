package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	_ "time/tzdata" // IANA zones for /api/stats tz even without system zoneinfo

	"github.com/flexdinesh/stage/server/internal/cli"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/logfile"
	"github.com/flexdinesh/stage/server/internal/serve"
	"github.com/flexdinesh/stage/server/internal/service"
)

func main() {
	// `db`: offline database commands (stage db copy).
	if len(os.Args) > 1 && os.Args[1] == "db" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := runDB(ctx, os.Args[2:], os.Stdout, os.Stderr)
		stop()
		os.Exit(code)
	}
	// No args (or `serve`): the server. Anything else: the agent CLI.
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := cli.Run(ctx, os.Args[1:], cli.OSEnv())
		stop()
		os.Exit(code)
	}
	var args []string
	if len(os.Args) > 1 {
		args = os.Args[2:]
	}
	managed, err := run(args)
	code := exitCode(err)
	if code != 0 {
		slog.Error("fatal", "err", err, "exit", code)
	}
	// launchd cannot exclude exit codes from KeepAlive: a managed server
	// that cannot succeed by restarting exits 0 there, so it is not respawned
	// every ThrottleInterval (the log line above says why).
	if managed && runtime.GOOS == "darwin" && (code == service.ExitConflict || code == service.ExitConfig) {
		code = 0
	}
	os.Exit(code)
}

// errUsage is a bad command line (exit 2).
type errUsage struct{ msg string }

func (e errUsage) Error() string { return e.msg }

// exitCode maps a serve error: data dir or database held by another
// server 75, invalid configuration 78 (neither is retried by systemd),
// usage 2.
func exitCode(err error) int {
	var le *instance.LockedError
	var oe *store.OwnedError
	var ie *config.InvalidError
	var ue errUsage
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &ue):
		return 2
	case errors.As(err, &le), errors.As(err, &oe):
		return service.ExitConflict
	case errors.As(err, &ie):
		return service.ExitConfig
	}
	return 1
}

// listenFlags collects repeated --listen values.
type listenFlags []string

func (l *listenFlags) String() string     { return strings.Join(*l, ",") }
func (l *listenFlags) Set(v string) error { *l = append(*l, v); return nil }

// run is `stage` or `stage serve [--listen ADDR]... | --managed`.
func run(args []string) (bool, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var listens listenFlags
	fs.Var(&listens, "listen", "listen address, repeatable or comma-separated: host:port, unix:/abs/path.sock, or unix (the private default socket); overrides STAGE_LISTEN; default "+listen.Default)
	managed := fs.Bool("managed", false, "run as the per-user service (`stage service`): config from service.env only, the default data dir, unix listener required")
	localHarnesses := fs.Bool("local-harnesses", false, "let this foreground server run harnesses that are local processes (the managed service always may; a container never); also "+harness.LocalHarnessesEnv+"=1")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, err
		}
		return false, errUsage{err.Error()}
	}
	if fs.NArg() > 0 {
		return *managed, errUsage{fmt.Sprintf("serve: unexpected argument %q", fs.Arg(0))}
	}
	if *managed && len(listens) > 0 {
		return true, errUsage{"serve: --managed takes its listeners from service.env (STAGE_LISTEN); drop --listen"}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts := serve.Options{Logger: slog.Default(), Profile: harness.ResolveProfile(*managed, *localHarnesses, os.Getenv)}
	var err error
	if *managed {
		opts.Config, err = loadManaged()
		opts.Service = &serve.Service{Manager: service.ManagerKind(runtime.GOOS), Name: service.Name(runtime.GOOS)}
		if runtime.GOOS == "darwin" {
			opts.OnLocked = managedLog
		}
	} else {
		opts.Config, err = config.Load(listens)
	}
	if err != nil {
		return *managed, config.Invalid(err)
	}
	return *managed, serve.Run(ctx, opts)
}

// loadManaged is the managed server's config: service.env over the
// identity's defaults (docs/service.md).
func loadManaged() (config.Config, error) {
	path, err := config.ServiceEnvPath(os.Getenv, runtime.GOOS)
	if err != nil {
		return config.Config{}, err
	}
	vals, err := config.ReadServiceEnv(path)
	if err != nil {
		return config.Config{}, err
	}
	return config.LoadManaged(os.Getenv, vals)
}

// managedLog (macOS) switches logging to the rotated ~/Library/Logs file
// once the data dir lock is held, and trims launchd's stderr file (panics
// only) when it grew past 1 MiB. Failing that, logs stay on stderr.
func managedLog() (*slog.Logger, error) {
	dir := config.ServiceLogDir(os.Getenv, runtime.GOOS)
	if dir == "" {
		return nil, nil
	}
	if st, err := os.Stderr.Stat(); err == nil && st.Mode().IsRegular() && st.Size() > 1<<20 {
		if os.Stderr.Truncate(0) == nil {
			_, _ = os.Stderr.Seek(0, 0)
		}
	}
	r, err := logfile.Open(filepath.Join(dir, "stage.log"), logfile.DefaultMaxBytes, logfile.DefaultKeep)
	if err != nil {
		slog.Warn("log file; logging to stderr", "err", err)
		return nil, nil
	}
	l := slog.New(slog.NewTextHandler(r, nil))
	slog.SetDefault(l)
	return l, nil
}
