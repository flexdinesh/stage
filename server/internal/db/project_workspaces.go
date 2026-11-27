package db

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/projectmatch"
)

type Workspace = store.Workspace
type WorkspaceRefresh = store.WorkspaceRefresh

type workspaceRef struct {
	connection int64
	externalID string
	path       string
}

// WorkspaceID derives a stable selector from the exact catalog key. A
// different root or connection gets a different selector, regardless of
// grouping or path canonicalization.
func WorkspaceID(connection int64, externalID, root string) string {
	key, _ := json.Marshal(struct {
		Connection int64
		NativeID   string
		Root       string
	}{connection, externalID, root})
	return fmt.Sprintf("ws_%x", sha256.Sum256(key))
}

func refOf(w projectmatch.Workspace) workspaceRef {
	return workspaceRef{w.Connection, w.ExternalID, w.Path}
}

func evidence(w store.StoredWorkspace) projectmatch.Workspace {
	var fileHost int64
	if w.FileHost != nil {
		fileHost = *w.FileHost
	}
	return projectmatch.Workspace{
		Connection: w.ConnectionID, Machine: w.MachineID, ExternalID: w.ExternalID, Path: w.Workspace.Workspace,
		FileHost: fileHost, CanonicalPath: w.CanonicalPath, CheckoutKey: w.CheckoutKey,
		Subpath: w.Subpath, Remote: w.Remote, InodeHint: w.InodeHint, Available: w.Available, Legacy: w.EvidenceAt == nil,
	}
}

type identityState struct {
	rows     []store.StoredWorkspace
	bindings []store.IdentityBinding
	declines []store.WorkspaceDecline
	input    projectmatch.Input
	byRef    map[workspaceRef]int
}

func loadIdentity(ctx context.Context, pr store.Projects) (identityState, error) {
	var state identityState
	var err error
	if state.rows, err = pr.WorkspaceRows(ctx); err != nil {
		return state, err
	}
	if state.bindings, err = pr.IdentityBindings(ctx); err != nil {
		return state, err
	}
	if state.declines, err = pr.WorkspaceDeclines(ctx); err != nil {
		return state, err
	}
	declined := make(map[store.WorkspaceDecline]bool, len(state.declines))
	for _, d := range state.declines {
		declined[d] = true
	}
	state.byRef = make(map[workspaceRef]int, len(state.rows))
	for i := range state.rows {
		row := &state.rows[i]
		w := evidence(*row)
		row.WorkspaceID = WorkspaceID(w.Connection, w.ExternalID, w.Path)
		row.IdentityKey = projectmatch.Key(w)
		row.Declined = declined[store.WorkspaceDecline{ConnectionID: w.Connection, Key: row.IdentityKey}] ||
			declined[store.WorkspaceDecline{ConnectionID: w.Connection, Key: projectmatch.ExternalKey(w)}]
		var bound int64
		if row.ProjectID != nil {
			bound = *row.ProjectID
		}
		state.byRef[refOf(w)] = i
		state.input.Workspaces = append(state.input.Workspaces, projectmatch.Candidate{
			Workspace: w, Bound: bound, Declined: row.Declined, Importable: row.Importable && !row.HarnessArchived,
			ActiveAt: row.ActiveAt,
		})
	}
	for _, b := range state.bindings {
		key := workspaceRef{b.ConnectionID, b.ExternalProjectID, b.Workspace}
		w := projectmatch.Workspace{
			Connection: b.ConnectionID, Machine: b.MachineID, ExternalID: b.ExternalProjectID,
			Path: b.Workspace, Available: b.Available, Legacy: true,
		}
		if i, ok := state.byRef[key]; ok {
			w = state.input.Workspaces[i].Workspace
		}
		state.input.Bindings = append(state.input.Bindings, projectmatch.Binding{
			ID: b.ID, Project: b.ProjectID, Primary: b.Primary, Workspace: w, Retired: !b.ConnectionActive,
		})
	}
	return state, nil
}

func workspaceResolution(v projectmatch.Verdict) store.WorkspaceResolution {
	return store.WorkspaceResolution{
		Outcome: string(v.Outcome), Reason: string(v.Reason), Rule: string(v.Rule),
		Candidates: append([]int64{}, v.Candidates...),
	}
}

