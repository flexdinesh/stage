package store

import (
	"context"
	"time"
)

// Snippet is reusable prompt text; ProjectID nil = global.
type Snippet struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Body      string    `json:"body"`
	ProjectID *int64    `json:"projectId"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// SnippetPatch is the POST/PATCH body for snippets.
type SnippetPatch struct {
	Name      Optional[string] `json:"name"`
	Body      Optional[string] `json:"body"`
	ProjectID Optional[int64]  `json:"projectId"`
}

// Snippets is the snippets repository. Writes return the engine's
// constraint errors unchanged (ErrorClassifier decides).
type Snippets interface {
	// List orders global snippets first, then by project title, then name.
	List(ctx context.Context) ([]Snippet, error)
	Get(ctx context.Context, id int64) (Snippet, error)
	Create(ctx context.Context, p SnippetPatch) (Snippet, error)
	// Update applies the set fields (none: Get).
	Update(ctx context.Context, id int64, p SnippetPatch) (Snippet, error)
	Delete(ctx context.Context, id int64) error
}
