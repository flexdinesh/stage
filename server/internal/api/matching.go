package api

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/gitmeta"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/projectmatch"
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

// The explicit match action walks the project identity ladder
// (projectmatch.Resolve) for the connection's live inventory against the
// other connections' available bindings, with Git checkout proof from the
// matched connection's machine: a workspace the ladder places in this
// project binds when it is the only one, and equal remotes suggest.
// Discovery refresh here stays read-only for other bindings; the sync pass
// alone imports (db.Store.ImportWorkspaces).
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
	checkout := func(id int64, w execution.WorkspaceInfo) machine.CheckoutEvidence {
		if !hosted {
			return machine.CheckoutEvidence{}
		}
		var declared []machine.SharedRoot
		for _, p := range paths {
			if p.ConnectionID == id {
				declared = append(declared, machine.SharedRoot{HarnessRoot: p.HarnessRoot, StageRoot: p.StageRoot})
			}
		}
		roots := machine.RootsFor(w, declared)
		if len(roots) == 0 {
			return machine.CheckoutEvidence{}
		}
		proof, err := ops.Checkout(ctx, roots, w.WorkspaceRoot)
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
	evidence := func(id int64, key location) (projectmatch.Evidence, bool) {
		items, loaded := inventories[id]
		if !loaded {
			inventories[id] = nil
			call, cancel := context.WithTimeout(ctx, 3*time.Second)
			rows, e := s.Sync.Inventory(call, id)
			cancel()
			if e == nil {
				items = index(rows)
				inventories[id] = items
			}
		}
		w, ok := items[key]
		if !ok {
			return projectmatch.Evidence{}, false
		}
		m := s.Store.ConnectionMachine(id)
		var proof machine.CheckoutEvidence
		if m == machineID {
			proof = checkout(id, w)
		}
		e := projectmatch.Evidence{Connection: id, Machine: m, Path: w.WorkspaceRoot, Common: proof.Common, Subproject: proof.Subproject, Remote: proof.Remote, RepoPath: proof.RepoPath}
		if e.Remote == "" {
			e.Remote = cmp.Or(w.Repo, remoteIdentity(w.OriginRemote))
		}
		// A proven checkout carries its own subproject; otherwise the
		// inventory's, when it names the same remote.
		if proof.Common == "" && e.Remote != "" && e.Remote == w.Repo {
			e.RepoPath = w.RepoPath
		}
		return e, true
	}
	var owners []projectmatch.Binding
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
				if e, ok := evidence(b.ConnectionID, location{b.ExternalProjectID, b.Workspace}); ok {
					owners = append(owners, projectmatch.Binding{ID: b.ID, Project: p.ID, Evidence: e})
				}
			}
		}
	}
	var chosen []db.ProjectBinding
	var source int64
	ambiguous := false
	suggestions := map[string]string{}
	for _, c := range candidates {
		e, ok := evidence(connection, location{c.ExternalID, c.Workspace})
		if !ok {
			continue
		}
		// An explicit match is the user's decision, like a manual bind: an
		// earlier decline does not stop it.
		v := projectmatch.Resolve(projectmatch.Candidate{Evidence: e, Bound: ptrValue(c.ProjectID)}, owners)
		switch {
		case v.Rung == projectmatch.RungBinding:
		case v.Project == id:
			suggestions[c.ExternalID] = matchReason(v)
			source = v.Via
			chosen = append(chosen, db.ProjectBinding{ProjectID: id, ConnectionID: connection, ExternalProjectID: c.ExternalID, Workspace: c.Workspace})
		case slices.Contains(v.Matches, id):
			suggestions[c.ExternalID] = matchReason(v)
			// The same machine names several projects: none binds.
			ambiguous = ambiguous || v.Rung != projectmatch.RungRemote
		case slices.Contains(v.Suggest, id):
			suggestions[c.ExternalID] = sameRemote
		}
	}
	var bound *db.Project
	if !already && !ambiguous && len(chosen) == 1 {
		p, err := s.Store.AutoBind(ctx, source, chosen[0], paths)
		if err != nil {
			return err
		}
		bound = &p
	}
	writeJSON(w, 200, map[string]any{"bound": bound, "suggestions": suggestions, "projects": candidates})
	return nil
}

const sameRemote = "Same Git remote. Confirm the checkout."

// matchReason says which rung named the project.
func matchReason(v projectmatch.Verdict) string {
	switch {
	case v.Rung == projectmatch.RungPath:
		return "Same path on this machine"
	case v.Rung == projectmatch.RungCheckout:
		return "Shared Git checkout and subproject"
	case v.Leave:
		return sameRemote
	default:
		return "Same Git remote and subproject on another machine"
	}
}

func ptrValue(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
