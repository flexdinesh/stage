package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/db"
)

// ---- threads (history) ----

func (s *server) listThreads(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	f := db.ThreadFilter{Limit: 100}
	var err error
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

// ---- templates ----

func validateTemplate(p *db.TemplatePatch, create bool) error {
	if create && !p.Name.Set {
		return db.Invalid("name is required")
	}
	if err := requiredText("name", &p.Name); err != nil {
		return err
	}
	if p.Prompt.Set && p.Prompt.Null {
		p.Prompt.V, p.Prompt.Null = "", false
	}
	nullableText(&p.ProviderInstanceID)
	nullableText(&p.Model)
	if p.ModelOptions.Set {
		opts, err := validateOptions(p.ModelOptions.V)
		if err != nil {
			return err
		}
		p.ModelOptions.V, p.ModelOptions.Null = opts, false
	}
	if err := enumField("runtimeMode", &p.RuntimeMode, db.RuntimeModes); err != nil {
		return err
	}
	return enumField("interactionMode", &p.InteractionMode, db.InteractionModes)
}

func (s *server) listTemplates(w http.ResponseWriter, r *http.Request) error {
	ts, err := s.Store.ListTemplates(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": ts})
	return nil
}

func (s *server) createTemplate(w http.ResponseWriter, r *http.Request) error {
	var p db.TemplatePatch
	if err := decode(r, &p); err != nil {
		return err
	}
	if err := validateTemplate(&p, true); err != nil {
		return err
	}
	t, err := s.Store.CreateTemplate(r.Context(), p)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, t)
	return nil
}

func (s *server) getTemplate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	t, err := s.Store.GetTemplate(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, t)
	return nil
}

func (s *server) updateTemplate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var p db.TemplatePatch
	if err := decode(r, &p); err != nil {
		return err
	}
	if err := validateTemplate(&p, false); err != nil {
		return err
	}
	t, err := s.Store.UpdateTemplate(r.Context(), id, p)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, t)
	return nil
}

func (s *server) deleteTemplate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteTemplate(r.Context(), id); err != nil {
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
