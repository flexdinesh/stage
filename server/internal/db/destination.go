package db

import (
	"context"
	"errors"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// ErrInvalidBinding is the validation error for an invalid explicit workspace choice.
var ErrInvalidBinding = Invalid("bindingId must be an active workspace of this project on the chosen connection")

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
