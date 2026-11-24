package db

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"slices"
	"strings"
	"unicode/utf8"
)

type (
	// JournalOwner selects exactly one task or tree.
	JournalOwner    = store.JournalOwner
	JournalEntry    = store.JournalEntry
	JournalQuery    = store.JournalQuery
	JournalPage     = store.JournalPage
	AppendNoteInput = store.AppendNoteInput
)

func validateOwner(o JournalOwner) error {
	if (o.TaskID == nil) == (o.TreeID == nil) {
		return Invalid("exactly one journal owner is required")
	}
	if (o.TaskID != nil && *o.TaskID <= 0) || (o.TreeID != nil && *o.TreeID <= 0) {
		return Invalid("journal owner must be positive")
	}
	return nil
}

func (s *Store) GetJournalEntry(ctx context.Context, id int64) (JournalEntry, error) {
	return read(ctx, s, func(tx store.Tx) (JournalEntry, error) { return tx.Journal().Get(ctx, id) })
}

func (s *Store) AppendNote(ctx context.Context, o JournalOwner, in AppendNoteInput) (JournalEntry, error) {
	return write(ctx, s, func(tx store.Tx) (JournalEntry, error) { return appendNote(ctx, tx, o, in) })
}

var journalCategories = []string{"", "finding", "decision", "blocker", "outcome"}

func validateJournalCategory(category string) error {
	if !slices.Contains(journalCategories, category) {
		return Invalid("category must be empty, finding, decision, blocker or outcome")
	}
	return nil
}

func appendNote(ctx context.Context, tx store.Tx, o JournalOwner, in AppendNoteInput) (JournalEntry, error) {
	var sourceID *int64
	if in.SourceSession != nil {
		id, err := ensureSession(ctx, tx, *in.SourceSession)
		if err != nil {
			return JournalEntry{}, err
		}
		sourceID = &id
	}

	if err := validateOwner(o); err != nil {
		return JournalEntry{}, err
	}
	if err := validateJournalCategory(in.Category); err != nil {
		return JournalEntry{}, err
	}
	if strings.TrimSpace(in.Text) == "" || !utf8.ValidString(in.Text) || len(in.Text) > 16*1024 {
		return JournalEntry{}, Invalid("text: nonempty UTF-8, at most 16 KiB")
	}
	if strings.TrimSpace(in.RequestID) == "" {
		return JournalEntry{}, Invalid("requestId is required")
	}
	// requestId is idempotent per owner: appends to one owner serialize on
	// its row, so a racing retry sees the first note.
	if o.TreeID != nil {
		if err := tx.Trees().Lock(ctx, *o.TreeID); err != nil {
			return JournalEntry{}, err
		}
	} else if err := tx.Tasks().Lock(ctx, *o.TaskID); err != nil {
		return JournalEntry{}, err
	}
	if _, err := tx.Journal().OwnerRevision(ctx, o); err != nil {
		return JournalEntry{}, err
	}
	// A committed retry succeeds even if provenance's task has since moved.
	existing, err := tx.Journal().NoteByRequest(ctx, o, in.RequestID)
	if err == nil {
		if existing.Text != in.Text || existing.Category != in.Category || !equalPtr(existing.SourceTaskID, in.SourceTaskID) || !equalPtr(existing.SourceSession, in.SourceSession) {
			return JournalEntry{}, Conflict("requestId already used with different note content")
		}
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return JournalEntry{}, err
	}
	if in.SourceTaskID != nil {
		tree, err := tx.Tasks().TreeID(ctx, *in.SourceTaskID)
		if err != nil {
			return JournalEntry{}, err
		}
		if o.TreeID != nil && !equalPtr(tree, o.TreeID) {
			return JournalEntry{}, Conflict("source task is no longer a member of this tree")
		}
	}
	return insertNote(ctx, tx, o, in, sourceID)
}

func equalPtr[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

type journalCursor struct {
	Before   int64  `json:"before"`
	Revision int64  `json:"revision"`
	Scope    string `json:"scope"`
}

func (s *Store) ReadJournal(ctx context.Context, o JournalOwner, q JournalQuery) (JournalPage, error) {
	page := JournalPage{Entries: []JournalEntry{}}
	if err := validateOwner(o); err != nil {
		return page, err
	}
	if err := validateJournalCategory(q.Category); err != nil {
		return page, err
	}
	if q.Kind == "" {
		q.Kind = "notes"
	}
	if q.Kind != "notes" && q.Kind != "events" && q.Kind != "all" {
		return page, Invalid("kind must be notes, events or all")
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Limit < 1 || q.Limit > 100 {
		return page, Invalid("limit must be between 1 and 100")
	}
	if q.TaskID != nil && (o.TreeID == nil || *q.TaskID <= 0) {
		return page, Invalid("taskId is only supported for tree journals")
	}
	scopeBytes, _ := json.Marshal(struct {
		Owner             JournalOwner
		Kind, Category, Q string
		TaskID            *int64
	}{o, q.Kind, q.Category, q.Q, q.TaskID})
	scope := fmt.Sprintf("%x", sha256.Sum256(scopeBytes))
	err := s.e.Write(ctx, func(tx store.Tx) error {
		// Write may rerun fn (Postgres retry): start each run from scratch.
		page = JournalPage{Entries: []JournalEntry{}}
		rev, err := tx.Journal().OwnerRevision(ctx, o)
		if err != nil {
			return err
		}
		var cursor journalCursor
		if q.Cursor != "" {
			data, err := base64.RawURLEncoding.DecodeString(q.Cursor)
			if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.Before <= 0 || cursor.Scope != scope {
				return Invalid("invalid journal cursor")
			}
			if cursor.Revision != rev {
				return Conflict("tree membership changed: restart journal pagination")
			}
		}
		entries, err := tx.Journal().Read(ctx, o, q, cursor.Before, q.Limit+1)
		if err != nil {
			return err
		}
		if len(entries) > q.Limit {
			entries = entries[:q.Limit]
			data, _ := json.Marshal(journalCursor{Before: entries[len(entries)-1].ID, Revision: rev, Scope: scope})
			encoded := base64.RawURLEncoding.EncodeToString(data)
			page.NextCursor = &encoded
		}
		for i := range entries {
			runes := []rune(entries[i].Text)
			if len(runes) > 600 {
				entries[i].Text = string(runes[:600])
				entries[i].Truncated = true
			}
		}
		page.Entries = entries
		return nil
	})
	return page, err
}

// JournalLaunchContext freezes instructions for the launch attempt, including retries.
func (s *Store) JournalLaunchContext(ctx context.Context, task Task, proposed string) (string, error) {
	var result string
	err := s.e.Write(ctx, func(tx store.Tx) error {
		// Write may rerun fn (Postgres retry): start each run from scratch.
		result = ""
		ls, err := tx.Tasks().LaunchState(ctx, task.ID)
		if err != nil {
			return err
		}
		attempt := ls.Attempt
		if task.LaunchAttempt != attempt || attempt < 1 {
			return Conflict("launch attempt changed")
		}
		result, err = tx.Journal().LaunchContext(ctx, task.ID, attempt)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := tx.Journal().SetLaunchContext(ctx, task.ID, attempt, proposed); err != nil {
			return err
		}
		result = proposed
		return nil
	})
	return result, err
}
