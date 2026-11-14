package sync_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestResolveBase(t *testing.T) {
	id := int64(12)
	task := db.HandoffSource{TaskID: &id, ThreadID: "th-1", Title: "Build API"}
	session := db.HandoffSource{ThreadID: "th-2", Title: "Planner"}
	wt := func(project, branch, path string, prs ...t3.PullRequest) t3.ThreadDetail {
		return t3.ThreadDetail{Thread: t3.Thread{ProjectID: project, PullRequests: prs}, Branch: branch, WorktreePath: path}
	}
	cases := []struct {
		name         string
		src          db.HandoffSource
		d            t3.ThreadDetail
		branch, want string
	}{
		{"worktree branch", task, wt("p1", "feat/api", "/w/wt"), "feat/api", ""},
		{"trims branch", task, wt("p1", " feat/api ", "/w/wt"), "feat/api", ""},
		{"unknown project is trusted", task, wt("", "feat/api", "/w/wt"), "feat/api", ""},
		{"open and merged PRs", task, wt("p1", "feat/api", "/w/wt", t3.PullRequest{State: "merged"}, t3.PullRequest{State: "open"}), "feat/api", ""},
		{"closed PR", task, wt("p1", "feat/api", "/w/wt", t3.PullRequest{State: "closed"}), "feat/api", ""},
		{"other project", task, wt("p2", "feat/api", "/w/wt"), "", "#12 ran in another project"},
		{"root", task, wt("p1", "main", ""), "", "#12 ran at project root"},
		{"no branch", task, wt("p1", "", "/w/wt"), "", "#12 has no branch"},
		{"all merged", task, wt("p1", "feat/api", "/w/wt", t3.PullRequest{State: "merged"}, t3.PullRequest{State: "MERGED"}), "", "#12's pull requests are merged"},
		{"session label", session, wt("p1", "", ""), "", `session "Planner" ran at project root`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			branch, reason := t3sync.ResolveBase("p1", c.src, c.d)
			if branch != c.branch || reason != c.want {
				t.Fatalf("got (%q, %q), want (%q, %q)", branch, reason, c.branch, c.want)
			}
		})
	}
}

func TestBaseRefRejected(t *testing.T) {
	rejected := &t3.ToolError{Tool: "t3_thread_launch", Code: "invalid_request",
		Message: `Base ref "feat/a" does not resolve to a commit in project "P" (/w). No thread was created. Pass a branch, tag, or commit that exists there, or set startFromOrigin:true if it exists only on a remote.`}
	cases := []struct {
		name string
		err  error
		ref  string
		want bool
	}{
		{"rejected", rejected, "feat/a", true},
		{"wrapped", fmt.Errorf("launch: %w", rejected), "feat/a", true},
		{"other ref", rejected, "feat/b", false},
		{"prefix ref", rejected, "feat", false},
		{"other code", &t3.ToolError{Code: "unavailable", Message: rejected.Message}, "feat/a", false},
		{"other invalid request", &t3.ToolError{Code: "invalid_request", Message: "bad model"}, "feat/a", false},
		{"transport", errors.New("connection reset"), "feat/a", false},
		{"nil", nil, "feat/a", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := t3sync.BaseRefRejected(c.err, c.ref); got != c.want {
				t.Fatalf("BaseRefRejected = %v, want %v", got, c.want)
			}
		})
	}
}
