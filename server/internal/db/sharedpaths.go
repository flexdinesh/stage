package db

import (
	"context"
	"path/filepath"
	"slices"
)

type SharedPath struct {
	ConnectionID int64  `json:"connectionId"`
	HarnessRoot  string `json:"harnessRoot"`
	StageRoot    string `json:"stageRoot"`
}

func (s *Store) SharedPaths(ctx context.Context) ([]SharedPath, error) {
	return sharedPaths(ctx, s.pool)
}
func sharedPaths(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (*rowSet, error)
}) ([]SharedPath, error) {
	rows, err := q.Query(ctx, `SELECT connection_id,harness_root,stage_root FROM shared_paths ORDER BY connection_id,length(harness_root) DESC,harness_root`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (SharedPath, error) {
		var p SharedPath
		err := r.Scan(&p.ConnectionID, &p.HarnessRoot, &p.StageRoot)
		return p, err
	})
}

func (s *Store) SetSharedPath(ctx context.Context, p SharedPath) error {
	if !filepath.IsAbs(p.HarnessRoot) {
		return Invalid("Enter the absolute harness directory.")
	}
	root := filepath.Clean(p.HarnessRoot)
	if p.StageRoot == "" {
		_, err := s.pool.Exec(ctx, `DELETE FROM shared_paths WHERE connection_id=$1 AND harness_root=$2`, p.ConnectionID, root)
		return err
	}
	if !filepath.IsAbs(p.StageRoot) {
		return Invalid("Enter the absolute directory visible to Stage.")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO shared_paths(connection_id,harness_root,stage_root) VALUES($1,$2,$3) ON CONFLICT(connection_id,harness_root) DO UPDATE SET stage_root=excluded.stage_root`, p.ConnectionID, root, filepath.Clean(p.StageRoot))
	return err
}

// AutoBind cannot replace a manual choice or act on a retired source revision.
func (s *Store) AutoBind(ctx context.Context, source int64, b ProjectBinding, expected []SharedPath) (Project, error) {
	err := s.inTx(ctx, func(tx *Tx) error {
		current, err := sharedPaths(ctx, tx)
		if err != nil {
			return err
		}
		if !slices.Equal(current, expected) {
			return Conflict("Shared paths changed. Match again.")
		}
		var eligible bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_bindings WHERE id=$1 AND project_id=$2 AND active=1 AND status='available') AND NOT EXISTS(SELECT 1 FROM project_bindings WHERE project_id=$2 AND connection_id=$3 AND active=1)
 AND EXISTS(SELECT 1 FROM external_projects WHERE connection_id=$3 AND external_id=$4 AND workspace=$5 AND available=1)`, source, b.ProjectID, b.ConnectionID, b.ExternalProjectID, b.Workspace).Scan(&eligible)
		if err != nil {
			return err
		}
		if !eligible {
			return Conflict("The project bindings changed. Refresh and choose the workspace again.")
		}
		_, err = bindProject(ctx, tx, b)
		return err
	})
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, b.ProjectID)
}
