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
		{"http://127.0.0.1:7380/sessions/thread-abc", Ref{Kind: KindSession, SessionID: "thread-abc"}},
		{"http://host/sessions/codex%3Aabc%25x", Ref{Kind: KindSession, SessionID: "codex:abc%x"}},
		{"/tasks/3", Ref{Kind: KindTask, TaskID: 3}},
		{"https://proxy.lan/stage/tasks/9", Ref{Kind: KindTask, TaskID: 9}},
		{"[stage task #12: Fix [login] \\] flake](http://localhost:7380/tasks/12)", Ref{Kind: KindTask, TaskID: 12}},
		{"[stage session: Fix](http://localhost:7380/sessions/t1)", Ref{Kind: KindSession, SessionID: "t1"}},
		{"<http://localhost:7380/tasks/4>", Ref{Kind: KindTask, TaskID: 4}},
		{"stage-dev://connections/2/sessions/ses_abc", Ref{Kind: KindSession, ConnectionID: 2, SessionID: "ses_abc"}},
		{"stage://tasks/12", Ref{Kind: KindTask, TaskID: 12}},
		{"stage://localhost/tasks/12", Ref{Kind: KindTask, TaskID: 12}},
		{"stage-dev://sessions/codex%3Aabc", Ref{Kind: KindSession, SessionID: "codex:abc"}},
		{"Stage://Tasks/3?view=transcript", Ref{Kind: KindTask, TaskID: 3}},
		{"[stage task #8: Fix](stage://tasks/8)", Ref{Kind: KindTask, TaskID: 8}},
		{"https://stage.tail.ts.net/trees/4?task=9", Ref{Kind: KindTree, TreeID: 4}},
		{"https://proxy.lan/stage/trees/4", Ref{Kind: KindTree, TreeID: 4}},
		{"/trees/4", Ref{Kind: KindTree, TreeID: 4}},
		{"[stage tree #4: Ship [auth\\]](http://localhost:7380/trees/4)", Ref{Kind: KindTree, TreeID: 4}},
		{"stage://trees/4", Ref{Kind: KindTree, TreeID: 4}},
		{"stage-dev://localhost/trees/4", Ref{Kind: KindTree, TreeID: 4}},
		{"https://stage.tail.ts.net/schedules/5?tab=runs#x", Ref{Kind: KindSchedule, ScheduleID: 5}},
		{"/schedules/5", Ref{Kind: KindSchedule, ScheduleID: 5}},
		{"[stage schedule #5: Nightly](http://localhost:7380/schedules/5)", Ref{Kind: KindSchedule, ScheduleID: 5}},
		{"stage://schedules/5", Ref{Kind: KindSchedule, ScheduleID: 5}},
		{"stage-dev://localhost/schedules/5", Ref{Kind: KindSchedule, ScheduleID: 5}},
		{"https://stage.tail.ts.net/projects/2", Ref{Kind: KindProject, ProjectID: 2}},
		{"https://stage.tail.ts.net/projects/2?archived=true", Ref{Kind: KindProject, ProjectID: 2}},
		{"/projects/2", Ref{Kind: KindProject, ProjectID: 2}},
		{"[stage project #2: App](http://localhost:7380/projects/2)", Ref{Kind: KindProject, ProjectID: 2}},
		{"stage://projects/2", Ref{Kind: KindProject, ProjectID: 2}},
		{"stage-dev://localhost/projects/2", Ref{Kind: KindProject, ProjectID: 2}},
		{"https://stage.tail.ts.net/tasks/12?entry=30#e", Ref{Kind: KindJournal, TaskID: 12, EntryID: 30}},
		{"/tasks/12?view=journal&entry=30", Ref{Kind: KindJournal, TaskID: 12, EntryID: 30}},
		{"[stage note #30](http://localhost:7380/tasks/12?entry=30)", Ref{Kind: KindJournal, TaskID: 12, EntryID: 30}},
		{"stage://tasks/12?entry=30", Ref{Kind: KindJournal, TaskID: 12, EntryID: 30}},
		{"https://proxy.lan/stage/trees/4?entry=31", Ref{Kind: KindJournal, TreeID: 4, EntryID: 31}},
		{"/trees/4?task=9&entry=31", Ref{Kind: KindJournal, TreeID: 4, EntryID: 31}},
		{"stage-dev://localhost/trees/4?entry=31", Ref{Kind: KindJournal, TreeID: 4, EntryID: 31}},
		{"thread-abc", Ref{Kind: KindSession, SessionID: "thread-abc"}},
		{"codex:019a-ff", Ref{Kind: KindSession, SessionID: "codex:019a-ff"}},
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
		"http://localhost:7380/projects/0",
		"http://localhost:7380/projects/abc",
		"http://localhost:7380/schedules/0",
		"http://localhost:7380/schedules/abc",
		"stage://schedules",
		"stage://schedules/-1",
		"stage://projects",
		"stage://projects/x",
		"http://localhost:7380/tasks/12?entry=",
		"http://localhost:7380/tasks/12?entry=0",
		"http://localhost:7380/tasks/12?entry=abc",
		"/trees/4?entry=-1",
		"stage://tasks/12?entry=x",
		"stage://trees/4?entry=0",
		// A harness's own links resolve through its adapter
		// (execution.LinkResolver), never here.
		"http://localhost:3773/env-1/thread-9",
		"t3code://threads/env-1/thread-9",
		"https://github.com/o/r/pull/12",
		// Schedule and project paths must be the whole path.
		"https://github.com/orgs/acme/projects/3",
		"https://github.com/users/dee/projects/3",
		"https://proxy.lan/stage/schedules/5",
		"t3code://projects/x/y",
		"stage://",
		"stage://tasks",
		"stage://tasks/abc",
		"http://localhost:7380/trees/0",
		"http://localhost:7380/trees/abc",
		"stage://trees",
		"stage://trees/-1",
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
