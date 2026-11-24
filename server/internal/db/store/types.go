package store

import "slices"

// TaskState values (docs/api.md TaskState).
type TaskState string

const (
	StateBacklog     TaskState = "backlog"
	StateLaunching   TaskState = "launching"
	StateRunning     TaskState = "running"
	StateNeedsInput  TaskState = "needs_input"
	StateDone        TaskState = "done"
	StateSettled     TaskState = "settled"
	StateFailed      TaskState = "failed"
	StateInterrupted TaskState = "interrupted"
	StateCancelled   TaskState = "cancelled"
)

var TaskStates = []TaskState{
	StateBacklog, StateLaunching, StateRunning, StateNeedsInput, StateDone,
	StateSettled, StateFailed, StateInterrupted, StateCancelled,
}

func (s TaskState) Valid() bool { return slices.Contains(TaskStates, s) }

// Finished: entering one of these sets finishedAt (first entry per attempt).
func (s TaskState) Finished() bool {
	switch s {
	case StateDone, StateFailed, StateInterrupted, StateCancelled:
		return true
	}
	return false
}

// Active: a thread is (or is about to be) working; delete/edit restricted.
func (s TaskState) Active() bool {
	switch s {
	case StateLaunching, StateRunning, StateNeedsInput:
		return true
	}
	return false
}
