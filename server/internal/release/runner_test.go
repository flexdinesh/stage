package release

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

func TestOnTransitionKicksForLeavingBacklog(t *testing.T) {
	r := NewRunner(nil, nil, nil)
	r.OnTransition(t.Context(), workevent.TaskChange{From: db.StateRunning, To: db.StateDone})
	select {
	case <-r.kick:
		t.Fatal("finished execution kicked tree release")
	default:
	}
	r.OnTransition(t.Context(), workevent.TaskChange{From: db.StateBacklog, To: db.StateLaunching})
	select {
	case <-r.kick:
	default:
		t.Fatal("leaving backlog did not kick tree release")
	}
}
