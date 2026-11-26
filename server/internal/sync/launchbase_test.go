package sync_test

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestPredecessorBaseAvailability(t *testing.T) {
	cases := []struct {
		branch, path string
		prs          []t3.PullRequest
		want         bool
	}{
		{"feat/api", "/w/wt", nil, true},
		{" feat/api ", "/w/wt", nil, true},
		{"main", "", nil, false},
		{"", "/w/wt", nil, false},
		{"feat/api", "/w/wt", []t3.PullRequest{{State: "merged"}}, false},
		{"feat/api", "/w/wt", []t3.PullRequest{{State: "merged"}, {State: "open"}}, true},
	}
	for _, c := range cases {
		o := (t3.ThreadDetail{Thread: t3.Thread{Status: t3.StatusIdle, PullRequests: c.prs}, Branch: c.branch, WorktreePath: c.path}).Observation()
		if o.Base.Available != c.want || !c.want && o.Base.Reason == "" {
			t.Fatalf("%+v", o.Base)
		}
	}
}
