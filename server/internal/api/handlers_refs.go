package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/refs"
)

// refJSON is GET /api/refs/resolve: what a pasted reference names. A task's
// threadId is its T3 thread (null before launch); a session's taskId is the
// task that owns it (null for sessions started outside stage).
type refJSON struct {
	State        *db.TaskState   `json:"state,omitempty"`
	Links        execution.Links `json:"links"`
	SessionTitle string          `json:"sessionTitle,omitempty"`
	Session      *db.SessionRef  `json:"session"`
	Kind         refs.Kind       `json:"kind"`
	TaskID       *int64          `json:"taskId"`
	ThreadID     *string         `json:"threadId"`
}

func (s *server) resolveRef(w http.ResponseWriter, r *http.Request) error {
	raw := strings.TrimSpace(r.URL.Query().Get("ref"))
	ref, err := refs.Parse(raw, s.Setup.Endpoint().PublicURL)
	selection := r.URL.Query().Get("connectionId")
	if selection != "" {
		id, parseErr := strconv.ParseInt(selection, 10, 64)
		if parseErr != nil || id <= 0 {
			return db.Invalid("choose a valid connectionId")
		}
		if ref.ConnectionID != 0 && ref.ConnectionID != id {
			return db.Invalid("session link belongs to a different connection")
		}
		if ref.Kind != refs.KindTask && ref.ConnectionID == 0 {
			if err == nil && !strings.ContainsAny(raw, "/<>[]") {
				ref.ConnectionID = id
			} else {
				a, e := s.Sync.Adapter(id)
				if e != nil {
					return e
				}
				resolver, ok := a.(execution.LinkResolver)
				if !ok {
					return db.Invalid("this connection cannot resolve native session links")
				}
				native, e := resolver.ResolveLink(r.Context(), raw)
				if e != nil {
					return db.Invalid("%s", e)
				}
				ref, err = refs.Ref{Kind: refs.KindSession, ConnectionID: native.ConnectionID, ThreadID: native.ExternalSessionID}, nil
			}
		}
	} else if err != nil {
		s.Connectors.Each(func(_ harness.Descriptor, c harness.Connector) {
			if err == nil {
				return
			}
			a, e := s.Sync.Adapter(c.Summary(r.Context()).ConnectionID)
			if e != nil {
				return
			}
			if resolver, ok := a.(execution.LinkResolver); ok {
				if native, e := resolver.ResolveLink(r.Context(), raw); e == nil {
					ref, err = refs.Ref{Kind: refs.KindSession, ConnectionID: native.ConnectionID, ThreadID: native.ExternalSessionID}, nil
				}
			}
		})
	}
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
		if t.Harness == db.HarnessT3 {
			out.ThreadID = t.T3ThreadID
		}
		out.Session = t.Session
	case refs.KindSession:
		id := ref.ConnectionID
		if id == 0 {
			id = s.Store.T3ID()
		}
		target := db.SessionRef{ConnectionID: id, ExternalSessionID: ref.ThreadID}
		out.Session = &target
		if s.Store.IsT3(id) {
			out.ThreadID = &target.ExternalSessionID
		}
		owner, err := s.Store.TaskBySession(ctx, target)
		if err == nil {
			out.TaskID = &owner.ID
			out.State = &owner.State
			out.SessionTitle = owner.Title
			break
		}
		if !errors.Is(err, db.ErrNotFound) {
			return err
		}
		obs, err := s.Store.Observation(ctx, target)
		if errors.Is(err, db.ErrNotFound) && id == s.Store.T3ID() {
			th, e := s.Store.GetThread(ctx, target.ExternalSessionID)
			if e == nil {
				out.SessionTitle = th.Title
				out.Links = s.sessionLinks(ctx, id, &target.ExternalSessionID)
				break
			}
			if !errors.Is(e, db.ErrNotFound) {
				return e
			}
		}
		if errors.Is(err, db.ErrNotFound) && r.URL.Query().Get("cached") != "true" {
			if err = s.Sync.RefreshScopedSession(ctx, target); err == nil {
				obs, err = s.Store.Observation(ctx, target)
			}
		}
		if err != nil {
			return notFoundAs(err, fmt.Sprintf("session %s not found", ref.ThreadID))
		}
		out.SessionTitle, out.Links = obs.Title, s.sessionLinks(ctx, id, &target.ExternalSessionID)
		if obs.State != "" && obs.State != "idle" {
			state := db.TaskState(obs.State)
			out.State = &state
		}
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
