package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
)

var journalCategories = []string{"", "finding", "decision", "blocker", "outcome"}

func (s *server) taskJournal(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	return s.journal(w, r, db.JournalOwner{TaskID: &id})
}

func (s *server) treeJournal(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	return s.journal(w, r, db.JournalOwner{TreeID: &id})
}

func (s *server) journal(w http.ResponseWriter, r *http.Request, owner db.JournalOwner) error {
	if r.Method == http.MethodPost {
		var in db.AppendNoteInput
		if err := decodeStrict(r, &in); err != nil {
			return err
		}
		if err := validEnum("category", in.Category, journalCategories); err != nil {
			return err
		}
		if strings.TrimSpace(in.Text) == "" || len(in.Text) > 16*1024 {
			return db.Invalid("text must be non-empty and at most 16384 bytes")
		}
		if strings.TrimSpace(in.RequestID) == "" {
			return db.Invalid("requestId is required")
		}
		if in.SourceTaskID != nil && *in.SourceTaskID <= 0 {
			return db.Invalid("sourceTaskId must be a positive integer")
		}
		entry, err := s.Store.AppendNote(r.Context(), owner, in)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusCreated, entry)
		return nil
	}

	q := r.URL.Query()
	in := db.JournalQuery{Kind: q.Get("kind"), Category: q.Get("category"), Q: q.Get("q"), Cursor: q.Get("cursor"), Limit: 20}
	if err := validEnum("category", in.Category, journalCategories); err != nil {
		return err
	}
	if in.Kind == "" {
		in.Kind = "notes"
	}
	if err := validEnum("kind", in.Kind, []string{"notes", "events", "all"}); err != nil {
		return err
	}
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 100 {
			return db.Invalid("limit must be between 1 and 100")
		}
		in.Limit = n
	}
	if q.Has("taskId") {
		if owner.TreeID == nil {
			return db.Invalid("taskId filters tree journals only")
		}
		n, err := strconv.ParseInt(q.Get("taskId"), 10, 64)
		if err != nil || n <= 0 {
			return db.Invalid("taskId must be a positive integer")
		}
		in.TaskID = &n
	}
	page, err := s.Store.ReadJournal(r.Context(), owner, in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, page)
	return nil
}

func (s *server) getJournalEntry(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	entry, err := s.Store.GetJournalEntry(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, entry)
	return nil
}
