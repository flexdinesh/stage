package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"

	"github.com/flexdinesh/stage/server/internal/install"
	"github.com/flexdinesh/stage/server/internal/service"
)

func (e Env) installDeps() install.Deps {
	if e.Install != nil {
		return e.Install()
	}
	d := install.OSDeps(e.Stderr, func() service.Deps { return e.serviceDeps(service.RoleServer) })
	if e.Getenv != nil {
		d.Getenv = e.Getenv
		if h := e.Getenv("HOME"); h != "" {
			d.Home = h
		}
	}
	if e.Home != "" {
		d.Home = e.Home
	}
	return d
}

func (e Env) releases(ctx context.Context, d install.Deps) install.Source {
	if e.Releases != nil {
		return e.Releases
	}
	return install.NewGitHub(install.Token(ctx, d))
}

func installFlags(name string) (*flag.FlagSet, *bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs, fs.Bool("json", false, "JSON output")
}

func parseFlags(env Env, fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(env.Stdout, usage)
			return err
		}
		return usagef("%s: %v", fs.Name(), err)
	}
	if fs.NArg() > 0 {
		return usagef("%s: unexpected argument %q", fs.Name(), fs.Arg(0))
	}
	return nil
}

// runInstall is `stage install --from DIR`: scripts/install.sh,
// scripts/install-dev.sh and `stage update` run it with the new release's
// CLI (docs/install.md).
func runInstall(ctx context.Context, env Env, args []string) error {
	fs, asJSON := installFlags("install")
	var o install.Options
	var headless, desktop bool
	fs.StringVar(&o.From, "from", "", "verified asset directory of the release")
	fs.BoolVar(&headless, "headless", false, "CLI/server only")
	fs.BoolVar(&desktop, "desktop", false, "desktop app with its CLI/server (default)")
	fs.StringVar(&o.CLIName, "cli-name", "", "name of the CLI on PATH (default the identity)")
	if err := parseFlags(env, fs, args); err != nil {
		return err
	}
	switch {
	case headless && desktop:
		return usagef("install: --headless and --desktop are exclusive")
	case headless:
		o.Mode = install.ModeHeadless
	case desktop:
		o.Mode = install.ModeDesktop
	}
	if o.From == "" {
		return usagef("install: --from DIR is required (use scripts/install.sh to install a release)")
	}
	res := install.Install(ctx, env.installDeps(), o)
	return printInstall(env, *asJSON, res)
}

// runUpdate is `stage update`: installs the latest stable release (or
// --version) in this installation's mode through the release's own CLI.
func runUpdate(ctx context.Context, env Env, args []string) error {
	fs, asJSON := installFlags("update")
	var o install.UpdateOptions
	fs.BoolVar(&o.Check, "check", false, "report installed and latest versions only")
	fs.StringVar(&o.Version, "version", "", "install this version (pre-releases and older versions only this way)")
	fs.StringVar(&o.From, "from", "", "install from a local asset directory instead of GitHub")
	if err := parseFlags(env, fs, args); err != nil {
		return err
	}
	d := env.installDeps()
	rec, err := install.Installed(d, env.Executable)
	if err != nil {
		return printInstall(env, *asJSON, installErr(err))
	}
	src := env.releases(ctx, d)
	version, newer, err := install.Target(ctx, src, rec, o)
	if err != nil {
		return printInstall(env, *asJSON, installErr(err))
	}
	if o.Check {
		c := install.Check{Installed: rec.Version, Latest: version, Mode: rec.Mode, UpdateAvailable: newer}
		st := d.Service.Status(ctx)
		if st.Stage.Reachable {
			c.Server = st.Stage.Version
		}
		if *asJSON {
			return writeJSON(env.Stdout, c)
		}
		w := env.stdout()
		if newer {
			warn(w, "Update available: %s", c.Latest)
		} else {
			success(w, "Up to date")
		}
		kv(w, "installed", "%s %s", c.Installed, dim("("+c.Mode+" mode)"))
		kv(w, "latest", "%s", c.Latest)
		if c.Server != "" {
			kv(w, "server", "%s", c.Server)
		}
		if newer {
			nextSteps(w, step{rec.CLIName + " update", "install " + c.Latest})
		}
		return nil
	}
	if !newer && o.Version == "" && o.From == "" {
		return printInstall(env, *asJSON, install.UpToDate(rec, version))
	}
	prep, err := install.Prepare(ctx, d, src, rec, version, o)
	if err != nil {
		return printInstall(env, *asJSON, installErr(err))
	}
	defer prep.Cleanup()
	cmdArgs := []string{"install", "--from", prep.Dir}
	if *asJSON {
		cmdArgs = append(cmdArgs, "--json")
	}
	cmd := exec.CommandContext(ctx, prep.CLI, cmdArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, env.Stdout, env.Stderr
	if err := cmd.Run(); err != nil {
		var xe *exec.ExitError
		if errors.As(err, &xe) {
			return exitError{xe.ExitCode()}
		}
		return printInstall(env, *asJSON, installErr(fmt.Errorf("running the new CLI: %w", err)))
	}
	return nil
}

// runUninstall is `stage uninstall`: removes the installation, keeps data.
func runUninstall(ctx context.Context, env Env, args []string) error {
	fs, asJSON := installFlags("uninstall")
	if err := parseFlags(env, fs, args); err != nil {
		return err
	}
	res := install.Uninstall(ctx, env.installDeps())
	return printInstall(env, *asJSON, res)
}

func installErr(err error) *install.Result {
	return &install.Result{Code: install.CodeOf(err), Message: err.Error(), Fix: install.FixOf(err), Notes: []string{}, Warnings: []string{}}
}

func printInstall(env Env, asJSON bool, r *install.Result) error {
	if asJSON {
		if err := writeJSON(env.Stdout, r); err != nil {
			return err
		}
		return codeErr(r.Code)
	}
	out := env.stdout()
	printOutcome(out, env.stderr(), outcome{ok: r.OK, message: r.Message, fix: r.Fix, logTail: r.LogTail, notes: r.Notes, warnings: r.Warnings})
	if len(r.Retained) > 0 {
		fmt.Fprintf(out, "  %s\n", dim("kept:"))
		for _, p := range r.Retained {
			fmt.Fprintf(out, "    %s\n", p)
		}
	}
	return codeErr(r.Code)
}

// installInfo is the installation this binary belongs to (stage version).
func installInfo(env Env) *install.Record {
	return install.Lookup(env.installDeps(), env.Executable)
}
