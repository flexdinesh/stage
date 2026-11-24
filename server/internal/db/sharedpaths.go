package db

import (
	"context"
	"path/filepath"
	"slices"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type SharedPath = store.SharedPath

// SharedPaths lists every machine's shared paths.
func (s *Store) SharedPaths(ctx context.Context) ([]SharedPath, error) {
	return read(ctx, s, func(tx store.Tx) ([]SharedPath, error) { return tx.Connections().SharedPaths(ctx, 0) })
}

// MachineSharedPaths lists the shared paths of machine's connections: path
// translation and checkout proof hold only within one machine.
func (s *Store) MachineSharedPaths(ctx context.Context, machine int64) ([]SharedPath, error) {
	return read(ctx, s, func(tx store.Tx) ([]SharedPath, error) { return tx.Connections().SharedPaths(ctx, machine) })
}

// sharedPaths (transitional raw SQL) lists machine's shared paths (0: every machine).
func sharedPaths(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (*rowSet, error)
}, machine int64) ([]SharedPath, error) {
	rows, err := q.Query(ctx, `SELECT p.connection_id,p.harness_root,p.stage_root FROM shared_paths p JOIN harness_connections c ON c.id=p.connection_id
 WHERE $1=0 OR c.machine_id=$1 ORDER BY p.connection_id,length(p.harness_root) DESC,p.harness_root`, machine)
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
		return s.e.Write(ctx, func(tx store.Tx) error { return tx.Connections().DeleteSharedPath(ctx, p.ConnectionID, root) })
	}
	if !filepath.IsAbs(p.StageRoot) {
		return Invalid("Enter the absolute directory visible to Stage.")
	}
	p = SharedPath{ConnectionID: p.ConnectionID, HarnessRoot: root, StageRoot: filepath.Clean(p.StageRoot)}
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Connections().PutSharedPath(ctx, p) })
}

// AutoBind cannot replace a manual choice or act on a retired source
// revision. expected is the MachineSharedPaths of b's connection's machine
// the match was proved with.
func (s *Store) AutoBind(ctx context.Context, source int64, b ProjectBinding, expected []SharedPath) (Project, error) {
	err := s.inTx(ctx, func(tx *Tx) error {
		var machine int64
		if err := tx.QueryRow(ctx, `SELECT machine_id FROM harness_connections WHERE id=$1`, b.ConnectionID).Scan(&machine); err != nil {
			return notFound(err)
		}
		current, err := sharedPaths(ctx, tx, machine)
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
