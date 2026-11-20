package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// JournalOwner selects exactly one task or tree.
type JournalOwner struct {
	TaskID *int64
	TreeID *int64
}

type JournalEntry struct {
	SourceSession  *SessionRef `json:"sourceSession"`
	ID             int64       `json:"id"`
	Kind           string      `json:"kind"`
	TaskID         *int64      `json:"taskId"`
	TreeID         *int64      `json:"treeId"`
	SourceTaskID   *int64      `json:"sourceTaskId"`
	SourceThreadID *string     `json:"sourceThreadId"`
	Attempt        *int        `json:"attempt"`
	Category       string      `json:"category"`
	Text           string      `json:"text"`
	EventType      string      `json:"eventType"`
	FromTreeID     *int64      `json:"fromTreeId"`
	ToTreeID       *int64      `json:"toTreeId"`
	CreatedAt      time.Time   `json:"createdAt"`
	Truncated      bool        `json:"truncated"`
}

type JournalQuery struct {
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
	// SourceThreadID is a legacy T3 thread id from a task create's
	// originThreadId (initial note); not accepted on the journal API.
	SourceThreadID *string `json:"-"`
}

const journalColumns = `id, kind, task_id, tree_id, source_task_id, COALESCE(source_thread_id,(SELECT external_id FROM session_identities WHERE id=source_session_id AND connection_id IN ` + t3ConnectionsSQL + `)), attempt, category, text, event_type, from_tree_id, to_tree_id, created_at, (SELECT json_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=source_session_id)`

func scanJournal(r rowScanner) (JournalEntry, error) {
	var e JournalEntry
	err := r.Scan(&e.ID, &e.Kind, &e.TaskID, &e.TreeID, &e.SourceTaskID, &e.SourceThreadID, &e.Attempt, &e.Category, &e.Text, &e.EventType, &e.FromTreeID, &e.ToTreeID, &e.CreatedAt, &e.SourceSession)
	return e, notFound(err)
}

func validateOwner(o JournalOwner) error {
	if (o.TaskID == nil) == (o.TreeID == nil) {
		return Invalid("exactly one journal owner is required")
	}
	if (o.TaskID != nil && *o.TaskID <= 0) || (o.TreeID != nil && *o.TreeID <= 0) {
		return Invalid("journal owner must be positive")
	}
	return nil
}

func journalOwnerRevision(ctx context.Context, tx *Tx, o JournalOwner) (int64, error) {
	var rev int64
	if o.TreeID != nil {
		err := tx.QueryRow(ctx, `SELECT membership_revision FROM task_trees WHERE id = $1`, o.TreeID).Scan(&rev)
		return rev, notFound(err)
	}
	err := tx.QueryRow(ctx, `SELECT 0 FROM tasks WHERE id = $1`, o.TaskID).Scan(&rev)
	return rev, notFound(err)
}

func (s *Store) GetJournalEntry(ctx context.Context, id int64) (JournalEntry, error) {
	return scanJournal(s.pool.QueryRow(ctx, `SELECT `+journalColumns+` FROM journal_entries WHERE id = $1`, id))
}

func (s *Store) AppendNote(ctx context.Context, o JournalOwner, in AppendNoteInput) (JournalEntry, error) {
	var entry JournalEntry
	err := s.inTx(ctx, func(tx *Tx) error {
		var err error
		entry, err = appendNote(ctx, tx, o, in)
		return err
	})
	return entry, err
}

var journalCategories = []string{"", "finding", "decision", "blocker", "outcome"}

func validateJournalCategory(category string) error {
	if !slices.Contains(journalCategories, category) {
		return Invalid("category must be empty, finding, decision, blocker or outcome")
	}
	return nil
}

