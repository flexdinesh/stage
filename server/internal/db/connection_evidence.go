package db

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// ConnectionEvidence includes retired bindings and all session identities.
// An unobserved identity cannot establish continuity across an address change.
func (s *Store) ConnectionEvidence(ctx context.Context, id int64) ([]ProjectBinding, []execution.Observation, bool, error) {
	var bindings []ProjectBinding
	var observed []*execution.Observation
	var pending bool
	err := s.e.Read(ctx, func(tx store.Tx) error {
		var err error
		bindings, observed, pending, err = tx.Connections().Evidence(ctx, id)
		return err
	})
	if err != nil {
		return nil, nil, false, err
	}
	var observations []execution.Observation
	unknown := false
	for _, o := range observed {
		if o == nil {
			unknown = true
			continue
		}
		observations = append(observations, *o)
	}
	return bindings, observations, unknown || pending, nil
}
