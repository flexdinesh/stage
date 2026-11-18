package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/refs"
)

// refJSON is GET /api/refs/resolve: what a pasted reference names. A task's
// threadId is its T3 thread (null before launch); a session's taskId is the
// task that owns it (null for sessions started outside stage).
type refJSON struct {
	Links        execution.Links `json:"links"`
	SessionTitle string          `json:"sessionTitle,omitempty"`
	Session      *db.SessionRef  `json:"session"`
	Kind         refs.Kind       `json:"kind"`
	TaskID       *int64          `json:"taskId"`
	ThreadID     *string         `json:"threadId"`
}

func (s *server) resolveRef(w http.ResponseWriter, r *http.Request) error {
	ref, err := refs.Parse(r.URL.Query().Get("ref"), s.Setup.Endpoint().PublicURL)
	if err != nil {
		return db.Invalid("%s", err.Error())
	}
	ctx := r.Context()
	out := refJSON{Kind: ref.Kind}
	switch ref.Kind {
	case refs.KindTask:
		t, err := s.Store.GetTask(ctx, ref.TaskID)
		if err != nil {
			return notFoundAs(err, fmt.Sprintf("task %d not found", ref.TaskID))
		}
		out.TaskID = &t.ID
		if t.ConnectionID == db.T3ConnectionID {
			out.ThreadID = t.T3ThreadID
		}
		out.Session = t.Session
	case refs.KindSession:
		id := ref.ConnectionID
		if id == 0 {
			id = db.T3ConnectionID
		}
		target := db.SessionRef{ConnectionID: id, ExternalSessionID: ref.ThreadID}
		out.Session = &target
		if id == db.T3ConnectionID {
			out.ThreadID = &target.ExternalSessionID
		}
		owner, err := s.Store.TaskBySession(ctx, target)
		if err == nil {
			out.TaskID = &owner.ID
			break
		}
		if !errors.Is(err, db.ErrNotFound) {
			return err
		}
		obs, err := s.Store.Observation(ctx, target)
		if errors.Is(err, db.ErrNotFound) && id == db.T3ConnectionID {
			th, e := s.Store.GetThread(ctx, target.ExternalSessionID)
			if e == nil {
				out.SessionTitle = th.Title
				break
			}
			if !errors.Is(e, db.ErrNotFound) {
				return e
			}
		}
		if errors.Is(err, db.ErrNotFound) {
			if err = s.Sync.RefreshScopedSession(ctx, target); err == nil {
				obs, err = s.Store.Observation(ctx, target)
			}
		}
		if err != nil {
			return notFoundAs(err, fmt.Sprintf("session %s not found", ref.ThreadID))
		}
		out.SessionTitle, out.Links = obs.Title, obs.Links
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// notFoundAs gives db.ErrNotFound a message naming the reference.
func notFoundAs(err error, msg string) error {
	if errors.Is(err, db.ErrNotFound) {
		return fmt.Errorf("%s: %w", msg, db.ErrNotFound)
	}
	return err
}
