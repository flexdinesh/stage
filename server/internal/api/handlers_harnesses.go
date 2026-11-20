package api

import (
	"context"
	"fmt"
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
	// Connectable: this server has connection setup for the kind
	// (/api/harnesses/{kind}/status and friends).
	Connectable bool `json:"connectable"`
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
		if d.Attribution.MetaKeys == nil {
			d.Attribution.MetaKeys = []string{}
		}
		if d.Setup.Fields == nil {
			d.Setup.Fields = []harness.Field{}
		}
		if d.Setup.Actions == nil {
			d.Setup.Actions = []harness.Action{}
		}
		_, connectable := s.Connectors.Get(d.Kind)
		out = append(out, harnessJSON{Descriptor: d, Availability: profile.Allows(d), Connectable: connectable})
	}
	writeJSON(w, http.StatusOK, map[string]any{"machine": machineJSON{Machine: m, Profile: profile}, "harnesses": out})
	return nil
}

// connectionStatusJSON is GET /api/harnesses/{kind}/status.
type connectionStatusJSON struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	harness.Status
	// Replaced is the connection a setting change or action just retired.
	Replaced *db.HarnessConnection `json:"replaced,omitempty"`
}

// connector is the {kind} path value's descriptor and connector: 404 for an
// unknown kind, 409 for a kind with no connection setup here.
func (s *server) connector(r *http.Request) (harness.Descriptor, harness.Connector, error) {
	d, ok := s.registry().Lookup(r.PathValue("kind"))
	if !ok {
		return d, nil, fmt.Errorf("%w: unknown harness %q", db.ErrNotFound, r.PathValue("kind"))
	}
	c, ok := s.Connectors.Get(d.Kind)
	if !ok {
		return d, nil, db.Conflict("%s has no connection setup on this server.", d.Name)
	}
	return d, c, nil
}

func (s *server) writeConnectionStatus(w http.ResponseWriter, ctx context.Context, d harness.Descriptor, c harness.Connector, refresh bool, res harness.Result) error {
	st, err := c.Status(ctx, refresh)
	if err != nil {
		return err
	}
	if st.Fields == nil {
		st.Fields = []harness.FieldState{}
	}
	if st.Actions == nil {
		st.Actions = []harness.ActionState{}
	}
	out := connectionStatusJSON{Kind: d.Kind, Name: d.Name, Status: st}
	if res.Replaced != 0 {
		if old, err := s.Store.GetHarnessConnection(ctx, res.Replaced); err == nil {
			out.Replaced = &old
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// harnessStatus is GET /api/harnesses/{kind}/status (refreshes a stale probe).
func (s *server) harnessStatus(w http.ResponseWriter, r *http.Request) error {
	d, c, err := s.connector(r)
	if err != nil {
		return err
	}
	return s.writeConnectionStatus(w, r.Context(), d, c, true, harness.Result{})
}

// harnessSettings is PUT /api/harnesses/{kind}/settings {field: value|null}.
func (s *server) harnessSettings(w http.ResponseWriter, r *http.Request) error {
	d, c, err := s.connector(r)
	if err != nil {
		return err
	}
	body, err := rawBody(r)
	if err != nil {
		return err
	}
	res, err := c.Configure(r.Context(), body)
	if err != nil {
		return err
	}
	return s.writeConnectionStatus(w, r.Context(), d, c, true, res)
}

// harnessAction is POST /api/harnesses/{kind}/actions/{action} {input}.
func (s *server) harnessAction(w http.ResponseWriter, r *http.Request) error {
	d, c, err := s.connector(r)
	if err != nil {
		return err
	}
	action := r.PathValue("action")
	if _, ok := d.Setup.Action(action); !ok {
		return fmt.Errorf("%w: %s has no setup action %q", db.ErrNotFound, d.Name, action)
	}
	body, err := rawBody(r)
	if err != nil {
		return err
	}
	res, err := c.Run(r.Context(), action, body)
	if err != nil {
		return err
	}
	return s.writeConnectionStatus(w, r.Context(), d, c, false, res)
}

// harnessDisconnect is DELETE /api/harnesses/{kind}/connection.
func (s *server) harnessDisconnect(w http.ResponseWriter, r *http.Request) error {
	d, c, err := s.connector(r)
	if err != nil {
		return err
	}
	if !d.Setup.Disconnect {
		return db.Conflict("%s cannot be disconnected from Stage.", d.Name)
	}
	if err := c.Disconnect(r.Context()); err != nil {
		return err
	}
	return s.writeConnectionStatus(w, r.Context(), d, c, false, harness.Result{})
}
