package db

import (
	"github.com/flexdinesh/stage/server/internal/db/store"
)

// TaskState values (docs/api.md TaskState).
type TaskState = store.TaskState

const (
	StateBacklog     = store.StateBacklog
	StateLaunching   = store.StateLaunching
	StateRunning     = store.StateRunning
	StateNeedsInput  = store.StateNeedsInput
	StateDone        = store.StateDone
	StateSettled     = store.StateSettled
	StateFailed      = store.StateFailed
	StateInterrupted = store.StateInterrupted
	StateCancelled   = store.StateCancelled
)

var TaskStates = store.TaskStates

type (
	Project       = store.Project
	ImportSummary = store.ImportSummary
)

// Workspace bases: ref or predecessor (docs/api.md "Predecessor base").
const (
	BaseRef         = store.BaseRef
	BasePredecessor = store.BasePredecessor
)

type (
	Workspace = store.Workspace
	Task      = store.Task
	TaskEvent = store.TaskEvent
)
