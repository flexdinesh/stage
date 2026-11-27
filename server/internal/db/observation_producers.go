package db

import (
	"context"
	"errors"
	"regexp"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

var (
	ErrObservationProducer = errors.New("observation producer is unbound or fenced")
	ErrObservationCursor   = errors.New("observation cursor is not contiguous")
)

type ObservationDisposition = store.ObservationDisposition

type ObservationCommit struct {
	Cursor  int64
	Applied bool
}

// BindObservationProducer starts a new machine-scoped epoch at cursor zero.
// Binding the current epoch preserves its cursor. Retired epochs stay fenced.
func (s *Store) BindObservationProducer(ctx context.Context, machine int64, producer string) error {
	if producer == "" {
		return ErrObservationProducer
	}
	return s.e.Write(ctx, func(tx store.Tx) error {
		producers := tx.ObservationProducers()
		if err := lockObservationMachine(ctx, producers, machine); err != nil {
			return err
		}
		state, err := producers.Get(ctx, machine, producer)
		if errors.Is(err, ErrNotFound) {
			return producers.Replace(ctx, machine, producer)
		}
		if err == nil && !state.Active {
			return ErrObservationProducer
		}
		return err
	})
}

// CommitObservationRecord applies the next contiguous cursor, or acknowledges
// a replay with the latest durable cursor. Applied is true after the callback
// and its writes commit. A nonzero disposition stores metadata for the record.
// The engine may retry the callback. Keep its effects inside the scoped Store
// and use that Store only during the callback.
func (s *Store) CommitObservationRecord(ctx context.Context, machine int64, producer string, cursor int64, apply func(*Store) (ObservationDisposition, error)) (ObservationCommit, error) {
	result, err := write(ctx, s, func(tx store.Tx) (ObservationCommit, error) {
		producers := tx.ObservationProducers()
		if err := lockObservationMachine(ctx, producers, machine); err != nil {
			return ObservationCommit{}, err
		}
		state, err := producers.Get(ctx, machine, producer)
		if errors.Is(err, ErrNotFound) || (err == nil && !state.Active) {
			return ObservationCommit{}, ErrObservationProducer
		}
		if err != nil {
			return ObservationCommit{}, err
		}
		if cursor <= 0 {
			return ObservationCommit{}, ErrObservationCursor
		}
		if cursor <= state.Cursor {
			return ObservationCommit{Cursor: state.Cursor}, nil
		}
		if cursor != state.Cursor+1 {
			return ObservationCommit{}, ErrObservationCursor
		}
		disposition, err := apply(s.observationStore(tx))
		if err != nil {
			return ObservationCommit{}, err
		}
		if disposition.Code != "" || disposition.Lost != 0 || !disposition.From.IsZero() || !disposition.To.IsZero() {
			if !observationCode.MatchString(disposition.Code) {
				disposition.Code = "invalid_disposition"
			}
			if err := producers.RecordDisposition(ctx, machine, producer, cursor, disposition); err != nil {
				return ObservationCommit{}, err
			}
		}
		if err := producers.SetCursor(ctx, machine, producer, cursor); err != nil {
			return ObservationCommit{}, err
		}
		return ObservationCommit{Cursor: cursor, Applied: true}, nil
	})
	if err != nil {
		return ObservationCommit{}, err
	}
	return result, nil
}

var observationCode = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func lockObservationMachine(ctx context.Context, producers store.ObservationProducers, machine int64) error {
	err := producers.LockMachine(ctx, machine)
	if errors.Is(err, ErrNotFound) {
		return ErrObservationProducer
	}
	return err
}

// WriteObservation groups local or bridge observation writes in one
// transaction. The engine may retry apply. Keep its effects inside the
// scoped Store and use that Store only during the callback.
func (s *Store) WriteObservation(ctx context.Context, apply func(*Store) error) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return apply(s.observationStore(tx)) })
}

// LockObservationConnection serializes observation validation and writes on
// the same connection. Call on the scoped Store before reading a session.
func (s *Store) LockObservationConnection(ctx context.Context, connection int64) error {
	e, ok := s.e.(observationEngine)
	if !ok {
		return errors.New("observation connection lock requires a scoped store")
	}
	return e.tx.Connections().Lock(ctx, connection)
}

func (s *Store) observationStore(tx store.Tx) *Store {
	return NewStore(observationEngine{Engine: s.e, tx: tx})
}

// Both reads and writes join the enclosing transaction. Use a separate Store
// to keep transaction-local reads out of the root Store's connection cache.
type observationEngine struct {
	store.Engine
	tx store.Tx
}

func (e observationEngine) Read(_ context.Context, fn func(store.Tx) error) error {
	return fn(e.tx)
}

func (e observationEngine) Write(_ context.Context, fn func(store.Tx) error) error {
	return fn(e.tx)
}
