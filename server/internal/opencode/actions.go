package opencode

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// ActionArchive archives a tracked session in OpenCode (session.update
// with archived: true). User-triggered, never automatic; the archive
// reaches Stage as an observed closure.
const ActionArchive = "archive"

var _ execution.Actor = (*Adapter)(nil)

const archiveUnsupported = "This OpenCode version cannot archive sessions."

func sessionActions(contract serverContract) []execution.Action {
	availability := execution.Availability{Reason: archiveUnsupported}
	if contract.archive {
		availability = available
	}
	return []execution.Action{{
		ID: ActionArchive, Label: "Archive in OpenCode", Target: execution.TargetSession,
		Confirm:      "OpenCode archives this session. If this connection infers acceptance from harness closure, Stage completes the task.",
		Callers:      []execution.Caller{execution.CallerUser, execution.CallerAgent},
		Availability: availability,
	}}
}

// sessionActionStates is an observed session's availability of the
// declared session actions.
func sessionActionStates(archived bool) []execution.ActionState {
	if !archived {
		return nil
	}
	return []execution.ActionState{{ID: ActionArchive, Availability: execution.Availability{Reason: "Already archived in OpenCode."}}}
}

// Act runs a declared session or project action.
func (a *Adapter) Act(ctx context.Context, r execution.ActionRequest) (execution.ActionResult, error) {
	if r.Target == execution.TargetProject {
		return a.actOnProject(ctx, r)
	}
	if r.Target != execution.TargetSession || r.Session == nil || !a.valid(*r.Session) {
		return execution.ActionResult{}, &execution.Invalid{Reason: "An OpenCode action needs a session on this connection."}
	}
	if r.ID != ActionArchive {
		return execution.ActionResult{}, &execution.Invalid{Reason: "Unknown OpenCode action."}
	}
	contract, err := a.checkContract(ctx)
	if err != nil {
		return execution.ActionResult{}, err
	}
	if !contract.archive {
		return execution.ActionResult{}, fmt.Errorf("%w: %s", execution.ErrUnavailable, archiveUnsupported)
	}
	err = a.request(ctx, http.MethodPatch, "/api/session/"+url.PathEscape(r.Session.ExternalSessionID), map[string]bool{"archived": true}, nil)
	var status responseError
	switch {
	case missing(err):
		return execution.ActionResult{}, execution.ErrNotFound
	case errors.As(err, &status) && status.code == http.StatusBadRequest:
		return execution.ActionResult{}, &execution.Busy{Reason: "OpenCode refused to archive this session."}
	case err != nil:
		return execution.ActionResult{}, err
	}
	return execution.ActionResult{Message: "Archived in OpenCode."}, nil
}
