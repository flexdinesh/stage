package execution

import (
	"context"
	"errors"
	"time"
)

const (
	SessionSearchDefaultLimit = 20
	SessionSearchMaxLimit     = 50
	SessionSearchMaxQuery     = 256
	SessionSearchMaxCursor    = 2048
)

// ErrSessionSearchCursor means the caller must start a fresh search.
var ErrSessionSearchCursor = errors.New("Start a fresh search. This cursor is invalid or expired.")

// SessionSearchUnsupported carries the adapter's fallback when the harness
// rejects listing, even though the connection ordinarily supports it.
type SessionSearchUnsupported struct{ Reason string }

func (e *SessionSearchUnsupported) Error() string { return e.Reason }

// SessionSearcher browses one connection's current roster. The adapter owns
// match and ordering semantics and its continuation token. Browsing does not
// observe or subscribe to sessions.
type SessionSearcher interface {
	SearchSessions(context.Context, SessionSearchQuery) (SessionSearchPage, error)
}

type SessionSearchQuery struct {
	Query  string `json:"query,omitempty"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitempty"`
}

func (q SessionSearchQuery) Valid() error {
	switch {
	case q.Limit < 1 || q.Limit > SessionSearchMaxLimit:
		return &Invalid{Reason: "session search limit must be between 1 and 50"}
	case len(q.Query) > SessionSearchMaxQuery:
		return &Invalid{Reason: "session search query must be at most 256 bytes"}
	case len(q.Cursor) > SessionSearchMaxCursor:
		return ErrSessionSearchCursor
	}
	return nil
}

type SessionSearchPage struct {
	Sessions   []ListedSession `json:"sessions"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// ListedSession is metadata for a candidate. Eligibility is preliminary
// since a named lookup may find changes or evidence outside the roster.
type ListedSession struct {
	Session     SessionRef   `json:"session"`
	Title       string       `json:"title,omitempty"`
	Workspace   string       `json:"workspace,omitempty"`
	StartedAt   *time.Time   `json:"startedAt,omitempty"`
	Eligibility Availability `json:"eligibility"`
	// Origin lets Stage apply its wrapper policy. It is internal to adapter
	// calls and is omitted from the human API projection.
	Origin string `json:"origin,omitempty"`
}
