//go:build postgres

package postgres

import (
	"context"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type snippets struct{ *runner }

func (t *tx) Snippets() store.Snippets { return snippets{t.r} }

const snippetCols = `s.id, s.name, s.body, s.project_id, s.created_at, s.updated_at`

// snippetReturning is snippetCols without the table alias, for RETURNING.
const snippetReturning = `id, name, body, project_id, created_at, updated_at`

func scanSnippet(row rowScanner) (store.Snippet, error) {
	var sn store.Snippet
	err := row.Scan(&sn.ID, &sn.Name, &sn.Body, &sn.ProjectID, &sn.CreatedAt, &sn.UpdatedAt)
	if err != nil {
		return store.Snippet{}, notFound(err)
	}
	return sn, nil
}

func (r snippets) List(ctx context.Context) ([]store.Snippet, error) {
	rows, err := r.Query(ctx, `
SELECT `+snippetCols+` FROM snippets s LEFT JOIN projects p ON p.id = s.project_id
ORDER BY s.project_id IS NOT NULL, lower(p.title), s.project_id, lower(s.name), s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.Snippet{}
	for rows.Next() {
		sn, err := scanSnippet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

func (r snippets) Get(ctx context.Context, id int64) (store.Snippet, error) {
	return scanSnippet(r.QueryRow(ctx, `SELECT `+snippetCols+` FROM snippets s WHERE s.id = $1`, id))
}

func (r snippets) Create(ctx context.Context, p store.SnippetPatch) (store.Snippet, error) {
	return scanSnippet(r.QueryRow(ctx, `
INSERT INTO snippets (name, body, project_id) VALUES ($1, $2, $3) RETURNING `+snippetReturning,
		p.Name.V, p.Body.V, p.ProjectID.Ptr()))
}

func (r snippets) Update(ctx context.Context, id int64, p store.SnippetPatch) (store.Snippet, error) {
	var w where
	w.arg(id)
	var sets []string
	set := func(col string, v any) { sets = append(sets, col+" = "+w.arg(v)) }
	if p.Name.Set {
		set("name", p.Name.V)
	}
	if p.Body.Set {
		set("body", p.Body.V)
	}
	if p.ProjectID.Set {
		set("project_id", p.ProjectID.Ptr())
	}
	if len(sets) == 0 {
		return r.Get(ctx, id)
	}
	sets = append(sets, "updated_at = "+nowSQL)
	return scanSnippet(r.QueryRow(ctx,
		`UPDATE snippets SET `+strings.Join(sets, ", ")+` WHERE id = $1 RETURNING `+snippetReturning, w.args...))
}

func (r snippets) Delete(ctx context.Context, id int64) error {
	tag, err := r.Exec(ctx, `DELETE FROM snippets WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return err
}
