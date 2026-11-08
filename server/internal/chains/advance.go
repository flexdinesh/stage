// Package chains runs sequential task chains: the next step launches only
// once the previous step's thread is settled in T3 (docs/api.md "Chain rules").
package chains

import "github.com/flexdinesh/t3-stage/server/internal/db"

// Step is the advancement-relevant view of a chain step.
type Step struct {
	TaskID int64
	State  db.TaskState
}

type ActionKind int

const (
	ActionNone   ActionKind = iota
	ActionLaunch            // launch Action.TaskID (new thread)
	ActionDone              // no step left: chain done
	ActionPause             // current step failed/interrupted: pause chain
)

func (k ActionKind) String() string {
	switch k {
	case ActionLaunch:
		return "launch"
	case ActionDone:
		return "done"
	case ActionPause:
		return "pause"
	}
	return "none"
}

type Action struct {
	Kind   ActionKind
	TaskID int64 // ActionLaunch / ActionPause: the current step
}

// Passed: settled, or cancelled (skipped / rolled back).
func Passed(s db.TaskState) bool { return s == db.StateSettled || s == db.StateCancelled }

// Current returns the first step not passed.
func Current(steps []Step) (Step, bool) {
	for _, s := range steps {
		if !Passed(s.State) {
			return s, true
		}
	}
	return Step{}, false
}

// Advance decides the runner's next action. Only running chains launch or
// pause; running or paused chains with every step passed become done; idle
// and done chains never act.
func Advance(state db.ChainState, steps []Step) Action {
	if state != db.ChainRunning && state != db.ChainPaused {
		return Action{}
	}
	cur, ok := Current(steps)
	if !ok {
		return Action{Kind: ActionDone}
	}
	if state != db.ChainRunning {
		return Action{}
	}
	switch cur.State {
	case db.StateBacklog:
		return Action{Kind: ActionLaunch, TaskID: cur.TaskID}
	case db.StateFailed, db.StateInterrupted:
		return Action{Kind: ActionPause, TaskID: cur.TaskID}
	}
	// launching | running | needs_input | completed: wait for settle.
	return Action{}
}

// StepsOf converts chain tasks to steps (already ordered by position).
func StepsOf(tasks []db.Task) []Step {
	out := make([]Step, len(tasks))
	for i, t := range tasks {
		out[i] = Step{TaskID: t.ID, State: t.State}
	}
	return out
}

// CurrentTaskID is the chain's currentTaskId (nil when none).
func CurrentTaskID(tasks []db.Task) *int64 {
	cur, ok := Current(StepsOf(tasks))
	if !ok {
		return nil
	}
	return &cur.TaskID
}
