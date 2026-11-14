package api_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/t3"
)

type searchResult struct {
	T3ThreadID       string  `json:"t3ThreadId"`
	Title            string  `json:"title"`
	ProjectTitle     *string `json:"projectTitle"`
	MatchSource      string  `json:"matchSource"`
	Snippet          *string `json:"snippet"`
	MessageCreatedAt *string `json:"messageCreatedAt"`
	T3DeepLink       *string `json:"t3DeepLink"`
	Thread           *thread `json:"thread"`
}

type searchResponse struct {
	Results []searchResult `json:"results"`
	T3      string         `json:"t3"`
}

// launch creates a T3-side thread (outside stage) and returns its id.
func launch(t *testing.T, f *t3.Fake, projectID, title string) string {
	t.Helper()
	res, err := f.LaunchThread(context.Background(), t3.LaunchInput{ProjectID: projectID, Title: title, Message: "go"})
	if err != nil {
		t.Fatal(err)
	}
	return res.ThreadID
}

func (e *env) fullSync() {
	e.t.Helper()
	if _, err := e.sync.FullSync(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) search(path string) searchResponse {
	e.t.Helper()
	var out searchResponse
	e.must("GET", path, nil, 200, &out)
	return out
}

func strOr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestSearchThreadsMergesLocalAndT3(t *testing.T) {
	e := newEnv(t)
	stage := e.fake.AddProject("Stage", "/w/stage")
	kit := e.fake.AddProject("Palette kit", "/w/kit")

	titled := launch(t, e.fake, stage.ID, "Palette search")
	branched := launch(t, e.fake, stage.ID, "Fix auth refresh")
	if err := e.fake.SetThread(branched, func(d *t3.ThreadDetail) { d.Branch = "feat/palette" }); err != nil {
		t.Fatal(err)
	}
	inKit := launch(t, e.fake, kit.ID, "Other work")
	said := launch(t, e.fake, stage.ID, "What more?")
	e.fake.AddMessage(said, t3.SearchUser, "…the ctrl k palette should find sessions…")
	e.fullSync()
	late := launch(t, e.fake, stage.ID, "Late palette idea") // not synced yet

	res := e.search("/api/threads/search?q=%20palette%20")
	if res.T3 != "ok" {
		t.Fatalf("t3 = %q", res.T3)
	}
	byID := map[string]searchResult{}
	order := make([]string, len(res.Results))
	for i, r := range res.Results {
		if _, dup := byID[r.T3ThreadID]; dup {
			t.Fatalf("duplicate %s in %+v", r.T3ThreadID, res.Results)
		}
		byID[r.T3ThreadID] = r
		order[i] = r.T3ThreadID
	}
	if len(res.Results) != 5 || order[0] != titled {
		t.Fatalf("order = %v (local title hit first)", order)
	}
	if e.fake.Searches[0].Query != "palette" || e.fake.Searches[0].Limit != 20 || e.fake.Searches[0].ProjectID != "" {
		t.Fatalf("t3 search input = %+v", e.fake.Searches[0])
	}
	want := map[string]string{titled: "title", branched: "branch", inKit: "project", said: "user", late: "title"}
	for id, source := range want {
		if got := byID[id].MatchSource; got != source {
			t.Errorf("%s matchSource = %q, want %q", byID[id].Title, got, source)
		}
	}
	if r := byID[said]; strOr(r.Snippet) != "…the ctrl k palette should find sessions…" || r.MessageCreatedAt == nil || r.Thread == nil {
		t.Fatalf("message hit = %+v", r)
	}
	if r := byID[titled]; r.Snippet != nil || r.Thread == nil || strOr(r.ProjectTitle) != "Stage" || r.T3DeepLink == nil {
		t.Fatalf("title hit = %+v", r)
	}
	if r := byID[late]; r.Thread != nil || r.Title != "Late palette idea" || strOr(r.ProjectTitle) != "Stage" ||
		strOr(r.T3DeepLink) != "t3code://threads/fake-env/"+late {
		t.Fatalf("unsynced hit = %+v", r)
	}
}

func TestSearchThreadsLocalWinsOverMessageHit(t *testing.T) {
	e := newEnv(t)
	p := e.fake.AddProject("Stage", "/w/stage")
	id := launch(t, e.fake, p.ID, "Palette search")
	e.fake.AddMessage(id, t3.SearchAssistant, "palette palette")
	e.fullSync()
	// The fake lists title hits first, so feed T3 only the message hit.
	if err := e.fake.SetThread(id, func(d *t3.ThreadDetail) { d.Title = "renamed in T3" }); err != nil {
		t.Fatal(err)
	}
	res := e.search("/api/threads/search?q=palette")
	if len(res.Results) != 1 || res.Results[0].MatchSource != "title" || res.Results[0].Snippet != nil {
		t.Fatalf("results = %+v", res.Results)
	}
}

func TestSearchThreadsProjectScope(t *testing.T) {
	e := newEnv(t)
	a := e.fake.AddProject("A", "/w/a")
	b := e.fake.AddProject("B", "/w/b")
	inA := launch(t, e.fake, a.ID, "palette in a")
	launch(t, e.fake, b.ID, "palette in b")
	e.fullSync()
	var list struct {
		Projects []project `json:"projects"`
	}
	e.must("GET", "/api/projects", nil, 200, &list)
	var appA int64
	for _, p := range list.Projects {
		if p.T3ProjectID == a.ID {
			appA = p.ID
		}
	}
	res := e.search(fmt.Sprintf("/api/threads/search?q=palette&project=%d&limit=5", appA))
	if len(res.Results) != 1 || res.Results[0].T3ThreadID != inA {
		t.Fatalf("results = %+v", res.Results)
	}
	if s := e.fake.Searches[0]; s.ProjectID != a.ID || s.Limit != 5 {
		t.Fatalf("t3 search input = %+v", s)
	}
	e.must("GET", "/api/threads/search?q=palette&project=999", nil, 404, nil)
}

func TestSearchThreadsDegradesWithoutT3(t *testing.T) {
	e := newEnv(t, func(d *api.Deps) { d.SearchTimeout = 50 * time.Millisecond })
	p := e.fake.AddProject("Stage", "/w/stage")
	local := launch(t, e.fake, p.ID, "Palette search")
	said := launch(t, e.fake, p.ID, "What more?")
	e.fake.AddMessage(said, t3.SearchUser, "palette")
	e.fullSync()

	onlyLocal := func(res searchResponse, status string) {
		t.Helper()
		if res.T3 != status || len(res.Results) != 1 || res.Results[0].T3ThreadID != local {
			t.Fatalf("want local only with t3=%q, got %+v", status, res)
		}
	}

	e.t3.offline.Store(true)
	onlyLocal(e.search("/api/threads/search?q=palette"), "offline")
	if len(e.fake.Searches) != 0 {
		t.Fatalf("searched T3 while offline: %+v", e.fake.Searches)
	}
	e.t3.offline.Store(false)

	e.fake.SearchDelay = time.Second
	start := time.Now()
	onlyLocal(e.search("/api/threads/search?q=palette"), "timeout")
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("timeout took %v", d)
	}
	e.fake.SearchDelay = 0

	e.fake.SearchErr = &t3.ToolError{Tool: "t3_thread_search", Message: "boom"}
	onlyLocal(e.search("/api/threads/search?q=palette"), "error")
	e.fake.SearchErr = fmt.Errorf("wrapped: %w", t3.ErrNotConnected)
	onlyLocal(e.search("/api/threads/search?q=palette"), "offline")
	e.fake.SearchErr = errors.New("connection reset")
	onlyLocal(e.search("/api/threads/search?q=palette"), "error")
}

func TestSearchThreadsValidation(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{
		"/api/threads/search",
		"/api/threads/search?q=p",
		"/api/threads/search?q=%20p%20",
		"/api/threads/search?q=palette&limit=0",
		"/api/threads/search?q=palette&limit=51",
		"/api/threads/search?q=palette&limit=x",
		"/api/threads/search?q=palette&project=x",
	} {
		e.must("GET", path, nil, 400, nil)
	}
	if n := len(e.fake.Searches); n != 0 {
		t.Fatalf("invalid requests reached T3: %d", n)
	}
}
