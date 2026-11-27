package codex

import (
	"context"
	"errors"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var _ execution.SessionNamer = (*Adapter)(nil)

// RenameSession sets the thread's name (thread/name/set), as launch does.
func (a *Adapter) RenameSession(ctx context.Context, ref execution.SessionRef, title string) error {
	if !a.valid(ref) {
		return execution.ErrNotFound
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("a Codex thread name must not be empty")
	}
	return a.client.call(ctx, "thread/name/set", map[string]any{"threadId": ref.ExternalSessionID, "name": title}, nil)
}
