package agenttools

import (
	"context"
	"errors"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
)

// ---- projects, groups, connections, machines, harnesses, preferences ----

// DissolvedOut confirms a dissolve.
type DissolvedOut struct {
	Dissolved bool  `json:"dissolved"`
	ID        int64 `json:"id"`
}

// DissolveTree detaches every member (their needs go) and archives the tree.
func (o *Ops) DissolveTree(ctx context.Context, a TreeIDArgs) (DissolvedOut, error) {
	err := o.Client.DissolveTree(ctx, a.ID)
	return DissolvedOut{Dissolved: err == nil, ID: a.ID}, err
}

// ProjectIDArgs names one Stage project.
type ProjectIDArgs struct {
	ID int64 `json:"id" jsonschema:"Stage project id (a merged id resolves)"`
}

func (o *Ops) GetProject(ctx context.Context, a ProjectIDArgs) (db.ProjectSummary, error) {
	return o.Client.ProjectSummary(ctx, a.ID)
}

// ListProjectGroupsArgs is list_project_groups.
type ListProjectGroupsArgs struct {
	Archived string `json:"archived,omitempty" jsonschema:"false (default: unarchived) | true | all"`
}

// ProjectGroupsOut is list_project_groups.
type ProjectGroupsOut struct {
	Groups []db.ProjectGroupSummary `json:"groups"`
}

func (o *Ops) ListProjectGroups(ctx context.Context, a ListProjectGroupsArgs) (ProjectGroupsOut, error) {
	gs, err := o.Client.ProjectGroups(ctx, a.Archived)
	if gs == nil {
		gs = []db.ProjectGroupSummary{}
	}
	return ProjectGroupsOut{Groups: gs}, err
}

// ProjectGroupIDArgs names one project group.
type ProjectGroupIDArgs struct {
	ID int64 `json:"id" jsonschema:"project group id (a project id or legacy alias)"`
}

func (o *Ops) GetProjectGroup(ctx context.Context, a ProjectGroupIDArgs) (db.ProjectGroupSummary, error) {
	return o.Client.ProjectGroup(ctx, a.ID)
}

// ConnectionsOut is list_connections.
type ConnectionsOut struct {
	Connections []db.HarnessConnection `json:"connections"`
}

func (o *Ops) ListConnections(ctx context.Context, _ struct{}) (ConnectionsOut, error) {
	cs, err := o.Client.Connections(ctx)
	if cs == nil {
		cs = []db.HarnessConnection{}
	}
	return ConnectionsOut{Connections: cs}, err
}

// MachinesOut is list_machines.
type MachinesOut struct {
	Machines []client.Machine `json:"machines"`
}

func (o *Ops) ListMachines(ctx context.Context, _ struct{}) (MachinesOut, error) {
	ms, err := o.Client.Machines(ctx)
	if ms == nil {
		ms = []client.Machine{}
	}
	return MachinesOut{Machines: ms}, err
}

// MachineIDArgs names one machine.
type MachineIDArgs struct {
	ID int64 `json:"id" jsonschema:"machine id from list_machines"`
}

func (o *Ops) GetMachine(ctx context.Context, a MachineIDArgs) (client.Machine, error) {
	return o.Client.Machine(ctx, a.ID)
}

// ListHarnessesArgs is list_harnesses.
type ListHarnessesArgs struct {
	MachineID *int64 `json:"machineId,omitempty" jsonschema:"machine id from list_machines; omitted = the server's machine"`
}

func (o *Ops) ListHarnesses(ctx context.Context, a ListHarnessesArgs) (client.Harnesses, error) {
	if a.MachineID != nil {
		return o.Client.MachineHarnesses(ctx, *a.MachineID)
	}
	return o.Client.Harnesses(ctx)
}

// HarnessStatusArgs is get_harness_status.
type HarnessStatusArgs struct {
	Kind      string `json:"kind" jsonschema:"harness kind from list_harnesses"`
	MachineID *int64 `json:"machineId,omitempty" jsonschema:"machine id from list_machines; omitted = the server's machine"`
}

func (o *Ops) GetHarnessStatus(ctx context.Context, a HarnessStatusArgs) (client.HarnessStatus, error) {
	if a.Kind == "" {
		return client.HarnessStatus{}, errors.New("kind is required: choose a harness kind from list_harnesses")
	}
	if a.MachineID != nil {
		return o.Client.MachineHarnessStatus(ctx, *a.MachineID, a.Kind)
	}
	return o.Client.HarnessStatus(ctx, a.Kind)
}

func (o *Ops) GetPreferences(ctx context.Context, _ struct{}) (client.Preferences, error) {
	return o.Client.Preferences(ctx)
}
