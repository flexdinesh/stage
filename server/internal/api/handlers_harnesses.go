package api

import (
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

type machineJSON struct {
	db.Machine
	Profile harness.Profile `json:"profile"`
}

// harnessJSON is a descriptor plus whether it can run on this machine.
type harnessJSON struct {
	harness.Descriptor
	execution.Availability
}

// listHarnesses is GET /api/harnesses: the harness kinds this server knows,
// their policies and copy, and which ones this machine can run.
func (s *server) listHarnesses(w http.ResponseWriter, r *http.Request) error {
	m, err := s.Store.GetMachine(r.Context(), db.LocalMachine)
	if err != nil {
		return err
	}
	profile := s.Profile
	if profile.Kind == "" {
		profile.Kind = harness.ProfileServer
	}
	out := []harnessJSON{}
	for _, d := range s.registry().All() {
		if d.Closures == nil {
			d.Closures = []harness.Closure{}
		}
		out = append(out, harnessJSON{Descriptor: d, Availability: profile.Allows(d)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"machine": machineJSON{Machine: m, Profile: profile}, "harnesses": out})
	return nil
}
