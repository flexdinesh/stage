package api

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

type checkoutEvidence struct {
	common             os.FileInfo
	subproject, remote string
}

func beneath(root, dir string) (string, bool) {
	rel, err := filepath.Rel(root, dir)
	return rel, err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func checkout(ctx context.Context, paths []db.SharedPath, connection int64, directory string) checkoutEvidence {
	for _, p := range paths {
		if p.ConnectionID != connection {
			continue
		}
		sub, ok := beneath(p.HarnessRoot, directory)
		if !ok {
			continue
		}
		root, err := filepath.EvalSymlinks(p.StageRoot)
		if err != nil {
			return checkoutEvidence{}
		}
		dir, err := filepath.EvalSymlinks(filepath.Join(root, sub))
		if err != nil {
			return checkoutEvidence{}
		}
		if _, ok = beneath(root, dir); !ok {
			return checkoutEvidence{}
		}
		git := func(args ...string) string {
			c := exec.CommandContext(ctx, "git", args...)
			c.Dir = dir
			c.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_OPTIONAL_LOCKS=0"}
			out, err := c.Output()
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		}
		top := git("rev-parse", "--show-toplevel")
		if top == "" {
			return checkoutEvidence{}
		}
		sub, ok = beneath(top, dir)
		if !ok {
			return checkoutEvidence{}
		}
		common, err := os.Stat(git("rev-parse", "--path-format=absolute", "--git-common-dir"))
		if err != nil {
			return checkoutEvidence{}
		}
		return checkoutEvidence{common: common, subproject: sub, remote: remoteIdentity(git("config", "--local", "--no-includes", "--get", "remote.origin.url"))}
	}
	return checkoutEvidence{}
}

// Only repository hosts and paths enter comparisons. Credentials and local
// directory remotes are never returned to a client or treated as proof.
func remoteIdentity(raw string) string {
	if !strings.Contains(raw, "://") {
		host, path, ok := strings.Cut(raw, ":")
		if !ok || strings.ContainsAny(host, "/\\") {
			return ""
		}
		raw = "ssh://" + host + "/" + path
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Path == "" || (u.Scheme != "ssh" && u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "git") {
		return ""
	}
	return strings.ToLower(u.Host) + strings.TrimSuffix(strings.TrimRight(u.Path, "/"), ".git")
}

func (s *server) sharedPaths(w http.ResponseWriter, r *http.Request) error {
	if r.Method == http.MethodPut {
		var p db.SharedPath
		if err := decodeStrict(r, &p); err != nil {
			return err
		}
		if err := s.plannedConnection(p.ConnectionID); err != nil {
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

// Only this explicit setup action may automatically bind. Refresh is read-only
// with respect to binding ownership.
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
	adapter, err := s.Sync.Adapter(connection)
	if err != nil {
		return err
	}
	inventory, err := adapter.Workspaces(ctx)
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
	paths, err := s.Store.SharedPaths(ctx)
	if err != nil {
		return err
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
	evidence := func(id int64, key location) (checkoutEvidence, bool) {
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
			return checkoutEvidence{}, false
		}
		proof := checkout(ctx, paths, id, key.dir)
		if proof.remote == "" {
			proof.remote = remoteIdentity(w.OriginRemote)
		}
		return proof, true
	}
	type owner struct {
		binding db.ProjectBinding
		proof   checkoutEvidence
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
			if proof.common != nil && o.proof.common != nil && proof.subproject == o.proof.subproject && os.SameFile(proof.common, o.proof.common) {
				matches++
				if o.binding.ProjectID == id {
					selected = o.binding
					suggestions[c.ExternalID] = "Shared Git checkout and subproject"
				}
			} else if o.binding.ProjectID == id && proof.remote != "" && proof.remote == o.proof.remote {
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
