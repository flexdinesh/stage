package t3

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"

	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestSessionSearchUsesT3Roster(t *testing.T) {
	ctx := t.Context()
	c := NewFake()
	a := NewConnectionAdapter(c, 42)
	p := c.AddProject("Atlas", "/work/checkout")
	at := time.Date(2026, 11, 27, 2, 0, 0, 0, time.UTC)
	seed := func(title string, configure func(*ThreadDetail)) string {
		t.Helper()
		result, err := c.LaunchThread(ctx, LaunchInput{ProjectID: p.ID, Title: title})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.SetThread(result.ThreadID, func(d *ThreadDetail) {
			d.CreatedAt = at
			configure(d)
		}); err != nil {
			t.Fatal(err)
		}
		return result.ThreadID
	}
	running := seed("Active", func(d *ThreadDetail) { d.Status = StatusRunning })
	archived := seed("Archived", func(d *ThreadDetail) { d.Archived, d.Status, d.Settled = true, StatusCompleted, true })
	child := seed("Child", func(d *ThreadDetail) {
		d.ParentThreadID, d.RelationshipToParent, d.Status = running, "subagent", StatusRunning
	})
	fork := seed("Fork", func(d *ThreadDetail) {
		d.ParentThreadID, d.RelationshipToParent, d.Status = running, "fork", StatusCompleted
	})
	done := seed("Completed", func(d *ThreadDetail) { d.Status, d.Settled = StatusCompleted, true })

	features, err := a.Features(ctx)
	if err != nil || !features.Operations[execution.OpSessionSearch].Available {
		t.Fatalf("features: %+v %v", features.Operations, err)
	}
	first, err := a.SearchSessions(ctx, execution.SessionSearchQuery{Limit: 2})
	if err != nil || len(first.Sessions) != 2 || first.NextCursor != "2" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	if s := first.Sessions[0]; s.Session != (execution.SessionRef{ConnectionID: 42, ExternalSessionID: done}) ||
		s.Title != "Completed" || s.Workspace != "Atlas" || s.StartedAt == nil || !s.StartedAt.Equal(at) || !s.Eligibility.Available {
		t.Fatalf("settled session: %+v", s)
	}
	if s := first.Sessions[1]; s.Session.ExternalSessionID != fork || !s.Eligibility.Available {
		t.Fatalf("fork session: %+v", s)
	}
	second, err := a.SearchSessions(ctx, execution.SessionSearchQuery{Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Sessions) != 2 || second.NextCursor != "4" {
		t.Fatalf("second page: %+v %v", second, err)
	}
	for i, id := range []string{child, archived} {
		s := second.Sessions[i]
		if s.Session.ExternalSessionID != id || s.Eligibility.Available || s.Eligibility.Reason == "" {
			t.Fatalf("ineligible session: %+v", s)
		}
	}
	for _, tc := range []struct {
		q    string
		want int
	}{
		{" CoMpLeTeD ", 1}, {"ATLAS", 5}, {"/work/checkout", 5}, {done, 1}, {"no matches", 0},
	} {
		p, err := a.SearchSessions(ctx, execution.SessionSearchQuery{Limit: 10, Query: tc.q})
		if err != nil || len(p.Sessions) != tc.want || p.NextCursor != "" {
			t.Fatalf("search %q: %+v %v", tc.q, p, err)
		}
	}
	empty, err := a.SearchSessions(ctx, execution.SessionSearchQuery{Limit: 2, Query: running})
	if err != nil || len(empty.Sessions) != 0 || empty.NextCursor != first.NextCursor {
		t.Fatalf("filtered page: %+v %v", empty, err)
	}
	if r, err := a.SearchSessions(ctx, execution.SessionSearchQuery{Limit: 2, Cursor: second.NextCursor}); err != nil ||
		len(r.Sessions) != 1 || r.Sessions[0].Session.ExternalSessionID != running || r.NextCursor != "" {
		t.Fatalf("final page: %+v %v", r, err)
	}
	o, err := a.Observe(ctx, execution.SessionRef{ConnectionID: 42, ExternalSessionID: archived})
	if err != nil || !o.Archived || o.Session.ExternalSessionID != archived {
		t.Fatalf("named archived thread: %+v %v", o, err)
	}
	refs, err := a.Attention(ctx)
	if err != nil || len(refs) != 1 || refs[0].ExternalSessionID != running {
		t.Fatalf("Attention: %+v %v", refs, err)
	}
}

type searchListFixture struct {
	*Fake
	page ThreadPage
	err  error
}

func (f *searchListFixture) ListThreads(context.Context, ListThreadsInput) (ThreadPage, error) {
	return f.page, f.err
}

func TestSessionSearchT3CursorAndUnavailable(t *testing.T) {
	offset := 2
	c := &searchListFixture{Fake: NewFake(), page: ThreadPage{NextCursor: &offset}}
	a := NewAdapter(c)
	for _, cursor := range []string{"-1", "not-a-number", "2"} {
		if _, err := a.SearchSessions(t.Context(), execution.SessionSearchQuery{Limit: 2, Cursor: cursor}); !errors.Is(err, execution.ErrSessionSearchCursor) {
			t.Fatalf("cursor %q: %v", cursor, err)
		}
	}
	c.err = &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "unknown tool t3_thread_list"}
	_, err := a.SearchSessions(t.Context(), execution.SessionSearchQuery{Limit: 2})
	var unsupported *execution.SessionSearchUnsupported
	if !errors.As(err, &unsupported) || unsupported.Reason != searchUnsupported {
		t.Fatalf("unknown tool: %v", err)
	}
}
