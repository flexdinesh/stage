package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/projectmatch"
)

type ProjectGroupSummary struct {
	ID         int64          `json:"id"`
	Title      string         `json:"title"`
	ArchivedAt *time.Time     `json:"archivedAt"`
	Aliases    []ProjectAlias `json:"aliases"`
}

func SummarizeProjectGroup(p Project) ProjectGroupSummary {
	return ProjectGroupSummary{ID: p.ID, Title: p.Title, ArchivedAt: p.ArchivedAt, Aliases: p.Aliases}
}

type ProjectGroup struct {
	ProjectGroupSummary
	LastActivityAt    *time.Time       `json:"lastActivityAt"`
	State             string           `json:"state"`
	Members           []GroupWorkspace `json:"members"`
	RelatedProjectIDs []int64          `json:"relatedProjectIds"`
}

type GroupWorkspace struct {
	ConnectionID int64  `json:"connectionId"`
	WorkspaceID  string `json:"workspaceId"`
	MachineID    int64  `json:"machineId"`
	Title        string `json:"title"`
	Path         string `json:"path"`
	CatalogState string `json:"catalogState"`
}

func groupWorkspace(row store.StoredWorkspace) GroupWorkspace {
	state := "listed"
	switch {
	case !row.ConnectionActive:
		state = "retired"
	case !row.Available:
		state = "missing"
	case row.HarnessArchived:
		state = "archived"
	}
	return GroupWorkspace{
		ConnectionID: row.ConnectionID,
		WorkspaceID:  WorkspaceID(row.ConnectionID, row.ExternalID, row.Workspace.Workspace),
		MachineID:    row.MachineID,
		Title:        WorkspaceTitle(row.Title, row.Workspace.Workspace),
		Path:         row.Workspace.Workspace,
		CatalogState: state,
	}
}

type groupingIndex map[string]map[int64]struct{}

func workspaceGroupingKey(row store.StoredWorkspace) string {
	if row.EvidenceSource == "" {
		return ""
	}
	return projectmatch.GroupingKey(evidence(row))
}

func (index groupingIndex) add(key string, project int64) {
	if key == "" {
		return
	}
	if index[key] == nil {
		index[key] = make(map[int64]struct{})
	}
	index[key][project] = struct{}{}
}

func (index groupingIndex) sole(key string) int64 {
	if len(index[key]) == 1 {
		for id := range index[key] {
			return id
		}
	}
	return 0
}

// organizeWorkspaces assigns only unplaced catalog rows. Existing membership
// survives evidence changes, connection retirement and manual separation.
func organizeWorkspaces(ctx context.Context, pr store.Projects, state identityState, connection int64) error {
	index := groupIndex(state.rows)
	bindings := make(map[int64]store.IdentityBinding)
	for _, b := range state.bindings {
		if b.Rule == "manual" || b.Rule == "new" {
			bindings[b.ID] = b
		}
	}
	anchors := make(map[int]store.IdentityBinding)
	fallbacks := make(map[int]int64)
	var pending []int
	var moves projectmatch.Input
	for i, row := range state.rows {
		candidate := state.input.Workspaces[i]
		if row.GroupProjectID != nil {
			moves.Bindings = append(moves.Bindings, projectmatch.Binding{
				Project: *row.GroupProjectID, Workspace: candidate.Workspace, Retired: !row.ConnectionActive,
			})
		} else if row.ConnectionActive && row.Available && !row.HarnessArchived {
			if row.ConnectionID == connection {
				pending = append(pending, i)
				if row.HistoricalProjectID != nil {
					fallbacks[i] = *row.HistoricalProjectID
				}
				if row.BindingID != nil {
					anchor := bindings[*row.BindingID]
					anchors[i] = anchor
					if anchor.Rule == "manual" {
						index.add(workspaceGroupingKey(row), anchor.ProjectID)
						continue
					}
					if anchor.Rule == "new" && fallbacks[i] == 0 {
						fallbacks[i] = anchor.ProjectID
					}
				}
			}
			candidate.Bound = 0
			moves.Workspaces = append(moves.Workspaces, candidate)
		}
	}
	resolver := projectmatch.NewResolver(moves)
	// A legacy import can supply the first project ID for a key. Established
	// grouping and manual anchors take precedence over that import.
	for _, i := range pending {
		if fallback := fallbacks[i]; fallback != 0 && anchors[i].Rule != "manual" {
			key := workspaceGroupingKey(state.rows[i])
			candidate := state.input.Workspaces[i]
			candidate.Bound = 0
			move := resolver.Resolve(candidate)
			if len(index[key]) == 0 && !candidate.Declined && move.Rule != projectmatch.RuleMoved {
				index.add(key, fallback)
			}
		}
	}
	attribution := WorkAttribution(AutomatedWork(ctx, "system", Cause{Kind: "workspace_discovery"}, Attribution{}))
	for _, i := range pending {
		row := state.rows[i]
		candidate := state.input.Workspaces[i]
		candidate.Bound = 0
		key := workspaceGroupingKey(row)
		group := index.sole(key)
		anchor := anchors[i]
		if anchor.Rule == "manual" {
			group = anchor.ProjectID
		} else if move := resolver.Resolve(candidate); candidate.Declined || move.Reason == projectmatch.ReasonMoved ||
			(move.Rule == projectmatch.RuleMoved && move.Project != group) {
			group = 0
		}
		if group == 0 {
			group = fallbacks[i]
		}
		if group == 0 {
			var err error
			group, err = pr.InsertCatalogGroup(ctx, WorkspaceTitle(row.Title, row.Workspace.Workspace), DefaultUser, nil)
			if err != nil {
				return err
			}
		}
		if err := pr.SetWorkspaceGroup(ctx, row, group, attribution); err != nil {
			return err
		}
		index.add(key, group)
	}
	return nil
}

