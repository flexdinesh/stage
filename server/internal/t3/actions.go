package t3

import (
	"context"
	"errors"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Session actions: T3's own thread organization, relayed through
// t3_thread_organize. Each is user-triggered (users and agents), never
// automatic; a settle the poller observes may complete the task.
const (
	ActionSettle  = "settle"
	ActionArchive = "archive"
)

func sessionActions() []execution.Action {
	callers := []execution.Caller{execution.CallerUser, execution.CallerAgent}
	yes := execution.Availability{Available: true}
	return []execution.Action{
		{
			ID: ActionSettle, Label: "Settle in T3", Target: execution.TargetSession, Callers: callers, Availability: yes,
			Confirm: "T3 settles this session. If this connection infers acceptance from harness closure, Stage completes the task.",
		},
		{
			ID: ActionArchive, Label: "Archive in T3", Target: execution.TargetSession, Callers: callers, Availability: yes,
			Confirm: "T3 archives this session. Stage completes the task only when T3 settles it.",
		},
	}
}

// actionStates mirrors T3's own refusals for the thread's session actions.
func (d ThreadDetail) actionStates(state string) []execution.ActionState {
	var out []execution.ActionState
	switch {
	case d.Archived:
		out = append(out, execution.ActionState{ID: ActionSettle, Availability: execution.Availability{Reason: "Archived in T3. Unarchive it in T3 to settle."}})
	case d.Settled:
		out = append(out, execution.ActionState{ID: ActionSettle, Availability: execution.Availability{Reason: "Already settled in T3."}})
	case state == "running":
		out = append(out, execution.ActionState{ID: ActionSettle, Availability: execution.Availability{Reason: "Still running in T3. Stop it before settling."}})
	case state == "needs_input":
		out = append(out, execution.ActionState{ID: ActionSettle, Availability: execution.Availability{Reason: "Waiting for an answer in T3. Answer it before settling."}})
	}
	if d.Archived {
		out = append(out, execution.ActionState{ID: ActionArchive, Availability: execution.Availability{Reason: "Already archived in T3."}})
	}
	return out
}

func (a *Adapter) Act(ctx context.Context, r execution.ActionRequest) (execution.ActionResult, error) {
	if r.Target == execution.TargetProject {
		return a.actOnProject(ctx, r)
	}
	if r.Target != execution.TargetSession || r.Session == nil || r.Session.ConnectionID != a.ConnectionID {
		return execution.ActionResult{}, &execution.Invalid{Reason: "T3 actions apply to a session on this connection."}
	}
	var done string
	switch r.ID {
	case ActionSettle:
		done = "Settled in T3."
	case ActionArchive:
		done = "Archived in T3."
	default:
		return execution.ActionResult{}, &execution.Invalid{Reason: "Unknown T3 action."}
	}
	err := a.Client.OrganizeThread(ctx, r.Session.ExternalSessionID, r.ID)
	var te *ToolError
	switch {
	case err == nil:
		return execution.ActionResult{Message: done}, nil
	case errors.Is(err, ErrNotFound):
		return execution.ActionResult{}, execution.ErrNotFound
	case errors.As(err, &te) && (te.Code == "orchestration_error" || te.Code == "invalid_request") && te.Message != "":
		// T3 refused it (running, archived, waiting on a question): nothing changed.
		return execution.ActionResult{}, &execution.Busy{Reason: te.Message}
	}
	return execution.ActionResult{}, err
}

var (
	_ execution.Actor             = (*Adapter)(nil)
	_ execution.WorkspaceFeatures = (*Adapter)(nil)
)
