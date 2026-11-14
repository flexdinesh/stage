package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

// ---- threads (history) ----

func (s *server) listThreads(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	f := db.ThreadFilter{Limit: 100}
	var err error
	f.States = multi(q, "state")
	for _, state := range f.States {
		if !db.TaskState(state).Valid() {
			return db.Invalid("invalid state %q", state)
		}
	}
	if parent, ok := q["parent"]; ok {
		if len(parent) != 1 || parent[0] == "" {
			return db.Invalid("parent is required")
		}
		f.Parent = &parent[0]
	}
	switch sort := q.Get("sort"); sort {
	case "":
	case "activity":
		f.SortActivity = true
	default:
		return db.Invalid("invalid sort %q", sort)
	}
	switch active := q.Get("active"); active {
	case "":
	case "1", "true":
		f.Active = true
	default:
		return db.Invalid("invalid active %q", active)
	}
	if f.ProjectID, err = queryInt64(q, "project"); err != nil {
		return err
	}
	f.Statuses = multi(q, "status")
	for _, st := range f.Statuses {
		if err := validEnum("status", st, threadStatuses); err != nil {
			return err
		}
	}
	if f.Settled, err = queryBool(q, "settled"); err != nil {
		return err
	}
	f.Provider = strings.TrimSpace(q.Get("provider"))
	if f.Source = strings.TrimSpace(q.Get("source")); f.Source != "" {
		if err := validEnum("source", f.Source, []string{db.SourceApp, db.SourceT3}); err != nil {
			return err
		}
	}
	f.Q = strings.TrimSpace(q.Get("q"))
	if f.From, err = queryTime(q, "from", time.UTC); err != nil {
		return err
	}
	if f.To, err = queryTime(q, "to", time.UTC); err != nil {
		return err
	}
	if f.ExcludeSubagents, err = querySubagents(q); err != nil {
		return err
	}
	switch v := strings.TrimSpace(q.Get("needsInput")); v {
	case "":
	case "1", "true":
		f.NeedsInput = true
	default:
		return db.Invalid("invalid needsInput %q (want 1|true)", v)
	}
	switch v := strings.TrimSpace(q.Get("running")); v {
	case "":
	case "1", "true":
		f.Running = true
	default:
		return db.Invalid("invalid running %q (want 1|true)", v)
	}
	if f.SettledFrom, err = queryTime(q, "settledFrom", time.UTC); err != nil {
		return err
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return db.Invalid("invalid limit %q", v)
		}
		f.Limit = min(n, 500)
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return db.Invalid("invalid offset %q", v)
		}
		f.Offset = n
	}
	threads, total, err := s.Store.ListThreads(r.Context(), f)
	if err != nil {
		return err
	}
	env := s.envID(r.Context())
	out := make([]threadJSON, len(threads))
	for i, t := range threads {
		out[i] = s.thread(env, t)
	}
	writeJSON(w, http.StatusOK, map[string]any{"threads": out, "total": total})
	return nil
}

// ---- snippets ----

func validateSnippet(p *db.SnippetPatch, create bool) error {
	if create && !p.Name.Set {
		return db.Invalid("name is required")
	}
	if create && !p.Body.Set {
		return db.Invalid("body is required")
	}
	if err := requiredText("name", &p.Name); err != nil {
		return err
	}
	if p.Body.Set && (p.Body.Null || strings.TrimSpace(p.Body.V) == "") {
		return db.Invalid("body must be a non-empty string")
	}
	return nil
}

func (s *server) listSnippets(w http.ResponseWriter, r *http.Request) error {
	sns, err := s.Store.ListSnippets(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"snippets": sns})
	return nil
}

func (s *server) createSnippet(w http.ResponseWriter, r *http.Request) error {
	var p db.SnippetPatch
	if err := decode(r, &p); err != nil {
		return err
	}
	if err := validateSnippet(&p, true); err != nil {
		return err
	}
	sn, err := s.Store.CreateSnippet(r.Context(), p)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, sn)
	return nil
}

func (s *server) getSnippet(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	sn, err := s.Store.GetSnippet(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, sn)
	return nil
}

func (s *server) updateSnippet(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var p db.SnippetPatch
	if err := decode(r, &p); err != nil {
		return err
	}
	if err := validateSnippet(&p, false); err != nil {
		return err
	}
	sn, err := s.Store.UpdateSnippet(r.Context(), id, p)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, sn)
	return nil
}

func (s *server) deleteSnippet(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteSnippet(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- stats ----

func (s *server) stats(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	tz := strings.TrimSpace(q.Get("tz"))
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "Local" {
		return db.Invalid("invalid tz %q", tz)
	}
	to := time.Now().UTC()
	if t, err := queryTime(q, "to", loc); err != nil {
		return err
	} else if t != nil {
		to = t.UTC()
	}
	from := to.Add(-30 * 24 * time.Hour)
	if t, err := queryTime(q, "from", loc); err != nil {
		return err
	} else if t != nil {
		from = t.UTC()
	}
	excludeSubagents, err := querySubagents(q)
	if err != nil {
		return err
	}
	st, err := s.Store.Stats(r.Context(), from, to, tz, excludeSubagents)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, st)
	return nil
}
