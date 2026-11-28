package db

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Store is the data access facade: business rules over the storage ports
// (internal/db/store) of one engine.
type Store struct {
	e store.Engine
	// conns caches harness_connections (a handful of rows). Every Store
	// write to that table drops it; nil reloads on the next read.
	conns atomic.Pointer[connectionSnapshot]
}

// NewStore is the Store over engine e.
func NewStore(e store.Engine) *Store {
	return &Store{e: e}
}

// App errors shared by db, sync, links and api (api maps them to statuses).
var ErrNotFound = store.ErrNotFound

// ConflictError: action not allowed in current state / duplicate (409).
// Code, when set, is a machine-readable reason (error body "code").
type ConflictError struct {
	Msg  string
	Code string
}

func (e *ConflictError) Error() string { return e.Msg }

func Conflict(format string, args ...any) error {
	return &ConflictError{Msg: fmt.Sprintf(format, args...)}
}

// AlreadyAnsweredError: a guarded answer (expectedStateRevision) lost:
// the task left needs_input, moved to another revision, or that revision
// was answered already (409 already_answered, ADR 0013).
type AlreadyAnsweredError struct {
	State         TaskState
	StateRevision int64
}

func (e *AlreadyAnsweredError) Error() string {
	return fmt.Sprintf("Already answered: the task is %s at state revision %d.", e.State, e.StateRevision)
}

// InvalidError: invalid input (400). Code, when set, is a machine-readable
// reason (error body "code").
type InvalidError struct {
	Msg  string
	Code string
}

func (e *InvalidError) Error() string { return e.Msg }

func Invalid(format string, args ...any) error {
	return &InvalidError{Msg: fmt.Sprintf(format, args...)}
}

// HarnessError wraps a failed adapter operation.
type HarnessError struct{ Err error }

func (e *HarnessError) Error() string { return e.Err.Error() }
func (e *HarnessError) Unwrap() error { return e.Err }

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Optional is a PATCH field: Set when present in JSON, Null when JSON null.
type Optional[T any] = store.Optional[T]

// read runs fn in a store Read and returns its value.
func read[T any](ctx context.Context, s *Store, fn func(store.Tx) (T, error)) (T, error) {
	var out T
	err := s.e.Read(ctx, func(tx store.Tx) error {
		var err error
		out, err = fn(tx)
		return err
	})
	return out, err
}

// write runs fn in a store Write (one transaction) and returns its value.
func write[T any](ctx context.Context, s *Store, fn func(store.Tx) (T, error)) (T, error) {
	var out T
	err := s.e.Write(ctx, func(tx store.Tx) error {
		var err error
		out, err = fn(tx)
		return err
	})
	return out, err
}

// DecodeStrict decodes one JSON object (empty: {}) into v, rejecting unknown
// fields; errors are Invalid.
func DecodeStrict(raw []byte, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return Invalid("invalid JSON body: %v", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Invalid("body must contain one JSON object")
	}
	return nil
}

// OnSteeringEvent calls fn after each committed Write that enqueued a
// steering event (the delivery dispatcher's kick). fn must not block. Call
// once, before the Store is used concurrently.
func (s *Store) OnSteeringEvent(fn func()) {
	s.e = steeringEngine{Engine: s.e, hook: fn}
}

// steeringEngine reports committed Writes that inserted steering events.
type steeringEngine struct {
	store.Engine
	hook func()
}

func (e steeringEngine) Write(ctx context.Context, fn func(store.Tx) error) error {
	var inserted atomic.Bool
	err := e.Engine.Write(ctx, func(tx store.Tx) error {
		// Write may rerun fn (Postgres retry): count only the committed run.
		inserted.Store(false)
		return fn(steeringTx{Tx: tx, inserted: &inserted})
	})
	if err == nil && inserted.Load() {
		e.hook()
	}
	return err
}

type steeringTx struct {
	store.Tx
	inserted *atomic.Bool
}

func (t steeringTx) Steering() store.Steering {
	return steeringRepo{Steering: t.Tx.Steering(), inserted: t.inserted}
}

type steeringRepo struct {
	store.Steering
	inserted *atomic.Bool
}

func (r steeringRepo) InsertEvent(ctx context.Context, ev store.NewSteeringEvent) (int64, error) {
	id, err := r.Steering.InsertEvent(ctx, ev)
	if err == nil {
		r.inserted.Store(true)
	}
	return id, err
}
