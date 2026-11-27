package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var _ execution.SessionNamer = (*Adapter)(nil)

const sessionRenameUnavailable = "This OpenCode version cannot rename sessions (session.update with a title)."

// inspectSessionUpdate reports whether session.update takes a title.
func inspectSessionUpdate(op operation, components map[string]json.RawMessage) bool {
	if op.OperationID != "session.update" {
		return false
	}
	shape := op.RequestBody.Content["application/json"].Schema
	if shape.Ref != "" {
		ref := components[strings.TrimPrefix(shape.Ref, "#/components/schemas/")]
		shape = schemaShape{}
		if len(ref) == 0 || json.Unmarshal(ref, &shape) != nil {
			return false
		}
	}
	return shape.Properties["title"] != nil
}

// RenameSession sets the session's title (PATCH /api/session/{id}).
func (a *Adapter) RenameSession(ctx context.Context, ref execution.SessionRef, title string) error {
	if !a.valid(ref) {
		return execution.ErrNotFound
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("an OpenCode session title must not be empty")
	}
	contract, err := a.checkContract(ctx)
	if err != nil {
		return err
	}
	if !contract.rename {
		return errors.New(sessionRenameUnavailable)
	}
	return a.request(ctx, "PATCH", "/api/session/"+ref.ExternalSessionID, map[string]string{"title": title}, nil)
}
