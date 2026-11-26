package machine_test

import (
	"errors"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
)

func TestConnectionsAdapterErrors(t *testing.T) {
	var testDB dbtest.DB
	pool := testDB.Reset(t)
	const retired, denied, missing = 10, 11, 12
	if _, err := pool.ExecContext(t.Context(), `
		INSERT INTO harness_connections(id,harness,machine_id,active,retired_at)
		VALUES($1,$4,$6,0,$7),($2,$5,$6,1,NULL),($3,$4,$6,1,NULL)`,
		retired, denied, missing, connectedKind.Kind, hostedKind.Kind, db.LocalMachine, time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}

	store := db.NewStore(pool)
	local := machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileContainer}, nil)
	local.Install(denied, denied, &adapter{name: hostedKind.Kind})
	conns := machine.NewConnections(store, machine.MustSet(local), harness.MustRegistry(hostedKind, connectedKind))

	for _, tt := range []struct {
		name    string
		id      int64
		code    string
		message string
	}{
		{
			name: "retired", id: retired,
			code:    "connection_retired",
			message: "This harness connection was replaced. Its history stays here; start new work on the current connection.",
		},
		{
			name: "kind not allowed", id: denied,
			code:    "connection_unavailable",
			message: "Proc runs on your computer, not in a container. Use the Stage desktop app.",
		},
		{
			name: "no adapter", id: missing,
			code:    "connection_unavailable",
			message: "This harness is unavailable. Select a supported connection.",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, err := conns.Adapter(tt.id)
			var conflict *db.ConflictError
			if a != nil || !errors.As(err, &conflict) || conflict.Code != tt.code || conflict.Msg != tt.message {
				t.Fatalf("adapter = %v, error = %v, want %s: %s", a, err, tt.code, tt.message)
			}
		})
	}
}
