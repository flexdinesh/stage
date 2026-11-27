package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/projectmatch"
)

// ImportNoticeWindow is how long the Projects page reports an import.
const ImportNoticeWindow = 7 * 24 * time.Hour

type ImportResult struct{ Created, Bound, Moved int }

type ResolvePlacement struct {
	Workspace       Workspace            `json:"workspace"`
	Outcome         projectmatch.Outcome `json:"outcome"`
	Reason          projectmatch.Reason  `json:"reason"`
	Rule            projectmatch.Rule    `json:"rule"`
	ProjectID       *int64               `json:"projectId"`
	NewProjectTitle string               `json:"newProjectTitle"`
	Candidates      []int64              `json:"candidates"`
	Acts            bool                 `json:"acts"`
}

type ResolveResult struct {
	Applied      bool               `json:"applied"`
	PreviewToken string             `json:"previewToken"`
	Placements   []ResolvePlacement `json:"placements"`
	Created      int                `json:"created"`
	Joined       int                `json:"joined"`
	Moved        int                `json:"moved"`
	Review       int                `json:"review"`
}

// ResolveWorkspaces previews or applies placements from persisted evidence.
// A caller confirming a preview supplies its token. Automatic import plans and
// applies in the same transaction.
func (s *Store) ResolveWorkspaces(ctx context.Context, connection int64, apply bool, previewToken ...string) (ResolveResult, error) {
	return s.resolveWorkspaces(ctx, connection, apply, nil, previewToken)
}

// ConfirmWorkspaceResolution compares a complete live inventory inside the
// write transaction. A stale confirmation rolls back its tentative refresh.
func (s *Store) ConfirmWorkspaceResolution(ctx context.Context, connection int64, inventory []execution.WorkspaceInfo, token string) (ResolveResult, error) {
	return s.resolveWorkspaces(ctx, connection, true, &inventory, []string{token})
}

func (s *Store) resolveWorkspaces(ctx context.Context, connection int64, apply bool, inventory *[]execution.WorkspaceInfo, previewToken []string) (ResolveResult, error) {
	fn := func(tx store.Tx) (ResolveResult, error) {
		pr := tx.Projects()
		if apply {
			if err := pr.LockIdentity(ctx); err != nil {
				return ResolveResult{}, err
			}
		}
		if err := connectionUsable(ctx, tx, connection); err != nil {
			return ResolveResult{}, err
		}
		if inventory != nil {
			if _, err := refreshWorkspaceRows(ctx, tx, connection, *inventory); err != nil {
				return ResolveResult{}, err
			}
			if err := resolveAll(ctx, tx); err != nil {
				return ResolveResult{}, err
			}
		}
		state, err := loadIdentity(ctx, pr)
		if err != nil {
			return ResolveResult{}, err
		}
		plan := state.plan(connection)
		result, err := state.preview(connection, plan)
		if err != nil {
			return result, err
		}
		if !apply {
			return result, nil
		}
		if len(previewToken) > 0 && previewToken[0] != result.PreviewToken {
			return result, &PreviewChangedError{Fresh: result}
		}
		if err := s.applyPlacements(ctx, tx, state, plan, &result); err != nil {
			return ResolveResult{}, err
		}
		if err := resolveCatalog(ctx, tx, connection); err != nil {
			return ResolveResult{}, err
		}
		result.Applied = true
		return result, nil
	}
	if apply {
		return write(ctx, s, fn)
	}
	return read(ctx, s, fn)
}

func (state identityState) plan(connection int64) []projectmatch.Placement {
	titles := make(map[string]string)
	for _, row := range state.rows {
		if row.ConnectionID != connection {
			continue
		}
		title := strings.TrimSpace(row.Title)
		if title == "" && row.Workspace.Workspace != "" {
			title = filepath.Base(row.Workspace.Workspace)
		}
		if title == "" {
			title = row.ExternalID
		}
		titles[projectmatch.TitleKey(row.ExternalID, row.Workspace.Workspace)] = title
	}
	return projectmatch.Plan(state.input, connection, titles)
}

