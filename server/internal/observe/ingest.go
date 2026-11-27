package observe

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

const maxClockSkew = 5 * time.Minute

var ErrWrongMachine = errors.New("observation names a connection on another machine")

type recordResult struct {
	ingested    Ingested
	disposition db.ObservationDisposition
	rejection   error
	task        *workevent.TaskChange
	session     *workevent.SessionChange
	inferred    bool
	afterCommit func()
}

func reject(code string, err error) recordResult {
	return recordResult{disposition: db.ObservationDisposition{Code: code}, rejection: err}
}

func (s *Service) ingestLocal(ctx context.Context, r Record) (Ingested, error) {
	var result recordResult
	err := s.store.WriteObservation(ctx, func(tx *db.Store) error {
		var err error
		result, err = s.applyRecord(ctx, tx, 0, r)
		return err
	})
	if err != nil {
		return Unowned, err
	}
	if result.rejection != nil {
		return Unowned, result.rejection
	}
	s.publishRecord(ctx, r, result)
	return result.ingested, nil
}

func (s *Service) publishRecord(ctx context.Context, r Record, result recordResult) {
	ctx = context.WithoutCancel(ctx)
	if result.afterCommit != nil {
		result.afterCommit()
	}
	if result.session != nil && s.RecordNotification == nil && s.OnSessionChange != nil {
		s.OnSessionChange(ctx, *result.session)
	}
	s.coord.PublishObservation(ctx, result.task, result.inferred, s.RecordNotification != nil)
	if r.StreamError == nil || result.rejection != nil {
		return
	}
	if *r.StreamError == "" {
		s.streamErrors.Delete(r.ConnectionID)
		return
	}
	v, _ := s.streamErrors.LoadOrStore(r.ConnectionID, &atomic.Pointer[string]{})
	v.(*atomic.Pointer[string]).Store(r.StreamError)
}

func (s *Service) applyRecord(ctx context.Context, tx *db.Store, machine int64, r Record) (recordResult, error) {
	if r.Gap != nil {
		g := r.Gap
		if r.ConnectionID != 0 || r.ExternalSessionID != "" || r.Observation != nil || r.Unavailable || r.StreamError != nil ||
			g.Count == 0 || g.From.IsZero() || g.To.Before(g.From) {
			return reject("invalid_record", db.Invalid("malformed observation gap")), nil
		}
		return recordResult{disposition: db.ObservationDisposition{Code: "gap", Lost: g.Count, From: g.From, To: g.To}}, nil
	}
	if r.ConnectionID <= 0 || r.ObservedAt.IsZero() || r.ObservedAt.After(time.Now().Add(maxClockSkew)) {
		return reject("invalid_record", db.Invalid("malformed observation identity or observed time")), nil
	}
	if err := tx.LockObservationConnection(ctx, r.ConnectionID); err != nil && !errors.Is(err, db.ErrNotFound) {
		return recordResult{}, err
	}
	c, err := tx.GetHarnessConnection(ctx, r.ConnectionID)
	if errors.Is(err, db.ErrNotFound) {
		return reject("unknown_connection", err), nil
	}
	if err != nil {
		return recordResult{}, err
	}
	if machine != 0 && c.MachineID != machine {
		return recordResult{}, ErrWrongMachine
	}
	if !c.Active {
		return reject("retired_connection", db.ConnectionRetired()), nil
	}
	ref := execution.SessionRef{ConnectionID: r.ConnectionID, ExternalSessionID: r.ExternalSessionID}
	switch {
	case r.StreamError != nil && r.Observation == nil && !r.Unavailable && r.ExternalSessionID == "":
		return recordResult{}, nil
	case r.Unavailable && r.Observation == nil && r.StreamError == nil && r.ExternalSessionID != "":
		previous, err := tx.Observation(ctx, ref)
		if errors.Is(err, db.ErrNotFound) || err == nil && previous.UpdatedAt.After(r.ObservedAt) {
			return recordResult{}, nil
		}
		if err != nil {
			return recordResult{}, err
		}
		return recordResult{}, tx.ObservationUnavailable(ctx, ref, "The harness could not read this session.")
	case r.Observation == nil || r.StreamError != nil || r.Unavailable || r.Observation.Session != ref:
		return reject("invalid_record", db.Invalid("malformed observation session")), nil
	}
	o := *r.Observation
	if err := s.coord.CheckObservation(c.Harness, o); err != nil {
		return reject("invalid_record", db.Invalid("%s", err)), nil
	}
	if o.UpdatedAt.IsZero() {
		return reject("invalid_record", db.Invalid("malformed observation: missing updatedAt")), nil
	}
	if o.UpdatedAt.After(time.Now().Add(maxClockSkew)) {
		return reject("invalid_record", db.Invalid("malformed observation: updatedAt is more than 5 minutes ahead of the server's clock")), nil
	}
	task, err := tx.TaskBySession(ctx, ref)
	var result recordResult
	switch {
	case errors.Is(err, db.ErrNotFound):
		result.ingested = Unowned
		if r.Attention && !o.Child {
			result.session, err = s.applyAttention(ctx, tx, c.Harness, o, r.Quiet)
		} else {
			err = nil
		}
	case err != nil:
		return result, err
	case task.State == db.StateLaunching:
		result.ingested = Launching
	case r.Attention:
		// A task adopted between watch publication and ingestion will receive
		// its own cadence. An attention read cannot supersede that schedule.
		result.ingested = Unowned
	default:
		result.ingested = Applied
		result.task, result.inferred, err = s.coord.ApplyObservation(ctx, tx, task, o, r.ObservedAt)
	}
	if err == nil && s.RecordNotification != nil && (result.task != nil || result.session != nil) {
		result.afterCommit, err = s.RecordNotification(ctx, tx, workevent.ObservationChange{
			Task: result.task, Session: result.session, At: r.ObservedAt,
		})
	}
	return result, err
}

