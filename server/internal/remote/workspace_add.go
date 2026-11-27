package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// codeOutcomeUncertain: the worker's adapter may have added the workspace
// (execution.OutcomeUncertain).
const codeOutcomeUncertain = "outcome_uncertain"

var _ execution.WorkspaceAdder = (*Adapter)(nil)

// AddWorkspace relays an add-workspace request to the worker's adapter. A
// lost reply, a cancellation or the deadline is an uncertain outcome.
func (a *Adapter) AddWorkspace(ctx context.Context, r execution.WorkspaceAddRequest) (execution.WorkspaceAdded, error) {
	var out execution.WorkspaceAdded
	if err := ctx.Err(); err != nil {
		return out, err
	}
	raw, err := a.m.hub.CallWithTrait(ctx, a.m.id, KindWorkspaceAdd, a.conn, r, execution.WorkspaceAddTimeout, nil, execution.TraitOf[execution.WorkspaceAdder]())
	if err != nil {
		return out, workspaceAddCallError(err)
	}
	var added *execution.WorkspaceAdded
	if err := jsonUnmarshal(raw, &added); err != nil {
		return out, &execution.OutcomeUncertain{Cause: err}
	}
	if added == nil {
		return out, &execution.OutcomeUncertain{Cause: errors.New("worker returned no workspace result")}
	}
	return *added, nil
}

func workspaceAddCallError(err error) error {
	var re *workerchan.RemoteError
	switch {
	case errors.Is(err, workerchan.ErrLostReply), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return &execution.OutcomeUncertain{Cause: err}
	case errors.As(err, &re):
		switch re.Code {
		case codeOutcomeUncertain:
			return &execution.OutcomeUncertain{Cause: err}
		case workerchan.CodeCancelled:
			return &execution.OutcomeUncertain{Cause: fmt.Errorf("%w: %w", context.Canceled, err)}
		case workerchan.CodeTimeout:
			return &execution.OutcomeUncertain{Cause: fmt.Errorf("%w: %w", context.DeadlineExceeded, err)}
		}
	}
	return decodeError(err)
}

// addWorkspace is the worker side of adapter.workspaceAdd.
func addWorkspace(ctx context.Context, a execution.Adapter, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
	var r execution.WorkspaceAddRequest
	if err := decode(env, &r); err != nil {
		return nil, &execution.Invalid{Reason: "Malformed add-workspace request."}
	}
	if strings.TrimSpace(r.Source) == "" || strings.TrimSpace(r.RequestID) == "" || !json.Valid(r.Input) {
		return nil, &execution.Invalid{Reason: "An add-workspace request needs a source, input and requestId."}
	}
	adder, ok := execution.As[execution.WorkspaceAdder](a)
	if !ok {
		return nil, unsupported("adding workspaces")
	}
	out, err := adder.AddWorkspace(ctx, r)
	if err != nil {
		return nil, encodeWorkspaceAddError(err)
	}
	return out, nil
}

func encodeWorkspaceAddError(err error) error {
	var uncertain *execution.OutcomeUncertain
	switch {
	case errors.As(err, &uncertain):
		return &workerchan.RemoteError{Code: codeOutcomeUncertain, Message: err.Error()}
	case errors.Is(err, context.Canceled):
		return &workerchan.RemoteError{Code: workerchan.CodeCancelled, Message: err.Error()}
	case errors.Is(err, context.DeadlineExceeded):
		return &workerchan.RemoteError{Code: workerchan.CodeTimeout, Message: err.Error()}
	}
	return err
}