func appendNote(ctx context.Context, tx *Tx, o JournalOwner, in AppendNoteInput) (JournalEntry, error) {
	var sourceID *int64
	if in.SourceSession != nil {
		if in.SourceThreadID != nil {
			return JournalEntry{}, Invalid("use sourceSession or the legacy T3 sourceThreadId, not both")
		}
		id, err := sessionIdentity(ctx, tx, *in.SourceSession)
		if err != nil {
			return JournalEntry{}, err
		}
		sourceID = &id
	} else if in.SourceThreadID != nil {
		t3, err := activeT3(ctx, tx)
		if err != nil {
			return JournalEntry{}, err
		}
		ref := SessionRef{ConnectionID: t3, ExternalSessionID: *in.SourceThreadID}
		in.SourceSession = &ref
		id, err := sessionIdentity(ctx, tx, ref)
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
	if _, err := journalOwnerRevision(ctx, tx, o); err != nil {
		return JournalEntry{}, err
	}
	// A committed retry succeeds even if provenance's task has since moved.
	existing, err := scanJournal(tx.QueryRow(ctx, `SELECT `+journalColumns+` FROM journal_entries WHERE kind = 'note' AND task_id IS $1 AND tree_id IS $2 AND request_id = $3`, o.TaskID, o.TreeID, in.RequestID))
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
		var tree *int64
		if err := tx.QueryRow(ctx, `SELECT tree_id FROM tasks WHERE id = $1`, in.SourceTaskID).Scan(&tree); err != nil {
			return JournalEntry{}, notFound(err)
		}
		if o.TreeID != nil && !equalPtr(tree, o.TreeID) {
			return JournalEntry{}, Conflict("source task is no longer a member of this tree")
		}
	}
	return scanJournal(tx.QueryRow(ctx, `INSERT INTO journal_entries (kind, task_id, tree_id, source_task_id, source_thread_id, category, text, request_id,source_session_id) VALUES ('note', $1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+journalColumns, o.TaskID, o.TreeID, in.SourceTaskID, in.SourceThreadID, in.Category, in.Text, in.RequestID, sourceID))
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
	err := s.inTx(ctx, func(tx *Tx) error {
		rev, err := journalOwnerRevision(ctx, tx, o)
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
		var w where
		if o.TaskID != nil {
			w.add("task_id = " + w.arg(*o.TaskID))
		} else {
			id := w.arg(*o.TreeID)
			w.add("(tree_id = " + id + " OR (kind = 'note' AND task_id IN (SELECT id FROM tasks WHERE tree_id = " + id + ")))")
		}
		if q.Kind != "all" {
			kind := "note"
			if q.Kind == "events" {
				kind = "event"
			}
			w.add("kind = " + w.arg(kind))
		}
		if q.Category != "" {
			w.add("category = " + w.arg(q.Category))
		}
		if q.Q != "" {
			w.add(likeSQL("text", w.arg(likePattern(q.Q))))
		}
		if q.TaskID != nil {
			p := w.arg(*q.TaskID)
			w.add("(task_id = " + p + " OR source_task_id = " + p + ")")
		}
		if cursor.Before > 0 {
			w.add("id < " + w.arg(cursor.Before))
		}
		limit := w.arg(q.Limit + 1)
		rows, err := tx.Query(ctx, `SELECT `+journalColumns+` FROM journal_entries`+w.sql()+` ORDER BY id DESC LIMIT `+limit, w.args...)
		if err != nil {
			return err
		}
		entries, err := collectRows(rows, scanJournal)
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
	err := s.inTx(ctx, func(tx *Tx) error {
		var attempt int
		if err := tx.QueryRow(ctx, `SELECT launch_attempt FROM tasks WHERE id = $1`, task.ID).Scan(&attempt); err != nil {
			return notFound(err)
		}
		if task.LaunchAttempt != attempt || attempt < 1 {
			return Conflict("launch attempt changed")
		}
		err := tx.QueryRow(ctx, `SELECT instructions FROM journal_launch_context WHERE task_id = $1 AND attempt = $2`, task.ID, attempt).Scan(&result)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO journal_launch_context (task_id, attempt, instructions) VALUES ($1,$2,$3)`, task.ID, attempt, proposed); err != nil {
			return err
		}
		result = proposed
		return nil
	})
	return result, err
}
