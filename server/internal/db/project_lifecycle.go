package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type ProjectEvent = store.ProjectEvent
type ProjectAlias = store.ProjectAlias

type MergeSource struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

type MergeMoves struct {
	Tasks      int `json:"tasks"`
	Schedules  int `json:"schedules"`
	Snippets   int `json:"snippets"`
	Workspaces int `json:"workspaces"`
}

type MergeBlock struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

type MergeResult struct {
	Preview      bool        `json:"preview"`
	Project      Project     `json:"project"`
	From         MergeSource `json:"from"`
	Moves        MergeMoves  `json:"moves"`
	Blocked      *MergeBlock `json:"blocked"`
	PreviewToken string      `json:"previewToken"`
}

type SplitResult struct {
	Project Project `json:"project"`
	Source  Project `json:"source"`
}

// PreviewChangedError returns the current preview when prior confirmation
// no longer describes the rows the operation would affect.
type PreviewChangedError struct{ Fresh any }

func (e *PreviewChangedError) Error() string { return e.Unwrap().Error() }
func (e *PreviewChangedError) Unwrap() error {
	return &ConflictError{Code: "preview_changed", Msg: "The preview changed. Review it again."}
}

func projectID(ctx context.Context, tx store.Tx, id int64) (int64, error) {
	return tx.Projects().CanonicalID(ctx, id)
}

func (s *Store) ProjectID(ctx context.Context, id int64) (int64, error) {
	return read(ctx, s, func(tx store.Tx) (int64, error) {
		return projectID(ctx, tx, id)
	})
}

func (s *Store) MergeProjects(ctx context.Context, into, from int64, confirm bool, previewToken ...string) (MergeResult, error) {
	if !confirm {
		return read(ctx, s, func(tx store.Tx) (MergeResult, error) {
			dst, src, err := projectPair(ctx, tx, into, from)
			if err != nil {
				return MergeResult{}, err
			}
			if dst == src {
				return MergeResult{}, Invalid("Choose two distinct projects.")
			}
			return previewMerge(ctx, tx.Projects(), dst, src)
		})
	}
	return write(ctx, s, func(tx store.Tx) (MergeResult, error) {
		pr := tx.Projects()
		if err := pr.LockIdentity(ctx); err != nil {
			return MergeResult{}, err
		}
		dst, src, err := projectPair(ctx, tx, into, from)
		if err != nil {
			return MergeResult{}, err
		}
		if dst == src && len(previewToken) == 0 {
			return MergeResult{}, Invalid("Choose two distinct projects.")
		}
		if dst != src {
			if err := pr.LockMergeRows(ctx, dst, src); err != nil {
				return MergeResult{}, err
			}
		}
		result, err := previewMerge(ctx, pr, dst, src)
		if err != nil {
			return MergeResult{}, err
		}
		if len(previewToken) > 0 && previewToken[0] != result.PreviewToken {
			return result, &PreviewChangedError{Fresh: result}
		}
		if result.Blocked != nil {
			return result, &ConflictError{Code: result.Blocked.Code, Msg: result.Blocked.Reason}
		}
		by := ActingUser(ctx)
		if err := pr.ApplyMerge(ctx, dst, src, by); err != nil {
			return MergeResult{}, err
		}
		m := result.Moves
		text := fmt.Sprintf("Merged #%d %s: %d tasks, %d schedules, %d snippets, %d workspaces.",
			src, result.From.Title, m.Tasks, m.Schedules, m.Snippets, m.Workspaces)
		if err := pr.AddProjectEvent(ctx, dst, "project_merged", text, by, WorkAttribution(ctx)); err != nil {
			return MergeResult{}, err
		}
		if err := resolveAll(ctx, tx); err != nil {
			return MergeResult{}, err
		}
		result.Preview = false
		result.Project, err = pr.Get(ctx, dst)
		return result, err
	})
}

func projectPair(ctx context.Context, tx store.Tx, into, from int64) (int64, int64, error) {
	dst, err := projectID(ctx, tx, into)
	if err != nil {
		return 0, 0, err
	}
	src, err := projectID(ctx, tx, from)
	if err != nil {
		return 0, 0, err
	}
	return dst, src, nil
}

func previewMerge(ctx context.Context, pr store.Projects, into, from int64) (MergeResult, error) {
	dst, err := pr.Get(ctx, into)
	if err != nil {
		return MergeResult{}, err
	}
	result := MergeResult{
		Preview: true,
		Project: dst,
	}
	src := dst
	var rows store.ProjectMergeRows
	if into == from {
		result.Blocked = &MergeBlock{Code: "same_project", Reason: "Both project IDs now resolve to the same project."}
	} else {
		src, err = pr.Get(ctx, from)
		if err != nil {
			return MergeResult{}, err
		}
		rows, err = pr.MergeRows(ctx, into, from)
		if err != nil {
			return MergeResult{}, err
		}
	}
	result.From = MergeSource{ID: src.ID, Title: src.Title}
	for _, row := range rows.Content {
		switch row.Kind {
		case "task":
			result.Moves.Tasks++
			if row.State == store.StateLaunching {
				result.Blocked = &MergeBlock{
					Code:   "task_launching",
					Reason: "A task in the source project is launching. Wait for it to finish launching before merging.",
				}
			}
		case "schedule":
			result.Moves.Schedules++
		case "snippet":
			result.Moves.Snippets++
		}
	}
	for _, w := range rows.Workspaces {
		if w.ProjectID == from && w.Active {
			result.Moves.Workspaces++
		}
	}

	payload := struct {
		Into, From                     MergeSource
		IntoUpdatedAt, FromUpdatedAt   time.Time
		IntoArchivedAt, FromArchivedAt *time.Time
		Rows                           store.ProjectMergeRows
	}{
		Into: MergeSource{ID: dst.ID, Title: dst.Title}, From: result.From,
		IntoUpdatedAt: dst.UpdatedAt, FromUpdatedAt: src.UpdatedAt,
		IntoArchivedAt: dst.ArchivedAt, FromArchivedAt: src.ArchivedAt,
		Rows: rows,
	}
	input, err := json.Marshal(payload)
	if err != nil {
		return MergeResult{}, err
	}
	sum := sha256.Sum256(input)
	result.PreviewToken = hex.EncodeToString(sum[:])
	return result, nil
}

