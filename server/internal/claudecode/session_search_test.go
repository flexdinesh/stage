package claudecode_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakestate"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestSessionSearchUsesOnlyListedMetadata(t *testing.T) {
	const (
		first      = "11111111-1111-4111-8111-111111111111"
		second     = "22222222-2222-4222-8222-222222222222"
		third      = "33333333-3333-4333-8333-333333333333"
		historical = "44444444-4444-4444-8444-444444444444"
	)
	f := claudetest.New(t)
	a := f.Adapter(conn)
	f.Update(func(s *fakestate.State) {
		s.Sessions = []fakestate.Session{
			{Entry: fakestate.Entry{SessionID: second, Name: "Terminal", Kind: "interactive", Cwd: f.Workspace, StartedAt: 300, Status: "idle"}},
			{Entry: fakestate.Entry{SessionID: third, Name: "Different title", Kind: "background", Cwd: f.Workspace, StartedAt: 200, State: "done"}},
			{Entry: fakestate.Entry{SessionID: first, ID: "ab12cd34", Name: "Completed deploy", Kind: "background", Cwd: f.Workspace, StartedAt: 300, State: "done"}},
			{Entry: fakestate.Entry{SessionID: second, Name: "Duplicate", Cwd: f.Workspace, StartedAt: 100}},
		}
	})
	path := fakestate.TranscriptPath(f.ProjectsDir(), f.Workspace, historical)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	log := `{"type":"user","sessionId":"` + historical + `","timestamp":"2026-11-27T00:00:00Z","cwd":"` + f.Workspace + `","message":{"content":"historical content"}}` + "\n"
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}

	search := func(query string, limit int, cursor string) execution.SessionSearchPage {
		t.Helper()
		page, err := a.SearchSessions(t.Context(), execution.SessionSearchQuery{Query: query, Limit: limit, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	ids := func(page execution.SessionSearchPage) []string {
		out := make([]string, 0, len(page.Sessions))
		for _, item := range page.Sessions {
			if item.Session.ConnectionID != conn || item.Workspace != f.Workspace || !item.Eligibility.Available {
				t.Fatalf("unexpected listed metadata: %+v", item)
			}
			out = append(out, item.Session.ExternalSessionID)
		}
		return out
	}
	page := search("", 2, "")
	if got := ids(page); !slices.Equal(got, []string{first, second}) || page.NextCursor == "" ||
		page.Sessions[0].Title != "Completed deploy" || page.Sessions[0].StartedAt == nil || page.Sessions[0].StartedAt.UnixMilli() != 300 {
		t.Fatalf("first page: %+v", page)
	}
	page = search("", 2, page.NextCursor)
	if got := ids(page); !slices.Equal(got, []string{third}) || page.NextCursor != "" {
		t.Fatalf("last page: %+v", page)
	}
	for _, q := range []string{"DEPLOY", "AB12CD34", first, "src/repo"} {
		if got := ids(search(q, 1, "")); !slices.Equal(got, []string{first}) {
			t.Errorf("query %q returned %v", q, got)
		}
	}
	if got := search(historical, 20, ""); len(got.Sessions) != 0 || got.NextCursor != "" {
		t.Fatalf("transcript-only session appeared in browse: %+v", got)
	}
	if got, err := a.Observe(t.Context(), execution.SessionRef{ConnectionID: conn, ExternalSessionID: historical}); err != nil ||
		got.Session.ExternalSessionID != historical {
		t.Fatalf("explicit historical lookup: %+v, %v", got, err)
	}
	if _, err := a.SearchSessions(t.Context(), execution.SessionSearchQuery{Limit: 1, Cursor: "invalid"}); !errors.Is(err, execution.ErrSessionSearchCursor) {
		t.Fatalf("invalid adapter cursor: %v", err)
	}
	f.Update(func(s *fakestate.State) { s.FailAgents = true })
	if _, err := a.SearchSessions(t.Context(), execution.SessionSearchQuery{Limit: 1}); err == nil {
		t.Fatal("a failed roster read appeared as an empty page")
	}
}

func TestSessionSearchUsesConfiguredClaudeInstance(t *testing.T) {
	f := claudetest.New(t)
	a := f.Adapter(conn)
	f.Update(func(s *fakestate.State) { s.ConfigDir = filepath.Join(f.Home, "different-profile") })
	_, err := a.SearchSessions(t.Context(), execution.SessionSearchQuery{Limit: 1})
	if !errors.Is(err, execution.ErrUnavailable) {
		t.Fatalf("changed config identity: %v", err)
	}
}