func groupIndex(rows []store.StoredWorkspace) groupingIndex {
	index := make(groupingIndex)
	for _, row := range rows {
		if row.GroupProjectID != nil {
			index.add(workspaceGroupingKey(row), *row.GroupProjectID)
		}
	}
	return index
}

func groupViews(projects []Project, rows []store.StoredWorkspace) []ProjectGroup {
	index := groupIndex(rows)
	groups := make(map[int64]*ProjectGroup, len(projects))
	out := make([]ProjectGroup, len(projects))
	for i, p := range projects {
		out[i] = ProjectGroup{
			ProjectGroupSummary: SummarizeProjectGroup(p), LastActivityAt: p.LastActivityAt,
			State: "empty", Members: []GroupWorkspace{}, RelatedProjectIDs: []int64{},
		}
		groups[p.ID] = &out[i]
	}
	for _, row := range rows {
		if row.GroupProjectID == nil {
			continue
		}
		group := groups[*row.GroupProjectID]
		if group == nil {
			continue
		}
		group.Members = append(group.Members, groupWorkspace(row))
		switch {
		case row.ConnectionActive:
			group.State = "current"
		case group.State == "empty":
			group.State = "retired"
		}
		for other := range index[workspaceGroupingKey(row)] {
			if other != group.ID {
				group.RelatedProjectIDs = append(group.RelatedProjectIDs, other)
			}
		}
	}
	for i := range out {
		ids := out[i].RelatedProjectIDs
		slices.Sort(ids)
		out[i].RelatedProjectIDs = slices.Compact(ids)
	}
	return out
}

func (s *Store) ListProjectGroupSummaries(ctx context.Context, archived string) ([]ProjectGroupSummary, error) {
	return read(ctx, s, func(tx store.Tx) ([]ProjectGroupSummary, error) {
		projects, err := tx.Projects().List(ctx, projectArchive(archived), true)
		if err != nil {
			return nil, err
		}
		out := make([]ProjectGroupSummary, len(projects))
		for i, project := range projects {
			out[i] = SummarizeProjectGroup(project)
		}
		return out, nil
	})
}

func (s *Store) ListProjectGroups(ctx context.Context, archived string) ([]ProjectGroup, error) {
	return read(ctx, s, func(tx store.Tx) ([]ProjectGroup, error) {
		pr := tx.Projects()
		projects, err := pr.List(ctx, projectArchive(archived), true)
		if err != nil {
			return nil, err
		}
		rows, err := pr.WorkspaceRows(ctx)
		if err != nil {
			return nil, err
		}
		return groupViews(projects, rows), nil
	})
}

func (s *Store) GetProjectGroup(ctx context.Context, id int64) (ProjectGroup, error) {
	return read(ctx, s, func(tx store.Tx) (ProjectGroup, error) {
		pr := tx.Projects()
		canonical, err := pr.CanonicalID(ctx, id)
		if err != nil {
			return ProjectGroup{}, err
		}
		project, err := pr.Get(ctx, canonical)
		if err != nil {
			return ProjectGroup{}, err
		}
		rows, err := pr.WorkspaceRows(ctx)
		if err != nil {
			return ProjectGroup{}, err
		}
		return groupViews([]Project{project}, rows)[0], nil
	})
}

type GroupEditInput struct {
	ConnectionID    int64  `json:"connectionId"`
	WorkspaceID     string `json:"workspaceId"`
	TargetProjectID *int64 `json:"targetProjectId"`
	PreviewToken    string `json:"previewToken"`
}

