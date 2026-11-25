package claudecode

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// A predecessor base is Claude's own worktree (docs/adr/0005 E10): Stage
// launches `claude --bg -w <name>` from the checkout that has the
// predecessor's branch checked out, with worktree.baseRef "head", so Claude
// creates, names and owns a worktree that starts from that branch's tip.
// Stage makes no worktree itself.

// headSettings makes Claude start the new worktree from the launch
// checkout's HEAD instead of origin's default branch.
const headSettings = `{"worktree":{"baseRef":"head"}}`

// launchSettings is a launch's one --settings value: the hook settings
// file (its head variant for a based launch), else headSettings for a
// based launch without hooks, else none.
func (a *Adapter) launchSettings(based bool) string {
	switch {
	case a.hooks != nil && based:
		return a.hooks.HeadSettingsPath()
	case a.hooks != nil:
		return a.hooks.SettingsPath()
	case based:
		return headSettings
	}
	return ""
}

// checkoutBranch is the branch a checkout has checked out, read without a
// git binary: <dir>/.git (a directory, or a worktree's "gitdir:" file),
// then its HEAD. "" is a detached HEAD.
func checkoutBranch(dir string) (string, error) {
	gitDir := filepath.Join(dir, ".git")
	fi, err := os.Stat(gitDir)
	if err != nil {
		return "", err
	}
	if fi.Mode().IsRegular() {
		b, err := os.ReadFile(gitDir)
		if err != nil {
			return "", err
		}
		p, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
		if !ok {
			return "", fmt.Errorf("%s is not a git worktree link", gitDir)
		}
		if gitDir = strings.TrimSpace(p); !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(dir, gitDir)
		}
	}
	b, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", err
	}
	branch, _ := strings.CutPrefix(strings.TrimSpace(string(b)), "ref: refs/heads/")
	if branch == strings.TrimSpace(string(b)) {
		return "", nil
	}
	return branch, nil
}

// baseAvailability says whether a session can supply a predecessor base:
// it ran in a Claude worktree that still has its branch checked out.
// Merged branches are not detected (Claude reports no pull requests).
func baseAvailability(cwd, branch string) execution.Availability {
	switch {
	case cwd == "" || projectOf(cwd) == cwd:
		return execution.Availability{Reason: "Session ran in the project folder, not a Claude Code worktree."}
	case branch == "":
		return execution.Availability{Reason: "Session has no branch."}
	}
	head, err := checkoutBranch(cwd)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return execution.Availability{Reason: "The predecessor's worktree was removed."}
	case err != nil:
		return execution.Availability{Reason: "Stage can't read the predecessor's worktree: " + err.Error()}
	case head != branch:
		now := head
		if now == "" {
			now = "a detached HEAD"
		}
		return execution.Availability{Reason: fmt.Sprintf("The predecessor's worktree is now on %s, not %s.", now, branch)}
	}
	return execution.Availability{Available: true}
}

// baseCheckout is the checkout of project's repository that has branch
// checked out (`git worktree list --porcelain`). An error means nothing can
// start: the launch is rejected with it.
func baseCheckout(ctx context.Context, project, branch string) (string, error) {
	c := exec.CommandContext(ctx, "git", "-C", project, "worktree", "list", "--porcelain")
	c.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_OPTIONAL_LOCKS=0"}
	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("Stage can't list the worktrees of %s (git worktree list: %v)", project, err)
	}
	dirs := worktreesOn(string(out), branch)
	for _, d := range dirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d, nil
		}
	}
	return "", fmt.Errorf("no checkout of %s has branch %s checked out. Claude Code starts a worktree from a checkout's HEAD: restore the predecessor's worktree, or choose a Claude Code worktree without a predecessor", project, branch)
}

// worktreesOn parses `git worktree list --porcelain` for the worktrees
// with branch checked out.
func worktreesOn(porcelain, branch string) []string {
	var dirs []string
	dir := ""
	sc := bufio.NewScanner(strings.NewReader(porcelain))
	for sc.Scan() {
		line := sc.Text()
		if d, ok := strings.CutPrefix(line, "worktree "); ok {
			dir = d
		} else if b, ok := strings.CutPrefix(line, "branch "); ok && b == "refs/heads/"+branch && dir != "" {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}
