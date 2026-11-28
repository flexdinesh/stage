package claudecode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Trusting a folder is Claude Code's documented manual step: set
// projects[<folder>].hasTrustDialogAccepted to true in its state file
// (ADR 0012 decision 8). Claude has no API for it. Stage writes only that
// key, for one folder, behind the caller's explicit confirmation; the
// workspace source and the project action share this code path.
const (
	SourceTrust  = "trust"
	ActionTrust  = "trust"
	trustLabel   = "Trust in Claude Code"
	trustConfirm = "Claude Code will trust this folder. Its .claude settings, hooks and MCP servers will run in Claude sessions there."
	trustHelp    = "An existing folder on this machine. Claude Code lists it once it is trusted."
	// reasonTrusted: the project action on a folder Claude already trusts.
	reasonTrusted = "Claude Code already trusts this folder."
	reasonNoOps   = "Stage can't check folders on this machine."
)

// Claude saves its state under a proper-lockfile lock: a directory next to
// the file, which the holder touches every 5s and which counts as stale
// after 10s (Claude Code 2.1.332).
const (
	stateLockStale = 10 * time.Second
	trustAttempts  = 3
)

// Inventory reads filesystem evidence for local workspaces and applies
// their trust rules (machine.Ops): the adapter never reads Git itself.
type Inventory interface {
	Inventory(ctx context.Context, ws []execution.WorkspaceInfo) ([]execution.WorkspaceInfo, error)
}

var trustField = execution.Field{ID: "path", Label: "Folder", Kind: execution.FieldPath, Help: trustHelp, Required: true}

// trustSource is the workspace source; trustAction the project action on
// one listed folder, available where Claude does not trust it.
func (a *Adapter) trustSource() execution.WorkspaceSource {
	return execution.WorkspaceSource{ID: SourceTrust, Label: trustLabel, Help: trustHelp, Input: []execution.Field{trustField}, Confirm: trustConfirm, Availability: a.trustAvailability()}
}

func (a *Adapter) trustAction(availability execution.Availability) execution.Action {
	return execution.Action{
		ID: ActionTrust, Label: trustLabel, Target: execution.TargetProject, Confirm: trustConfirm, Primary: true,
		Callers: []execution.Caller{execution.CallerUser, execution.CallerAgent}, Availability: availability,
	}
}

func (a *Adapter) trustAvailability() execution.Availability {
	if a.inventory == nil {
		return execution.Availability{Reason: reasonNoOps}
	}
	return available
}

// FeaturesForWorkspace scopes the trust action to the folder: available
// only where Claude does not trust it. It never fails on a trust read.
func (a *Adapter) FeaturesForWorkspace(ctx context.Context, _, directory string) (execution.Capabilities, error) {
	caps, err := a.Features(ctx)
	if err != nil {
		return caps, err
	}
	availability := a.trustAvailability()
	if availability.Available {
		availability = a.folderTrust(ctx, directory)
	}
	caps.Actions = []execution.Action{a.trustAction(availability)}
	return caps, nil
}

// folderTrust is the trust action's availability on dir.
func (a *Adapter) folderTrust(ctx context.Context, dir string) execution.Availability {
	dir, err := a.trustTarget(dir)
	if err != nil {
		return execution.Availability{Reason: err.Error()}
	}
	path, err := a.statePath(ctx)
	if err != nil {
		return execution.Availability{Reason: err.Error()}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return execution.Availability{Reason: stateError(path, err).Error()}
	}
	trusted, _, err := a.trustedIn(ctx, b, dir)
	switch {
	case err != nil:
		return execution.Availability{Reason: err.Error()}
	case trusted:
		return execution.Availability{Reason: reasonTrusted}
	default:
		return available
	}
}

// AddWorkspace runs the trust source.
func (a *Adapter) AddWorkspace(ctx context.Context, r execution.WorkspaceAddRequest) (execution.WorkspaceAdded, error) {
	if r.Source != SourceTrust {
		return execution.WorkspaceAdded{}, &execution.Invalid{Reason: fmt.Sprintf("Claude Code has no workspace source %q.", r.Source)}
	}
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(r.Input, &in); err != nil {
		return execution.WorkspaceAdded{}, &execution.Invalid{Reason: "Enter the folder to trust as input.path."}
	}
	return a.trust(ctx, in.Path, r.Confirmed)
}

// Act runs the project action trust on its listed folder.
func (a *Adapter) Act(ctx context.Context, r execution.ActionRequest) (execution.ActionResult, error) {
	if r.Target != execution.TargetProject || r.ID != ActionTrust || r.Project == nil {
		return execution.ActionResult{}, fmt.Errorf("%w: Claude Code has no %s action %q", execution.ErrNotFound, r.Target, r.ID)
	}
	added, err := a.trust(ctx, r.Project.Directory, r.Confirmed)
	if err != nil {
		return execution.ActionResult{}, err
	}
	return execution.ActionResult{Message: added.Message, Facts: added.Facts}, nil
}

