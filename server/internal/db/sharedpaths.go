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
	return write(ctx, s, func(tx store.Tx) (Project, error) {
		machine, err := tx.Connections().MachineOf(ctx, b.ConnectionID)
		if err != nil {
			return Project{}, err
		}
		current, err := tx.Connections().SharedPaths(ctx, machine)
		if err != nil {
			return Project{}, err
		}
		if !slices.Equal(current, expected) {
			return Project{}, Conflict("Shared paths changed. Match again.")
		}
		eligible, err := tx.Projects().AutoBindEligible(ctx, source, b)
		if err != nil {
			return Project{}, err
		}
		if !eligible {
			return Project{}, Conflict("The project bindings changed. Refresh and choose the workspace again.")
		}
		if _, err := s.bind(ctx, tx, b); err != nil {
			return Project{}, err
		}
		return tx.Projects().Get(ctx, b.ProjectID)
	})
}
