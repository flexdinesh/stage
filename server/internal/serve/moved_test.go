package serve

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// summaryConnector reports a fixed summary; setup is never driven here.
type summaryConnector struct{ sum harness.Summary }

func (c *summaryConnector) Summary(context.Context) harness.Summary { return c.sum }
func (c *summaryConnector) Status(context.Context, bool) (harness.Status, error) {
	return harness.Status{}, nil
}
func (c *summaryConnector) Configure(context.Context, json.RawMessage) (harness.Result, error) {
	return harness.Result{}, nil
}
func (c *summaryConnector) Run(context.Context, string, json.RawMessage) (harness.Result, error) {
	return harness.Result{}, nil
}
func (c *summaryConnector) Disconnect(context.Context) error { return nil }

func TestMovedRetirerRetiresOnlyADisconnectedConnectionWhoseInstanceRunsElsewhere(t *testing.T) {
	var d dbtest.DB
	pool := d.Reset(t)
	ctx := context.Background()
	store := db.NewStore(pool)
	if _, err := pool.Exec(`INSERT INTO machines(id,label) VALUES(2,'worker')`); err != nil {
		t.Fatal(err)
	}
	local, err := store.SwitchInstance(ctx, db.LocalMachine, t3.Descriptor, "env-a", "box", 0)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := store.SwitchInstance(ctx, 2, t3.Descriptor, "env-a", "box", 0)
	if err != nil {
		t.Fatal(err)
	}
	old, next := local.New, worker.New
	localT3 := &summaryConnector{harness.Summary{ConnectionID: old, Configured: true}}
	workerT3 := &summaryConnector{harness.Summary{ConnectionID: next, Configured: true}}
	cs, err := harness.NewConnectors(harness.MustRegistry(t3.Descriptor), map[int64]map[string]harness.Connector{
		db.LocalMachine: {t3.Kind: localT3},
		2:               {t3.Kind: workerT3},
	})
	if err != nil {
		t.Fatal(err)
	}
	online := map[int64]bool{db.LocalMachine: true, 2: true}
	var retired [][2]int64
	m := newMovedRetirer(store, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.connectors = func() harness.Connectors { return cs }
	m.reachable = func(machine int64) bool { return online[machine] }
	m.onRetired = func(machine, o, n int64) {
		if machine != db.LocalMachine {
			t.Errorf("retired on machine %d", machine)
		}
		retired = append(retired, [2]int64{o, n})
	}
	pass := func() {
		t.Helper()
		if err := m.Once(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// Both connected: the same T3 reached from two machines stays.
	pass()
	// The worker is down and its T3 unknown: nothing moves.
	localT3.sum.Configured = false
	workerT3.sum.Configured = false
	pass()
	// The worker's T3 is connected but the local one sits on an offline
	// machine: its state is unknown, so it stays.
	workerT3.sum.Configured = true
	online[db.LocalMachine] = false
	pass()
	if len(retired) != 0 || !store.ConnectionActive(old) {
		t.Fatalf("retired early %v", retired)
	}
	// Disconnected here, connected there: retired onto the worker's.
	online[db.LocalMachine] = true
	pass()
	if len(retired) != 1 || retired[0] != [2]int64{old, next} || store.ConnectionActive(old) || !store.ConnectionActive(next) {
		t.Fatalf("retired %v (old active %v)", retired, store.ConnectionActive(old))
	}
	pass()
	if len(retired) != 1 {
		t.Fatalf("retired twice %v", retired)
	}
}
