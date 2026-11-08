package db

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

const templateCols = `id, name, prompt, provider_instance_id, model, model_options, runtime_mode, interaction_mode, created_at, updated_at`

func scanTemplate(row pgx.Row) (Template, error) {
	var t Template
	err := row.Scan(&t.ID, &t.Name, &t.Prompt, &t.ProviderInstanceID, &t.Model, &t.ModelOptions,
		&t.RuntimeMode, &t.InteractionMode, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return Template{}, notFound(err)
	}
	t.ModelOptions = nonNilOptions(t.ModelOptions)
	return t, nil
}

// TemplatePatch is the POST/PATCH body for templates.
type TemplatePatch struct {
	Name               Optional[string]           `json:"name"`
	Prompt             Optional[string]           `json:"prompt"`
	ProviderInstanceID Optional[string]           `json:"providerInstanceId"`
	Model              Optional[string]           `json:"model"`
	ModelOptions       Optional[[]t3.ModelOption] `json:"modelOptions"`
	RuntimeMode        Optional[string]           `json:"runtimeMode"`
	InteractionMode    Optional[string]           `json:"interactionMode"`
}

func (s *Store) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+templateCols+` FROM templates ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTemplate(ctx context.Context, id int64) (Template, error) {
	return scanTemplate(s.pool.QueryRow(ctx, `SELECT `+templateCols+` FROM templates WHERE id = $1`, id))
}

func (s *Store) CreateTemplate(ctx context.Context, p TemplatePatch) (Template, error) {
	runtime, interaction := "full-access", "default"
	if p.RuntimeMode.Set && !p.RuntimeMode.Null {
		runtime = p.RuntimeMode.V
	}
	if p.InteractionMode.Set && !p.InteractionMode.Null {
		interaction = p.InteractionMode.V
	}
	t, err := scanTemplate(s.pool.QueryRow(ctx, `
INSERT INTO templates (name, prompt, provider_instance_id, model, model_options, runtime_mode, interaction_mode)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+templateCols,
		p.Name.V, p.Prompt.V, p.ProviderInstanceID.Ptr(), p.Model.Ptr(), nonNilOptions(p.ModelOptions.V),
		runtime, interaction))
	if isUniqueViolation(err) {
		return Template{}, Conflict("template %q already exists", p.Name.V)
	}
	return t, err
}

func (s *Store) UpdateTemplate(ctx context.Context, id int64, p TemplatePatch) (Template, error) {
	var w where
	w.arg(id)
	var sets []string
	set := func(col string, v any) { sets = append(sets, col+" = "+w.arg(v)) }
	if p.Name.Set {
		set("name", p.Name.V)
	}
	if p.Prompt.Set {
		set("prompt", p.Prompt.V)
	}
	if p.ProviderInstanceID.Set {
		set("provider_instance_id", p.ProviderInstanceID.Ptr())
	}
	if p.Model.Set {
		set("model", p.Model.Ptr())
	}
	if p.ModelOptions.Set {
		set("model_options", nonNilOptions(p.ModelOptions.V))
	}
	if p.RuntimeMode.Set {
		set("runtime_mode", p.RuntimeMode.V)
	}
	if p.InteractionMode.Set {
		set("interaction_mode", p.InteractionMode.V)
	}
	if len(sets) == 0 {
		return s.GetTemplate(ctx, id)
	}
	sets = append(sets, "updated_at = now()")
	t, err := scanTemplate(s.pool.QueryRow(ctx,
		`UPDATE templates SET `+strings.Join(sets, ", ")+` WHERE id = $1 RETURNING `+templateCols, w.args...))
	if isUniqueViolation(err) {
		return Template{}, Conflict("template %q already exists", p.Name.V)
	}
	return t, err
}

func (s *Store) DeleteTemplate(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM templates WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
