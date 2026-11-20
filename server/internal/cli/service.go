package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/service"
)

// exitError ends a command with code after it printed its own output.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// runVersion is `stage version [--json]`.
func runVersion(env Env, args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return usagef("version: %v", err)
	}
	if fs.NArg() > 0 {
		return usagef("version: unexpected argument %q", fs.Arg(0))
	}
	bi := service.ThisBuild()
	v := versionInfo{BuildInfo: bi}
	if rec := installInfo(env); rec != nil {
		v.Install = &installSummary{Mode: rec.Mode, Version: rec.Version, Channel: rec.Channel, App: rec.App, CLI: rec.CLI, Link: rec.Link}
	}
	if *asJSON {
		return writeJSON(env.Stdout, v)
	}
	fmt.Fprintf(env.Stdout, "stage %s (identity %s, commit %s, api v%d, schema %d)\n", bi.Version, bi.Identity, orNone(bi.Commit), bi.APIVersion, bi.Schema)
	if v.Install != nil {
		fmt.Fprintf(env.Stdout, "installed %s mode at %s (%s)\n", v.Install.Mode, v.Install.App, v.Install.Link)
	}
	return nil
}

// versionInfo is `stage version --json`: the build, plus the installation
// this binary belongs to when the installer installed it.
type versionInfo struct {
	service.BuildInfo
	Install *installSummary `json:"install,omitempty"`
}

type installSummary struct {
	Mode    string `json:"mode"`
	Version string `json:"version"`
	Channel string `json:"channel"`
	App     string `json:"app"`
	CLI     string `json:"cli"`
	Link    string `json:"link"`
}

func orNone(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (e Env) serviceDeps() service.Deps {
	if e.Service != nil {
		return e.Service()
	}
	return service.OSDeps()
}

// runService is `stage service VERB`: the per-user OS service of this
// installation (docs/service.md). It always targets the local identity:
// --url and STAGE_URL do not apply.
func runService(ctx context.Context, env Env, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usagef("service: want start|stop|restart|status|logs|enable|disable|uninstall")
	}
	verb, rest := args[0], args[1:]
	fs := flag.NewFlagSet("service "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "JSON output")
	var exe string
	var wait time.Duration
	var n int
	var follow bool
	switch verb {
	case "start", "restart":
		def := service.DefaultStartWait
		if verb == "restart" {
			def = service.DefaultRestartWait
		}
		fs.StringVar(&exe, "exe", "", "executable the service runs (default this CLI)")
		fs.DurationVar(&wait, "wait", def, "wait this long for readiness (0: return once requested)")
	case "stop", "uninstall":
		fs.DurationVar(&wait, "wait", service.DefaultStopWait, "wait this long for the server to exit")
	case "enable":
		fs.StringVar(&exe, "exe", "", "executable the service runs (default this CLI)")
	case "logs":
		fs.IntVar(&n, "n", 100, "number of recent lines")
		fs.BoolVar(&follow, "f", false, "follow")
	case "status", "disable":
	default:
		return usagef("service %s: unknown (want start|stop|restart|status|logs|enable|disable|uninstall)", verb)
	}
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(env.Stdout, usage)
			return err
		}
		return usagef("service %s: %v (service commands target this installation; --url/STAGE_URL do not apply)", verb, err)
	}
	if fs.NArg() > 0 {
		return usagef("service %s: unexpected argument %q", verb, fs.Arg(0))
	}
	c := service.New(env.serviceDeps())
	switch verb {
	case "status":
		st, code := c.Status(ctx)
		if *asJSON {
			if err := writeJSON(env.Stdout, st); err != nil {
				return err
			}
		} else {
			printServiceStatus(env.Stdout, st)
		}
		return codeErr(code)
	case "logs":
		if *asJSON {
			return usagef("service logs: --json is not supported")
		}
		if err := c.Logs(ctx, env.Stdout, n, follow); err != nil {
			return err
		}
		return nil
	}
	var rep service.Report
	switch verb {
	case "start":
		rep = c.Start(ctx, service.StartOptions{Exe: exe, Wait: wait})
	case "restart":
		rep = c.Restart(ctx, service.StartOptions{Exe: exe, Wait: wait})
	case "stop":
		rep = c.Stop(ctx, wait)
	case "enable":
		rep = c.Enable(ctx, exe)
	case "disable":
		rep = c.Disable(ctx)
	case "uninstall":
		rep = c.Uninstall(ctx, wait)
	}
	if *asJSON {
		if err := writeJSON(env.Stdout, rep); err != nil {
			return err
		}
	} else {
		printReport(env.Stdout, env.Stderr, rep)
	}
	return codeErr(rep.Code)
}

