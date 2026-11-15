package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/refs"
)

// refJSON is GET /api/refs/resolve: what a pasted reference names. A task's
// threadId is its T3 thread (null before launch); a session's taskId is the
// task that owns it (null for sessions started outside stage).
type refJSON struct {
	Kind     refs.Kind `json:"kind"`
	TaskID   *int64    `json:"taskId"`
	ThreadID *string   `json:"threadId"`
}

func (s *server) resolveRef(w http.ResponseWriter, r *http.Request) error {
	ref, err := refs.Parse(r.URL.Query().Get("ref"), s.Config.T3PublicURL)
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
		out.TaskID, out.ThreadID = &t.ID, t.T3ThreadID
	case refs.KindSession:
		th, err := s.Store.GetThread(ctx, ref.ThreadID)
		if errors.Is(err, db.ErrNotFound) && s.T3.Status(ctx).Connected {
			// Not mirrored yet: read it from T3 once.
			if err = s.Sync.RefreshSession(ctx, ref.ThreadID); err == nil {
				th, err = s.Store.GetThread(ctx, ref.ThreadID)
			}
		}
		if err != nil {
			return notFoundAs(err, fmt.Sprintf("session %s not found", ref.ThreadID))
		}
		out.TaskID, out.ThreadID = th.TaskID, &th.T3ThreadID
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