func (state identityState) preview(connection int64, plan []projectmatch.Placement) (ResolveResult, error) {
	out := ResolveResult{Placements: make([]ResolvePlacement, 0, len(plan))}
	titles := make(map[int64]string)
	for _, p := range plan {
		row := state.rows[state.byRef[refOf(p.Workspace)]]
		placement := ResolvePlacement{
			Workspace: row.Workspace, Outcome: p.Outcome, Reason: p.Reason, Rule: p.Rule, Acts: p.Acts,
			Candidates: append([]int64{}, p.Candidates...), NewProjectTitle: p.NewTitle,
		}
		if p.Project > 0 {
			placement.ProjectID = &p.Project
		}
		if p.Outcome == projectmatch.OutcomeNew && p.Acts {
			titles[p.Project] = p.NewTitle
		}
		if p.Project < 0 {
			placement.NewProjectTitle = titles[p.Project]
		}
		switch {
		case p.Outcome == projectmatch.OutcomeReview:
			out.Review++
		case p.Outcome == projectmatch.OutcomeNew && p.Acts:
			out.Created++
		case p.Rule == projectmatch.RuleMoved:
			out.Moved++
		case p.Outcome == projectmatch.OutcomeJoin:
			out.Joined++
		}
		out.Placements = append(out.Placements, placement)
	}
	// The resolver's input includes semantic evidence and binding ownership.
	// Binding revisions also catch a replacement with otherwise equal evidence.
	// Excluding evidenceAt keeps an identical inventory refresh confirmable.
	revisions := make([]int64, 0, len(state.bindings))
	for _, b := range state.bindings {
		revisions = append(revisions, b.Revision)
	}
	body, err := json.Marshal(struct {
		Connection int64
		Input      projectmatch.Input
		Revisions  []int64
		Plan       []projectmatch.Placement
	}{connection, state.input, revisions, plan})
	if err != nil {
		return ResolveResult{}, err
	}
	sum := sha256.Sum256(body)
	out.PreviewToken = hex.EncodeToString(sum[:])
	return out, nil
}

func (s *Store) applyPlacements(ctx context.Context, tx store.Tx, state identityState, plan []projectmatch.Placement, out *ResolveResult) error {
	pr := tx.Projects()
	// Every placement belongs to the one connection being resolved.
	placed := make(map[[2]int64]bool)
	bindings := make(map[int64]store.IdentityBinding, len(state.bindings))
	for _, b := range state.bindings {
		placed[[2]int64{b.ProjectID, b.ConnectionID}] = true
		bindings[b.ID] = b
	}
	projectIDs := map[int64]int64{}
	for i, p := range plan {
		if !p.Acts {
			continue
		}
		id := p.Project
		var err error
		if p.Outcome == projectmatch.OutcomeNew {
			id, err = pr.InsertDiscovered(ctx, p.NewTitle, p.Connection, ActingUser(ctx))
			if err != nil {
				return err
			}
			projectIDs[p.Project] = id
		} else if id < 0 {
			id = projectIDs[id]
		}
		key := [2]int64{id, p.Connection}
		b := ProjectBinding{ProjectID: id, ConnectionID: p.Connection, ExternalProjectID: p.ExternalID,
			Workspace: p.Path, Rule: string(p.Rule), Primary: !placed[key]}
		switch {
		case p.Outcome == projectmatch.OutcomeNew:
			b.Rule = "new"
			_, err = pr.InsertBinding(ctx, b)
		case p.Rule == projectmatch.RuleMoved:
			_, err = s.moveBinding(ctx, tx, b, bindings[p.Via].ProjectBinding)
		default:
			_, err = pr.InsertBinding(ctx, b)
		}
		if err != nil {
			return err
		}
		placed[key] = true
		out.Placements[i].ProjectID = &id
	}
	for i := range out.Placements {
		for j, candidate := range out.Placements[i].Candidates {
			if id, ok := projectIDs[candidate]; ok {
				out.Placements[i].Candidates[j] = id
			}
		}
	}
	return nil
}

func (s *Store) ImportWorkspaces(ctx context.Context, connection int64) (ImportResult, error) {
	ctx = WithUser(withoutMachine(ctx), DefaultUser)
	result, err := s.ResolveWorkspaces(ctx, connection, true)
	var conflict *ConflictError
	if errors.As(err, &conflict) && conflict.Code == "connection_retired" {
		return ImportResult{}, nil
	}
	return ImportResult{Created: result.Created, Bound: result.Joined, Moved: result.Moved}, err
}

// SyncWorkspaceInventory commits the catalog before running legacy imports.
// Imports can supply a group anchor for a new row. If they fail, the same
// catalog still supports independent group placement.
func (s *Store) SyncWorkspaceInventory(ctx context.Context, connection int64, inventory []execution.WorkspaceInfo) (int, ImportResult, error) {
	count, err := s.refreshWorkspaces(ctx, connection, inventory, false)
	if err != nil {
		return 0, ImportResult{}, err
	}
	result, err := s.ImportWorkspaces(ctx, connection)
	if err == nil {
		return count, result, nil
	}
	groupErr := s.e.Write(ctx, func(tx store.Tx) error {
		if err := tx.Projects().LockIdentity(ctx); err != nil {
			return err
		}
		return resolveCatalog(ctx, tx, connection)
	})
	return count, result, errors.Join(err, groupErr)
}

// ImportSummaries reports each connection's recent project creation.
func (s *Store) ImportSummaries(ctx context.Context) ([]ImportSummary, error) {
	since := time.Now().Add(-ImportNoticeWindow)
	return read(ctx, s, func(tx store.Tx) ([]ImportSummary, error) { return tx.Projects().ImportSummaries(ctx, since) })
}
