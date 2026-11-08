package db

import (
	"context"

	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

// ModelDefaults is a new-task model preset (pinned or last used).
// ProviderInstanceID / Model nil = T3 default; set together.
type ModelDefaults struct {
	ProviderInstanceID *string          `json:"providerInstanceId"`
	Model              *string          `json:"model"`
	ModelOptions       []t3.ModelOption `json:"modelOptions"`
	RuntimeMode        string           `json:"runtimeMode"`
	InteractionMode    string           `json:"interactionMode"`
}

// Preferences: new-task prefill is Pinned, else LastUsed.
type Preferences struct {
	Pinned   *ModelDefaults `json:"pinned"`
	LastUsed *ModelDefaults `json:"lastUsed"`
}

func (s *Store) GetPreferences(ctx context.Context) (Preferences, error) {
	var p Preferences
	err := s.pool.QueryRow(ctx, `SELECT pinned, last_used FROM preferences WHERE id = 1`).Scan(&p.Pinned, &p.LastUsed)
	return normalize(p), err
}

// SetPinned stores (or clears, nil) the pinned defaults.
func (s *Store) SetPinned(ctx context.Context, d *ModelDefaults) (Preferences, error) {
	if _, err := s.pool.Exec(ctx, `UPDATE preferences SET pinned = $1, updated_at = now() WHERE id = 1`, d); err != nil {
		return Preferences{}, err
	}
	return s.GetPreferences(ctx)
}

// SetLastUsed records the model config of the latest created task.
func (s *Store) SetLastUsed(ctx context.Context, d ModelDefaults) error {
	d.ModelOptions = nonNilOptions(d.ModelOptions)
	_, err := s.pool.Exec(ctx, `UPDATE preferences SET last_used = $1, updated_at = now() WHERE id = 1`, d)
	return err
}

func normalize(p Preferences) Preferences {
	for _, d := range []*ModelDefaults{p.Pinned, p.LastUsed} {
		if d != nil {
			d.ModelOptions = nonNilOptions(d.ModelOptions)
		}
	}
	return p
}
