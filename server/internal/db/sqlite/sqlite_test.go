package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

// A panic inside inTx (Write) rolls back, releasing the write lock for later writes.
func TestInTxRollsBackOnPanic(t *testing.T) {
	ctx := context.Background()
	pool, err := Open(ctx, filepath.Join(t.TempDir(), DBFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := db.NewStore(pool)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("fn did not panic")
			}
		}()
		_ = pool.inTx(ctx, func(tx *runner) error {
			if _, err := tx.Exec(ctx, `INSERT INTO harness_sync_status(machine_id, kind, last_error) VALUES(1, 'test', 'in tx')`); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if status, err := s.HarnessSyncStatus(ctx, db.LocalMachine, "test"); err != nil || status.LastError != nil {
		t.Fatalf("panicked transaction persisted: %+v %v", status, err)
	}
	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.SetHarnessLastError(wctx, db.LocalMachine, "test", "after"); err != nil {
		t.Fatalf("write after panicked tx: %v", err)
	}
	c, err := s.HarnessSyncStatus(ctx, db.LocalMachine, "test")
	if err != nil || c.LastError == nil || *c.LastError != "after" {
		t.Fatalf("connection %+v, %v", c, err)
	}
}
