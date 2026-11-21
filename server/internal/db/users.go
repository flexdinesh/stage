package db

import (
	"context"
	"time"
)

// DefaultUser is the user migration 0017 seeds. Without sign-in every write
// attributes to it; only ActingUser reads it (docs/architecture.md "Users").
const DefaultUser int64 = 1

// User is a person who creates work. Sign-in identities (identities table)
// map to a user later; machine tokens are machine principals, never users.
type User struct {
	ID          int64     `json:"id"`
	DisplayName string    `json:"displayName"`
	CreatedAt   time.Time `json:"createdAt"`
}

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

// GetUser reads a user.
func (s *Store) GetUser(ctx context.Context, id int64) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `SELECT id,display_name,created_at FROM users WHERE id=$1`, id).Scan(&u.ID, &u.DisplayName, &u.CreatedAt)
	return u, notFound(err)
}
