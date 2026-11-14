package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

const defaultSearchTimeout = 2 * time.Second

// Match sources: where a session matched the query.
const (
	matchTitle     = "title"
	matchBranch    = "branch"
	matchProject   = "project"
	matchPR        = "pr"
	matchUser      = "user"
	matchAssistant = "assistant"
)

// T3 search outcome reported with the results.
const (
	t3SearchOK      = "ok"
	t3SearchOffline = "offline"
	t3SearchTimeout = "timeout"
	t3SearchError   = "error"
)

type searchResultJSON struct {
	T3ThreadID       string      `json:"t3ThreadId"`
	Title            string      `json:"title"`
	ProjectTitle     *string     `json:"projectTitle"`
	MatchSource      string      `json:"matchSource"`
	Snippet          *string     `json:"snippet"`
	MessageCreatedAt *time.Time  `json:"messageCreatedAt"`
	T3DeepLink       *string     `json:"t3DeepLink"`
	Thread           *threadJSON `json:"thread"` // nil when stage has not synced the thread yet
}

// searchThreads merges stage's title matches (root sessions, newest activity
// first) with T3 t3_thread_search matches not already listed. T3 failures
// never fail the request; the outcome is reported in "t3".
func (s *server) searchThreads(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	if n := len([]rune(query)); n < 2 || n > 200 {
		return db.Invalid("q must be 2 to 200 characters")
	}
	limit := 20
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 50 {
			return db.Invalid("invalid limit %q (want 1..50)", v)
		}
		limit = n
	}
	projectID, err := queryInt64(q, "project")
	if err != nil {
		return err
	}
	t3ProjectID := ""
	if projectID != nil {
		p, err := s.Store.GetProject(r.Context(), *projectID)
		if err != nil {
			return err
		}
		t3ProjectID = p.T3ProjectID
	}

	type t3Result struct {
		matches []t3.SearchMatch
		status  string
	}
	t3Done := make(chan t3Result, 1)
	go func() {
		if !s.T3.Status(r.Context()).Connected {
			t3Done <- t3Result{status: t3SearchOffline}
			return
		}
		timeout := s.SearchTimeout
		if timeout <= 0 {
			timeout = defaultSearchTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		matches, err := s.T3.SearchThreads(ctx, t3.SearchThreadsInput{Query: query, ProjectID: t3ProjectID, Limit: limit})
		switch {
		case err == nil:
			t3Done <- t3Result{matches: matches, status: t3SearchOK}
		case errors.Is(err, t3.ErrNotConnected):
			t3Done <- t3Result{status: t3SearchOffline}
		case ctx.Err() != nil && r.Context().Err() == nil:
			t3Done <- t3Result{status: t3SearchTimeout}
		default:
			s.log.Debug("t3 thread search failed", "err", err)
			t3Done <- t3Result{status: t3SearchError}
		}
	}()

	local, _, err := s.Store.ListThreads(r.Context(), db.ThreadFilter{
		Q: query, ProjectID: projectID, ExcludeSubagents: true, SortActivity: true, Limit: limit,
	})
	if err != nil {
		return err
	}
	remote := <-t3Done

	env := s.envID(r.Context())
	results := make([]searchResultJSON, 0, len(local)+len(remote.matches))
	listed := make(map[string]bool, len(local))
	for _, t := range local {
		results = append(results, s.searchResult(env, t.T3ThreadID, t.Title, matchTitle, &t, nil))
		listed[t.T3ThreadID] = true
	}

	var unlisted []string
	for _, m := range remote.matches {
		if !listed[m.ThreadID] {
			unlisted = append(unlisted, m.ThreadID)
		}
	}
	rows := map[string]db.Thread{}
	projectTitles := map[string]string{}
	if len(unlisted) > 0 {
		threads, _, err := s.Store.ListThreads(r.Context(), db.ThreadFilter{IDs: unlisted, Limit: len(unlisted)})
		if err != nil {
			return err
		}
		for _, t := range threads {
			rows[t.T3ThreadID] = t
		}
		if len(threads) < len(unlisted) {
			projects, err := s.Store.ListProjects(r.Context(), "all")
			if err != nil {
				return err
			}
			for _, p := range projects {
				projectTitles[p.T3ProjectID] = p.Title
			}
		}
	}
	for _, m := range remote.matches {
		if listed[m.ThreadID] {
			continue
		}
		listed[m.ThreadID] = true
		var row *db.Thread
		if t, ok := rows[m.ThreadID]; ok {
			row = &t
		}
		res := s.searchResult(env, m.ThreadID, m.Title, matchSource(query, m.Source, row), row, &m)
		if row == nil {
			if title, ok := projectTitles[m.ProjectID]; ok {
				res.ProjectTitle = &title
			}
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "t3": remote.status})
	return nil
}

func (s *server) searchResult(env, threadID, title, source string, row *db.Thread, m *t3.SearchMatch) searchResultJSON {
	res := searchResultJSON{T3ThreadID: threadID, Title: title, MatchSource: source, T3DeepLink: deepLink(env, &threadID)}
	if row != nil {
		shaped := s.thread(env, *row)
		res.Thread = &shaped
		res.ProjectTitle = row.ProjectTitle
		if title == "" {
			res.Title = row.Title
		}
	}
	if m != nil && m.Source != t3.SearchTitle {
		if m.Snippet != "" {
			snippet := m.Snippet
			res.Snippet = &snippet
		}
		res.MessageCreatedAt = m.MessageCreatedAt
	}
	return res
}

// matchSource labels a T3 match. T3 reports "title" for title, branch, PR
// and project matches alike; stage's mirrored row tells them apart, and PR
// (not mirrored) is what remains.
func matchSource(query string, source t3.SearchSource, row *db.Thread) string {
	switch source {
	case t3.SearchUser:
		return matchUser
	case t3.SearchAssistant:
		return matchAssistant
	}
	if row == nil {
		return matchTitle
	}
	switch {
	case fieldMatches(row.Title, query):
		return matchTitle
	case row.Branch != nil && fieldMatches(*row.Branch, query):
		return matchBranch
	case row.ProjectTitle != nil && fieldMatches(*row.ProjectTitle, query):
		return matchProject
	}
	return matchPR
}

// fieldMatches: field contains the query, or every query word.
func fieldMatches(field, query string) bool {
	field, query = strings.ToLower(field), strings.ToLower(query)
	if strings.Contains(field, query) {
		return true
	}
	words := strings.Fields(query)
	for _, w := range words {
		if !strings.Contains(field, w) {
			return false
		}
	}
	return len(words) > 0
}
