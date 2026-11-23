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

func (e Env) serviceDeps(role service.Role) service.Deps {
	d := service.OSDeps()
	if e.Service != nil {
		d = e.Service()
	}
	d.Role = role
	return d
}

// runService is `stage service VERB`: the per-user OS service of this
// installation (docs/service.md). It always targets the local identity:
// --url and STAGE_URL do not apply.
func runService(ctx context.Context, env Env, args []string) error {
	return runRoleService(ctx, env, args, service.RoleServer)
}

// runRoleService is `stage service VERB` (server) or `stage worker
// service VERB` (worker).
func runRoleService(ctx context.Context, env Env, args []string, role service.Role) error {
	name := "service"
	if role == service.RoleWorker {
		name = "worker service"
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usagef("%s: want start|stop|restart|status|logs|enable|disable|uninstall", name)
	}
	verb, rest := args[0], args[1:]
	fs := flag.NewFlagSet(name+" "+verb, flag.ContinueOnError)
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
		fs.DurationVar(&wait, "wait", service.DefaultStopWait, "wait this long for the process to exit")
	case "enable":
		fs.StringVar(&exe, "exe", "", "executable the service runs (default this CLI)")
	case "logs":
		fs.IntVar(&n, "n", 100, "number of recent lines")
		fs.BoolVar(&follow, "f", false, "follow")
	case "status", "disable":
	default:
		return usagef("%s %s: unknown (want start|stop|restart|status|logs|enable|disable|uninstall)", name, verb)
	}
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(env.Stdout, usage)
			return err
		}
		return usagef("%s %s: %v (service commands target this installation; --url/STAGE_URL do not apply)", name, verb, err)
	}
	if fs.NArg() > 0 {
		return usagef("%s %s: unexpected argument %q", name, verb, fs.Arg(0))
	}
	c := service.New(env.serviceDeps(role))
	switch verb {
	case "status":
		st, code := c.Status(ctx)
		if *asJSON {
			if err := writeJSON(env.Stdout, st); err != nil {
				return err
			}
		} else {
			printServiceStatus(env.stdout(), st, env.cliName())
		}
		return codeErr(code)
	case "logs":
		if *asJSON {
			return usagef("%s logs: --json is not supported", name)
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
		printReport(env.stdout(), env.stderr(), rep)
	}
	return codeErr(rep.Code)
}

// printWorkerServiceStatus is the rest of `stage worker service status`.
func printWorkerServiceStatus(w io.Writer, s service.Status, wk *service.WorkerStatus, cli string) {
	if wk.Joined {
		kv(w, "machine", "%d at %s", wk.MachineID, wk.URL)
	} else {
		kv(w, "machine", "%s", toneWarn.paint("not joined"))
	}
	if wk.State != "" {
		kv(w, "channel", "%s", stateTone(wk.State).paint(wk.State))
	}
	printServiceDefinition(w, s)
	kv(w, "worker", "%s", wk.Config)
	kv(w, "config", "%s", s.ServiceEnv.Path)
	printServiceLogs(w, s, cli+" worker service logs")
	if h := wk.Holder; h != nil && !wk.OwnedByService {
		kv(w, "held by", "pid %d (managed %t) holds the worker dir", h.PID, h.Managed)
	}
	printProblems(w, s.Problems)
}

// printServiceDefinition is the login start, linger, definition and exe rows.
func printServiceDefinition(w io.Writer, s service.Status) {
	login := "off"
	if s.Definition.LoginStartup {
		login = "on"
	}
	kv(w, "login start", "%s", login)
	if s.Manager.Linger != nil {
		kv(w, "linger", "%t", *s.Manager.Linger)
	}
	if s.Definition.Present {
		kv(w, "definition", "%s", s.Definition.Path)
		kv(w, "exe", "%s", s.Definition.Exe)
	}
}

// printServiceLogs is the logs row: where the logs are and the command that
// prints them.
func printServiceLogs(w io.Writer, s service.Status, logsCmd string) {
	where := s.Logs.Path
	if s.Logs.Kind == "journald" {
		where = "journalctl --user -u " + s.Logs.Unit
	}
	kv(w, "logs", "%s %s", where, dim("("+logsCmd+")"))
}

