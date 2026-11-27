package codex_test

import (
	"errors"
	"testing"

	"github.com/flexdinesh/stage/server/internal/codex/codextest"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestSessionSearchUsesCodexRoster(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	done := "01900000-0000-7000-8000-000000000001"
	child := "01900000-0000-7000-8000-000000000002"
	wrapped := "01900000-0000-7000-8000-000000000003"
	archived := "01900000-0000-7000-8000-000000000004"
	r.srv.AddThread(codextest.Thread{ID: done, Cwd: "/w/Atlas", Name: "Completed work", Created: 30,
		Turns: []*codextest.Turn{{ID: "done", Status: "completed", Prompt: "private prompt", Final: "private reply", Started: 30, Completed: 31}}})
	r.srv.AddThread(codextest.Thread{ID: child, Cwd: "/w/Atlas", Parent: done, Name: "Nested", Created: 20})
	r.srv.AddThread(codextest.Thread{ID: wrapped, Cwd: "/w/Other", Originator: "T3 Code", Preview: "private prompt", Created: 10,
		Status: "active", Flags: []string{"waitingOnApproval"}})
	r.srv.AddThread(codextest.Thread{ID: archived, Cwd: "/w/Atlas", Name: "Archived", Archived: true, Created: 40})

	c, err := r.adapter.Features(ctx)
	if err != nil || !c.Operations[execution.OpSessionSearch].Available {
		t.Fatalf("features: %+v %v", c.Operations, err)
	}
	p, err := r.adapter.SearchSessions(ctx, execution.SessionSearchQuery{Limit: 2})
	if err != nil || len(p.Sessions) != 2 || p.NextCursor == "" {
		t.Fatalf("first page: %+v %v", p, err)
	}
	if s := p.Sessions[0]; s.Session != (execution.SessionRef{ConnectionID: conn, ExternalSessionID: done}) ||
		s.Title != "Completed work" || s.Workspace != "/w/Atlas" || s.StartedAt == nil || s.StartedAt.Unix() != 30 || !s.Eligibility.Available {
		t.Fatalf("completed session: %+v", s)
	}
	if s := p.Sessions[1]; s.Session.ExternalSessionID != child || s.Eligibility.Available || s.Eligibility.Reason == "" {
		t.Fatalf("child session: %+v", s)
	}
	p2, err := r.adapter.SearchSessions(ctx, execution.SessionSearchQuery{Limit: 2, Cursor: p.NextCursor})
	if err != nil || len(p2.Sessions) != 1 || p2.NextCursor != "" {
		t.Fatalf("second page: %+v %v", p2, err)
	}
	if s := p2.Sessions[0]; s.Session.ExternalSessionID != wrapped || s.Origin != "T3 Code" || s.Title != "" {
		t.Fatalf("wrapped session metadata: %+v", s)
	}
	for _, tc := range []struct {
		q    string
		want int
	}{
		{" COMpletEd ", 1}, {"/W/ATLAS", 2}, {wrapped, 1}, {"private prompt", 0}, {"Archived", 0},
	} {
		p, err := r.adapter.SearchSessions(ctx, execution.SessionSearchQuery{Limit: 10, Query: tc.q})
		if err != nil || len(p.Sessions) != tc.want || p.NextCursor != "" {
			t.Fatalf("search %q: %+v %v", tc.q, p, err)
		}
	}
	empty, err := r.adapter.SearchSessions(ctx, execution.SessionSearchQuery{Limit: 1, Query: wrapped})
	if err != nil || len(empty.Sessions) != 0 || empty.NextCursor == "" {
		t.Fatalf("filtered page: %+v %v", empty, err)
	}
	if _, err := r.adapter.SearchSessions(ctx, execution.SessionSearchQuery{Limit: 1, Cursor: "stale"}); !errors.Is(err, execution.ErrSessionSearchCursor) {
		t.Fatalf("stale cursor: %v", err)
	}
	if r.srv.Calls("thread/read")+r.srv.Calls("thread/turns/list")+r.srv.Calls("thread/loaded/list") != 0 {
		t.Fatal("search must only use roster metadata")
	}
	ref, err := r.adapter.ResolveLink(ctx, archived)
	if err != nil || ref.ExternalSessionID != archived {
		t.Fatalf("named resolution: %+v %v", ref, err)
	}
	if observed, err := r.adapter.Observe(ctx, ref); err != nil || !observed.Archived {
		t.Fatalf("named archived thread: %+v %v", observed, err)
	}
	if refs, err := r.adapter.Attention(ctx); err != nil || len(refs) != 1 || refs[0].ExternalSessionID != wrapped {
		t.Fatalf("Attention: %+v %v", refs, err)
	}
}

func TestSessionSearchCodexUnavailableMethod(t *testing.T) {
	r := newRig(t)
	r.srv.Unsupported("thread/list")
	_, err := r.adapter.SearchSessions(t.Context(), execution.SessionSearchQuery{Limit: 1})
	var unavailable *execution.SessionSearchUnsupported
	if !errors.As(err, &unavailable) || unavailable.Reason == "" {
		t.Fatalf("unsupported: %v", err)
	}
}
