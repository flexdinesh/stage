package db

import (
	"context"
	"strings"
)

const snippetCols = `s.id, s.name, s.body, s.project_id, s.created_at, s.updated_at`

// snippetReturning is snippetCols for RETURNING (SQLite allows no alias there).
const snippetReturning = `id, name, body, project_id, created_at, updated_at`

func scanSnippet(row rowScanner) (Snippet, error) {
	var sn Snippet
	err := row.Scan(&sn.ID, &sn.Name, &sn.Body, &sn.ProjectID, &sn.CreatedAt, &sn.UpdatedAt)
	if err != nil {
		return Snippet{}, notFound(err)
	}
	return sn, nil
}

// SnippetPatch is the POST/PATCH body for snippets.
type SnippetPatch struct {
	Name      Optional[string] `json:"name"`
	Body      Optional[string] `json:"body"`
	ProjectID Optional[int64]  `json:"projectId"`
}

// snippetWriteErr maps constraint violations to API errors.
func snippetWriteErr(sn Snippet, err error, name string) (Snippet, error) {
	switch {
	case isUniqueViolation(err):
		return Snippet{}, Conflict("snippet %q already exists", name)
	case isForeignKeyViolation(err):
		return Snippet{}, Invalid("unknown projectId")
	}
	return sn, err
}

// ListSnippets orders global snippets first, then by project title, then name.
func (s *Store) ListSnippets(ctx context.Context) ([]Snippet, error) {
	rows, err := s.pool.Query(ctx, `
SELECT `+snippetCols+` FROM snippets s LEFT JOIN projects p ON p.id = s.project_id
ORDER BY s.project_id IS NOT NULL, lower(p.title), s.project_id, lower(s.name), s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Snippet{}
	for rows.Next() {
		sn, err := scanSnippet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

func (s *Store) GetSnippet(ctx context.Context, id int64) (Snippet, error) {
	return scanSnippet(s.pool.QueryRow(ctx, `SELECT `+snippetCols+` FROM snippets s WHERE s.id = $1`, id))
}

func (s *Store) CreateSnippet(ctx context.Context, p SnippetPatch) (Snippet, error) {
	sn, err := scanSnippet(s.pool.QueryRow(ctx, `
INSERT INTO snippets (name, body, project_id) VALUES ($1, $2, $3) RETURNING `+snippetReturning,
		p.Name.V, p.Body.V, p.ProjectID.Ptr()))
	return snippetWriteErr(sn, err, p.Name.V)
}

func (s *Store) UpdateSnippet(ctx context.Context, id int64, p SnippetPatch) (Snippet, error) {
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
		return s.GetSnippet(ctx, id)
	}
	sets = append(sets, "updated_at = "+nowSQL)
	sn, err := scanSnippet(s.pool.QueryRow(ctx,
		`UPDATE snippets SET `+strings.Join(sets, ", ")+` WHERE id = $1 RETURNING `+snippetReturning, w.args...))
	return snippetWriteErr(sn, err, p.Name.V)
}

func (s *Store) DeleteSnippet(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM snippets WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
