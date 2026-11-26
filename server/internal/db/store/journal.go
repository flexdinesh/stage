package store

import (
	"context"
	"time"
)

// JournalOwner selects exactly one task or tree.
type JournalOwner struct {
	TaskID *int64
	TreeID *int64
}

type JournalEntry struct {
	SourceSession *SessionRef `json:"sourceSession"`
	ID            int64       `json:"id"`
	Kind          string      `json:"kind"`
	TaskID        *int64      `json:"taskId"`
	TreeID        *int64      `json:"treeId"`
	SourceTaskID  *int64      `json:"sourceTaskId"`
	Attempt       *int        `json:"attempt"`
	Category      string      `json:"category"`
	Text          string      `json:"text"`
	EventType     string      `json:"eventType"`
	FromTreeID    *int64      `json:"fromTreeId"`
	ToTreeID      *int64      `json:"toTreeId"`
	CreatedAt     time.Time   `json:"createdAt"`
	// CreatedBy: the user who wrote the note or caused the Go-written event;
	// derived events (docs/storage.md "Journal events") carry the default
	// user.
	CreatedBy int64 `json:"createdBy"`
	// SourceMachineID: the machine whose agent gateway forwarded the write
	// (nil: not through a gateway).
	SourceMachineID *int64 `json:"sourceMachineId"`
	Truncated       bool   `json:"truncated"`
}

type JournalQuery struct {
	// Q is the Search capability (docs/storage.md "Search"): a
	// case-insensitive substring of the entry text, every character
	// literal; it filters without changing the order or paging.
	Kind, Category, Q, Cursor string
	TaskID                    *int64
	Limit                     int
}
type JournalPage struct {
	Entries    []JournalEntry `json:"entries"`
	NextCursor *string        `json:"nextCursor"`
}
type AppendNoteInput struct {
	SourceSession *SessionRef `json:"sourceSession,omitempty"`
	Text          string      `json:"text"`
	Category      string      `json:"category"`
	RequestID     string      `json:"requestId"`
	SourceTaskID  *int64      `json:"sourceTaskId"`
}

// JournalEvent is an event row db derives from a change in the same Write
// (docs/storage.md "Journal events").
type JournalEvent struct {
	TaskID, TreeID, SourceTaskID *int64
	SourceSessionID              *int64
	Attempt                      *int
	EventType, Text              string
	FromTreeID, ToTreeID         *int64
	// At is the event time (nil: now).
	At *time.Time
}

// Journal is the task and tree journal repository.
type Journal interface {
	// OwnerRevision is a tree's membership revision (0 for a task;
	// ErrNotFound for an unknown owner).
	OwnerRevision(ctx context.Context, o JournalOwner) (int64, error)
	Get(ctx context.Context, id int64) (JournalEntry, error)
	// NoteByRequest is o's note written with requestID (ErrNotFound).
	NoteByRequest(ctx context.Context, o JournalOwner, requestID string) (JournalEntry, error)
	InsertNote(ctx context.Context, o JournalOwner, in AppendNoteInput, sourceSessionID *int64, createdBy int64, sourceMachine *int64) (JournalEntry, error)
	// InsertEvent appends a derived event. It takes the column defaults for
	// its creator (the default user) and source machine (none).
	InsertEvent(ctx context.Context, e JournalEvent) error
	// Read pages o's entries matching q, newest first: at most limit
	// entries with id < before (0: from the newest).
	Read(ctx context.Context, o JournalOwner, q JournalQuery, before int64, limit int) ([]JournalEntry, error)
	// Duplicated records that task dst was duplicated from src.
	Duplicated(ctx context.Context, dst, src, createdBy int64, sourceMachine *int64) error
	// LaunchContext is the attempt's frozen instructions (ErrNotFound).
	LaunchContext(ctx context.Context, taskID int64, attempt int) (string, error)
	SetLaunchContext(ctx context.Context, taskID int64, attempt int, instructions string) error
}
