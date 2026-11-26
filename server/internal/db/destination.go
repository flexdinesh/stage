package db

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// ErrInvalidBinding is the validation error for an invalid explicit workspace choice.
var ErrInvalidBinding = Invalid("bindingId must be an active workspace of this project on the chosen connection")

// ValidateBinding checks an explicit choice before the API reads capabilities.
// Writes check it again while holding the project identity lock.
func (s *Store) ValidateBinding(ctx context.Context, project, connection int64, id *int64) error {
	if id == nil {
		return nil
	}
	return s.e.Read(ctx, func(tx store.Tx) error {
		return validateBinding(ctx, tx, project, connection, id)
	})
}

func validateBinding(ctx context.Context, tx store.Tx, project, connection int64, id *int64) error {
	if id == nil {
		return nil
	}
	if *id <= 0 {
		return ErrInvalidBinding
	}
	b, err := tx.Projects().Binding(ctx, *id)
	if errors.Is(err, ErrNotFound) {
		return ErrInvalidBinding
	}
	if err != nil {
		return err
	}
	if !b.Active || b.ProjectID != project || b.ConnectionID != connection {
		return ErrInvalidBinding
	}
	return nil
}

// bindingPatch clears an explicit choice when either part of its destination
// changes, or validates the replacement when the caller chooses a new one.
func bindingPatch(ctx context.Context, tx store.Tx, project, connection int64, p, c, b Optional[int64]) (Optional[int64], error) {
	nextProject, nextConnection := project, connection
	if p.Set {
		nextProject = p.V
	}
	if c.Set {
		nextConnection = c.V
	}
	if !b.Set && (nextProject != project || nextConnection != connection) {
		b = Optional[int64]{Set: true, Null: true}
	}
	return b, validateBinding(ctx, tx, nextProject, nextConnection, b.Ptr())
}

// NewTaskDestination picks the connection for a task with no explicit choice.
// It prefers the default when its primary workspace is available, then the
// only active connection. With a default set, it can fall back to another
// available primary, ordered by machine and connection id.
func NewTaskDestination(bindings []ProjectBinding, def *int64, machine func(int64) int64) int64 {
	active := make(map[int64]struct{})
	var available []int64
	for _, b := range bindings {
		if !b.ConnectionActive {
			continue
		}
		active[b.ConnectionID] = struct{}{}
		if b.Primary && b.Status == "available" {
			available = append(available, b.ConnectionID)
		}
	}
	if def != nil && slices.Contains(available, *def) {
		return *def
	}
	if len(active) == 1 {
		for id := range active {
			return id
		}
	}
	if def == nil || len(available) == 0 {
		return 0
	}
	return slices.MinFunc(available, func(a, b int64) int {
		return cmp.Or(cmp.Compare(machine(a), machine(b)), cmp.Compare(a, b))
	})
}
