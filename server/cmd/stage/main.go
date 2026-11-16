package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	_ "time/tzdata" // IANA zones for /api/stats tz even without system zoneinfo

	"github.com/flexdinesh/stage/server/internal/cli"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/serve"
)

func main() {
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
	if err := run(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// listenFlags collects repeated --listen values.
type listenFlags []string

func (l *listenFlags) String() string     { return strings.Join(*l, ",") }
func (l *listenFlags) Set(v string) error { *l = append(*l, v); return nil }

// run is `stage` or `stage serve [--listen ADDR]...`.
func run(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var listens listenFlags
	fs.Var(&listens, "listen", "listen address, repeatable or comma-separated: host:port, unix:/abs/path.sock, or unix (the private default socket); overrides STAGE_LISTEN; default "+listen.Default)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("serve: unexpected argument %q", fs.Arg(0))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load(listens)
	if err != nil {
		return err
	}
	return serve.Run(ctx, serve.Options{Config: cfg, Logger: slog.Default()})
}
