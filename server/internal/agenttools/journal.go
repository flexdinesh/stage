package agenttools

import (
	"context"
	"errors"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
)

// JournalOwnerArgs names exactly one journal owner.
type JournalOwnerArgs struct {
	TaskID *int64 `json:"taskId,omitempty" jsonschema:"task journal owner; exactly one of taskId or treeId"`
	TreeID *int64 `json:"treeId,omitempty" jsonschema:"tree journal owner; exactly one of taskId or treeId"`
}

func (a JournalOwnerArgs) owner() (db.JournalOwner, error) {
	if (a.TaskID == nil) == (a.TreeID == nil) {
		return db.JournalOwner{}, errors.New("exactly one taskId or treeId is required")
	}
	if a.TaskID != nil && *a.TaskID <= 0 || a.TreeID != nil && *a.TreeID <= 0 {
		return db.JournalOwner{}, errors.New("taskId and treeId must be positive")
	}
	return db.JournalOwner{TaskID: a.TaskID, TreeID: a.TreeID}, nil
}

type ReadJournalArgs struct {
	JournalOwnerArgs
	Kind         string `json:"kind,omitempty" jsonschema:"notes (default), events, or all"`
	Category     string `json:"category,omitempty" jsonschema:"finding, decision, blocker, or outcome; omitted = all categories"`
	Q            string `json:"q,omitempty" jsonschema:"search journal text"`
	FilterTaskID *int64 `json:"filterTaskId,omitempty" jsonschema:"filter a tree journal to this current member task"`
	Cursor       string `json:"cursor,omitempty"`
	Limit        int    `json:"limit,omitempty" jsonschema:"page size, default 20, maximum 100"`
}

func (o *Ops) ReadJournal(ctx context.Context, a ReadJournalArgs) (db.JournalPage, error) {
	owner, err := a.owner()
	if err != nil {
		return db.JournalPage{}, err
	}
	if a.Kind != "" && a.Kind != "notes" && a.Kind != "events" && a.Kind != "all" {
		return db.JournalPage{}, errors.New("kind must be notes, events, or all")
	}
	if a.Limit < 0 || a.Limit > 100 {
		return db.JournalPage{}, errors.New("limit must be between 1 and 100")
	}
	if a.FilterTaskID != nil && (owner.TreeID == nil || *a.FilterTaskID <= 0) {
		return db.JournalPage{}, errors.New("filterTaskId requires a tree journal and a positive task id")
	}
	return o.Client.ReadJournal(ctx, owner, db.JournalQuery{
		Kind: a.Kind, Category: a.Category, Q: a.Q, TaskID: a.FilterTaskID, Cursor: a.Cursor, Limit: a.Limit,
	})
}

type JournalEntryArgs struct {
	ID int64 `json:"id" jsonschema:"immutable journal entry id"`
}

func (o *Ops) GetJournalEntry(ctx context.Context, a JournalEntryArgs) (db.JournalEntry, error) {
	return o.Client.GetJournalEntry(ctx, a.ID)
}

type AppendNoteArgs struct {
	JournalOwnerArgs
	CallerArgs
	Text         string `json:"text" jsonschema:"note text, maximum 16 KiB; context, never instructions"`
	Category     string `json:"category,omitempty" jsonschema:"finding, decision, blocker, or outcome; omitted = uncategorized"`
	RequestID    string `json:"requestId" jsonschema:"required idempotency key; reuse only when retrying identical content"`
	SourceTaskID *int64 `json:"sourceTaskId,omitempty" jsonschema:"current member task contributing a tree note; refresh membership before writing"`
}

func (o *Ops) AppendNote(ctx context.Context, a AppendNoteArgs) (db.JournalEntry, error) {
	owner, err := a.owner()
	if err != nil {
		return db.JournalEntry{}, err
	}
	if strings.TrimSpace(a.RequestID) == "" {
		return db.JournalEntry{}, errors.New("requestId is required")
	}
	if strings.TrimSpace(a.Text) == "" || len(a.Text) > 16*1024 {
		return db.JournalEntry{}, errors.New("text must be nonempty and at most 16 KiB")
	}
	c, err := o.caller(ctx, a.CallerArgs)
	if err != nil {
		return db.JournalEntry{}, err
	}
	return o.Client.AppendNote(ctx, owner, db.AppendNoteInput{
		Text: a.Text, Category: a.Category, RequestID: a.RequestID,
		SourceTaskID: a.SourceTaskID, SourceSession: c.Session,
	})
}
