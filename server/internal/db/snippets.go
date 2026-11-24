package db

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type (
	Snippet      = store.Snippet
	SnippetPatch = store.SnippetPatch
)

// snippetWriteErr maps constraint violations to API errors.
func (s *Store) snippetWriteErr(sn Snippet, err error, name string) (Snippet, error) {
	switch {
	case s.e.Errors().IsUniqueViolation(err):
		return Snippet{}, Conflict("snippet %q already exists", name)
	case s.e.Errors().IsForeignKeyViolation(err):
		return Snippet{}, Invalid("unknown projectId")
	}
	return sn, err
}

// ListSnippets orders global snippets first, then by project title, then name.
func (s *Store) ListSnippets(ctx context.Context) ([]Snippet, error) {
	return read(ctx, s, func(tx store.Tx) ([]Snippet, error) { return tx.Snippets().List(ctx) })
}

func (s *Store) GetSnippet(ctx context.Context, id int64) (Snippet, error) {
	return read(ctx, s, func(tx store.Tx) (Snippet, error) { return tx.Snippets().Get(ctx, id) })
}

func (s *Store) CreateSnippet(ctx context.Context, p SnippetPatch) (Snippet, error) {
	sn, err := write(ctx, s, func(tx store.Tx) (Snippet, error) { return tx.Snippets().Create(ctx, p) })
	return s.snippetWriteErr(sn, err, p.Name.V)
}

func (s *Store) UpdateSnippet(ctx context.Context, id int64, p SnippetPatch) (Snippet, error) {
	sn, err := write(ctx, s, func(tx store.Tx) (Snippet, error) { return tx.Snippets().Update(ctx, id, p) })
	return s.snippetWriteErr(sn, err, p.Name.V)
}

func (s *Store) DeleteSnippet(ctx context.Context, id int64) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Snippets().Delete(ctx, id) })
}