func printProblems(w io.Writer, ps []service.Problem) {
	for _, p := range ps {
		line(w, toneBad, "%s", prose(p.Message))
		if p.Fix != "" {
			fixLine(w, inlineCmds(p.Fix))
		}
	}
}

func codeErr(code int) error {
	if code == 0 {
		return nil
	}
	return exitError{code}
}

func printReport(out, errw io.Writer, r service.Report) {
	printOutcome(out, errw, outcome{ok: r.OK, message: r.Message, fix: r.Fix, logTail: r.LogTail, notes: r.Notes, warnings: r.Warnings})
}

// outcome is a service or install command's result: its message (with a
// fix and recent log lines on failure), warnings and notes.
type outcome struct {
	ok                       bool
	message, fix             string
	logTail, notes, warnings []string
}

// printOutcome prints warnings and failures to errw; the success and notes
// to out.
func printOutcome(out, errw io.Writer, o outcome) {
	for _, w := range o.warnings {
		warn(errw, "%s", prose(w))
	}
	if o.ok {
		success(out, "%s", prose(o.message))
	} else {
		fail(errw, "%s", inlineCmds(o.message))
		if o.fix != "" {
			fixLine(errw, inlineCmds(o.fix))
		}
		if len(o.logTail) > 0 {
			fmt.Fprintf(errw, "  %s\n", dim("recent log lines:"))
			for _, l := range o.logTail {
				fmt.Fprintf(errw, "    %s\n", dim(l))
			}
		}
	}
	for _, n := range o.notes {
		fmt.Fprintf(out, "  %s %s\n", dim("note:"), inlineCmds(n))
	}
}

// serviceTone is a service's heading tone: ok when running (and ready, for
// the server), bad when failed or unknown, warn otherwise.
func serviceTone(s service.Status) tone {
	switch s.State {
	case string(service.PhaseRunning):
		if s.Ready || s.Worker != nil {
			return toneOK
		}
	case string(service.PhaseFailed), "unknown":
		return toneBad
	}
	return toneWarn
}

func printServiceStatus(w io.Writer, s service.Status, cli string) {
	kind := "service"
	if s.Worker != nil {
		kind = "worker service"
	}
	ready := ""
	if s.Ready {
		ready = ", ready"
	}
	t := serviceTone(s)
	line(w, t, "%s %s %s: %s", s.Identity, kind, dim("("+s.Manager.Kind+" "+s.Definition.Name+")"), t.paint(s.State+ready))
	if s.Process.PID != 0 {
		kv(w, "pid", "%d (restarts %d)", s.Process.PID, s.Process.Restarts)
	}
	if wk := s.Worker; wk != nil {
		printWorkerServiceStatus(w, s, wk, cli)
		return
	}
	if s.Stage.Reachable {
		kv(w, "stage", "%s at %s %s", s.Stage.Version, s.Stage.URL, dim("(db "+s.Stage.DB+")"))
	} else {
		kv(w, "stage", "%s at %s", toneBad.paint("not reachable"), s.Stage.URL)
	}
	if s.Harnesses == nil {
		kv(w, "harnesses", "%s", dim("unknown (Stage not reachable)"))
	}
	for _, h := range s.Harnesses {
		printHarnessState(w, h.Kind, h.Connected, h.Configured, h.LastError)
	}
	printServiceDefinition(w, s)
	kv(w, "data dir", "%s", s.DataDir)
	kv(w, "config", "%s", s.ServiceEnv.Path)
	printServiceLogs(w, s, cli+" service logs")
	if h := s.Lock.Holder; h != nil && !s.Lock.OwnedByService {
		kv(w, "held by", "pid %d (managed %t) holds the data dir", h.PID, h.Managed)
	}
	printProblems(w, s.Problems)
}

// printHarnessState is a harness row: connected, not set up, or offline.
func printHarnessState(w io.Writer, kind string, connected, configured bool, lastError *string) {
	switch {
	case connected:
		kv(w, kind, "%s", toneOK.paint("connected"))
	case !configured:
		kv(w, kind, "%s", toneWarn.paint("not set up"))
	case lastError != nil:
		kv(w, kind, "%s %s", toneBad.paint("offline:"), *lastError)
	default:
		kv(w, kind, "%s", toneBad.paint("offline"))
	}
}