// trust makes Claude trust folder. An already trusted folder with its own
// project entry changes nothing (Existing). Otherwise it writes the folder's
// own record, also when an ancestor's record already covers it, so Claude
// lists it.
func (a *Adapter) trust(ctx context.Context, folder string, confirmed bool) (execution.WorkspaceAdded, error) {
	var added execution.WorkspaceAdded
	if !confirmed {
		return added, &execution.Invalid{Reason: fmt.Sprintf("Confirm before running %s: %s Send confirmed: true.", trustLabel, trustConfirm)}
	}
	if a.inventory == nil {
		return added, fmt.Errorf("%w: %s", execution.ErrUnavailable, reasonNoOps)
	}
	dir, err := a.trustTarget(folder)
	if err != nil {
		return added, err
	}
	path, err := a.statePath(ctx)
	if err != nil {
		return added, err
	}
	added.Workspace = execution.WorkspaceInfo{ID: dir, Title: filepath.Base(dir), WorkspaceRoot: dir, Local: true, ImportIf: execution.ImportCheckoutTop}

	a.trustMu.Lock()
	defer a.trustMu.Unlock()
	for attempt := 0; attempt < trustAttempts; attempt++ {
		existing, changed, err := a.trustOnce(ctx, path, dir)
		if err != nil {
			return added, err
		}
		if changed {
			continue
		}
		added.Existing = existing
		if existing {
			added.Message = "Claude Code already trusts " + dir + "."
		} else {
			added.Message = "Claude Code now trusts " + dir + "."
			added.Facts = []execution.Fact{{ID: "trustRecord", Label: "Trust record", Value: fmt.Sprintf("projects[%s].%s in %s", quote(dir), trustKey, path), Copyable: true}}
		}
		return added, nil
	}
	return added, &execution.Busy{Reason: "Claude Code kept changing its state file while Stage was saving it. Nothing was changed. Try again."}
}

// trustOnce holds Claude's lock for one read-patch-write. changed: the file
// changed after the read, so nothing was written.
func (a *Adapter) trustOnce(ctx context.Context, path, dir string) (existing, changed bool, err error) {
	unlock, err := lockState(ctx, path, a.lockWait)
	if err != nil {
		return false, false, err
	}
	defer unlock()
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, false, stateError(path, err)
	}
	read, b, err := snapshot(target)
	if err != nil {
		return false, false, stateError(path, err)
	}
	trusted, own, err := a.trustedIn(ctx, b, dir)
	if err != nil {
		return false, false, err
	}
	if trusted && own {
		return true, false, nil
	}
	patched, err := setTrust(b, dir)
	if err != nil {
		return false, false, fmt.Errorf("Stage did not change %s: %w", path, err)
	}
	changed, err = a.replace(target, read, patched)
	return false, changed, err
}

// fileState identifies the bytes Stage read.
type fileState struct {
	sum  [sha256.Size]byte
	mod  time.Time
	size int64
	perm fs.FileMode
}

func snapshot(path string) (fileState, []byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return fileState{}, nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fileState{}, nil, err
	}
	return fileState{sum: sha256.Sum256(b), mod: st.ModTime(), size: st.Size(), perm: st.Mode().Perm()}, b, nil
}

// replace writes data beside path (temp file, fsync, path's mode) and
// renames it over path only if path still holds the bytes Stage read.
func (a *Adapter) replace(path string, read fileState, data []byte) (changed bool, err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".stage-*")
	if err != nil {
		return false, err
	}
	name := tmp.Name()
	defer func() {
		if err != nil || changed {
			_ = os.Remove(name)
		}
	}()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Chmod(read.perm)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, err
	}
	if a.beforeRename != nil {
		a.beforeRename()
	}
	now, _, err := snapshot(path)
	if err != nil {
		return false, err
	}
	if now.sum != read.sum || !now.mod.Equal(read.mod) || now.size != read.size {
		return true, nil
	}
	if err = os.Rename(name, path); err != nil {
		return false, err
	}
	if d, derr := os.Open(dir); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return false, nil
}

// lockState takes Claude's lock on its state file: mkdir <file>.lock, as
// proper-lockfile does. A lock untouched for stateLockStale is a dead
// holder's and is taken over. A live holder past wait answers Busy.
func lockState(ctx context.Context, file string, wait time.Duration) (func(), error) {
	lock := file + ".lock"
	deadline := time.Now().Add(wait)
	delay := 25 * time.Millisecond
	for {
		err := os.Mkdir(lock, 0o755)
		if err == nil {
			return func() { _ = os.Remove(lock) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("lock %s: %w", file, err)
		}
		if st, err := os.Stat(lock); err == nil && time.Since(st.ModTime()) > stateLockStale {
			_ = os.Remove(lock)
			continue
		}
		left := time.Until(deadline)
		if left <= 0 {
			return nil, &execution.Busy{Reason: "Claude Code is saving its state file. Nothing was changed. Try again in a moment."}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(min(delay, left)):
		}
		delay = min(2*delay, 500*time.Millisecond)
	}
}

// statePath is the connection's state file.
func (a *Adapter) statePath(ctx context.Context) (string, error) {
	au, err := a.checkedAuth(ctx, false)
	if err != nil {
		return "", err
	}
	return a.cli.claudeJSON(au.ConfigDirectory), nil
}

func stateError(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return &execution.Invalid{Reason: "Claude Code has no state file at " + path + " yet. Run claude once, then try again."}
	}
	return fmt.Errorf("read %s: %w", path, err)
}

