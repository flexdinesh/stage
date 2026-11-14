package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
)

func (s *server) getPreferences(w http.ResponseWriter, r *http.Request) error {
	p, err := s.Store.GetPreferences(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, p)
	return nil
}

// putPinned: body ModelDefaults (pin) or null (clear).
func (s *server) putPinned(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return db.Invalid("read body: %v", err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return db.Invalid("body required: defaults object or null")
	}
	var d *db.ModelDefaults
	if err := json.Unmarshal(body, &d); err != nil {
		return db.Invalid("invalid JSON: %v", err)
	}
	if d != nil {
		if err := validateDefaults(d); err != nil {
			return err
		}
	}
	p, err := s.Store.SetPinned(r.Context(), d)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, p)
	return nil
}

// putNotifyEvents: body {"events": TaskState[]} (needs_input|failed|done|settled).
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
	writeJSON(w, http.StatusOK, p)
	return nil
}

func validateDefaults(d *db.ModelDefaults) error {
	d.ProviderInstanceID = trimmedPtr(d.ProviderInstanceID)
	d.Model = trimmedPtr(d.Model)
	if (d.ProviderInstanceID == nil) != (d.Model == nil) {
		return db.Invalid("providerInstanceId and model must be set together")
	}
	opts, err := validateOptions(d.ModelOptions)
	if err != nil {
		return err
	}
	d.ModelOptions = opts
	if strings.TrimSpace(d.RuntimeMode) == "" {
		d.RuntimeMode = "full-access"
	}
	if strings.TrimSpace(d.InteractionMode) == "" {
		d.InteractionMode = "default"
	}
	if err := validEnum("runtimeMode", d.RuntimeMode, db.RuntimeModes); err != nil {
		return err
	}
	return validEnum("interactionMode", d.InteractionMode, db.InteractionModes)
}
