package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/principal"
)

type preferencesJSON struct {
	db.Preferences
	SharedNotifications bool `json:"sharedNotifications,omitempty"`
}

func (s *server) preferencesResponse(w http.ResponseWriter, r *http.Request, p db.Preferences) {
	actor, _ := principal.From(r.Context())
	if s.Access != nil && !actor.IsHuman() {
		p.NotifyEvents = []db.TaskState{}
	}
	writeJSON(w, http.StatusOK, preferencesJSON{p, s.Access != nil && actor.IsHuman()})
}

func (s *server) getPreferences(w http.ResponseWriter, r *http.Request) error {
	p, err := s.Store.GetPreferences(r.Context())
	if err != nil {
		return err
	}
	s.preferencesResponse(w, r, p)
	return nil
}

// putDefaultConnection: body {"connectionId": id | null}; null clears it.
func (s *server) putDefaultConnection(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		ConnectionID *int64 `json:"connectionId"`
	}
	if err := decodeStrict(r, &body); err != nil {
		return err
	}
	p, err := s.Store.SetDefaultConnection(r.Context(), body.ConnectionID)
	if err != nil {
		return err
	}
	s.preferencesResponse(w, r, p)
	return nil
}

// putNotifyEvents: body {"events": TaskState[]} (needs_input|failed|ready|done).
func (s *server) putNotifyEvents(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Events *[]db.TaskState `json:"events"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		return db.Invalid("invalid JSON: %v", err)
	}
	if body.Events == nil {
		return db.Invalid("events required")
	}
	p, err := s.Store.SetNotifyEvents(r.Context(), *body.Events)
	if err != nil {
		return err
	}
	s.preferencesResponse(w, r, p)
	return nil
}
