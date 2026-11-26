package claudecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// WorktreeName is the Claude worktree a worktree launch asks for: unique per
// attempt, since `-w <name>` silently reuses an existing worktree (ADR 0005
// E5).
func WorktreeName(requestID string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(requestID) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return "stage-" + b.String()
}

// WorktreeDir is where Claude puts a session's worktree.
func WorktreeDir(project, name string) string {
	return filepath.Join(project, ".claude", "worktrees", name)
}

// Attached are a launch's images as written: their folder and files, in
// order (none: no images).
type Attached struct {
	Dir   string
	Paths []string
}

// Args are the CLI arguments of a background launch (ADR 0005 decision 2).
// settings is the one --settings value, a file or JSON (launchSettings; ""
// for none): Claude takes a single --settings, which also lets Claude read
// the images. The prompt follows "--", so a prompt starting with "-" is not
// a flag.
func Args(in execution.Intent, att Attached, settings string) []string {
	args := []string{"--bg"}
	if t := strings.TrimSpace(in.Title); t != "" {
		args = append(args, "-n", t)
	}
	if in.Config.Model != "" {
		args = append(args, "--model", in.Config.Model)
	}
	for _, o := range in.Config.Options {
		if o.ID == optionEffort && o.Value.Str != "" {
			args = append(args, "--effort", o.Value.Str)
		}
	}
	if in.Config.Permission != "" {
		args = append(args, "--permission-mode", in.Config.Permission)
	}
	if in.Workspace.Kind == workspaceWorktree {
		args = append(args, "-w", WorktreeName(in.RequestID))
	}
	if settings != "" {
		args = append(args, "--settings", settings)
	}
	prompt := in.Prompt
	if len(att.Paths) > 0 {
		prompt += "\n\n" + ImagePrompt(in.Images, att.Paths)
	}
	return append(args, "--", prompt)
}

// validate checks the intent before anything runs; a failure is a
// rejection (nothing started).
func (a *Adapter) validate(ctx context.Context, in execution.Intent) error {
	switch {
	case in.ConnectionID != a.conn:
		return errors.New("launch for another connection")
	case in.Workspace.Kind != workspaceRoot && in.Workspace.Kind != workspaceWorktree:
		return fmt.Errorf("Claude Code can't run in a %q workspace; choose the project folder or a Claude Code worktree", in.Workspace.Kind)
	case in.Workspace.Branch != "" || (in.Workspace.Base != "" && in.Workspace.Kind != workspaceWorktree):
		return errors.New(reasonBase)
	case !filepath.IsAbs(in.Directory):
		return fmt.Errorf("workspace %q is not an absolute folder", in.Directory)
	}
	if err := checkImages(in.Images); err != nil {
		return err
	}
	if fi, err := os.Stat(in.Directory); err != nil || !fi.IsDir() {
		return fmt.Errorf("the folder %s doesn't exist on this computer", in.Directory)
	}
	f, err := a.Features(ctx)
	if err != nil {
		return err
	}
	return f.ValidateConfig(in.Config)
}

var backgrounded = regexp.MustCompile(`backgrounded · ([0-9a-f]{8})`)

// ParseBackgrounded is the short id `claude --bg` prints.
func ParseBackgrounded(stdout string) (string, bool) {
	m := backgrounded.FindStringSubmatch(stdout)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// untrusted is the CLI's refusal of a workspace its user never trusted;
// exit 1 before anything starts (ADR 0005 E1).
const untrusted = "Workspace not trusted"

// Launch starts the background session once (the coordinator recorded the
// dispatch first) and maps its short id to the session through the session
// list. Images are written to files first, and what earlier versions wrote
// in the project is removed (best effort). Rejected: the intent is invalid,
// its images can't be written, claude could not start, or Claude refused the
// folder; the images written are removed. Any other failure is uncertain and
// keeps them: the coordinator finds the session by the intent's marker
// (Correlate).
func (a *Adapter) Launch(ctx context.Context, in execution.Intent, _ execution.Checkpoint, _ execution.SaveCheckpoint) (execution.Observation, error) {
	if err := a.validate(ctx, in); err != nil {
		return execution.Observation{}, &execution.Rejected{Reason: err.Error()}
	}
	if fi, err := os.Stat(a.cli.Bin); err != nil || fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
		return execution.Observation{}, &execution.Rejected{Reason: fmt.Sprintf("Stage can't run %s. Check the Claude command in Settings.", a.cli.Bin)}
	}
	if err := removeLegacyImages(in.Directory); err != nil {
		a.log.Warn("claude: remove old project images", "dir", LegacyImageRoot(in.Directory), "err", err)
	}
	att, err := a.attach(in)
	if err != nil {
		return execution.Observation{}, &execution.Rejected{Reason: err.Error()}
	}
	reject := func(reason string) (execution.Observation, error) {
		if att.Dir != "" {
			if err := os.RemoveAll(att.Dir); err != nil {
				a.log.Warn("claude: remove rejected launch images", "dir", att.Dir, "err", err)
			}
		}
		return execution.Observation{}, &execution.Rejected{Reason: reason}
	}
	call, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// A base is a branch: launch from its checkout so Claude's worktree
	// starts at its HEAD (launchSettings).
	dir := in.Directory
	if in.Workspace.Base != "" {
		if dir, err = baseCheckout(call, in.Directory, in.Workspace.Base); err != nil {
			return reject(err.Error())
		}
	}
	res, err := a.cli.Launch(call, dir, Args(in, att, a.launchSettings(in.Workspace.Base != "", len(att.Paths) > 0))...)
	var start *StartError
	if errors.As(err, &start) {
		return reject(fmt.Sprintf("Stage can't run %s (%v). Check the Claude command in Settings.", a.cli.Bin, start.Err))
	}
	if err != nil {
		return execution.Observation{}, fmt.Errorf("claude --bg: %w", err)
	}
	if res.Code != 0 {
		if out := res.Stderr + res.Stdout; strings.Contains(out, untrusted) {
			return reject(fmt.Sprintf("Claude Code doesn't trust %s yet. Run claude in that folder once and accept the trust prompt, then start the task again.", in.Directory))
		}
		return execution.Observation{}, exitError("--bg", res)
	}
	short, ok := ParseBackgrounded(res.Stdout)
	if !ok {
		return execution.Observation{}, fmt.Errorf("claude --bg printed no session id: %s", excerpt(res.Stdout))
	}
	return a.awaitListed(ctx, short)
}

