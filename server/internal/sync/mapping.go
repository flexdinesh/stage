// Package sync keeps Postgres in step with T3: full project/thread mirror
// sync, the active task poller, the capabilities catalog cache, and task
// launch (the only places task state follows T3).
package sync

import (
	"github.com/flexdinesh/t3-stage/server/internal/db"
	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

// MapThreadState derives a task state from a T3 thread (docs/api.md "State
// mapping"). latestRun is recentRuns[0].status ("" when absent). Returns
// current when no rule applies (idle without runs).
func MapThreadState(current db.TaskState, status t3.ThreadStatus, settled bool, pendingRequests int, latestRun t3.ThreadStatus) db.TaskState {
	if settled {
		return db.StateSettled
	}
	if pendingRequests > 0 || status == t3.StatusWaiting {
		return db.StateNeedsInput
	}
	if s, ok := runState(status); ok {
		return s
	}
	if status == t3.StatusIdle && latestRun != "" {
		if s, ok := runState(latestRun); ok {
			return s
		}
	}
	return current
}

// runState applies rules 3-7.
func runState(s t3.ThreadStatus) (db.TaskState, bool) {
	switch s {
	case t3.StatusPreparing, t3.StatusQueued, t3.StatusStarting, t3.StatusRunning:
		return db.StateRunning, true
	case t3.StatusCompleted:
		return db.StateDone, true
	case t3.StatusFailed:
		return db.StateFailed, true
	case t3.StatusInterrupted:
		return db.StateInterrupted, true
	case t3.StatusCancelled, t3.StatusRolledBack:
		return db.StateCancelled, true
	}
	return "", false
}

// mapDetail applies MapThreadState to a t3_thread_read result.
func mapDetail(current db.TaskState, d t3.ThreadDetail) db.TaskState {
	var latest t3.ThreadStatus
	if len(d.RecentRuns) > 0 {
		latest = d.RecentRuns[0].Status
	}
	return MapThreadState(current, d.Status, d.Settled, d.PendingRequestCount, latest)
}