// resolveAll updates derived state from a single indexed view of the inventory.
// The writer must hold the identity lock.
func resolveAll(ctx context.Context, tx store.Tx) error {
	pr := tx.Projects()
	state, err := loadIdentity(ctx, pr)
	if err != nil {
		return err
	}
	resolver := projectmatch.NewResolver(state.input)
	for i, c := range state.input.Workspaces {
		var verdict projectmatch.Verdict
		if c.Available || c.Bound != 0 {
			verdict = resolver.Resolve(c)
		}
		v := workspaceResolution(verdict)
		old := state.rows[i].Resolution
		if v.Outcome == old.Outcome && v.Reason == old.Reason && v.Rule == old.Rule && slices.Equal(v.Candidates, old.Candidates) {
			continue
		}
		if err := pr.SetWorkspaceResolution(ctx, c.Connection, c.ExternalID, c.Path, v); err != nil {
			return err
		}
	}
	return nil
}

// Workspaces returns the connection's currently available stored inventory.
func (s *Store) Workspaces(ctx context.Context, connection int64) ([]Workspace, error) {
	return read(ctx, s, func(tx store.Tx) ([]Workspace, error) {
		state, err := loadIdentity(ctx, tx.Projects())
		if err != nil {
			return nil, err
		}
		out := []Workspace{}
		for _, r := range state.rows {
			if r.ConnectionID == connection && r.Available {
				out = append(out, r.Workspace)
			}
		}
		return out, nil
	})
}

// WorkspaceCatalog reads the last complete inventory and its refresh status.
// Archived and missing rows stay in storage for history.
func (s *Store) WorkspaceCatalog(ctx context.Context, connection int64) ([]Workspace, WorkspaceRefresh, error) {
	var refresh WorkspaceRefresh
	rows, err := read(ctx, s, func(tx store.Tx) ([]Workspace, error) {
		var err error
		refresh, err = tx.Connections().WorkspaceRefresh(ctx, connection)
		if err != nil {
			return nil, err
		}
		state, err := loadIdentity(ctx, tx.Projects())
		if err != nil {
			return nil, err
		}
		out := []Workspace{}
		for _, row := range state.rows {
			if row.ConnectionID == connection && row.Available && !row.HarnessArchived {
				out = append(out, row.Workspace)
			}
		}
		return out, nil
	})
	return rows, refresh, err
}

func (s *Store) RecordWorkspaceRefreshFailure(ctx context.Context, connection int64, failure string) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		return tx.Connections().RecordWorkspaceRefresh(ctx, connection, &failure)
	})
}

// DestinationWorkspaces returns exact rows with a complete, successful
// inventory. Callers still check reachability and launch policy.
func (s *Store) DestinationWorkspaces(ctx context.Context) ([]Workspace, error) {
	return read(ctx, s, func(tx store.Tx) ([]Workspace, error) {
		rows, err := tx.Projects().WorkspaceRows(ctx)
		if err != nil {
			return nil, err
		}
		fresh := make(map[int64]bool)
		out := []Workspace{}
		for _, row := range rows {
			if !row.ConnectionActive || !row.Available || row.HarnessArchived {
				continue
			}
			ok, checked := fresh[row.ConnectionID]
			if !checked {
				status, err := tx.Connections().WorkspaceRefresh(ctx, row.ConnectionID)
				if err != nil {
					return nil, err
				}
				ok = status.RefreshedAt != nil && status.LastError == nil
				fresh[row.ConnectionID] = ok
			}
			if ok {
				row.WorkspaceID = WorkspaceID(row.ConnectionID, row.ExternalID, row.Workspace.Workspace)
				out = append(out, row.Workspace)
			}
		}
		return out, nil
	})
}

func setWorkspaceDecline(ctx context.Context, pr store.Projects, w projectmatch.Workspace, decline bool) error {
	key, external := projectmatch.Key(w), projectmatch.ExternalKey(w)
	if err := pr.SetWorkspaceDecline(ctx, w.Connection, key, decline); err != nil {
		return err
	}
	if key != external {
		return pr.SetWorkspaceDecline(ctx, w.Connection, external, decline)
	}
	return nil
}

