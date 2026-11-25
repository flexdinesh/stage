package api

import (
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// Workspace validation follows each harness descriptor's worktree policy,
// never its name: T3 takes a base ref, branch and predecessor; OpenCode and
// Codex take none; Claude Code takes a predecessor without a ref or branch.
func TestWorkspaceFollowsWorktreePolicy(t *testing.T) {
	branch := "feat"
	need := int64(4)
	ref := db.Workspace{Type: "worktree", BaseRef: "origin/main", Branch: &branch}
	predRef := db.Workspace{Type: "worktree", BaseRef: "origin/main", Base: db.BasePredecessor, BaseTaskID: &need}
	plain := db.Workspace{Type: "worktree"}
	pred := db.Workspace{Type: "worktree", Base: db.BasePredecessor, BaseTaskID: &need}
	root := db.Workspace{Type: "root"}
	rootPred := db.Workspace{Type: "root", Base: db.BasePredecessor}
	named := db.Workspace{Type: "worktree", Branch: &branch, Base: db.BasePredecessor}
	cases := []struct {
		name  string
		rule  worktreeRule
		ok    []db.Workspace
		fails map[string]db.Workspace // want error text -> workspace
	}{
		{"T3", rule(t3.Descriptor.Name, t3.Descriptor.Worktree), []db.Workspace{ref, predRef, root},
			map[string]db.Workspace{"baseRef is required": plain, "needs type worktree": rootPred}},
		{"OpenCode", rule(opencode.Descriptor.Name, opencode.Descriptor.Worktree), []db.Workspace{plain, root},
			map[string]db.Workspace{"OpenCode manages the worktree base": pred, "without a ref, branch or predecessor": ref}},
		{"Codex", rule(codex.Descriptor.Name, codex.Descriptor.Worktree), []db.Workspace{plain, root},
			map[string]db.Workspace{"Codex manages the worktree base": pred}},
		{"Claude Code", rule(claudecode.Descriptor.Name, claudecode.Descriptor.Worktree), []db.Workspace{plain, pred, root},
			map[string]db.Workspace{"Claude Code manages the worktree base": ref, "a predecessor's branch, without a ref or branch": named, "needs type worktree": rootPred}},
	}
	for _, c := range cases {
		for _, w := range c.ok {
			if _, err := validateWorkspaceFor(w, 1, c.rule); err != nil {
				t.Errorf("%s %+v: %v", c.name, w, err)
			}
		}
		for want, w := range c.fails {
			if _, err := validateWorkspaceFor(w, 1, c.rule); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s %+v: want %q, got %v", c.name, w, want, err)
			}
		}
	}
	got, err := validateWorkspaceFor(pred, 1, rule(claudecode.Descriptor.Name, claudecode.Descriptor.Worktree))
	if err != nil || got.Base != db.BasePredecessor || got.BaseRef != "" || got.Branch != nil || got.BaseTaskID == nil || *got.BaseTaskID != need {
		t.Fatalf("Claude predecessor kept %+v %v", got, err)
	}
}

func rule(name string, p harness.Worktree) worktreeRule {
	return func(int64) (string, harness.Worktree) { return name, p }
}
