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

const codeActionUncertain = "action_uncertain"

func (a *Adapter) Act(ctx context.Context, r execution.ActionRequest) (execution.ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return execution.ActionResult{}, err
	}
	if len(r.Input) > 0 && !json.Valid(r.Input) {
		return execution.ActionResult{}, &execution.Invalid{Reason: "Malformed action input."}
	}
	raw, err := a.m.hub.CallWithTrait(ctx, a.m.id, KindAct, a.conn, r, CallTimeout, nil, execution.TraitOf[execution.Actor]())
	if err != nil {
		var re *workerchan.RemoteError
		if errors.As(err, &re) {
			switch re.Code {
			case CodeUnsupported, workerchan.CodeOperationUnavailable, workerchan.CodeUnknownKind:
				return execution.ActionResult{}, fmt.Errorf("%w: %s", execution.ErrUnavailable, re.Message)
			}
		}
		return execution.ActionResult{}, actionCallError(err, decodeError)
	}
	var out *execution.ActionResult
	if err := jsonUnmarshal(raw, &out); err != nil {
		return execution.ActionResult{}, &execution.ActionUncertain{Cause: err}
	}
	if out == nil {
		return execution.ActionResult{}, &execution.ActionUncertain{Cause: errors.New("worker returned no action result")}
	}
	return *out, nil
}

func act(ctx context.Context, a execution.Adapter, env workerchan.Envelope, _ workerchan.Upcall) (any, error) {
	var r execution.ActionRequest
	if err := decode(env, &r); err != nil {
		return nil, &execution.Invalid{Reason: "Malformed action request."}
	}
	if err := validateActionTarget(env.ConnectionID, r); err != nil {
		return nil, err
	}
	actor, ok := execution.As[execution.Actor](a)
	if !ok {
		return nil, unsupported("actions")
	}
	if err := ctx.Err(); err != nil {
		return nil, encodeActionError(err)
	}
	out, err := actor.Act(ctx, r)
	if err != nil {
		return nil, encodeActionError(err)
	}
	return out, nil
}

func validateActionTarget(connectionID int64, r execution.ActionRequest) error {
	if strings.TrimSpace(r.ID) == "" || connectionID <= 0 {
		return &execution.Invalid{Reason: "An action and connection are required."}
	}
	switch r.Target {
	case execution.TargetSession:
		if r.Session == nil || r.Project != nil || r.Session.ConnectionID != connectionID || strings.TrimSpace(r.Session.ExternalSessionID) == "" {
			return &execution.Invalid{Reason: "An action on a session requires a session on this connection."}
		}
	case execution.TargetProject:
		if r.Project == nil || r.Session != nil || r.Project.ExternalProjectID == "" && r.Project.Directory == "" {
			return &execution.Invalid{Reason: "An action on a project requires its workspace."}
		}
	default:
		return &execution.Invalid{Reason: "Invalid action target."}
	}
	return nil
}

func encodeActionError(err error) error {
	var uncertain *execution.ActionUncertain
	switch {
	case errors.As(err, &uncertain):
		return &workerchan.RemoteError{Code: codeActionUncertain, Message: err.Error()}
	case errors.Is(err, context.Canceled):
		return &workerchan.RemoteError{Code: workerchan.CodeCancelled, Message: err.Error()}
	case errors.Is(err, context.DeadlineExceeded):
		return &workerchan.RemoteError{Code: workerchan.CodeTimeout, Message: err.Error()}
	}
	return err
}

func actionCallError(err error, decode func(error) error) error {
	var re *workerchan.RemoteError
	switch {
	case errors.Is(err, workerchan.ErrLostReply), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return &execution.ActionUncertain{Cause: err}
	case errors.As(err, &re):
		switch re.Code {
		case codeActionUncertain:
			return &execution.ActionUncertain{Cause: err}
		case workerchan.CodeCancelled:
			return &execution.ActionUncertain{Cause: fmt.Errorf("%w: %w", context.Canceled, err)}
		case workerchan.CodeTimeout:
			return &execution.ActionUncertain{Cause: fmt.Errorf("%w: %w", context.DeadlineExceeded, err)}
		}
	}
	return decode(err)
}
