package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// machineJSON is the machine in a harness list: its /api/machines shape
// plus its hosting profile.
type machineJSON struct {
	machineInfoJSON
	Profile harness.Profile `json:"profile"`
}

// harnessJSON is a descriptor plus whether it can run on the machine.
type harnessJSON struct {
	harness.Descriptor
	execution.Availability
	// Connectable: the machine has connection setup for the kind
	// (.../harnesses/{kind}/status and friends).
	Connectable bool `json:"connectable"`
}

// harnessMachine is the {id} path value's machine (none: the local
// machine, the /api/harnesses alias); 404 when unknown or removed.
func (s *server) harnessMachine(r *http.Request) (db.MachineInfo, error) {
	id := s.Sync.Machines().DefaultID()
	if r.PathValue("id") != "" {
		var err error
		if id, err = pathID(r); err != nil {
			return db.MachineInfo{}, err
		}
	}
	m, err := s.Store.GetMachineInfo(r.Context(), id)
	if errors.Is(err, db.ErrNotFound) {
		return m, fmt.Errorf("%w: machine %d not found", db.ErrNotFound, id)
	}
	return m, err
}

// listHarnesses is GET /api/harnesses (the local machine) and GET
// /api/machines/{id}/harnesses: the harness kinds this server knows, their
// policies and copy, and which ones the machine can run and set up.
func (s *server) listHarnesses(w http.ResponseWriter, r *http.Request) error {
	m, err := s.harnessMachine(r)
	if err != nil {
		return err
	}
	// The machine's hosting profile and policy (its host in the machine set).
	var profile harness.Profile
	hm, hosted := s.Sync.Machines().Get(m.ID)
	if hosted {
		profile = hm.Profile()
	}
	if profile.Kind == "" {
		profile.Kind = harness.ProfileServer
	}
	allows := profile.Allows
	if hosted {
		allows = hm.Allows
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
		// The connectors index leaves out kinds the machine does not allow.
		_, connectable := s.connectors().Get(m.ID, d.Kind)
		out = append(out, harnessJSON{Descriptor: d, Availability: allows(d), Connectable: connectable})
	}
	writeJSON(w, http.StatusOK, map[string]any{"machine": machineJSON{machineInfoJSON: s.toMachineJSON(m), Profile: profile}, "harnesses": out})
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

// connector is the {kind} path value's descriptor and connector on the
// {id} machine (none: the local machine): 404 for an unknown kind or
// machine, 409 harness_unavailable with the machine's reason for a kind it
// does not allow (the connector is never reached), 409 for a kind with no
// connection setup there.
func (s *server) connector(r *http.Request) (harness.Descriptor, harness.Connector, error) {
	m, err := s.harnessMachine(r)
	if err != nil {
		return harness.Descriptor{}, nil, err
	}
	d, ok := s.registry().Lookup(r.PathValue("kind"))
	if !ok {
		return d, nil, fmt.Errorf("%w: unknown harness %q", db.ErrNotFound, r.PathValue("kind"))
	}
	if hm, ok := s.Sync.Machines().Get(m.ID); ok {
		if a := hm.Allows(d); !a.Available {
			return d, nil, &db.ConflictError{Code: "harness_unavailable", Msg: a.Reason}
		}
	}
	c, ok := s.connectors().Get(m.ID, d.Kind)
	if !ok {
		return d, nil, db.Conflict("%s has no connection setup on %s.", d.Name, m.Label)
	}
	return d, c, nil
}

func (s *server) writeConnectionStatus(w http.ResponseWriter, ctx context.Context, d harness.Descriptor, c harness.Connector, refresh bool, res harness.Result) error {
	st, err := c.Status(ctx, refresh)
	if err != nil {
		return err
	}
	st.Summary = s.harnessSummary(st.Summary)
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

func (s *server) harnessSummary(summary harness.Summary) harness.Summary {
	if summary.Configured && summary.LastError == "" {
		summary.LastError = s.Sync.StreamError(summary.ConnectionID)
	}
	return summary
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