func codeErr(code int) error {
	if code == 0 {
		return nil
	}
	return exitError{code}
}

func printReport(out, errw io.Writer, r service.Report) {
	for _, w := range r.Warnings {
		fmt.Fprintf(errw, "warning: %s\n", w)
	}
	if r.OK {
		fmt.Fprintln(out, r.Message)
	} else {
		fmt.Fprintf(errw, "error: %s\n", r.Message)
		if r.Fix != "" {
			fmt.Fprintf(errw, "fix: %s\n", r.Fix)
		}
		if len(r.LogTail) > 0 {
			fmt.Fprintln(errw, "recent log lines:")
			for _, l := range r.LogTail {
				fmt.Fprintf(errw, "  %s\n", l)
			}
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintf(out, "note: %s\n", n)
	}
}

func printServiceStatus(w io.Writer, s service.Status) {
	fmt.Fprintf(w, "%s service (%s %s): %s", s.Identity, s.Manager.Kind, s.Definition.Name, s.State)
	if s.Ready {
		fmt.Fprint(w, ", ready")
	}
	fmt.Fprintln(w)
	if s.Process.PID != 0 {
		fmt.Fprintf(w, "  pid         %d (restarts %d)\n", s.Process.PID, s.Process.Restarts)
	}
	if s.Stage.Reachable {
		fmt.Fprintf(w, "  stage       %s at %s (db %s)\n", s.Stage.Version, s.Stage.URL, s.Stage.DB)
	} else {
		fmt.Fprintf(w, "  stage       not reachable at %s\n", s.Stage.URL)
	}
	if s.Harnesses == nil {
		fmt.Fprintln(w, "  harnesses   unknown (Stage not reachable)")
	}
	for _, h := range s.Harnesses {
		switch {
		case h.Connected:
			fmt.Fprintf(w, "  %-11s connected\n", h.Kind)
		case !h.Configured:
			fmt.Fprintf(w, "  %-11s not set up\n", h.Kind)
		case h.LastError != nil:
			fmt.Fprintf(w, "  %-11s offline: %s\n", h.Kind, *h.LastError)
		default:
			fmt.Fprintf(w, "  %-11s offline\n", h.Kind)
		}
	}
	login := "off"
	if s.Definition.LoginStartup {
		login = "on"
	}
	fmt.Fprintf(w, "  login start %s\n", login)
	if s.Manager.Linger != nil {
		fmt.Fprintf(w, "  linger      %t\n", *s.Manager.Linger)
	}
	if s.Definition.Present {
		fmt.Fprintf(w, "  definition  %s\n", s.Definition.Path)
		fmt.Fprintf(w, "  exe         %s\n", s.Definition.Exe)
	}
	fmt.Fprintf(w, "  data dir    %s\n", s.DataDir)
	fmt.Fprintf(w, "  config      %s\n", s.ServiceEnv.Path)
	if s.Logs.Kind == "journald" {
		fmt.Fprintf(w, "  logs        journalctl --user -u %s (stage service logs)\n", s.Logs.Unit)
	} else {
		fmt.Fprintf(w, "  logs        %s (stage service logs)\n", s.Logs.Path)
	}
	if h := s.Lock.Holder; h != nil && !s.Lock.OwnedByService {
		fmt.Fprintf(w, "  data dir held by pid %d (managed %t)\n", h.PID, h.Managed)
	}
	for _, p := range s.Problems {
		fmt.Fprintf(w, "problem: %s\n", p.Message)
		if p.Fix != "" {
			fmt.Fprintf(w, "  fix: %s\n", p.Fix)
		}
	}
}
