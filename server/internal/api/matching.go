package api

import (
	"context"
	"net/http"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/gitmeta"
	"github.com/flexdinesh/stage/server/internal/machine"
)

// Only repository hosts and paths enter comparisons. Credentials and local
// directory remotes are never returned to a client or treated as proof.
func remoteIdentity(raw string) string { return gitmeta.RemoteIdentity(raw) }

func (s *server) sharedPaths(w http.ResponseWriter, r *http.Request) error {
	if r.Method == http.MethodPut {
		var p db.SharedPath
		if err := decodeStrict(r, &p); err != nil {
			return err
		}
		if err := s.plannedConnection(r.Context(), p.ConnectionID); err != nil {
			return err
		}
		if err := s.Store.SetSharedPath(r.Context(), p); err != nil {
			return err
		}
	}
	paths, err := s.Store.SharedPaths(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"paths": paths})
	return nil
}

// The explicit match action binds by verified checkout evidence. Discovery
// refresh here stays read-only for bindings; the sync pass alone imports
// (db.Store.ImportWorkspaces).
func (s *server) matchWorkspace(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, err := pathID(r)
	if err != nil {
		return err
	}
	connection, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	if _, err = s.Sync.Adapter(connection); err != nil {
		return err
	}
	inventory, err := s.Sync.Inventory(ctx, connection)
	if err != nil {
		return &db.HarnessError{Err: err}
	}
	if _, err = s.Store.RefreshWorkspaces(ctx, connection, inventory); err != nil {
		return err
	}
	candidates, err := s.Store.ExternalProjects(ctx, connection)
	if err != nil {
		return err
	}
	projects, err := s.Store.ListProjects(ctx, "all")
	if err != nil {
		return err
	}
	// Path translation and checkout proof hold only within one machine: the
	// matched connection's, through its machine's ops.
	machineID := s.Store.ConnectionMachine(connection)
	paths, err := s.Store.MachineSharedPaths(ctx, machineID)
	if err != nil {
		return err
	}
	ops, hosted := s.Sync.Ops(machineID)
	checkout := func(id int64, dir string) machine.CheckoutEvidence {
		if !hosted {
			return machine.CheckoutEvidence{}
		}
		var roots []machine.SharedRoot
		for _, p := range paths {
			if p.ConnectionID == id {
				roots = append(roots, machine.SharedRoot{HarnessRoot: p.HarnessRoot, StageRoot: p.StageRoot})
			}
		}
		if len(roots) == 0 {
			return machine.CheckoutEvidence{}
		}
		proof, err := ops.Checkout(ctx, roots, dir)
		if err != nil {
			return machine.CheckoutEvidence{}
		}
		return proof
	}
	// Cached availability cannot confirm a source whose harness changed paths.
	type location struct{ id, dir string }
	inventories := map[int64]map[location]execution.WorkspaceInfo{}
	index := func(rows []execution.WorkspaceInfo) map[location]execution.WorkspaceInfo {
		out := map[location]execution.WorkspaceInfo{}
		for _, w := range rows {
			if w.DeletedAt == nil {
				out[location{w.ID, w.WorkspaceRoot}] = w
			}
		}
		return out
	}
	inventories[connection] = index(inventory)
	evidence := func(id int64, key location) (machine.CheckoutEvidence, bool) {
		items, loaded := inventories[id]
		if !loaded {
			inventories[id] = nil
			if a, e := s.Sync.Adapter(id); e == nil {
				call, cancel := context.WithTimeout(ctx, 3*time.Second)
				rows, e := a.Workspaces(call)
				cancel()
				if e == nil {
					items = index(rows)
					inventories[id] = items
				}
			}
		}
		w, ok := items[key]
		if !ok {
			return machine.CheckoutEvidence{}, false
		}
		var proof machine.CheckoutEvidence
		if s.Store.ConnectionMachine(id) == machineID {
			proof = checkout(id, key.dir)
		}
		if proof.Remote == "" {
			proof.Remote = remoteIdentity(w.OriginRemote)
		}
		// A proven checkout carries its own subproject; otherwise the
		// harness's, when it reported the same remote.
		if proof.Common == "" && proof.Remote != "" && proof.Remote == w.Repo {
			proof.RepoPath = w.RepoPath
		}
		return proof, true
	}
	type owner struct {
		binding db.ProjectBinding
		proof   machine.CheckoutEvidence
	}
	var owners []owner
	already := false
	for _, p := range projects {
		for _, b := range p.Bindings {
			if b.ConnectionID == connection {
				if p.ID == id {
					already = true
				}
				continue
			}
			if b.Status == "available" {
				if proof, ok := evidence(b.ConnectionID, location{b.ExternalProjectID, b.Workspace}); ok {
					owners = append(owners, owner{b, proof})
				}
			}
		}
	}
	var chosen []db.ProjectBinding
	var source int64
	ambiguous := false
	suggestions := map[string]string{}
	for _, c := range candidates {
		proof, ok := evidence(connection, location{c.ExternalID, c.Workspace})
		if !ok {
			continue
		}
		matches := 0
		var selected db.ProjectBinding
		for _, o := range owners {
			if proof.SameCheckout(o.proof) {
				matches++
				if o.binding.ProjectID == id {
					selected = o.binding
					suggestions[c.ExternalID] = "Shared Git checkout and subproject"
				}
			} else if o.binding.ProjectID == id && proof.SameRemote(o.proof) {
				suggestions[c.ExternalID] = "Same Git remote. Confirm the checkout."
			}
		}
		if selected.ID != 0 && matches > 1 {
			ambiguous = true
		}
		if matches == 1 && selected.ID != 0 {
			source = selected.ID
			chosen = append(chosen, db.ProjectBinding{ProjectID: id, ConnectionID: connection, ExternalProjectID: c.ExternalID, Workspace: c.Workspace})
		}
	}
	var bound *db.Project
	if !already && !ambiguous && len(chosen) == 1 {
		for _, c := range candidates {
			if c.ExternalID == chosen[0].ExternalProjectID && c.Workspace == chosen[0].Workspace && c.ProjectID == nil {
				p, err := s.Store.AutoBind(ctx, source, chosen[0], paths)
				if err != nil {
					return err
				}
				bound = &p
				break
			}
		}
	}
	writeJSON(w, 200, map[string]any{"bound": bound, "suggestions": suggestions, "projects": candidates})
	return nil
}
