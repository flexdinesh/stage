//go:build postgres

package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/jackc/pgx/v5"
)

// ownerLockKey is the session advisory lock key that makes a server the
// database's single owner ("StageOwn" in ASCII). goose's migration lock uses
// its own, different key.
const ownerLockKey int64 = 0x53746167654f776e

// ownershipPing is how often a held lease checks its connection.
const ownershipPing = 5 * time.Second

// Ownership is a session advisory lock on a dedicated connection held for
// the lease's lifetime (docs/storage.md "Single owner").
func (d *DB) Ownership() store.Ownership { return ownership{d} }

type ownership struct{ d *DB }

func (o ownership) Acquire(ctx context.Context) (store.Lease, error) {
	cfg, err := pgx.ParseConfig(o.d.url)
	if err != nil {
		return nil, errors.New("ownership: invalid database url")
	}
	if o.d.opts.ApplicationName != "" {
		cfg.RuntimeParams["application_name"] = o.d.opts.ApplicationName
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("ownership: %w", err)
	}
	var held bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, ownerLockKey).Scan(&held); err != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("ownership: %w", err)
	}
	if !held {
		holder := lockHolder(ctx, conn)
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, &store.OwnedError{Holder: holder}
	}
	ping := o.d.opts.OwnershipPing
	if ping <= 0 {
		ping = ownershipPing
	}
	l := &lease{conn: conn, lost: make(chan struct{}), done: make(chan struct{}), logger: o.d.log}
	go l.watch(ping)
	return l, nil
}

// lockHolder is the application_name of the session holding the owner lock
// ("" when it cannot be read). A bigint advisory key shows in pg_locks as
// classid (high 32 bits) and objid (low 32 bits) with objsubid 1.
func lockHolder(ctx context.Context, conn *pgx.Conn) string {
	var name string
	err := conn.QueryRow(ctx, `SELECT a.application_name FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
 WHERE l.locktype = 'advisory' AND l.granted AND l.objsubid = 1
   AND l.classid = (($1::bigint >> 32) & 4294967295)::oid AND l.objid = ($1::bigint & 4294967295)::oid
 LIMIT 1`, ownerLockKey).Scan(&name)
	if err != nil {
		return ""
	}
	return name
}

type lease struct {
	mu       sync.Mutex
	conn     *pgx.Conn
	lost     chan struct{}
	done     chan struct{}
	lostOnce sync.Once
	released bool
	logger   *slog.Logger
}

func (l *lease) Lost() <-chan struct{} { return l.lost }

// watch pings the lock's connection until Release; a failed ping means the
// session (and its lock) may be gone, so ownership is lost.
func (l *lease) watch(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-l.done:
			return
		case <-t.C:
		}
		l.mu.Lock()
		if l.released {
			l.mu.Unlock()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), every)
		err := l.conn.Ping(ctx)
		cancel()
		l.mu.Unlock()
		if err != nil {
			l.logger.Error("database ownership lost: the advisory lock's connection failed", "err", err)
			l.lostOnce.Do(func() { close(l.lost) })
			return
		}
	}
}

func (l *lease) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	close(l.done)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := l.conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, ownerLockKey)
	if cerr := l.conn.Close(ctx); err == nil {
		err = cerr
	}
	return err
}
