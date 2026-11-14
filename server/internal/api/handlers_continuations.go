package api

import (
	"net/http"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
)

type continuationJSON struct {
	db.Continuation
	AfterThread  threadJSON `json:"afterThread"`
	TargetThread threadJSON `json:"targetThread"`
}

func (s *server) continuation(env string, c db.Continuation) continuationJSON {
	return continuationJSON{Continuation: c, AfterThread: s.thread(env, c.AfterThread), TargetThread: s.thread(env, c.TargetThread)}
}

func (s *server) continuations(env string, cs []db.Continuation) []continuationJSON {
	out := make([]continuationJSON, len(cs))
	for i, c := range cs {
		out[i] = s.continuation(env, c)
	}
	return out
}

func (s *server) getThread(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if s.T3.Status(r.Context()).Connected {
		if err := s.Sync.RefreshSession(r.Context(), id); err != nil {
			return err
		}
	}
	thread, err := s.Store.GetThread(r.Context(), id)
	if err != nil {
		return err
	}
	if thread.LastWord, err = s.Store.GetThreadLastWord(r.Context(), id); err != nil {
		return err
	}
	children, _, err := s.Store.ListThreads(r.Context(), db.ThreadFilter{Parent: &id, Limit: 2147483647, SortActivity: true})
	if err != nil {
		return err
	}
	cs, err := s.Store.ListContinuations(r.Context(), id)
	if err != nil {
		return err
	}
	env := s.envID(r.Context())
	childJSON := make([]threadJSON, len(children))
	for i, c := range children {
		childJSON[i] = s.thread(env, c)
	}
	var task *taskJSON
	if thread.TaskID != nil {
		t, err := s.Store.GetTask(r.Context(), *thread.TaskID)
		if err != nil {
			return err
		}
		shaped := s.task(env, t)
		task = &shaped
	}
	writeJSON(w, http.StatusOK, map[string]any{"thread": s.thread(env, thread), "children": childJSON, "continuations": s.continuations(env, cs), "task": task})
	return nil
}

func (s *server) listContinuations(w http.ResponseWriter, r *http.Request) error {
	cs, err := s.Store.ListContinuations(r.Context(), r.URL.Query().Get("thread"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"continuations": s.continuations(s.envID(r.Context()), cs)})
	return nil
}

func (s *server) createContinuation(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		AfterThreadID  string `json:"afterThreadId"`
		TargetThreadID string `json:"targetThreadId"`
		Prompt         string `json:"prompt"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	// Refresh known sessions before validating eligibility. Saving remains
	// possible offline using mirrored metadata; it never delivers.
	if s.T3.Status(r.Context()).Connected {
		for _, id := range []string{body.AfterThreadID, body.TargetThreadID} {
			if strings.TrimSpace(id) == "" {
				return db.Invalid("afterThreadId and targetThreadId are required")
			}
			if err := s.Sync.RefreshSession(r.Context(), id); err != nil {
				return err
			}
		}
	}
	c, err := s.Store.CreateContinuation(r.Context(), body.AfterThreadID, body.TargetThreadID, body.Prompt)
	if err != nil {
		return err
	}
	if err := s.Sync.RecheckContinuation(r.Context(), c.ID); err != nil {
		return err
	}
	c, err = s.Store.GetContinuation(r.Context(), c.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, s.continuation(s.envID(r.Context()), c))
	return nil
}

func (s *server) updateContinuation(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var body struct {
		Prompt *string `json:"prompt"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	c, err := s.Store.MutateContinuation(r.Context(), id, "edit", body.Prompt)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.continuation(s.envID(r.Context()), c))
	return nil
}

func (s *server) continuationAction(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	action := r.PathValue("action")
	if action != "pause" && action != "resume" && action != "cancel" {
		return db.ErrNotFound
	}
	c, err := s.Store.MutateContinuation(r.Context(), id, action, nil)
	if err != nil {
		return err
	}
	if action == "resume" {
		if err := s.Sync.RecheckContinuation(r.Context(), id); err != nil {
			return err
		}
		c, err = s.Store.GetContinuation(r.Context(), id)
		if err != nil {
			return err
		}
	}
	writeJSON(w, http.StatusOK, s.continuation(s.envID(r.Context()), c))
	return nil
}
