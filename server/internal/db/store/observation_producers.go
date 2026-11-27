package store

import (
	"context"
	"time"
)

// ObservationProducer tracks a machine-scoped producer epoch and its last
// committed cursor. A retired epoch remains recorded to fence replays.
type ObservationProducer struct {
	Active bool
	Cursor int64
}

// ObservationDisposition contains only metadata about a rejected or lost
// record. The payload itself is never stored here.
type ObservationDisposition struct {
	Code     string
	Lost     uint64
	From, To time.Time
}

type ObservationProducers interface {
	// LockMachine locks a live machine for checking its producer state and
	// writing an observation. A missing or removed machine returns ErrNotFound.
	LockMachine(ctx context.Context, machine int64) error
	Get(ctx context.Context, machine int64, producer string) (ObservationProducer, error)
	// Replace retires the active producer, if any, and inserts a new one.
	Replace(ctx context.Context, machine int64, producer string) error
	SetCursor(ctx context.Context, machine int64, producer string, cursor int64) error
	RecordDisposition(ctx context.Context, machine int64, producer string, cursor int64, disposition ObservationDisposition) error
}
