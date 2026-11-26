package t3

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Launch options (harness-wide): a new worktree, its base ref and its
// branch. The adapter translates them into T3's workspaceStrategy.
const (
	OptionWorktree = "worktree"
	OptionBaseRef  = "baseRef"
	OptionBranch   = "branch"
)

// launchOptions are T3's harness-wide launch options; baseRef and branch
// apply only to a new worktree.
func launchOptions() []execution.Option {
	off := execution.BoolValue(false)
	inWorktree := &execution.OptionDependency{Option: OptionWorktree, Value: execution.BoolValue(true)}
	return []execution.Option{
		{ID: OptionWorktree, Kind: execution.OptionBoolean, Label: "New worktree", Values: []execution.OptionValue{execution.BoolValue(true), off}, Default: &off},
		{ID: OptionBaseRef, Kind: execution.OptionText, Label: "Base ref", Values: []execution.OptionValue{}, DependsOn: inWorktree},
		{ID: OptionBranch, Kind: execution.OptionText, Label: "Branch", Values: []execution.OptionValue{}, DependsOn: inWorktree},
	}
}

// splitOptions separates a config's launch options from its model's.
func splitOptions(opts []execution.ModelOption) (launch, model []execution.ModelOption) {
	for _, o := range opts {
		if slices.Contains([]string{OptionWorktree, OptionBaseRef, OptionBranch}, o.ID) {
			launch = append(launch, o)
		} else {
			model = append(model, o)
		}
	}
	return launch, model
}

// validRefs rejects a base ref or branch Git can't take as one word.
func validRefs(c execution.Config) error {
	for _, id := range []string{OptionBaseRef, OptionBranch} {
		v := c.Text(id)
		if strings.HasPrefix(v, "-") || strings.ContainsFunc(v, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
			return fmt.Errorf("%s %q is not a Git ref name", id, v)
		}
	}
	return nil
}

// workspaceStrategy is the workspaceStrategy a config asks T3 for: the
// project checkout, or a new worktree from baseRef (required by T3) on
// branch, started from the remote when baseRef names a remote branch
// (origin/ or upstream/).
func workspaceStrategy(c execution.Config) (WorkspaceStrategy, error) {
	if !c.Bool(OptionWorktree) {
		return WorkspaceStrategy{Type: WorkspaceRoot}, nil
	}
	base := c.Text(OptionBaseRef)
	if base == "" {
		return WorkspaceStrategy{}, errors.New("T3 starts a new worktree from a base ref. Set Base ref (for example origin/main), or turn off New worktree.")
	}
	remote := strings.HasPrefix(base, "origin/") || strings.HasPrefix(base, "upstream/")
	return WorkspaceStrategy{Type: WorkspaceWorktree, BaseRef: base, Branch: c.Text(OptionBranch), StartFromOrigin: &remote}, nil
}
