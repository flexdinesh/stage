package observe

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Stream ingests connection id's pushed observations while its adapter
// streams them (execution.EventSource); it returns at once for an adapter
// that does not push. Polling reconciles whatever a stream misses.
func (s *Service) Stream(ctx context.Context, id int64) error {
	a, err := s.adapters.Adapter(id)
	if err != nil {
		return err
	}
	src, ok := execution.As[execution.EventSource](a)
	if !ok {
		// An offline worker cannot advertise traits. Keep the last failure
		// until that machine can answer or the connection changes.
		if s.adapters.Online(s.store.ConnectionMachine(id)) {
			s.streamErrors.Delete(id)
		}
		return nil
	}
	v, _ := s.streamErrors.LoadOrStore(id, &atomic.Pointer[string]{})
	status := v.(*atomic.Pointer[string])
	err = src.Events(ctx, func(ctx context.Context, o execution.Observation) error {
		if o.Session.ConnectionID != id {
			return errors.New("pushed observation names another connection")
		}
		_, err := s.Ingest(ctx, o)
		if err == nil {
			status.Store(nil)
		}
		return err
	})
	if ctx.Err() == nil {
		if err == nil {
			status.Store(nil)
		} else if current, _ := s.streamErrors.Load(id); current == status {
			msg := err.Error()
			status.Store(&msg)
			s.log.Warn("observation stream", "connection", id, "err", err)
		}
	}
	return err
}

// StreamError is the latest event stream failure for a connection, cleared
// when the stream recovers. Polling continues independently.
func (s *Service) StreamError(id int64) string {
	if v, ok := s.streamErrors.Load(id); ok {
		if msg := v.(*atomic.Pointer[string]).Load(); msg != nil {
			return *msg
		}
	}
	return ""
}

// Ingested is what Ingest did with a pushed observation.
type Ingested int

const (
	// Applied: the task that owns the session took the observation.
	Applied Ingested = iota
	// Unowned: no task owns the session; it is left to discovery.
	Unowned
	// Launching: the owning task is launching; its launch reconciles the
	// session.
	Launching
)

// Ingest applies a pushed observation (an EventSource event, or POST
// /api/connections/{id}/observations) to the task that owns its session.
// An unowned session is left to discovery.
func (s *Service) Ingest(ctx context.Context, o execution.Observation) (Ingested, error) {
	capturedAt := time.Now().UTC()
	at := o.UpdatedAt
	if at.IsZero() {
		at = capturedAt
	}
	return s.ingestLocal(ctx, Record{
		ConnectionID: o.Session.ConnectionID, ExternalSessionID: o.Session.ExternalSessionID,
		ObservedAt: at, Observation: &o, capturedAt: capturedAt,
	})
}
