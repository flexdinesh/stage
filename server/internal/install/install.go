package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/filelock"
	"github.com/flexdinesh/stage/server/internal/service"
)

// ServiceOps is this installation's per-user service.
type ServiceOps interface {
	Status(ctx context.Context) service.Status
	// Restart restarts the service onto exe and waits for readiness.
	Restart(ctx context.Context, exe string) service.Report
	Uninstall(ctx context.Context) service.Report
	// ServerSchema is the running server's database schema (ok false
	// when unknown: not reachable, or a server without the field).
	ServerSchema(ctx context.Context, st service.Status) (int64, bool)
}

// DesktopOps finds, quits and launches the desktop app.
type DesktopOps interface {
	Running(l Layout) []int
	Quit(ctx context.Context, pids []int) error
	Launch(l Layout) error
}

// Deps is the installer's environment (tests substitute fakes).
type Deps struct {
	Platform
	// Identity and Version are this binary's (buildinfo).
	Identity, Version string
	Service           ServiceOps
	// WorkerService is this installation's worker service (`stage worker
	// service`); nil when the platform has none (tests).
	WorkerService ServiceOps
	Desktop       DesktopOps
	// Run runs a command with extra env and returns its combined output.
	Run       func(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
	LookPath  func(name string) (string, error)
	FreeSpace func(dir string) (uint64, error)
	Log       io.Writer
	Now       func() time.Time
	PID       int
}

func (d Deps) logf(format string, a ...any) {
	if d.Log != nil {
		fmt.Fprintf(d.Log, format+"\n", a...)
	}
}

// Options are `stage install --from DIR` flags.
type Options struct {
	From    string
	Mode    string // "" keeps the recorded mode, else desktop
	CLIName string // "" keeps the recorded name, else the identity
}

// Result is what an install did.
type Result struct {
	OK       bool   `json:"ok"`
	Code     int    `json:"code"`
	Message  string `json:"message"`
	Fix      string `json:"fix,omitempty"`
	Version  string `json:"version,omitempty"`
	Previous string `json:"previous,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Changed  bool   `json:"changed"`
	Service  string `json:"service,omitempty"` // restarted | stopped | not-installed | foreign | unavailable | rolled-back | failed
	// WorkerService is the same for the worker service (restarted,
	// stopped, not-installed, foreign, unavailable, rolled-back, failed;
	// uninstall: removed, unavailable, foreign, not-installed).
	WorkerService string   `json:"workerService,omitempty"`
	Desktop       string   `json:"desktop,omitempty"` // relaunched | quit | not-running
	CLI           string   `json:"cli,omitempty"`
	Link          string   `json:"link,omitempty"`
	App           string   `json:"app,omitempty"`
	Backup        string   `json:"backup,omitempty"`
	Notes         []string `json:"notes"`
	Warnings      []string `json:"warnings"`
	LogTail       []string `json:"logTail,omitempty"`
	Retained      []string `json:"retained,omitempty"`
}

func (r *Result) note(format string, a ...any) { r.Notes = append(r.Notes, fmt.Sprintf(format, a...)) }
func (r *Result) warn(format string, a ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, a...))
}

// probeInfo is `stage version --json` (the fields the installer reads).
type probeInfo struct {
	Identity string `json:"identity"`
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Build    string `json:"build"`
	Schema   int64  `json:"schema"`
}

func (d Deps) probe(ctx context.Context, exe string) (probeInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := d.Run(ctx, nil, exe, "version", "--json")
	if err != nil {
		return probeInfo{}, fmt.Errorf("%s version --json: %w: %s", exe, err, firstLine(out))
	}
	var pi probeInfo
	if err := json.Unmarshal(out, &pi); err != nil || pi.Identity == "" {
		return probeInfo{}, fmt.Errorf("%s version --json: not a stage binary", exe)
	}
	return pi, nil
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// lockRoot takes root's install lock without waiting.
func lockRoot(l Layout) (*filelock.Lock, error) {
	if err := os.MkdirAll(l.Root, 0o700); err != nil {
		return nil, permErr(err, l.Root)
	}
	lk, err := filelock.Acquire(l.Lock, 0o600)
	if errors.Is(err, filelock.ErrLocked) {
		return nil, newErr(CodeBusy, "wait for it to finish, then retry", "another Stage install, update or uninstall is running (%s)", l.Lock)
	}
	if err != nil {
		return nil, permErr(err, l.Lock)
	}
	return lk, nil
}

func permErr(err error, path string) error {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return wrapErr(CodeFailed, err, "make it writable by you (never install Stage with sudo)", "permission denied at %s", path)
	case isNoSpace(err):
		return wrapErr(CodeFailed, err, "free some disk space and retry; nothing was changed", "disk full at %s", path)
	}
	return wrapErr(CodeFailed, err, "", "%s", path)
}

// recover finishes an interrupted activation: the app directory missing
// with a previous one present means the swap stopped halfway.
func recoverActivation(d Deps, l Layout, rec *Record) (*Record, error) {
	if rec == nil || rec.State != StateActivating {
		return rec, nil
	}
	_, appErr := os.Lstat(rec.App)
	_, prevErr := os.Lstat(rec.App + ".prev")
	switch {
	case errors.Is(appErr, fs.ErrNotExist) && prevErr == nil:
		if err := os.Rename(rec.App+".prev", rec.App); err != nil {
			return nil, permErr(err, rec.App)
		}
		d.logf("recovered an interrupted update: restored %s %s", l.AppName, rec.From)
		rec.Version = rec.From
	case appErr == nil:
		d.logf("recovered an interrupted update to %s", rec.To)
		rec.Version = rec.To
	}
	rec.State, rec.From, rec.To = StateActive, "", ""
	if err := writeRecord(l.Record, rec); err != nil {
		return nil, permErr(err, l.Record)
	}
	return rec, nil
}

// Install installs the release in o.From (run by that release's CLI).
func Install(ctx context.Context, d Deps, o Options) (res *Result) {
	res = &Result{Notes: []string{}, Warnings: []string{}}
	err := install(ctx, d, o, res)
	return finish(res, err)
}

func finish(res *Result, err error) *Result {
	if err == nil {
		res.OK = true
		return res
	}
	res.OK = false
	res.Code = CodeOf(err)
	res.Message = err.Error()
	if f := FixOf(err); f != "" {
		res.Fix = f
	}
	return res
}

func install(ctx context.Context, d Deps, o Options, res *Result) error {
	if o.From == "" {
		return newErr(CodeUsage, "", "--from DIR is required")
	}
	from, err := filepath.Abs(o.From)
	if err != nil {
		return wrapErr(CodeUsage, err, "", "--from")
	}
	if _, err := platformTarget(d.Platform); err != nil {
		return err
	}
	// The record decides the mode and CLI name of an existing install.
	root := RootFor(d.Platform, d.Identity)
	prior, err := readRecord(filepath.Join(root, "install.json"))
	if err != nil {
		return wrapErr(CodeFailed, err, "move it aside and reinstall", "unreadable install record")
	}
	mode, cliName := o.Mode, o.CLIName
	if prior != nil {
		if mode == "" {
			mode = prior.Mode
		}
		if cliName == "" {
			cliName = prior.CLIName
		}
	}
	if mode == "" {
		mode = ModeDesktop
	}
	l, err := LayoutFor(d.Platform, d.Identity, mode, cliName)
	if err != nil {
		return err
	}
	res.Mode, res.App, res.CLI, res.Link = mode, l.App, l.CLI, l.Link

	v, err := Verify(from, d.Platform, Expect{Identity: d.Identity, Mode: mode})
	if err != nil {
		return err
	}
	m := v.Manifest
	res.Version = m.Version
	if m.Version != d.Version {
		return newErr(CodeVerify, "run the CLI from the release you install (scripts/install.sh does)", "this stage is %s but the release is %s", d.Version, m.Version)
	}

	lk, err := lockRoot(l)
	if err != nil {
		return err
	}
	defer lk.Release()
	prior, err = readRecord(l.Record)
	if err != nil {
		return wrapErr(CodeFailed, err, "move it aside and reinstall", "unreadable install record")
	}
	if prior, err = recoverActivation(d, l, prior); err != nil {
		return err
	}
	if prior != nil {
		res.Previous = prior.Version
		if prior.Identity != d.Identity {
			return newErr(CodeUnsupported, "", "%s belongs to identity %s", l.Record, prior.Identity)
		}
		if prior.Mode != mode {
			return newErr(CodeUnsupported, fmt.Sprintf("uninstall first (%s uninstall), then install in %s mode", prior.CLIName, mode), "Stage is installed in %s mode", prior.Mode)
		}
		if prior.App != l.App {
			return newErr(CodeUnsupported, "uninstall first, then reinstall", "the install record names another app location (%s)", prior.App)
		}
	} else if _, err := os.Lstat(l.App); err == nil {
		return newErr(CodeUnsupported, fmt.Sprintf("move %s away (it was not installed by the Stage installer), then retry", l.App), "%s already exists", l.App)
	}

	// Same version and intact: repair links and entries only.
	if prior != nil && prior.Version == m.Version && appIntact(l, v.Artifact) {
		if err := preflight(d, l, prior); err != nil {
			return err
		}
		files, err := register(ctx, d, l, res)
		if err != nil {
			return err
		}
		prior.Files, prior.Link, prior.Entry, prior.CLIName = files, l.Link, l.Entry, filepath.Base(l.Link)
		if err := writeRecord(l.Record, prior); err != nil {
			return permErr(err, l.Record)
		}
		res.Message = fmt.Sprintf("%s %s is already installed (%s mode)", l.AppName, m.Version, mode)
		pathNote(d, l, res)
		return nil
	}

	// Stage: extract and check everything before touching the install.
	staged, cleanup, err := stage(ctx, d, l, v, res)
	if err != nil {
		return err
	}
	defer cleanup()
	target, err := d.probe(ctx, filepath.Join(staged, filepath.FromSlash(mustTree(v, d.Platform, mode).cli)))
	if err != nil {
		return wrapErr(CodeVerify, err, verifyFix, "the release's CLI does not run here")
	}
	if target.Identity != d.Identity || target.Version != m.Version {
		return newErr(CodeVerify, verifyFix, "the release's CLI is %s %s, the manifest says %s %s", target.Identity, target.Version, m.Identity, m.Version)
	}
	if mode == ModeDesktop {
		if err := selfcheck(ctx, d, l, staged, m.Version); err != nil {
			return err
		}
	}
	if err := preflight(d, l, prior); err != nil {
		return err
	}

	st := d.Service.Status(ctx)
	if dbSchema, ok := d.Service.ServerSchema(ctx, st); ok && target.Schema > 0 && dbSchema > target.Schema {
		return newErr(CodeUnsupported, "install the newer version again, or restore a database backup first (docs/install.md#recovery)",
			"the running server's database is at schema %d, newer than %s %s knows (%d): it would not start", dbSchema, l.AppName, m.Version, target.Schema)
	}
	var prevInfo probeInfo
	if prior != nil {
		if pi, err := d.probe(ctx, l.CLI); err == nil {
			prevInfo = pi
		}
	}
	ours := serviceOurs(st, l)
	running := ours && (st.State == "running" || st.State == "starting")
	var wst service.Status
	workerOurs, workerRunning := false, false
	if d.WorkerService != nil {
		wst = d.WorkerService.Status(ctx)
		workerOurs = serviceOurs(wst, l)
		workerRunning = workerOurs && (wst.State == "running" || wst.State == "starting")
	}

	// Disruption starts here.
	var desktopPIDs []int
	if l.Desktop != "" {
		desktopPIDs = d.Desktop.Running(l)
		if len(desktopPIDs) > 0 {
			d.logf("quitting %s", l.AppName)
			if err := d.Desktop.Quit(ctx, desktopPIDs); err != nil {
				return wrapErr(CodeFailed, err, fmt.Sprintf("quit %s yourself, then retry; nothing was changed", l.AppName), "could not quit %s", l.AppName)
			}
			res.Desktop = "quit"
		}
	}
	relaunch := func() {
		if len(desktopPIDs) == 0 {
			return
		}
		if err := d.Desktop.Launch(l); err != nil {
			res.note("reopen %s from your app launcher (%v)", l.AppName, err)
			return
		}
		res.Desktop = "relaunched"
	}

	rec := &Record{
		Identity: d.Identity, Channel: m.Channel, Mode: mode, Version: m.Version, Commit: m.Commit,
		CLIName: filepath.Base(l.Link), App: l.App, CLI: l.CLI, Link: l.Link, Entry: l.Entry,
		State: StateActivating, To: m.Version, InstalledAt: d.Now().UTC().Format(time.RFC3339),
	}
	if prior != nil {
		rec.From, rec.Files = prior.Version, prior.Files
	}
	if err := writeRecord(l.Record, rec); err != nil {
		relaunch()
		return permErr(err, l.Record)
	}
	hadPrev, err := swapIn(l, staged)
	if err != nil {
		rec.State, rec.Version, rec.From, rec.To = StateActive, res.Previous, "", ""
		if prior == nil {
			_ = os.Remove(l.Record)
		} else {
			_ = writeRecord(l.Record, rec)
		}
		relaunch()
		return permErr(err, l.App)
	}
	res.Changed = true
	files, regErr := register(ctx, d, l, res)
	if regErr == nil {
		rec.Files = files
	} else {
		res.warn("%v", regErr)
	}

	// Restart the service onto the new binary, only if it was running ours.
	switch {
	case running:
		d.logf("restarting the %s service", d.Identity)
		rep := d.Service.Restart(ctx, l.CLI)
		ok := rep.Code == 0 && reportVersion(rep) == m.Version
		if !ok {
			return activationFailed(ctx, d, l, rec, prior, hadPrev, prevInfo, target, rep, res, relaunch, false)
		}
		res.Service = "restarted"
	case st.State == "not-installed":
		res.Service = "not-installed"
		if mode == ModeHeadless {
			res.note("start the server as a per-user service: %s service start (docs/install.md#service)", filepath.Base(l.Link))
		}
	case st.State == "unknown":
		res.Service = "unavailable"
	case !ours && st.Definition.Present:
		res.Service = "foreign"
		res.note("the %s service runs %s, not this install; it was not restarted", d.Identity, st.Definition.Exe)
	default:
		res.Service = "stopped"
	}
	if h := st.Lock.Holder; h != nil && !st.Lock.OwnedByService {
		res.note("a foreground stage server (pid %d) holds the data dir; restart it to run %s", h.PID, m.Version)
	}
	// The worker service likewise (a machine runs one role's service).
	if d.WorkerService != nil {
		switch {
		case workerRunning:
			d.logf("restarting the %s worker service", d.Identity)
			rep := d.WorkerService.Restart(ctx, l.CLI)
			if rep.Code != 0 || workerVersion(rep) != m.Version {
				return activationFailed(ctx, d, l, rec, prior, hadPrev, prevInfo, target, rep, res, relaunch, true)
			}
			res.WorkerService = "restarted"
		case wst.State == "not-installed":
			res.WorkerService = "not-installed"
		case wst.State == "unknown":
			res.WorkerService = "unavailable"
		case !workerOurs && wst.Definition.Present:
			res.WorkerService = "foreign"
			res.note("the %s worker service runs %s, not this install; it was not restarted", d.Identity, wst.Definition.Exe)
		default:
			res.WorkerService = "stopped"
		}
		if wk := wst.Worker; wk != nil && wk.Holder != nil && !wk.OwnedByService {
			res.note("a foreground stage worker (pid %d) holds the worker dir; restart it to run %s", wk.Holder.PID, m.Version)
		}
	}

	rec.State, rec.From, rec.To = StateActive, "", ""
	if err := writeRecord(l.Record, rec); err != nil {
		relaunch()
		return permErr(err, l.Record)
	}
	relaunch()
	if prior == nil {
		res.Message = fmt.Sprintf("installed %s %s (%s mode)", l.AppName, m.Version, mode)
	} else if prior.Version == m.Version {
		res.Message = fmt.Sprintf("reinstalled %s %s", l.AppName, m.Version)
	} else {
		res.Message = fmt.Sprintf("updated %s %s -> %s", l.AppName, prior.Version, m.Version)
	}
	if mode == ModeDesktop && prior == nil {
		res.note("open %s from your app launcher; it starts the Stage server", l.AppName)
	}
	pathNote(d, l, res)
	return nil
}

func mustTree(v *Verified, p Platform, mode string) appTree {
	t, _ := treeFor(v.Artifact, p, mode)
	return t
}

// appIntact reports whether the installed CLI is this release's.
func appIntact(l Layout, a Artifact) bool {
	sum, _, err := fileSHA256(l.CLI)
	if err != nil || sum != a.CLI.SHA256 {
		return false
	}
	if l.Desktop != "" {
		if st, err := os.Stat(l.Desktop); err != nil || !st.Mode().IsRegular() {
			return false
		}
	}
	return true
}

// stage extracts the artifact into a fresh staging directory next to the
// app and checks the CLI against the manifest. cleanup removes it.
func stage(ctx context.Context, d Deps, l Layout, v *Verified, res *Result) (string, func(), error) {
	t, err := treeFor(v.Artifact, d.Platform, l.Mode)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(l.Staging, 0o700); err != nil {
		return "", nil, permErr(err, l.Staging)
	}
	if err := os.MkdirAll(filepath.Dir(l.App), 0o755); err != nil {
		return "", nil, permErr(err, filepath.Dir(l.App))
	}
	if d.FreeSpace != nil {
		need := uint64(v.Artifact.Size)*4 + 64<<20
		if free, err := d.FreeSpace(l.Staging); err == nil && free < need {
			return "", nil, newErr(CodeFailed, "free some disk space and retry; nothing was changed", "not enough disk space at %s: %d MiB free, %d MiB needed", l.Staging, free>>20, need>>20)
		}
	}
	// The staging dir must be on the app's filesystem so the swap is a rename.
	dir, err := os.MkdirTemp(stagingParent(l), ".stage-"+v.Manifest.Version+"-")
	if err != nil {
		return "", nil, permErr(err, l.Staging)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	app := filepath.Join(dir, filepath.Base(l.App))
	if err := extractApp(filepath.Join(v.Dir, v.Artifact.Name), t, app); err != nil {
		cleanup()
		if isNoSpace(err) || errors.Is(err, fs.ErrPermission) {
			return "", nil, permErr(err, dir)
		}
		return "", nil, wrapErr(CodeVerify, err, verifyFix, "unsafe or corrupt %s", v.Artifact.Name)
	}
	cli := filepath.Join(app, filepath.FromSlash(t.cli))
	sum, _, err := fileSHA256(cli)
	if err != nil || sum != v.Artifact.CLI.SHA256 {
		cleanup()
		return "", nil, newErr(CodeVerify, verifyFix, "the CLI in %s does not match the manifest", v.Artifact.Name)
	}
	if st, err := os.Stat(cli); err != nil || st.Mode()&0o111 == 0 {
		cleanup()
		return "", nil, newErr(CodeVerify, verifyFix, "the CLI in %s is not executable", v.Artifact.Name)
	}
	return app, cleanup, nil
}

// stagingParent is where staging goes: the root's staging dir when it
// shares the app's filesystem (Linux, headless), else next to the app.
func stagingParent(l Layout) string {
	if within(l.App, l.Root) {
		return l.Staging
	}
	return filepath.Dir(l.App)
}

// selfcheck runs the staged desktop app's STAGE_DESKTOP_SELFCHECK mode.
func selfcheck(ctx context.Context, d Deps, l Layout, stagedApp, version string) error {
	rel, err := filepath.Rel(l.App, l.Desktop)
	if err != nil {
		return err
	}
	exe := filepath.Join(stagedApp, rel)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := d.Run(ctx, []string{"STAGE_DESKTOP_SELFCHECK=1"}, exe)
	if err != nil {
		if d.GOOS == "linux" && !hasWebKit(ctx, d) {
			return newErr(CodeUnsupported, webkitFix(d), "%s needs WebKitGTK 4.1 and GTK 3, which are not installed", l.AppName)
		}
		return newErr(CodeVerify, "install the CLI/server only with --headless, or report it", "the desktop app does not start here: %s", firstLine(out))
	}
	var rep struct {
		Identity, Version string
	}
	if err := json.Unmarshal(lastJSONLine(out), &rep); err != nil || rep.Identity != d.Identity || rep.Version != version {
		return newErr(CodeVerify, verifyFix, "the desktop app reports %q %q, expected %s %s", rep.Identity, rep.Version, d.Identity, version)
	}
	return nil
}

func lastJSONLine(out []byte) []byte {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "{") {
			return []byte(lines[i])
		}
	}
	return nil
}

// swapIn moves the app aside (to Prev, replacing an older one) and the
// staged app in. On a failed second rename it moves the app back.
func swapIn(l Layout, staged string) (hadPrev bool, err error) {
	if err := os.RemoveAll(l.Prev); err != nil {
		return false, err
	}
	if _, err := os.Lstat(l.App); err == nil {
		if err := os.Rename(l.App, l.Prev); err != nil {
			return false, err
		}
		hadPrev = true
	}
	if err := os.Rename(staged, l.App); err != nil {
		if hadPrev {
			_ = os.Rename(l.Prev, l.App)
		}
		return false, err
	}
	return hadPrev, nil
}

// serviceOurs reports whether the service definition runs this install's
// CLI (directly or through the link).
func serviceOurs(st service.Status, l Layout) bool {
	if !st.Definition.Present || st.Definition.Exe == "" {
		return false
	}
	exe := st.Definition.Exe
	if exe == l.CLI || exe == l.Link {
		return true
	}
	a, err1 := filepath.EvalSymlinks(exe)
	b, err2 := filepath.EvalSymlinks(l.CLI)
	return err1 == nil && err2 == nil && a == b
}

// activationFailed handles a service that did not come back on the new
// version: the binaries roll back only when the schema did not change (a
// worker has no database: it always rolls back when a previous version
// exists).
func activationFailed(ctx context.Context, d Deps, l Layout, rec *Record, prior *Record, hadPrev bool, prev, target probeInfo, rep service.Report, res *Result, relaunch func(), worker bool) error {
	res.LogTail = rep.LogTail
	svc, outcome, cmd := d.Service, &res.Service, "service"
	if worker {
		svc, outcome, cmd = d.WorkerService, &res.WorkerService, "worker service"
	}
	why := rep.Message
	if rep.Code == 0 {
		v := reportVersion(rep)
		if worker {
			v = workerVersion(rep)
		}
		noun := "server"
		if worker {
			noun = "worker"
		}
		why = fmt.Sprintf("the %s reports version %q, expected %s", noun, v, target.Version)
	}
	safe := hadPrev && prior != nil && (worker || (prev.Schema > 0 && prev.Schema == target.Schema))
	if !safe {
		*outcome = "failed"
		if worker {
			rec.State, rec.From, rec.To = StateActive, "", ""
			_ = writeRecord(l.Record, rec)
			relaunch()
			return newErr(CodeNotRolled, fmt.Sprintf("check `%s worker service logs`; fix the cause and run `%s worker service restart`", filepath.Base(l.Link), filepath.Base(l.Link)),
				"%s %s is installed but its worker did not start (%s); no previous version to roll back to", l.AppName, target.Version, why)
		}
		dataDir := ""
		if rep.Status != nil {
			dataDir = rep.Status.DataDir
		}
		res.Backup = latestBackup(dataDir)
		rec.State, rec.From, rec.To = StateActive, "", ""
		_ = writeRecord(l.Record, rec)
		relaunch()
		fix := fmt.Sprintf("check `%s service logs`; fix the cause and run `%s service restart`, or recover as docs/install.md#recovery describes", filepath.Base(l.Link), filepath.Base(l.Link))
		if res.Backup != "" {
			fix += fmt.Sprintf(" (database backup taken before migrating: %s)", res.Backup)
		}
		return newErr(CodeNotRolled, fix, "%s %s is installed but its server did not start (%s); not rolled back because the database schema may have changed", l.AppName, target.Version, why)
	}
	d.logf("rolling back to %s %s", l.AppName, prior.Version)
	failed := filepath.Join(stagingParent(l), ".stage-failed-"+target.Version)
	_ = os.RemoveAll(failed)
	if err := os.Rename(l.App, failed); err == nil {
		err = os.Rename(l.Prev, l.App)
		if err != nil {
			_ = os.Rename(failed, l.App)
		} else {
			_ = os.RemoveAll(failed)
		}
	}
	files, err := register(ctx, d, l, res)
	if err == nil {
		prior.Files = files
	}
	prior.State, prior.From, prior.To = StateActive, "", ""
	_ = writeRecord(l.Record, prior)
	back := svc.Restart(ctx, l.CLI)
	*outcome = "rolled-back"
	if back.Code != 0 {
		res.warn("the previous version did not restart either: %s", back.Message)
	}
	relaunch()
	return newErr(CodeRolledBack, fmt.Sprintf("check `%s %s logs` and report it", filepath.Base(l.Link), cmd),
		"%s %s did not start (%s); rolled back to %s", l.AppName, target.Version, why, prior.Version)
}

// workerVersion is the version of the worker a worker service report's
// status names ("" when unknown).
func workerVersion(rep service.Report) string {
	if rep.Status == nil || rep.Status.Worker == nil || rep.Status.Worker.Holder == nil {
		return ""
	}
	return rep.Status.Worker.Holder.Version
}

func reportVersion(rep service.Report) string {
	if rep.Status == nil {
		return ""
	}
	return rep.Status.Stage.Version
}

// latestBackup is the newest database backup the server took (docs/runtime.md).
func latestBackup(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	m, _ := filepath.Glob(filepath.Join(dataDir, "backups", "stage-schema*.db"))
	if len(m) == 0 {
		return ""
	}
	// Newest by the UTC timestamp after the last '-' (schema numbers vary in width).
	stamp := func(p string) string { b := filepath.Base(p); return b[strings.LastIndexByte(b, '-')+1:] }
	sort.Slice(m, func(i, j int) bool { return stamp(m[i]) < stamp(m[j]) })
	return m[len(m)-1]
}

// pathNote tells the user how to reach the CLI.
func pathNote(d Deps, l Layout, res *Result) {
	dir := filepath.Dir(l.Link)
	for _, p := range filepath.SplitList(d.env("PATH")) {
		if filepath.Clean(p) == dir {
			if found, err := d.LookPath(filepath.Base(l.Link)); err == nil && found != l.Link {
				res.warn("%s comes first on PATH and shadows %s", found, l.Link)
			}
			return
		}
	}
	res.note("%s is not on PATH; add it: %s", dir, pathHint(d, dir))
}

func pathHint(d Deps, dir string) string {
	shell := filepath.Base(d.env("SHELL"))
	switch shell {
	case "fish":
		return fmt.Sprintf("fish_add_path %s", dir)
	case "zsh":
		return fmt.Sprintf("echo 'export PATH=\"%s:$PATH\"' >> ~/.zshrc", dir)
	default:
		return fmt.Sprintf("echo 'export PATH=\"%s:$PATH\"' >> ~/.bashrc", dir)
	}
}
