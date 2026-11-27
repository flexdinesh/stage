package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/principal"
)

func TestSessionSearchCursorExpiresBeforeAdapterLookup(t *testing.T) {
	s := &server{sessionSearchCursorKey: []byte("test key")}
	actor := principal.Actor{Kind: principal.Personal, AccountID: 1}
	scope := sessionSearchScope{
		Connection: 1, Account: actor.AccountID, Caller: actor.Kind,
		QueryHash: sha256.Sum256(nil), Limit: execution.SessionSearchDefaultLimit,
	}
	now := time.Now()
	next := strings.Repeat("\\", execution.SessionSearchMaxCursor)
	fresh := s.sealSearchCursor(scope, next, now)
	if got, err := s.openSearchCursor(fresh, scope, now); err != nil || got != next {
		t.Fatalf("maximum native token did not round-trip: %v", err)
	}
	expired := s.sealSearchCursor(scope, next, now.Add(-sessionSearchCursorTTL))
	if _, err := s.openSearchCursor(expired, scope, now); !errors.Is(err, execution.ErrSessionSearchCursor) {
		t.Fatalf("expired token: %v", err)
	}
	ctx := principal.With(context.Background(), actor)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/connections/1/sessions?cursor="+url.QueryEscape(expired), nil)
	req.SetPathValue("connectionId", "1")
	rec := httptest.NewRecorder()
	if err := s.searchSessions(rec, req); err != nil || rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "invalid_session_search_cursor") {
		t.Fatalf("expired request: %d %s %v", rec.Code, rec.Body, err)
	}
}
