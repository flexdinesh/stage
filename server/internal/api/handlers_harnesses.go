package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/discovery"
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
	id := s.Engine.Connections.Machines().DefaultID()
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
	hm, hosted := s.Engine.Connections.Machines().Get(m.ID)
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
		d.Setup.Actions = visibleActions(d.Setup.Actions, execution.TargetConnection, actionCaller(r))
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
	Message  string                `json:"message,omitempty"`
	Link     string                `json:"link,omitempty"`
}

// connector is the {kind} path value's descriptor and connector on the
// {id} machine (none: the local machine): 404 for an unknown kind or
// machine, 409 harness_unavailable with the machine's reason for a kind it
// does not allow (the connector is never reached), 409 for a kind with no
// connection setup there.
func (s *server) connector(r *http.Request) (int64, harness.Descriptor, harness.Connector, error) {
	m, err := s.harnessMachine(r)
	if err != nil {
		return 0, harness.Descriptor{}, nil, err
	}
	d, ok := s.registry().Lookup(r.PathValue("kind"))
	if !ok {
		return m.ID, d, nil, fmt.Errorf("%w: unknown harness %q", db.ErrNotFound, r.PathValue("kind"))
	}
	if hm, ok := s.Engine.Connections.Machines().Get(m.ID); ok {
		if a := hm.Allows(d); !a.Available {
			return m.ID, d, nil, &db.ConflictError{Code: "harness_unavailable", Msg: a.Reason}
		}
	}
	c, ok := s.connectors().Get(m.ID, d.Kind)
	if !ok {
		return m.ID, d, nil, db.Conflict("%s has no connection setup on %s.", d.Name, m.Label)
	}
	return m.ID, d, c, nil
}

func (s *server) writeConnectionStatus(w http.ResponseWriter, ctx context.Context, machine int64, d harness.Descriptor, c harness.Connector, refresh bool, res harness.Result) error {
	st, err := c.Status(ctx, refresh)
	if err != nil {
		return err
	}
	saved, err := s.Store.HarnessSyncStatus(ctx, machine, d.Kind)
	if err != nil {
		return err
	}
	var progress discovery.Progress
	if st.ConnectionID != 0 {
		progress = s.Engine.Discovery.Progress(st.ConnectionID)
	}
	st.Sync = &harness.SyncStatus{
		LastError: saved.LastError, LastFullSyncAt: saved.LastFullSyncAt,
		Running: progress.Running, StartedAt: progress.StartedAt,
		Projects: progress.Projects, Sessions: progress.Sessions,
	}
	if st.LastError != "" {
		message := st.LastError
		st.Sync.LastError = &message
	} else if saved.LastError != nil {
		st.LastError = *saved.LastError
	}
	st.Summary = s.harnessSummary(st.Summary)
	if st.Fields == nil {
		st.Fields = []harness.FieldState{}
	}
	if st.Actions == nil {
		st.Actions = []harness.ActionState{}
	}
	statusFacts := d.DisplayFacts(st.Facts)
	if st.Connected {
		if err := s.Store.SaveConnectionFacts(ctx, st.ConnectionID, statusFacts); err != nil {
			s.log.Warn("could not save connection facts", "connection", st.ConnectionID, "err", err)
		}
	}
	st.Facts = d.DisplayFacts(append(slices.Clone(res.Facts), statusFacts...))
	out := connectionStatusJSON{Kind: d.Kind, Name: d.Name, Status: st, Message: res.Message, Link: res.Link}
	if res.Replaced != 0 {
		if old, err := s.Store.GetHarnessConnection(ctx, res.Replaced); err == nil {
			out.Replaced = &old
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *server) connectionFacts(w http.ResponseWriter, r *http.Request) error {
	id, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	facts, err := s.Store.ConnectionFacts(r.Context(), id)
	if err != nil {
		return err
	}
	if facts == nil {
		facts = []execution.Fact{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"facts": facts})
	return nil
}

func (s *server) harnessSummary(summary harness.Summary) harness.Summary {
	if summary.Configured && summary.LastError == "" {
		summary.LastError = s.Engine.Coordinator.StreamError(summary.ConnectionID)
	}
	return summary
}

// harnessStatus is GET /api/harnesses/{kind}/status (refreshes a stale probe).
func (s *server) harnessStatus(w http.ResponseWriter, r *http.Request) error {
	machine, d, c, err := s.connector(r)
	if err != nil {
		return err
	}
	return s.writeConnectionStatus(w, r.Context(), machine, d, c, true, harness.Result{})
}

// harnessSettings is PUT /api/harnesses/{kind}/settings {field: value|null}.
func (s *server) harnessSettings(w http.ResponseWriter, r *http.Request) error {
	machine, d, c, err := s.connector(r)
	if err != nil {
		return err
	}
	body, err := rawBody(r)
	if err != nil {
		return err
	}
	unlock := s.lockSetupMutation(machine, d.Kind)
	defer unlock()
	res, err := c.Configure(r.Context(), body)
	if err != nil {
		return err
	}
	return s.writeConnectionStatus(w, r.Context(), machine, d, c, true, res)
}

// harnessAction is POST /api/harnesses/{kind}/actions/{action} {input}.
func (s *server) harnessAction(w http.ResponseWriter, r *http.Request) error {
	machine, d, c, err := s.connector(r)
	if err != nil {
		return err
	}
	res, err := s.runAction(r, actionSite{
		descriptor: d,
		machineID:  machine,
		connector:  c,
		request:    execution.ActionRequest{Target: execution.TargetConnection},
	}, true)
	if err != nil {
		return err
	}
	if err := s.writeConnectionStatus(w, r.Context(), machine, d, c, false, res); err != nil {
		return &db.HarnessError{Err: errors.New("The action ran, but connection status could not be refreshed. Check its status before retrying.")}
	}
	return nil
}

// harnessDisconnect is DELETE /api/harnesses/{kind}/connection.
func (s *server) harnessDisconnect(w http.ResponseWriter, r *http.Request) error {
	machine, d, c, err := s.connector(r)
	if err != nil {
		return err
	}
	if !d.Setup.Disconnect {
		return db.Conflict("%s cannot be disconnected from Stage.", d.Name)
	}
	unlock := s.lockSetupMutation(machine, d.Kind)
	defer unlock()
	if err := c.Disconnect(r.Context()); err != nil {
		return err
	}
	return s.writeConnectionStatus(w, r.Context(), machine, d, c, false, harness.Result{})
}