// attach prunes old image folders and writes the launch's images under the
// image root.
func (a *Adapter) attach(in execution.Intent) (Attached, error) {
	if len(in.Images) == 0 {
		return Attached{}, nil
	}
	root := a.imageRoot
	if root == "" {
		return Attached{}, errors.New(reasonImages)
	}
	if err := pruneImages(root, a.now().Add(-imageRetention)); err != nil {
		a.log.Warn("claude: prune launch images", "dir", root, "err", err)
	}
	dir := ImageDir(root, in.RequestID)
	fail := func(err error) (Attached, error) {
		_ = os.RemoveAll(dir)
		return Attached{}, fmt.Errorf("Stage can't write the task's images to %s for Claude Code: %v", root, err)
	}
	paths, err := writeImages(dir, in.Images)
	if err != nil {
		return fail(err)
	}
	return Attached{Dir: dir, Paths: paths}, nil
}

// awaitListed waits for the launched session to appear in the session list.
func (a *Adapter) awaitListed(ctx context.Context, short string) (execution.Observation, error) {
	deadline := a.now().Add(a.launchWait)
	for {
		es, err := a.roster.list(ctx, true)
		if err == nil {
			if e, ok := findShort(es, short); ok {
				s, err := a.read(ctx, e.SessionID, false)
				if err != nil {
					return execution.Observation{}, err
				}
				return a.observation(s)
			}
		}
		if !a.now().Before(deadline) {
			return execution.Observation{}, fmt.Errorf("claude started session %s, but its session list does not show it yet", short)
		}
		select {
		case <-ctx.Done():
			return execution.Observation{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Correlate finds the session a launch created by the marker in its first
// prompt: among listed background sessions in the launch folder (or its
// worktree) and the transcripts of those folders. It never launches.
func (a *Adapter) Correlate(ctx context.Context, in execution.Intent) (execution.Observation, error) {
	if in.Marker == "" {
		return execution.Observation{}, errors.New("launch has no marker")
	}
	dirs := []string{in.Directory}
	if in.Workspace.Kind == workspaceWorktree {
		dirs = append(dirs, WorktreeDir(in.Directory, WorktreeName(in.RequestID)))
	}
	es, err := a.roster.list(ctx, true)
	if err != nil {
		return execution.Observation{}, err
	}
	root := a.projects(ctx)
	if root == "" {
		return execution.Observation{}, errors.New("Claude Code's transcript folder is unknown")
	}
	candidates := map[string]bool{}
	for _, e := range es {
		for _, d := range dirs {
			if e.Background() && e.Cwd == d {
				candidates[e.SessionID] = true
			}
		}
	}
	for _, d := range dirs {
		ms, _ := filepath.Glob(filepath.Join(root, EncodeProject(d), "*.jsonl"))
		for _, m := range ms {
			if id := strings.TrimSuffix(filepath.Base(m), ".jsonl"); validUUID(id) {
				candidates[id] = true
			}
		}
	}
	var found []string
	for id := range candidates {
		e, _ := findSession(es, id)
		p, ok := transcriptPath(root, e.Cwd, id)
		if !ok {
			continue
		}
		if h, err := ReadHead(p); err == nil && strings.Contains(h.FirstPrompt, in.Marker) {
			found = append(found, id)
		}
	}
	switch len(found) {
	case 0:
		return execution.Observation{}, execution.ErrNotFound
	case 1:
	default:
		return execution.Observation{}, fmt.Errorf("several Claude sessions carry this launch's marker (%s); link the right one", strings.Join(found, ", "))
	}
	s, err := a.read(ctx, found[0], false)
	if err != nil {
		return execution.Observation{}, err
	}
	return a.observation(s)
}