type GroupEdit struct {
	Applied      bool           `json:"applied"`
	Workspace    GroupWorkspace `json:"workspace"`
	Source       MergeSource    `json:"source"`
	Target       *MergeSource   `json:"target"`
	NewTitle     string         `json:"newTitle,omitempty"`
	Blocked      *MergeBlock    `json:"blocked"`
	PreviewToken string         `json:"previewToken"`
}

// EditProjectGroup moves one exact catalog row into an existing group, or
// separates it into a new group when TargetProjectID is nil.
func (s *Store) EditProjectGroup(ctx context.Context, in GroupEditInput) (GroupEdit, error) {
	if in.ConnectionID <= 0 || in.WorkspaceID == "" || (in.TargetProjectID != nil && *in.TargetProjectID <= 0) {
		return GroupEdit{}, Invalid("Choose an exact workspace and, when grouping, a project.")
	}
	if in.PreviewToken == "" {
		return read(ctx, s, func(tx store.Tx) (GroupEdit, error) {
			preview, _, err := previewGroupEdit(ctx, tx, in)
			return preview, err
		})
	}
	return write(ctx, s, func(tx store.Tx) (GroupEdit, error) {
		pr := tx.Projects()
		if err := pr.LockIdentity(ctx); err != nil {
			return GroupEdit{}, err
		}
		preview, row, err := previewGroupEdit(ctx, tx, in)
		if err != nil {
			return GroupEdit{}, err
		}
		if preview.PreviewToken != in.PreviewToken {
			return GroupEdit{}, &PreviewChangedError{Fresh: preview}
		}
		if preview.Blocked != nil {
			return GroupEdit{}, &ConflictError{Code: preview.Blocked.Code, Msg: preview.Blocked.Reason}
		}
		if preview.Target == nil {
			id, err := pr.InsertCatalogGroup(ctx, preview.NewTitle, ActingUser(ctx), ActingMachine(ctx))
			if err != nil {
				return GroupEdit{}, err
			}
			preview.Target = &MergeSource{ID: id, Title: preview.NewTitle}
		}
		if err := pr.SetWorkspaceGroup(ctx, row, preview.Target.ID, WorkAttribution(ctx)); err != nil {
			return GroupEdit{}, err
		}
		preview.Applied = true
		preview.PreviewToken = ""
		return preview, nil
	})
}

func previewGroupEdit(ctx context.Context, tx store.Tx, in GroupEditInput) (GroupEdit, store.StoredWorkspace, error) {
	pr := tx.Projects()
	rows, err := pr.WorkspaceRows(ctx)
	if err != nil {
		return GroupEdit{}, store.StoredWorkspace{}, err
	}
	var row store.StoredWorkspace
	for _, candidate := range rows {
		if candidate.ConnectionID == in.ConnectionID && WorkspaceID(candidate.ConnectionID, candidate.ExternalID, candidate.Workspace.Workspace) == in.WorkspaceID {
			row = candidate
			break
		}
	}
	if row.GroupProjectID == nil {
		return GroupEdit{}, row, ErrNotFound
	}
	source, err := pr.Get(ctx, *row.GroupProjectID)
	if err != nil {
		return GroupEdit{}, row, err
	}
	var target Project
	out := GroupEdit{Workspace: groupWorkspace(row), Source: MergeSource{ID: source.ID, Title: source.Title}}
	if in.TargetProjectID == nil {
		out.NewTitle = out.Workspace.Title
	} else {
		id, err := pr.CanonicalID(ctx, *in.TargetProjectID)
		if err != nil {
			return GroupEdit{}, row, err
		}
		target, err = pr.Get(ctx, id)
		if err != nil {
			return GroupEdit{}, row, err
		}
		out.Target = &MergeSource{ID: id, Title: target.Title}
		if target.ID == source.ID {
			out.Blocked = &MergeBlock{Code: "same_project", Reason: "This workspace already belongs to that group."}
		}
	}
	type member struct {
		Workspace GroupWorkspace
		Group     int64
		Revision  int64
	}
	var members []member
	for _, workspace := range rows {
		if id := workspace.GroupProjectID; id != nil && (*id == source.ID || *id == target.ID) {
			members = append(members, member{groupWorkspace(workspace), *id, workspace.GroupRevision})
		}
	}
	installation, err := tx.Access().Installation(ctx)
	if err != nil {
		return GroupEdit{}, row, err
	}
	snapshot, err := json.Marshal(struct {
		Installation                   string
		Actor                          store.Actor
		SourceArchived, TargetArchived *time.Time
		Edit                           GroupEdit
		Members                        []member
	}{installation.ID, WorkAttribution(ctx).Actor, source.ArchivedAt, target.ArchivedAt, out, members})
	if err != nil {
		return GroupEdit{}, row, err
	}
	sum := sha256.Sum256(snapshot)
	out.PreviewToken = hex.EncodeToString(sum[:])
	return out, row, nil
}
