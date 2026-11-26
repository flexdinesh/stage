package refs

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Ref
	}{
		{"#12", Ref{Kind: KindTask, TaskID: 12}},
		{"12", Ref{Kind: KindTask, TaskID: 12}},
		{" http://localhost:7380/tasks/12 ", Ref{Kind: KindTask, TaskID: 12}},
		{"https://stage.tail.ts.net/tasks/7?view=transcript#answer", Ref{Kind: KindTask, TaskID: 7}},
		{"http://127.0.0.1:7380/sessions/thread-abc", Ref{Kind: KindSession, ThreadID: "thread-abc"}},
		{"http://host/sessions/codex%3Aabc%25x", Ref{Kind: KindSession, ThreadID: "codex:abc%x"}},
		{"/tasks/3", Ref{Kind: KindTask, TaskID: 3}},
		{"https://proxy.lan/stage/tasks/9", Ref{Kind: KindTask, TaskID: 9}},
		{"[stage task #12: Fix [login] \\] flake](http://localhost:7380/tasks/12)", Ref{Kind: KindTask, TaskID: 12}},
		{"[stage session: Fix](http://localhost:7380/sessions/t1)", Ref{Kind: KindSession, ThreadID: "t1"}},
		{"<http://localhost:7380/tasks/4>", Ref{Kind: KindTask, TaskID: 4}},
		{"stage-dev://connections/2/sessions/ses_abc", Ref{Kind: KindSession, ConnectionID: 2, ThreadID: "ses_abc"}},
		{"stage://tasks/12", Ref{Kind: KindTask, TaskID: 12}},
		{"stage://localhost/tasks/12", Ref{Kind: KindTask, TaskID: 12}},
		{"stage-dev://sessions/codex%3Aabc", Ref{Kind: KindSession, ThreadID: "codex:abc"}},
		{"Stage://Tasks/3?view=transcript", Ref{Kind: KindTask, TaskID: 3}},
		{"[stage task #8: Fix](stage://tasks/8)", Ref{Kind: KindTask, TaskID: 8}},
		{"https://stage.tail.ts.net/trees/4?task=9", Ref{Kind: KindTree, TreeID: 4}},
		{"https://proxy.lan/stage/trees/4", Ref{Kind: KindTree, TreeID: 4}},
		{"/trees/4", Ref{Kind: KindTree, TreeID: 4}},
		{"[stage tree #4: Ship [auth\\]](http://localhost:7380/trees/4)", Ref{Kind: KindTree, TreeID: 4}},
		{"stage://trees/4", Ref{Kind: KindTree, TreeID: 4}},
		{"stage-dev://localhost/trees/4", Ref{Kind: KindTree, TreeID: 4}},
		{"thread-abc", Ref{Kind: KindSession, ThreadID: "thread-abc"}},
		{"codex:019a-ff", Ref{Kind: KindSession, ThreadID: "codex:019a-ff"}},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"#0",
		"#-1",
		"http://localhost:7380/",
		"http://localhost:7380/tasks/abc",
		"http://localhost:7380/projects/2",
		// A harness's own links resolve through its adapter
		// (execution.LinkResolver), never here.
		"http://localhost:3773/env-1/thread-9",
		"t3code://threads/env-1/thread-9",
		"https://github.com/o/r/pull/12",
		"t3code://projects/x/y",
		"stage://",
		"stage://tasks",
		"stage://tasks/abc",
		"http://localhost:7380/trees/0",
		"http://localhost:7380/trees/abc",
		"stage://trees",
		"stage://trees/-1",
		"stage://projects/2",
		"stage://localhost/tasks/1/x",
		"stagex://tasks/1",
		"mygo://localhost/tasks/1",
		"two words",
		"a/b",
		"/settings",
	} {
		if got, err := Parse(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) = %+v, %v; want ErrInvalid", in, got, err)
		}
	}
}
