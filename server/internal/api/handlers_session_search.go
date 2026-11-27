package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/principal"
	"github.com/flexdinesh/stage/server/internal/remote"
)

const (
	sessionSearchCursorTTL = 5 * time.Minute
	maxSearchCursorBytes   = 4096
	referenceFallback      = "Enter a session link or ID."
)

type sessionSearchScope struct {
	Connection int64
	Account    int64
	Caller     principal.Kind
	QueryHash  [sha256.Size]byte
	Limit      int
}

type sessionSearchCursor struct {
	Scope   sessionSearchScope
	Expires int64
	Next    []byte
}

// sealSearchCursor binds the adapter's token to the admitted search. A
// server restart expires outstanding cursors along with their signing key.
func (s *server) sealSearchCursor(scope sessionSearchScope, next string, now time.Time) string {
	data, _ := json.Marshal(sessionSearchCursor{Scope: scope, Expires: now.Add(sessionSearchCursorTTL).Unix(), Next: []byte(next)})
	mac := hmac.New(sha256.New, s.sessionSearchCursorKey)
	_, _ = mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(data))
}

func (s *server) openSearchCursor(token string, scope sessionSearchScope, now time.Time) (string, error) {
	if len(token) > maxSearchCursorBytes {
		return "", execution.ErrSessionSearchCursor
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) <= sha256.Size {
		return "", execution.ErrSessionSearchCursor
	}
	data, tag := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, s.sessionSearchCursorKey)
	_, _ = mac.Write(data)
	var c sessionSearchCursor
	if !hmac.Equal(mac.Sum(nil), tag) || json.Unmarshal(data, &c) != nil ||
		c.Scope != scope || c.Expires <= now.Unix() || len(c.Next) == 0 || len(c.Next) > execution.SessionSearchMaxCursor {
		return "", execution.ErrSessionSearchCursor
	}
	return string(c.Next), nil
}

type sessionSearchResult struct {
	Session       execution.SessionRef   `json:"session"`
	Title         string                 `json:"title,omitempty"`
	Workspace     string                 `json:"workspace,omitempty"`
	StartedAt     *time.Time             `json:"startedAt,omitempty"`
	Eligibility   execution.Availability `json:"eligibility"`
	TrackedTaskID *int64                 `json:"trackedTaskId,omitempty"`
}

func (s *server) searchSessions(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "private, no-store")
	actor, _ := principal.From(r.Context())
	// The personal router also admits agent gateway requests to many
	// human routes. A roster can only be searched on the human surface.
	if !actor.IsHuman() && (actor.Kind != principal.Personal || r.Header.Get(principal.ToolTransportHeader) == "mcp") {
		accessError(w, http.StatusForbidden, "principal_forbidden", "Search sessions from the Stage app.")
		return nil
	}
	id, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	params := r.URL.Query()
	q := execution.SessionSearchQuery{Query: strings.TrimSpace(params.Get("q")), Limit: execution.SessionSearchDefaultLimit}
	if params.Has("limit") {
		q.Limit, err = strconv.Atoi(params.Get("limit"))
		if err != nil {
			return db.Invalid("session search limit must be between 1 and %d", execution.SessionSearchMaxLimit)
		}
	}
	if err := q.Valid(); err != nil {
		return db.Invalid("%s", err)
	}
	scope := sessionSearchScope{
		Connection: id, Account: actor.AccountID, Caller: actor.Kind,
		QueryHash: sha256.Sum256([]byte(q.Query)), Limit: q.Limit,
	}
	if params.Has("cursor") {
		q.Cursor, err = s.openSearchCursor(params.Get("cursor"), scope, time.Now())
		if err != nil {
			return writeSessionSearchError(w, err)
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), remote.CallTimeout)
	defer cancel()
	c, err := s.Store.GetHarnessConnection(ctx, id)
	if err != nil {
		return err
	}
	if !c.Active {
		return db.ConnectionRetired()
	}
	a, err := s.Engine.Connections.Adapter(id)
	if err != nil {
		return err
	}
	features, err := a.Features(ctx)
	if err != nil {
		return writeSessionSearchError(w, err)
	}
	searcher, ok := execution.As[execution.SessionSearcher](a)
	search := features.Operations[execution.OpSessionSearch]
	if !ok || !search.Available {
		reason := search.Reason
		if reason == "" {
			reason = "This connection does not support session search. " + referenceFallback
		}
		return &db.ConflictError{Code: "operation_unavailable", Msg: reason}
	}

	page, err := searcher.SearchSessions(ctx, q)
	if err != nil {
		return writeSessionSearchError(w, err)
	}
	if len(page.Sessions) > q.Limit || len(page.NextCursor) > execution.SessionSearchMaxCursor {
		return writeSessionSearchError(w, errors.New("session page exceeds search bounds"))
	}
	out := struct {
		Sessions   []sessionSearchResult `json:"sessions"`
		NextCursor string                `json:"nextCursor,omitempty"`
	}{Sessions: make([]sessionSearchResult, 0, len(page.Sessions))}
	registry := s.Engine.Connections.Harnesses()
	for _, item := range page.Sessions {
		if item.Session.ConnectionID != id || strings.TrimSpace(item.Session.ExternalSessionID) == "" {
			return writeSessionSearchError(w, errors.New("session search returned a mismatched reference"))
		}
		result := sessionSearchResult{
			Session: item.Session, Title: item.Title, Workspace: item.Workspace,
			StartedAt: item.StartedAt, Eligibility: item.Eligibility,
		}
		t, err := s.Store.TaskBySession(ctx, item.Session)
		switch {
		case err == nil:
			result.TrackedTaskID = &t.ID
		case !errors.Is(err, db.ErrNotFound):
			return err
		}
		if adopt := features.Operations[execution.OpAdopt]; result.Eligibility.Available && !adopt.Available {
			result.Eligibility = adopt
			if result.Eligibility.Reason == "" {
				result.Eligibility.Reason = "This connection does not support tracking sessions."
			}
		}
		if registry != nil {
			if refusal := registry.Refusal(c.Harness, item.Origin); refusal != nil {
				result.Eligibility = execution.Availability{Reason: refusal.Reason}
			}
		}
		out.Sessions = append(out.Sessions, result)
	}
	if page.NextCursor != "" {
		out.NextCursor = s.sealSearchCursor(scope, page.NextCursor, time.Now())
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func writeSessionSearchError(w http.ResponseWriter, err error) error {
	var conflict *db.ConflictError
	var unsupported *execution.SessionSearchUnsupported
	switch {
	case errors.Is(err, execution.ErrSessionSearchCursor):
		accessError(w, http.StatusBadRequest, "invalid_session_search_cursor", execution.ErrSessionSearchCursor.Error())
	case errors.As(err, &unsupported):
		return &db.ConflictError{Code: "operation_unavailable", Msg: unsupported.Reason}
	case errors.As(err, &conflict):
		return err
	case errors.Is(err, execution.ErrUnavailable):
		accessError(w, http.StatusServiceUnavailable, "session_search_unavailable", "The harness or its worker is unavailable. "+referenceFallback)
	default:
		accessError(w, http.StatusBadGateway, "session_search_failed", "Session search failed. Retry or enter a session link or ID.")
	}
	return nil
}
