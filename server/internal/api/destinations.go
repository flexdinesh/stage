package api

import (
	"cmp"
	"context"
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/principal"
)

func projectSummaryRequest(r *http.Request) bool {
	actor, _ := principal.From(r.Context())
	return actor.Kind == principal.Agent || r.Header.Get(principal.ToolTransportHeader) == "mcp" ||
		r.URL.Query().Get("view") == "destinations"
}

func (s *server) destinations(ctx context.Context) ([]db.Destination, error) {
	rows, err := s.Store.DestinationWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	cs, err := s.Store.Connections(ctx)
	if err != nil {
		return nil, err
	}
	connections := make(map[int64]db.HarnessConnection)
	for _, c := range cs {
		if !c.Active || !s.Engine.Connections.Online(c.MachineID) {
			continue
		}
		if _, err := s.Engine.Connections.Adapter(c.ID); err != nil {
			continue
		}
		if _, connector, ok := s.connectors().Owner(ctx, c.ID); ok {
			if status := connector.Summary(ctx); !status.Configured || !status.Connected {
				continue
			}
		}
		connections[c.ID] = c
	}
	machines := make(map[int64]string)
	type displayed struct{ title, connection, machine string }
	counts := make(map[displayed]int)
	keys := make([]displayed, 0, len(rows))
	out := make([]db.Destination, 0, len(rows))
	for _, row := range rows {
		c, ok := connections[row.ConnectionID]
		if !ok {
			continue
		}
		machine, ok := machines[c.MachineID]
		if !ok {
			m, err := s.Store.GetMachineInfo(ctx, c.MachineID)
			if err != nil {
				return nil, err
			}
			machine = cmp.Or(m.Label, "This computer")
			machines[c.MachineID] = machine
		}
		key := displayed{db.WorkspaceTitle(row.Title, row.Workspace), db.SummarizeConnection(c, s.registry().Name(c.Harness)).Label, machine}
		counts[key]++
		keys = append(keys, key)
		out = append(out, db.Destination{
			ConnectionID: c.ID, WorkspaceID: row.WorkspaceID, ProjectID: row.ProjectID,
			GroupProjectID: row.GroupProjectID,
			Title:          key.title, ConnectionLabel: key.connection, MachineLabel: key.machine, Available: true,
		})
	}
	for i, key := range keys {
		if counts[key] > 1 {
			out[i].Discriminator = out[i].WorkspaceID[len("ws_") : len("ws_")+8]
		}
	}
	return out, nil
}

func projectSummary(p db.Project, available map[string]bool) db.ProjectSummary {
	out := db.ProjectSummary{ID: p.ID, Title: p.Title, ArchivedAt: p.ArchivedAt, Aliases: p.Aliases,
		Bindings: make([]db.BindingSummary, 0, len(p.Bindings))}
	for _, b := range p.Bindings {
		id := db.WorkspaceID(b.ConnectionID, b.ExternalProjectID, b.Workspace)
		status := b.Status
		switch {
		case !b.ConnectionActive:
			status = "connection_retired"
		case status == "available" && !available[id]:
			status = "workspace_unavailable"
		}
		out.Bindings = append(out.Bindings, db.BindingSummary{
			ID: b.ID, ConnectionID: b.ConnectionID, WorkspaceID: id, Title: db.WorkspaceTitle(b.Title, b.Workspace),
			ConnectionActive: b.ConnectionActive, Primary: b.Primary, Status: status,
		})
	}
	return out
}

func availableDestinations(ds []db.Destination) map[string]bool {
	out := make(map[string]bool, len(ds))
	for _, d := range ds {
		out[d.WorkspaceID] = d.Available
	}
	return out
}
