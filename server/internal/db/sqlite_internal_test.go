package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// A panic inside inTx rolls back, releasing the write lock for later writes.
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
	s := NewStore(pool)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("fn did not panic")
			}
		}()
		_ = s.inTx(ctx, func(tx *Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE t3_connection SET last_error = 'in tx' WHERE machine_id = 1`); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.SetLastError(wctx, LocalMachine, "after"); err != nil {
		t.Fatalf("write after panicked tx: %v", err)
	}
	c, err := s.GetConnection(ctx, LocalMachine)
	if err != nil || c.LastError == nil || *c.LastError != "after" {
		t.Fatalf("connection %+v, %v", c, err)
	}
}
