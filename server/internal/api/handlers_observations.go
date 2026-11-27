package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/observe"
)

// maxPushedObservations caps one POST /api/connections/{id}/observations.
const maxPushedObservations = 100

// Pushed observation statuses (ObservationResult.Status).
const (
	observationIngested = "ingested"
	observationIgnored  = "ignored"
	observationInvalid  = "invalid"
)

// ObservationResult is what POST /api/connections/{id}/observations did
// with one observation, in request order.
type ObservationResult struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// pushObservations is POST /api/connections/{connectionId}/observations,
// strict body {"observations": [execution.Observation]}: an external bridge
// or hook pushes observations of an existing connection's sessions. Each
// goes through the same validation and ingest path as an EventSource event
// (docs/execution.md "Push"); polling still reconciles.
func (s *server) pushObservations(w http.ResponseWriter, r *http.Request) error {
	id, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	c, err := s.Store.GetHarnessConnection(r.Context(), id)
	if err != nil {
		return err
	}
	if !c.Active {
		return db.ConnectionRetired()
	}
	// A worker's agent gateway speaks for its own machine's harnesses only.
	if m := db.ActingMachine(r.Context()); m != nil && *m != c.MachineID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "a worker's agent gateway pushes observations only for its own machine's connections", "code": "gateway_forbidden"})
		return nil
	}
	var in struct {
		Observations []execution.Observation `json:"observations"`
	}
	body, err := rawBody(r)
	if err != nil {
		return err
	}
	if err := rejectRemovedObservationFields(body); err != nil {
		return err
	}
	if err := db.DecodeStrict(body, &in); err != nil {
		return err
	}
	if n := len(in.Observations); n == 0 || n > maxPushedObservations {
		return db.Invalid("observations must hold 1 to %d observations", maxPushedObservations)
	}
	for i, o := range in.Observations {
		if o.Session.ConnectionID != id {
			return db.Invalid("observations[%d]: session is not on connection %d", i, id)
		}
	}
	results := make([]ObservationResult, 0, len(in.Observations))
	for _, o := range in.Observations {
		// A storage failure ends the request (500); the observations before
		// it stay ingested, and pushing the batch again is safe.
		res, err := s.pushObservation(r, o)
		if err != nil {
			return err
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
	return nil
}

func rejectRemovedObservationFields(body []byte) error {
	var in struct {
		Observations []json.RawMessage `json:"observations"`
	}
	if json.Unmarshal(body, &in) != nil {
		return nil // The strict decoder reports malformed bodies.
	}
	for i, raw := range in.Observations {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		for _, field := range []string{"artifacts", "modelLabel"} {
			if _, ok := fields[field]; ok {
				return db.Invalid("observations[%d].%s was removed in API v9", i, field)
			}
		}
		for _, group := range []struct {
			name   string
			fields []string
		}{
			{"links", []string{"service", "app"}},
			{"reply", []string{"messageId", "position"}},
		} {
			var nested map[string]json.RawMessage
			if json.Unmarshal(fields[group.name], &nested) != nil {
				continue
			}
			for _, field := range group.fields {
				if _, ok := nested[field]; ok {
					return db.Invalid("observations[%d].%s.%s was removed in API v9", i, group.name, field)
				}
			}
		}
	}
	return nil
}

func (s *server) pushObservation(r *http.Request, o execution.Observation) (ObservationResult, error) {
	got, err := s.Engine.Observe.Ingest(r.Context(), o)
	var invalid *db.InvalidError
	switch {
	case errors.As(err, &invalid):
		return ObservationResult{Status: observationInvalid, Reason: invalid.Error()}, nil
	case err != nil:
		return ObservationResult{}, err
	case got == observe.Unowned:
		return ObservationResult{Status: observationIgnored, Reason: "no task owns this session; discovery may track it"}, nil
	case got == observe.Launching:
		return ObservationResult{Status: observationIgnored, Reason: "the task is launching; its launch reconciles the session"}, nil
	}
	return ObservationResult{Status: observationIngested}, nil
}
