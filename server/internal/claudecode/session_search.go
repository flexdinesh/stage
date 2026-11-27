package claudecode

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var _ execution.SessionSearcher = (*Adapter)(nil)

const searchHeaderBytes = 256 << 10

// SearchSessions uses the roster's own title, directory and start time.
// Transcript headers of listed matches supply origin for wrapper checks.
func (a *Adapter) SearchSessions(ctx context.Context, q execution.SessionSearchQuery) (execution.SessionSearchPage, error) {
	var out execution.SessionSearchPage
	if err := q.Valid(); err != nil {
		return out, err
	}
	var after Entry
	if q.Cursor != "" {
		start, id, ok := strings.Cut(q.Cursor, "/")
		at, err := strconv.ParseInt(start, 10, 64)
		if !ok || err != nil || at < 0 || !validUUID(id) {
			return out, execution.ErrSessionSearchCursor
		}
		after = Entry{StartedAt: at, SessionID: id}
	}
	if h := a.Health(ctx); !h.Available {
		return out, fmt.Errorf("%w: %s", execution.ErrUnavailable, h.Reason)
	}
	entries, err := a.roster.list(ctx, false)
	if err != nil {
		return out, err
	}
	matches := sessionMatches(entries, strings.ToLower(strings.TrimSpace(q.Query)), after, q.Limit+1)
	if len(matches) > q.Limit {
		matches = matches[:q.Limit]
		last := matches[len(matches)-1]
		out.NextCursor = strconv.FormatInt(last.StartedAt, 10) + "/" + last.SessionID
	}
	out.Sessions = make([]execution.ListedSession, 0, len(matches))
	if len(matches) == 0 {
		return out, nil
	}
	root := a.projects(ctx)
	for _, entry := range matches {
		if err := ctx.Err(); err != nil {
			return execution.SessionSearchPage{}, err
		}
		session := execution.ListedSession{
			Session:     execution.SessionRef{ConnectionID: a.conn, ExternalSessionID: entry.SessionID},
			Eligibility: execution.Availability{Available: true},
		}
		session.Title, _ = truncate(entry.Name, 256)
		session.Workspace, _ = truncate(entry.Cwd, 4096)
		if entry.StartedAt > 0 {
			t := time.UnixMilli(entry.StartedAt).UTC()
			session.StartedAt = &t
		}
		if path, ok := transcriptPath(root, entry.Cwd, entry.SessionID); ok {
			session.Origin = searchOrigin(path)
		}
		if _, err := MapState(entry); err != nil {
			session.Eligibility = execution.Availability{Reason: "Claude listed an unsupported session state."}
		} else if entry.Cwd == "" {
			session.Eligibility = execution.Availability{Reason: "Claude did not list a workspace for this session. Enter its ID to check it directly."}
		}
		out.Sessions = append(out.Sessions, session)
	}
	return out, nil
}

func sessionMatches(entries []Entry, query string, after Entry, count int) []Entry {
	sorted := slices.Clone(entries)
	slices.SortStableFunc(sorted, compareSessions)
	seen := make(map[string]bool)
	matches := make([]Entry, 0, count)
	for _, e := range sorted {
		if !validUUID(e.SessionID) || seen[e.SessionID] {
			continue
		}
		seen[e.SessionID] = true
		if after.SessionID != "" && compareSessions(e, after) <= 0 {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(e.Name), query) &&
			!strings.Contains(strings.ToLower(e.Cwd), query) &&
			!strings.Contains(strings.ToLower(e.SessionID), query) &&
			!strings.Contains(strings.ToLower(e.ID), query) {
			continue
		}
		e.StartedAt = max(0, e.StartedAt)
		matches = append(matches, e)
		if len(matches) == count {
			break
		}
	}
	return matches
}

func compareSessions(a, b Entry) int {
	if n := cmp.Compare(max(0, b.StartedAt), max(0, a.StartedAt)); n != 0 {
		return n
	}
	return strings.Compare(a.SessionID, b.SessionID)
}

func searchOrigin(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	head, err := readHead(io.LimitReader(f, searchHeaderBytes))
	if err != nil {
		return ""
	}
	return head.Entrypoint
}