func (s *Store) SetWorkspaceDeclined(ctx context.Context, connection int64, externalID, path string, decline bool) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		pr := tx.Projects()
		if err := pr.LockIdentity(ctx); err != nil {
			return err
		}
		if err := connectionUsable(ctx, tx, connection); err != nil {
			return err
		}
		state, err := loadIdentity(ctx, pr)
		if err != nil {
			return err
		}
		i, ok := state.byRef[workspaceRef{connection, externalID, path}]
		if !ok {
			return ErrNotFound
		}
		c := state.input.Workspaces[i]
		if c.Bound != 0 && decline {
			return workspaceBound(ctx, pr, c.Bound)
		}
		if err := setWorkspaceDecline(ctx, pr, c.Workspace, decline); err != nil {
			return err
		}
		return resolveAll(ctx, tx)
	})
}

type ReviewWorkspace struct {
	Workspace
	Reason string `json:"reason"`
}

type WorkspaceMergeSuggestion struct {
	IdentityKey string     `json:"identityKey"`
	ProjectIDs  []int64    `json:"projectIds"`
	Pairs       [][2]int64 `json:"pairs"`
}

type WorkspaceReviewCounts struct {
	New       int `json:"new"`
	Suggested int `json:"suggested"`
	Ambiguous int `json:"ambiguous"`
	Declined  int `json:"declined"`
	Moved     int `json:"moved"`
	Merges    int `json:"merges"`
}

type WorkspaceReview struct {
	Workspaces []ReviewWorkspace          `json:"workspaces"`
	Merges     []WorkspaceMergeSuggestion `json:"merges"`
	Counts     WorkspaceReviewCounts      `json:"counts"`
	Attention  int                        `json:"attention"`
}

func (s *Store) ReviewWorkspaces(ctx context.Context) (WorkspaceReview, error) {
	return read(ctx, s, func(tx store.Tx) (WorkspaceReview, error) {
		out := WorkspaceReview{Workspaces: []ReviewWorkspace{}, Merges: []WorkspaceMergeSuggestion{}}
		pr := tx.Projects()
		state, err := loadIdentity(ctx, pr)
		if err != nil {
			return out, err
		}
		dismissals, err := pr.MergeDismissals(ctx)
		if err != nil {
			return out, err
		}
		dismissed := make(map[[2]int64]bool, len(dismissals))
		for _, pair := range dismissals {
			dismissed[pair] = true
		}
		merges := map[string]map[[2]int64]bool{}
		for _, row := range state.rows {
			if !row.ConnectionActive {
				continue
			}
			v := row.Resolution
			if v.Outcome == "bound" && row.ProjectID != nil {
				for _, other := range v.Candidates {
					pair := [2]int64{min(*row.ProjectID, other), max(*row.ProjectID, other)}
					if dismissed[pair] {
						continue
					}
					if merges[row.IdentityKey] == nil {
						merges[row.IdentityKey] = map[[2]int64]bool{}
					}
					merges[row.IdentityKey][pair] = true
				}
			}
			var reason string
			switch v.Outcome {
			case "new":
				reason = "new"
			case "review":
				reason = v.Reason
			default:
				continue
			}
			switch reason {
			case "new":
				out.Counts.New++
			case "suggested":
				out.Counts.Suggested++
			case "ambiguous":
				out.Counts.Ambiguous++
			case "declined":
				out.Counts.Declined++
			case "moved":
				out.Counts.Moved++
			}
			out.Workspaces = append(out.Workspaces, ReviewWorkspace{Workspace: row.Workspace, Reason: reason})
		}
		for _, key := range slices.Sorted(maps.Keys(merges)) {
			pairs := slices.SortedFunc(maps.Keys(merges[key]), func(a, b [2]int64) int {
				if c := cmp.Compare(a[0], b[0]); c != 0 {
					return c
				}
				return cmp.Compare(a[1], b[1])
			})
			ids := make(map[int64]bool)
			for _, pair := range pairs {
				ids[pair[0]], ids[pair[1]] = true, true
			}
			out.Merges = append(out.Merges, WorkspaceMergeSuggestion{
				IdentityKey: key, ProjectIDs: slices.Sorted(maps.Keys(ids)), Pairs: pairs,
			})
		}
		out.Counts.Merges = len(out.Merges)
		out.Attention = out.Counts.Suggested + out.Counts.Ambiguous + out.Counts.Moved + out.Counts.Merges
		return out, nil
	})
}
