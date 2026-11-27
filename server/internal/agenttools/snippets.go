package agenttools

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
)

// ---- snippets and attachments ----

// SnippetsOut is list_snippets.
type SnippetsOut struct {
	Snippets []db.Snippet `json:"snippets"`
}

func (o *Ops) ListSnippets(ctx context.Context, _ struct{}) (SnippetsOut, error) {
	sns, err := o.Client.Snippets(ctx)
	if sns == nil {
		sns = []db.Snippet{}
	}
	return SnippetsOut{Snippets: sns}, err
}

// SnippetIDArgs names one snippet.
type SnippetIDArgs struct {
	ID int64 `json:"id" jsonschema:"snippet id from list_snippets"`
}

func (o *Ops) GetSnippet(ctx context.Context, a SnippetIDArgs) (db.Snippet, error) {
	return o.Client.Snippet(ctx, a.ID)
}

// CreateSnippetArgs is create_snippet.
type CreateSnippetArgs struct {
	Name      string `json:"name" jsonschema:"unique snippet name"`
	Body      string `json:"body" jsonschema:"the prompt text"`
	ProjectID *int64 `json:"projectId,omitempty" jsonschema:"scope to a Stage project; omitted = global"`
}

func (o *Ops) CreateSnippet(ctx context.Context, a CreateSnippetArgs) (db.Snippet, error) {
	p := client.Patch{"name": a.Name, "body": a.Body}
	if a.ProjectID != nil {
		p["projectId"] = *a.ProjectID
	}
	return o.Client.CreateSnippet(ctx, p)
}

// UpdateSnippetArgs is update_snippet (omitted = unchanged).
type UpdateSnippetArgs struct {
	ID        int64              `json:"id"`
	Name      *string            `json:"name,omitempty"`
	Body      *string            `json:"body,omitempty"`
	ProjectID db.Optional[int64] `json:"projectId,omitzero" jsonschema:"scope to a Stage project; null makes it global"`
}

func (o *Ops) UpdateSnippet(ctx context.Context, a UpdateSnippetArgs) (db.Snippet, error) {
	p := client.Patch{}
	if a.Name != nil {
		p["name"] = *a.Name
	}
	if a.Body != nil {
		p["body"] = *a.Body
	}
	if a.ProjectID.Set {
		p["projectId"] = a.ProjectID.Ptr()
	}
	return o.Client.UpdateSnippet(ctx, a.ID, p)
}

func (o *Ops) DeleteSnippet(ctx context.Context, a SnippetIDArgs) (DeletedOut, error) {
	err := o.Client.DeleteSnippet(ctx, a.ID)
	return DeletedOut{Deleted: err == nil, ID: a.ID}, err
}

// AttachmentIDArgs names one image attachment.
type AttachmentIDArgs struct {
	ID int64 `json:"id" jsonschema:"attachment id (get_task or get_schedule attachments, or a staged upload)"`
}

func (o *Ops) DeleteAttachment(ctx context.Context, a AttachmentIDArgs) (DeletedOut, error) {
	err := o.Client.DeleteAttachment(ctx, a.ID)
	return DeletedOut{Deleted: err == nil, ID: a.ID}, err
}