func (s *Service) applyAttention(ctx context.Context, tx *db.Store, kind string, o execution.Observation, quiet bool) (*workevent.SessionChange, error) {
	previous, err := tx.Observation(ctx, o.Session)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}
	if previous.UpdatedAt.After(o.UpdatedAt) {
		return nil, nil
	}
	descriptor, _ := s.adapters.Descriptor(kind)
	o.Facts = descriptor.DisplayFacts(o.Facts)
	if err := tx.PutObservation(ctx, o); err != nil {
		return nil, err
	}
	if quiet || descriptor.Attention == harness.AttentionSilent || previous.State == o.State || !workevent.Notifiable(db.TaskState(o.State)) ||
		previous.State == "" && !o.CreatedAt.After(s.started) {
		return nil, nil
	}
	prev := db.TaskState(previous.State)
	return &workevent.SessionChange{Session: &o.Session, ProjectID: o.Project, Title: o.Title, From: &prev, To: db.TaskState(o.State)}, nil
}

// CommitBatch validates the envelope before mutation, then commits one
// contiguous prefix. Permanent per-record dispositions advance the cursor.
func (s *Service) CommitBatch(ctx context.Context, machine int64, batch Batch) (BatchAck, error) {
	var ack BatchAck
	if machine <= 0 || len(batch.Records) == 0 || len(batch.Records) > 100 ||
		batch.Cursor < int64(len(batch.Records)) || !validProducer(batch.Producer) {
		return ack, db.Invalid("invalid observation batch")
	}
	for i, r := range batch.Records {
		if r.Cursor != batch.Cursor-int64(len(batch.Records)-1-i) {
			return ack, db.Invalid("observation batch cursors must be contiguous")
		}
		if r.ConnectionID > 0 {
			c, err := s.store.GetHarnessConnection(ctx, r.ConnectionID)
			if err != nil && !errors.Is(err, db.ErrNotFound) {
				return ack, err
			}
			if err == nil && c.MachineID != machine {
				return ack, ErrWrongMachine
			}
		}
	}
	for _, r := range batch.Records {
		var result recordResult
		commit, err := s.store.CommitObservationRecord(ctx, machine, batch.Producer, r.Cursor, func(tx *db.Store) (db.ObservationDisposition, error) {
			var err error
			result, err = s.applyRecord(ctx, tx, machine, r)
			return result.disposition, err
		})
		if err != nil {
			return ack, err
		}
		ack.Cursor = commit.Cursor
		if r.Gap != nil && (!commit.Applied || result.disposition.Code == "gap") {
			ack.Resample = true
		}
		if commit.Applied {
			if code := result.disposition.Code; code != "" {
				s.log.Warn("worker observation disposition", "machine", machine, "connection", r.ConnectionID,
					"cursor", r.Cursor, "code", code, "lost", result.disposition.Lost)
			}
			if result.rejection == nil {
				s.publishRecord(ctx, r, result)
			}
		}
	}
	return ack, nil
}

func validProducer(p string) bool {
	if len(p) == 0 || len(p) > 128 {
		return false
	}
	for _, c := range p {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