func (s *Store) SplitProject(ctx context.Context, project, binding int64, title string) (SplitResult, error) {
	return write(ctx, s, func(tx store.Tx) (SplitResult, error) {
		pr := tx.Projects()
		if err := pr.LockIdentity(ctx); err != nil {
			return SplitResult{}, err
		}
		from, err := projectID(ctx, tx, project)
		if err != nil {
			return SplitResult{}, err
		}
		w, err := pr.SplitWorkspace(ctx, from, binding)
		if err != nil {
			return SplitResult{}, err
		}
		if w.WorkspaceCount < 2 {
			return SplitResult{}, &ConflictError{Code: "split_last_workspace", Msg: "The source project must keep at least one active workspace."}
		}
		src, err := pr.Get(ctx, from)
		if err != nil {
			return SplitResult{}, err
		}

		name := splitProjectTitle(title, w, src.Title)
		by := ActingUser(ctx)
		into, err := pr.Insert(ctx, name, by, ActingMachine(ctx))
		if err != nil {
			return SplitResult{}, err
		}
		if err := pr.ApplySplit(ctx, from, into, binding); err != nil {
			return SplitResult{}, err
		}
		if w.Primary {
			if err := pr.PromoteBinding(ctx, from, w.ConnectionID, 0); err != nil {
				return SplitResult{}, err
			}
		}
		if err := pr.DismissMerge(ctx, from, into); err != nil {
			return SplitResult{}, err
		}
		label := w.Workspace
		if label == "" {
			label = w.ExternalID
		}
		if err := pr.AddProjectEvent(ctx, from, "project_split",
			fmt.Sprintf("Split workspace %s into #%d %s.", label, into, name), by, WorkAttribution(ctx)); err != nil {
			return SplitResult{}, err
		}
		if err := pr.AddProjectEvent(ctx, into, "project_split",
			fmt.Sprintf("Split workspace %s from #%d %s.", label, from, src.Title), by, WorkAttribution(ctx)); err != nil {
			return SplitResult{}, err
		}
		if err := resolveAll(ctx, tx); err != nil {
			return SplitResult{}, err
		}
		dst, err := pr.Get(ctx, into)
		if err != nil {
			return SplitResult{}, err
		}
		src, err = pr.Get(ctx, from)
		return SplitResult{Project: dst, Source: src}, err
	})
}

func splitProjectTitle(title string, w store.ProjectSplitWorkspace, sourceTitle string) string {
	if title := strings.TrimSpace(title); title != "" {
		return title
	}
	if title := strings.TrimSpace(w.Title); title != "" {
		return title
	}
	if base := path.Base(strings.ReplaceAll(w.Workspace, "\\", "/")); base != "." && base != "/" {
		return base
	}
	if w.ExternalID != "" {
		return w.ExternalID
	}
	return sourceTitle
}

func (s *Store) DismissProjectMerge(ctx context.Context, project, other int64) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		pr := tx.Projects()
		if err := pr.LockIdentity(ctx); err != nil {
			return err
		}
		id, otherID, err := projectPair(ctx, tx, project, other)
		if err != nil {
			return err
		}
		if id == otherID {
			return Invalid("Choose two distinct projects.")
		}
		if err := pr.DismissMerge(ctx, id, otherID); err != nil {
			return err
		}
		return resolveAll(ctx, tx)
	})
}

func (s *Store) ProjectEvents(ctx context.Context, project int64) ([]ProjectEvent, error) {
	return read(ctx, s, func(tx store.Tx) ([]ProjectEvent, error) {
		id, err := projectID(ctx, tx, project)
		if err != nil {
			return nil, err
		}
		return tx.Projects().ProjectEvents(ctx, id)
	})
}

// RecordWorkspaceAdded records an add-workspace request on the project
// group that holds the workspace (ADR 0012), with the admitted actor and a
// workspace_add cause naming the request and the caller session.
func (s *Store) RecordWorkspaceAdded(ctx context.Context, project int64, text, requestID string, caller *SessionRef) error {
	ctx = WithWorkCause(ctx, Cause{Kind: "workspace_add", RequestID: requestID, Session: caller})
	return s.e.Write(ctx, func(tx store.Tx) error {
		id, err := projectID(ctx, tx, project)
		if err != nil {
			return err
		}
		return tx.Projects().AddProjectEvent(ctx, id, "workspace_added", text, ActingUser(ctx), WorkAttribution(ctx))
	})
}
