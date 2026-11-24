package db

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// DefaultUser is the user migration 0017 seeds. Without sign-in every write
// attributes to it; only ActingUser reads it (docs/architecture.md "Users").
const DefaultUser int64 = 1

type User = store.User

type actingUserKey struct{}

// WithUser returns ctx with id as the acting user: the request principal
// (future sign-in) or the owner of background work, such as a schedule's
// creator.
func WithUser(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, actingUserKey{}, id)
}

// ActingUser is the one resolver for write attribution (created_by_user_id):
// the user WithUser set on ctx, else DefaultUser.
func ActingUser(ctx context.Context) int64 {
	if id, ok := ctx.Value(actingUserKey{}).(int64); ok {
		return id
	}
	return DefaultUser
}

type actingMachineKey struct{}

// WithMachine returns ctx with id as the forwarding machine: the request
// arrived through that machine's agent gateway (its machine token). A
// machine is a principal, never a user: ActingUser is unchanged.
func WithMachine(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, actingMachineKey{}, id)
}

// ActingMachine is the one resolver for source_machine_id: the machine
// WithMachine set on ctx, else nil (not through a gateway).
func ActingMachine(ctx context.Context) *int64 {
	if id, ok := ctx.Value(actingMachineKey{}).(int64); ok {
		return &id
	}
	return nil
}

// withoutMachine returns ctx with no forwarding machine (background work a
// request only triggered, such as a schedule spawn).
func withoutMachine(ctx context.Context) context.Context {
	return context.WithValue(ctx, actingMachineKey{}, nil)
}

// GetUser reads a user.
func (s *Store) GetUser(ctx context.Context, id int64) (User, error) {
	return read(ctx, s, func(tx store.Tx) (User, error) { return tx.Users().Get(ctx, id) })
}
