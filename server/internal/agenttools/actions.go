package agenttools

import (
	"context"
	"encoding/json"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

type ListActionsArgs struct {
	client.ActionScope
	CallerSession *db.SessionRef `json:"callerSession,omitempty" jsonschema:"your Stage connectionId and externalSessionId, if not supplied by your harness"`
}

type ActionsOut struct {
	Actions []execution.Action `json:"actions"`
}

type RunActionArgs struct {
	ListActionsArgs
	ActionID string         `json:"actionId" jsonschema:"stable action ID from list_actions"`
	Input    map[string]any `json:"input,omitempty" jsonschema:"action input object matching its declared fields"`
}

// ActionCaller derives action transport context using the same caller
// resolution as other operations.
func (o *Ops) ActionCaller(ctx context.Context, args CallerArgs) (client.ActionCaller, error) {
	caller, agent, err := o.detectCaller(ctx, args)
	return client.ActionCaller{Session: caller.Session, Agent: agent}, err
}

func (o *Ops) ListActions(ctx context.Context, args ListActionsArgs) (ActionsOut, error) {
	caller, err := o.ActionCaller(ctx, CallerArgs{CallerSession: args.CallerSession})
	if err != nil {
		return ActionsOut{}, err
	}
	caller.Agent = true
	actions, err := o.Client.ListActions(ctx, args.ActionScope, caller)
	return ActionsOut{Actions: actions}, err
}

func (o *Ops) RunAction(ctx context.Context, args RunActionArgs) (execution.ActionResult, error) {
	var input json.RawMessage
	if args.Input != nil {
		var err error
		input, err = json.Marshal(args.Input)
		if err != nil {
			return execution.ActionResult{}, err
		}
	}
	caller, err := o.ActionCaller(ctx, CallerArgs{CallerSession: args.CallerSession})
	if err != nil {
		return execution.ActionResult{}, err
	}
	caller.Agent = true
	return o.Client.RunAction(ctx, args.ActionScope, args.ActionID, input, caller)
}
