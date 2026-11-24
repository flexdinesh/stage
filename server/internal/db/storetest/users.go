package storetest

import (
	"context"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func init() {
	register("ActingUser", caseActingUser)
	register("DefaultUserSeeded", caseDefaultUserSeeded)
}

func caseActingUser(t *testing.T, open Factory) {
	ctx := context.Background()
	if got := db.ActingUser(ctx); got != db.DefaultUser {
		t.Fatalf("no principal: %d, want default %d", got, db.DefaultUser)
	}
	if got := db.ActingUser(db.WithUser(ctx, 7)); got != 7 {
		t.Fatalf("principal: %d, want 7", got)
	}
}

func caseDefaultUserSeeded(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	u, err := s.GetUser(ctx, db.DefaultUser)
	if err != nil || u.ID != 1 || u.DisplayName != "Default user" || u.CreatedAt.IsZero() {
		t.Fatalf("default user = %+v, %v", u, err)
	}
	if _, err := s.GetUser(ctx, 2); err == nil {
		t.Fatal("user 2 exists")
	}
}
