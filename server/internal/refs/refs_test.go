package refs

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	const t3 = "http://localhost:3773"
	cases := []struct {
		in   string
		t3   string
		want Ref
	}{
		{"#12", t3, Ref{Kind: KindTask, TaskID: 12}},
		{"12", t3, Ref{Kind: KindTask, TaskID: 12}},
		{" http://localhost:7380/tasks/12 ", t3, Ref{Kind: KindTask, TaskID: 12}},
		{"https://stage.tail.ts.net/tasks/7?view=transcript#answer", t3, Ref{Kind: KindTask, TaskID: 7}},
		{"http://127.0.0.1:7380/sessions/thread-abc", t3, Ref{Kind: KindSession, ThreadID: "thread-abc"}},
		{"http://host/sessions/codex%3Aabc%25x", t3, Ref{Kind: KindSession, ThreadID: "codex:abc%x"}},
		{"/tasks/3", t3, Ref{Kind: KindTask, TaskID: 3}},
		{"https://proxy.lan/stage/tasks/9", t3, Ref{Kind: KindTask, TaskID: 9}},
		{"[stage task #12: Fix [login] \\] flake](http://localhost:7380/tasks/12)", t3, Ref{Kind: KindTask, TaskID: 12}},
		{"[stage session: Fix](http://localhost:7380/sessions/t1)", t3, Ref{Kind: KindSession, ThreadID: "t1"}},
		{"<http://localhost:7380/tasks/4>", t3, Ref{Kind: KindTask, TaskID: 4}},
		{"t3code://threads/env-1/thread-9", t3, Ref{Kind: KindSession, ThreadID: "thread-9"}},
		{"t3code://threads/env-1/codex%3Ax", t3, Ref{Kind: KindSession, ThreadID: "codex:x"}},
		{"http://localhost:3773/env-1/thread-9", t3, Ref{Kind: KindSession, ThreadID: "thread-9"}},
		{"http://localhost:3773/t3/env-1/thread-9", "http://localhost:3773/t3/", Ref{Kind: KindSession, ThreadID: "thread-9"}},
		// A stage path on the T3 host still reads as stage.
		{"http://localhost:3773/tasks/5", t3, Ref{Kind: KindTask, TaskID: 5}},
		{"stage://tasks/12", t3, Ref{Kind: KindTask, TaskID: 12}},
		{"stage://localhost/tasks/12", t3, Ref{Kind: KindTask, TaskID: 12}},
		{"stage-dev://sessions/codex%3Aabc", t3, Ref{Kind: KindSession, ThreadID: "codex:abc"}},
		{"Stage://Tasks/3?view=transcript", t3, Ref{Kind: KindTask, TaskID: 3}},
		{"[stage task #8: Fix](stage://tasks/8)", t3, Ref{Kind: KindTask, TaskID: 8}},
		{"thread-abc", t3, Ref{Kind: KindSession, ThreadID: "thread-abc"}},
		{"codex:019a-ff", t3, Ref{Kind: KindSession, ThreadID: "codex:019a-ff"}},
	}
	for _, c := range cases {
		got, err := Parse(c.in, c.t3)
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
		"http://localhost:3773/env-1/thread-9", // T3 matching off below
		"https://github.com/o/r/pull/12",
		"t3code://projects/x/y",
		"t3code://threads/only-one",
		"stage://",
		"stage://tasks",
		"stage://tasks/abc",
		"stage://projects/2",
		"stage://localhost/tasks/1/x",
		"stagex://tasks/1",
		"mygo://localhost/tasks/1",
		"two words",
		"a/b",
		"/settings",
	} {
		t3 := "http://localhost:3773"
		if in == "http://localhost:3773/env-1/thread-9" {
			t3 = ""
		}
		if got, err := Parse(in, t3); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) = %+v, %v; want ErrInvalid", in, got, err)
		}
	}
}