// trustTarget is folder's canonical directory, refusing what Stage must
// not trust: home (Claude trusts it one session at a time), the filesystem
// root and home's ancestors (a record there trusts every folder below,
// home included) and Claude's own worktrees.
func (a *Adapter) trustTarget(folder string) (string, error) {
	folder = strings.TrimSpace(folder)
	if !filepath.IsAbs(folder) {
		return "", &execution.Invalid{Reason: "Enter an absolute folder path."}
	}
	dir, err := filepath.EvalSymlinks(filepath.Clean(folder))
	if errors.Is(err, fs.ErrNotExist) {
		return "", &execution.Invalid{Reason: folder + " does not exist on this machine."}
	}
	if err != nil {
		return "", &execution.Invalid{Reason: fmt.Sprintf("Stage can't read %s: %v", folder, err)}
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", &execution.Invalid{Reason: folder + " is not a folder."}
	}
	home := filepath.Clean(a.cli.claudeHome())
	if h, err := filepath.EvalSymlinks(home); err == nil {
		home = h
	}
	switch {
	case dir == home:
		return "", &execution.Invalid{Reason: homeReason}
	case dir == filepath.Dir(dir) || strings.HasPrefix(home, dir+string(filepath.Separator)):
		return "", &execution.Invalid{Reason: "Trusting " + dir + " would trust every folder under it, including your home folder. Choose a project folder."}
	case strings.Contains(filepath.ToSlash(dir)+"/", worktreeMarker):
		return "", &execution.Invalid{Reason: "This is a Claude Code worktree. Trust its repository folder instead."}
	}
	return dir, nil
}

// trustedIn reports whether Claude trusts dir under the state b (its trust
// rule, settled by machine evidence, or CLAUDE_CODE_SANDBOXED), and
// whether dir has its own project entry, so Claude lists it.
func (a *Adapter) trustedIn(ctx context.Context, b []byte, dir string) (trusted, own bool, err error) {
	projects, err := stateProjects(b)
	if err != nil {
		return false, false, err
	}
	own = slices.ContainsFunc(keysOf(projects), func(k string) bool { return filepath.IsAbs(k) && filepath.Clean(k) == dir })
	if sandboxed(a.cli.getenv("CLAUDE_CODE_SANDBOXED")) {
		return true, own, nil
	}
	ws, err := a.inventory.Inventory(ctx, []execution.WorkspaceInfo{{
		ID: dir, WorkspaceRoot: dir, Local: true, Trust: &execution.TrustRule{Records: trustRecords(projects), Reason: untrustedReason},
	}})
	if err != nil {
		return false, own, fmt.Errorf("Stage can't check %s: %w", dir, err)
	}
	if len(ws) != 1 {
		return false, own, fmt.Errorf("Stage can't check %s: inventory returned %d rows", dir, len(ws))
	}
	w := ws[0]
	return w.Evidence != nil && !w.Evidence.Gone && w.BlockedReason == "", own, nil
}

// stateProjects is the state file's project map (nil when a fresh state
// omits it).
func stateProjects(b []byte) (map[string]json.RawMessage, error) {
	var state *struct {
		Projects json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, fmt.Errorf("Claude Code's state file is not valid JSON: %w", err)
	}
	if state == nil {
		return nil, errors.New("Claude Code's state file is not a JSON object")
	}
	if state.Projects == nil {
		return nil, nil
	}
	var projects map[string]json.RawMessage
	if err := json.Unmarshal(state.Projects, &projects); err != nil || projects == nil {
		return nil, errors.New("Claude Code's state file has a projects value that is not an object")
	}
	return projects, nil
}

// trustRecords are the folders Claude recorded as trusted, as written:
// Claude looks keys up verbatim.
func trustRecords(projects map[string]json.RawMessage) []string {
	var records []string
	for dir, raw := range projects {
		var project struct {
			Trusted json.RawMessage `json:"hasTrustDialogAccepted"`
		}
		if json.Unmarshal(raw, &project) == nil && bytes.Equal(project.Trusted, []byte("true")) && filepath.IsAbs(dir) {
			records = append(records, dir)
		}
	}
	slices.Sort(records)
	return records
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
