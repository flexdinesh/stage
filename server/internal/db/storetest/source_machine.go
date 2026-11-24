package storetest

import (
	"context"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func init() {
	register("ActingMachine", caseActingMachine)
}

func caseActingMachine(t *testing.T, open Factory) {
	ctx := context.Background()
	if got := db.ActingMachine(ctx); got != nil {
		t.Fatalf("no gateway: %d, want nil", *got)
	}
	if got := db.ActingMachine(db.WithMachine(ctx, 7)); got == nil || *got != 7 {
		t.Fatalf("gateway: %v, want 7", got)
	}
	if got := db.ActingUser(db.WithMachine(ctx, 7)); got != db.DefaultUser {
		t.Fatalf("a machine is not a user: acting user %d", got)
	}
}
