package chains

import (
	"testing"

	"github.com/flexdinesh/t3-stage/server/internal/db"
)

func steps(states ...db.TaskState) []Step {
	out := make([]Step, len(states))
	for i, s := range states {
		out[i] = Step{TaskID: int64(i + 1), State: s}
	}
	return out
}

func TestAdvance(t *testing.T) {
	tests := []struct {
		name  string
		state db.ChainState
		steps []Step
		want  Action
	}{
		{"running first backlog -> launch", db.ChainRunning, steps(db.StateBacklog, db.StateBacklog), Action{ActionLaunch, 1}},
		{"running step1 launching -> wait", db.ChainRunning, steps(db.StateLaunching, db.StateBacklog), Action{}},
		{"running step1 running -> wait", db.ChainRunning, steps(db.StateRunning, db.StateBacklog), Action{}},
		{"running step1 needs_input -> wait", db.ChainRunning, steps(db.StateNeedsInput, db.StateBacklog), Action{}},
		{"running step1 completed (unsettled) -> wait", db.ChainRunning, steps(db.StateDone, db.StateBacklog), Action{}},
		{"running step1 settled -> launch step2", db.ChainRunning, steps(db.StateSettled, db.StateBacklog), Action{ActionLaunch, 2}},
		{"running step1 cancelled (skipped) -> launch step2", db.ChainRunning, steps(db.StateCancelled, db.StateBacklog), Action{ActionLaunch, 2}},
		{"running step1 failed -> pause", db.ChainRunning, steps(db.StateFailed, db.StateBacklog), Action{ActionPause, 1}},
		{"running step2 interrupted -> pause", db.ChainRunning, steps(db.StateSettled, db.StateInterrupted), Action{ActionPause, 2}},
		{"running all passed -> done", db.ChainRunning, steps(db.StateSettled, db.StateCancelled), Action{Kind: ActionDone}},
		{"running no steps -> done", db.ChainRunning, nil, Action{Kind: ActionDone}},
		{"paused backlog -> nothing", db.ChainPaused, steps(db.StateSettled, db.StateBacklog), Action{}},
		{"paused failed -> nothing", db.ChainPaused, steps(db.StateFailed), Action{}},
		{"paused all passed -> done", db.ChainPaused, steps(db.StateSettled, db.StateCancelled), Action{Kind: ActionDone}},
		{"idle backlog -> nothing", db.ChainIdle, steps(db.StateBacklog), Action{}},
		{"idle all passed -> nothing", db.ChainIdle, steps(db.StateSettled), Action{}},
		{"done -> nothing", db.ChainDone, steps(db.StateSettled, db.StateBacklog), Action{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Advance(tt.state, tt.steps); got != tt.want {
				t.Fatalf("Advance(%s, %v) = {%s %d}, want {%s %d}", tt.state, tt.steps, got.Kind, got.TaskID, tt.want.Kind, tt.want.TaskID)
			}
		})
	}
}

func TestCurrentTaskID(t *testing.T) {
	tasks := []db.Task{{ID: 10, State: db.StateSettled}, {ID: 11, State: db.StateDone}, {ID: 12, State: db.StateBacklog}}
	if got := CurrentTaskID(tasks); got == nil || *got != 11 {
		t.Fatalf("got %v, want 11", got)
	}
	if got := CurrentTaskID([]db.Task{{ID: 1, State: db.StateCancelled}}); got != nil {
		t.Fatalf("got %v, want nil", *got)
	}
}
