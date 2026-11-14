package t3

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestClient(t *testing.T, f *fakeT3, admin string, store *MemoryTokenStore) *MCPClient {
	t.Helper()
	c := NewMCPClient(Options{BaseURL: f.url() + "/", AdminToken: admin, PublicURL: "http://localhost:3773", Store: store, Logger: slog.New(slog.DiscardHandler), ClientName: DefaultClientName})
	t.Cleanup(c.Close)
	return c
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func capabilitiesTool(f *fakeT3) {
	f.tool("orchestrator_capabilities", func(map[string]any) (*mcp.CallToolResult, error) {
		return structured(`{"providers":[]}`), nil
	})
}

func TestS256MatchesRFC7636(t *testing.T) {
	// RFC 7636 Appendix B.
	if got := s256("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("s256 = %q", got)
	}
	v, ch, err := pkce()
	if err != nil {
		t.Fatal(err)
	}
	if len(v) < 43 || len(v) > 128 || len(ch) != 43 || s256(v) != ch {
		t.Fatalf("bad pkce pair: verifier %d chars, challenge %q", len(v), ch)
	}
	if strings.ContainsAny(v+ch, "+/=") {
		t.Fatal("pkce values must be base64url without padding")
	}
}

func TestAuthorizeWithAdminTokenSendsT3Payloads(t *testing.T) {
	f := newFakeT3(t)
	capabilitiesTool(f)
	store := NewMemoryTokenStore()
	c := newTestClient(t, f, f.adminToken, store)
	ctx := ctxT(t)

	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	mint, tokens := f.counts()
	if mint != 1 || tokens != 1 {
		t.Fatalf("mint=%d token=%d, want 1/1", mint, tokens)
	}
	saved, _ := store.LoadToken(ctx)
	if !strings.HasPrefix(saved.Token, "access-") || saved.ClientID == "" {
		t.Fatalf("token not saved: %+v", saved)
	}
	if d := time.Until(saved.ExpiresAt); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Fatalf("expiresAt %v not ~30 days out", saved.ExpiresAt)
	}

	f.mu.Lock()
	dec, form, auth := f.lastDecision, f.lastTokenForm, f.mcpAuthHeaders
	names, labels := f.clientNames, f.pairingLabels
	f.mu.Unlock()
	if !slices.Equal(names, []string{"stage"}) || !slices.Equal(labels, []string{"stage mcp"}) {
		t.Fatalf("client names %v, pairing labels %v", names, labels)
	}
	d, _ := dec["decision"].(map[string]any)
	if d["_tag"] != "pairing-code" || d["access"] != "full-access" || !strings.HasPrefix(d["code"].(string), "PAIR-") {
		t.Fatalf("decision = %v", d)
	}
	a, _ := dec["authorization"].(map[string]any)
	if a["redirect_uri"] != DefaultRedirectURI || a["code_challenge_method"] != "S256" || a["resource"] != f.url()+"/mcp" || a["state"] == "" {
		t.Fatalf("authorization = %v", a)
	}
	if form.Get("redirect_uri") != DefaultRedirectURI || form.Get("resource") != f.url()+"/mcp" || form.Get("client_id") != a["client_id"] {
		t.Fatalf("token form = %v", form)
	}
	if s256(form.Get("code_verifier")) != a["code_challenge"] {
		t.Fatal("verifier does not match challenge")
	}
	if len(auth) == 0 || auth[0] != "Bearer "+saved.Token {
		t.Fatalf("mcp auth header = %v", auth)
	}

	st := c.Status(ctx)
	if !st.Connected || st.ClientID != saved.ClientID || st.ExpiresAt == nil || st.EnvironmentID != "env-fake" || st.LastError != "" {
		t.Fatalf("status = %+v", st)
	}
	if got := c.WebURL(ctx, "thread-1"); got != "http://localhost:3773/env-fake/thread-1" {
		t.Fatalf("web url = %q", got)
	}
}

func TestPairUsesUserCodeWithoutAdmin(t *testing.T) {
	f := newFakeT3(t)
	capabilitiesTool(f)
	f.addUserPairingCode("USER-CODE")
	store := NewMemoryTokenStore()
	c := newTestClient(t, f, "", store)
	ctx := ctxT(t)

	if _, err := c.Capabilities(ctx); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("before pair: err = %v, want ErrNotConnected", err)
	}
	if err := c.Pair(ctx, "  USER-CODE "); err != nil {
		t.Fatal(err)
	}
	if mint, _ := f.counts(); mint != 0 {
		t.Fatalf("pair minted %d codes", mint)
	}
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Pair(ctx, "USER-CODE"); err == nil || !strings.Contains(err.Error(), "unknown or already used") {
		t.Fatalf("reused code: err = %v", err)
	}
	if err := c.Pair(ctx, " "); err == nil {
		t.Fatal("empty code accepted")
	}
}

func TestMintFailureIsReportedAndRecorded(t *testing.T) {
	f := newFakeT3(t)
	f.adminScopes = []string{"access:write", "orchestration:read", "orchestration:operate"}
	capabilitiesTool(f)
	store := NewMemoryTokenStore()
	c := newTestClient(t, f, f.adminToken, store)
	ctx := ctxT(t)

	_, err := c.Capabilities(ctx)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
	if !strings.Contains(err.Error(), "pairing-token") || !strings.Contains(err.Error(), "settings:write") {
		t.Fatalf("err lacks detail: %v", err)
	}
	if !strings.Contains(store.LastError(), "insufficient_scope") {
		t.Fatalf("store last error = %q", store.LastError())
	}
	if st := c.Status(ctx); st.Connected || st.LastError == "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestRenewsOnlyInsideWindow(t *testing.T) {
	f := newFakeT3(t)
	capabilitiesTool(f)
	ctx := ctxT(t)

	// 20 days left: valid, outside window -> no renewal. Register token with fake.
	store := NewMemoryTokenStore()
	f.addToken("old-valid")
	_ = store.SaveToken(ctx, StoredToken{ClientID: "c", Token: "old-valid", ExpiresAt: time.Now().Add(20 * 24 * time.Hour)})
	c := newTestClient(t, f, f.adminToken, store)
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	if mint, _ := f.counts(); mint != 0 {
		t.Fatalf("renewed outside window (mint=%d)", mint)
	}

	// 4 days left: inside window -> renewed lazily on the call.
	store2 := NewMemoryTokenStore()
	f.addToken("old-near")
	_ = store2.SaveToken(ctx, StoredToken{ClientID: "c", Token: "old-near", ExpiresAt: time.Now().Add(4 * 24 * time.Hour)})
	c2 := newTestClient(t, f, f.adminToken, store2)
	if _, err := c2.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	tok, _ := store2.LoadToken(ctx)
	if tok.Token == "old-near" || time.Until(tok.ExpiresAt) < 29*24*time.Hour {
		t.Fatalf("not renewed: %+v", tok)
	}
}

func TestRenewFailureKeepsValidToken(t *testing.T) {
	f := newFakeT3(t)
	capabilitiesTool(f)
	ctx := ctxT(t)
	store := NewMemoryTokenStore()
	f.addToken("old-near")
	_ = store.SaveToken(ctx, StoredToken{Token: "old-near", ExpiresAt: time.Now().Add(2 * 24 * time.Hour)})
	c := newTestClient(t, f, "wrong-admin", store)
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatalf("call should use still-valid token: %v", err)
	}
	if store.LastError() == "" {
		t.Fatal("renewal failure not recorded")
	}
	if st := c.Status(ctx); !st.Connected {
		t.Fatalf("status = %+v", st)
	}
}

func TestUnauthorizedRenewsOnceAndRetries(t *testing.T) {
	f := newFakeT3(t)
	var callsN atomic.Int32
	f.tool("orchestrator_capabilities", func(map[string]any) (*mcp.CallToolResult, error) {
		callsN.Add(1)
		return structured(`{"providers":[{"providerInstanceId":"claude","driverKind":"claude","displayName":null,"models":[],"canRunChildTask":true,"canRunCrossProviderChildTask":true,"constraints":[]}]}`), nil
	})
	ctx := ctxT(t)
	store := NewMemoryTokenStore()
	// Valid-looking token T3 does not know (e.g. revoked): first request 401s.
	_ = store.SaveToken(ctx, StoredToken{Token: "revoked", ExpiresAt: time.Now().Add(20 * 24 * time.Hour)})
	c := newTestClient(t, f, f.adminToken, store)

	cat, err := c.Capabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if calls := callsN.Load(); len(cat.Providers) != 1 || calls != 1 {
		t.Fatalf("providers=%d calls=%d", len(cat.Providers), calls)
	}
	if mint, _ := f.counts(); mint != 1 {
		t.Fatalf("mint = %d, want 1", mint)
	}

	// Token revoked mid-session: next call 401s, renews, retries.
	f.revokeAll()
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	if mint, _ := f.counts(); mint != 2 || callsN.Load() != 2 {
		t.Fatalf("mint=%d calls=%d, want 2/2", mint, callsN.Load())
	}
	if st := c.Status(ctx); !st.Connected {
		t.Fatalf("status = %+v", st)
	}
}

func TestUnauthorizedWithoutAdminIsNotConnected(t *testing.T) {
	f := newFakeT3(t)
	capabilitiesTool(f)
	ctx := ctxT(t)
	store := NewMemoryTokenStore()
	_ = store.SaveToken(ctx, StoredToken{Token: "revoked", ExpiresAt: time.Now().Add(20 * 24 * time.Hour)})
	c := newTestClient(t, f, "", store)
	if _, err := c.Capabilities(ctx); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
	if st := c.Status(ctx); st.Connected {
		t.Fatalf("status = %+v", st)
	}
}

func TestReconnectsWhenSessionDropped(t *testing.T) {
	f := newFakeT3(t)
	capabilitiesTool(f)
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	ctx := ctxT(t)
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	f.resetMCP() // server forgets the session -> 404 -> ErrSessionMissing
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatalf("after session drop: %v", err)
	}
	if mint, _ := f.counts(); mint != 1 {
		t.Fatalf("reconnect should not re-authorize (mint=%d)", mint)
	}
}

func TestRunRenewsInBackground(t *testing.T) {
	f := newFakeT3(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := NewMemoryTokenStore()
	_ = store.SaveToken(ctx, StoredToken{Token: "near", ExpiresAt: time.Now().Add(time.Hour)})
	c := newTestClient(t, f, f.adminToken, store)
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for store.Saves() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("Run did not renew")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestToolErrorMapsNotFound(t *testing.T) {
	f := newFakeT3(t)
	f.tool("t3_thread_read", func(args map[string]any) (*mcp.CallToolResult, error) {
		if args["limit"] != float64(1) || args["runLimit"] != float64(50) || args["maxCharsPerItem"] == nil {
			return structured(`{"bad":"args"}`), nil
		}
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{
			Text: `{"_tag":"OrchestratorMcpFailure","code":"thread_not_found","message":"Thread x is no longer available."}`,
		}}}, nil
	})
	f.tool("t3_thread_send", func(map[string]any) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{
			Text: `{"_tag":"OrchestratorMcpFailure","code":"thread_not_sendable","message":"Thread is archived."}`,
		}}}, nil
	})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	ctx := ctxT(t)
	_, err := c.ReadThread(ctx, "x")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	err = c.SendMessage(ctx, "x", "hi", SendAuto)
	var te *ToolError
	if !errors.As(err, &te) || te.Code != "thread_not_sendable" || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadItemsArgsAndDecode(t *testing.T) {
	f := newFakeT3(t)
	var mu sync.Mutex
	var calls []map[string]any
	f.tool("t3_thread_read", func(args map[string]any) (*mcp.CallToolResult, error) {
		mu.Lock()
		calls = append(calls, args)
		mu.Unlock()
		return structured(`{"thread":{"threadId":"th-1","status":"running","activeRunId":"run-2","link":"t3-thread://v2/env-a/th-1"},"recentRuns":[],
			"items":[{"position":4,"visibility":"local","sourceThreadId":"th-1","itemId":"i4","runId":"run-2","messageId":"m4","createdBy":"user","type":"user_message","status":"completed","title":null,"text":"hi","textTruncated":false,"nextTextOffset":null,"updatedAt":"2026-11-14T01:02:03.000Z"},
			{"position":9,"visibility":"inherited","sourceThreadId":"th-0","itemId":"i9","runId":null,"messageId":null,"createdBy":null,"type":"assistant_message","status":"running","title":null,"text":"par","textTruncated":true,"nextTextOffset":3,"updatedAt":"2026-11-14T01:02:04.000Z"}],
			"nextPosition":9,"hasMore":true}`), nil
	})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	ctx := ctxT(t)

	page, err := c.ReadItems(ctx, ReadItemsInput{ThreadID: "th-1", Limit: 500, MaxChars: 90000})
	if err != nil {
		t.Fatal(err)
	}
	after := 3
	if _, err := c.ReadItems(ctx, ReadItemsInput{ThreadID: "th-1", AfterPosition: &after, ItemID: "i9", TextOffset: 3}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	first, second := calls[0], calls[1]
	mu.Unlock()
	if _, ok := first["afterPosition"]; ok || first["view"] != "messages" || first["limit"] != float64(100) || first["maxCharsPerItem"] != float64(50000) {
		t.Fatalf("first args = %v (nil afterPosition must be omitted; limits clamped)", first)
	}
	if second["afterPosition"] != float64(3) || second["itemId"] != "i9" || second["textOffset"] != float64(3) {
		t.Fatalf("second args = %v", second)
	}
	if page.Thread.Status != StatusRunning || page.Thread.ActiveRunID != "run-2" || !page.HasMore || page.NextPosition == nil || *page.NextPosition != 9 {
		t.Fatalf("page = %+v", page)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items = %+v", page.Items)
	}
	a, b := page.Items[0], page.Items[1]
	if a.Position != 4 || a.ItemID != "i4" || a.Type != "user_message" || a.CreatedBy != "user" || a.Text != "hi" || !a.Settled() || a.UpdatedAt.IsZero() {
		t.Fatalf("item 0 = %+v", a)
	}
	if b.Visibility != "inherited" || b.SourceThreadID != "th-0" || b.RunID != "" || !b.TextTruncated || b.NextTextOffset == nil || *b.NextTextOffset != 3 || b.Settled() {
		t.Fatalf("item 1 = %+v", b)
	}
}

func TestFakeReadItemsPagesMessagesOnly(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	res, err := f.LaunchThread(ctx, LaunchInput{ProjectID: "p", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	id := res.ThreadID
	f.AddItem(id, TimelineItem{Type: "user_message", Text: "q"})
	f.AddItem(id, TimelineItem{Type: "command_execution", Text: "ls"})
	f.AddItem(id, TimelineItem{Type: "assistant_message", Text: "abcdef", Status: "running"})
	f.AddItem(id, TimelineItem{Type: "proposed_plan", Text: "plan"})
	page, err := f.ReadItems(ctx, ReadItemsInput{ThreadID: id, Limit: 2, MaxChars: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Position != 0 || page.Items[1].Position != 2 || !page.HasMore || *page.NextPosition != 2 {
		t.Fatalf("page = %+v", page)
	}
	if it := page.Items[1]; it.Text != "abcd" || !it.TextTruncated || *it.NextTextOffset != 4 {
		t.Fatalf("truncation = %+v", it)
	}
	rest, err := f.ReadItems(ctx, ReadItemsInput{ThreadID: id, AfterPosition: page.NextPosition})
	if err != nil || len(rest.Items) != 1 || rest.Items[0].Type != "proposed_plan" || rest.HasMore {
		t.Fatalf("rest = %+v, %v", rest, err)
	}
	tail, err := f.ReadItems(ctx, ReadItemsInput{ThreadID: id, ItemID: page.Items[1].ItemID, TextOffset: 4})
	if err != nil || len(tail.Items) != 1 || tail.Items[0].Text != "ef" || tail.Items[0].TextTruncated {
		t.Fatalf("continuation = %+v, %v", tail, err)
	}
	if _, err := f.ReadItems(ctx, ReadItemsInput{ThreadID: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown thread err = %v", err)
	}
}

func TestContinuationSendEnvelopeAndReceipt(t *testing.T) {
	f := newFakeT3(t)
	f.tool("t3_thread_send", func(args map[string]any) (*mcp.CallToolResult, error) {
		if args["threadId"] != "target" || args["message"] != " exact\n" || args["mode"] != "queue" || args["clientRequestId"] != "persisted-request" {
			t.Errorf("send args=%v", args)
		}
		return structured(`{"threadId":"target","delivery":"started","runId":"new-run","messageId":"new-message"}`), nil
	})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	out, err := c.QueueContinuation(ctxT(t), "target", " exact\n", "persisted-request")
	if err != nil || out.RunID != "new-run" || out.MessageID != "new-message" {
		t.Fatalf("receipt=%+v err=%v", out, err)
	}
	if retrySafe("t3_thread_send", map[string]any{"clientRequestId": "persisted-request"}) {
		t.Fatal("send retry assumes unproven deduplication")
	}
}

func TestToolArgumentsSent(t *testing.T) {
	f := newFakeT3(t)
	var gotMu sync.Mutex
	gotMap := map[string]map[string]any{}
	got := func(name string) map[string]any {
		gotMu.Lock()
		defer gotMu.Unlock()
		return gotMap[name]
	}
	record := func(name, result string) {
		f.tool(name, func(args map[string]any) (*mcp.CallToolResult, error) {
			gotMu.Lock()
			gotMap[name] = args
			gotMu.Unlock()
			return structured(result), nil
		})
	}
	record("t3_thread_launch", `{"threadId":"t1","link":"https://app.t3.codes/threads/env-a/t1","projectId":"p1","modelSelection":{"instanceId":"claude","model":"m","options":[{"id":"effort","value":"low"}]},"runId":null,"status":null}`)
	record("t3_thread_interrupt", `{"threadId":"t1","runId":null,"status":"no_active_run"}`)
	record("t3_thread_list", `{"projectId":null,"currentThreadId":null,"threads":[],"nextCursor":null,"total":0}`)
	record("t3_project_create", `{"id":"p9","title":"X","workspaceRoot":"/x","defaultModelSelection":null,"scripts":[],"createdAt":"2026-11-08T00:00:00.000Z","updatedAt":"2026-11-08T00:00:00.000Z","deletedAt":null}`)
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	ctx := ctxT(t)

	sfo := true
	res, err := c.LaunchThread(ctx, LaunchInput{
		ProjectID: "p1", Title: " T ", Message: "do it",
		ModelSelection:  &ModelSelection{InstanceID: "claude", Model: "m", Options: []ModelOption{{ID: "effort", Value: StringValue("low")}}},
		RuntimeMode:     RuntimeFullAccess,
		InteractionMode: InteractionPlan,
		Workspace:       &WorkspaceStrategy{Type: WorkspaceWorktree, BaseRef: "origin/main", Branch: "feat", StartFromOrigin: &sfo},
		ClientRequestID: "task-1-1",
		Attachments:     []Attachment{{ID: "pending-9", Name: "a.png", MimeType: "image/png", SizeBytes: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if atts, _ := got("t3_thread_launch")["attachments"].([]any); len(atts) != 1 {
		t.Fatalf("launch attachments = %v", got("t3_thread_launch")["attachments"])
	} else if a0, _ := atts[0].(map[string]any); a0["id"] != "pending-9" || a0["type"] != "image" || a0["name"] != "a.png" {
		t.Fatalf("launch attachment = %v", a0)
	}
	if res.ThreadID != "t1" || res.RunID != "" || res.Status != "" || len(res.ModelSelection.Options) != 1 {
		t.Fatalf("launch result = %+v", res)
	}
	la := got("t3_thread_launch")
	ws, _ := la["workspaceStrategy"].(map[string]any)
	ms, _ := la["modelSelection"].(map[string]any)
	if la["title"] != "T" || la["clientRequestId"] != "task-1-1" || la["runtimeMode"] != "full-access" || la["interactionMode"] != "plan" ||
		ws["type"] != "worktree" || ws["baseRef"] != "origin/main" || ws["branch"] != "feat" || ws["startFromOrigin"] != true ||
		ms["instanceId"] != "claude" {
		t.Fatalf("launch args = %v", la)
	}
	if st := c.Status(ctx); st.EnvironmentID == "" {
		t.Fatal("environment id unknown")
	}

	if err := c.Interrupt(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	if ia := got("t3_thread_interrupt"); ia["threadId"] != "t1" || ia["stop"] != true {
		t.Fatalf("interrupt args = %v", ia)
	}

	cursor := 100
	if _, err := c.ListThreads(ctx, ListThreadsInput{AllProjects: true, ProjectID: "ignored", Cursor: &cursor, Limit: 500}); err != nil {
		t.Fatal(err)
	}
	ta := got("t3_thread_list")
	if ta["allProjects"] != true || ta["projectId"] != nil || ta["cursor"] != float64(100) || ta["limit"] != float64(100) || ta["includeSubagents"] != false {
		t.Fatalf("list args = %v", ta)
	}

	p, err := c.CreateProject(ctx, CreateProjectInput{Title: "X", WorkspaceRoot: "/x"})
	if err != nil || p.ID != "p9" || p.DeletedAt != nil {
		t.Fatalf("create = %+v, %v", p, err)
	}
}

func TestListProjectsPagesToCompletion(t *testing.T) {
	f := newFakeT3(t)
	var cursorsMu sync.Mutex
	var cursors []any
	f.tool("t3_project_list", func(args map[string]any) (*mcp.CallToolResult, error) {
		cursorsMu.Lock()
		cursors = append(cursors, args["cursor"])
		cursorsMu.Unlock()
		if args["cursor"] == float64(0) {
			return structured(`{"projects":[{"id":"a","title":"A","workspaceRoot":"/a","createdAt":"2026-10-20T11:40:19.565Z","updatedAt":"2026-10-20T11:40:19.565Z","deletedAt":null,"archivedAt":null}],"nextCursor":1}`), nil
		}
		return structured(`{"projects":[{"id":"b","title":"B","workspaceRoot":"/b","createdAt":"2026-10-20T11:40:19.565Z","updatedAt":"2026-10-20T11:40:19.565Z","deletedAt":null,"archivedAt":"2026-11-06T05:05:33.809Z"}],"nextCursor":null}`), nil
	})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	ps, err := c.ListProjects(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	cursorsMu.Lock()
	defer cursorsMu.Unlock()
	if len(ps) != 2 || ps[0].ID != "a" || ps[1].ArchivedAt == nil || len(cursors) != 2 {
		t.Fatalf("projects = %+v cursors = %v", ps, cursors)
	}
}

func (c *MCPClient) currentSession() *mcp.ClientSession {
	c.sessMu.Lock()
	defer c.sessMu.Unlock()
	return c.session
}

func TestRenewalKeepsSessionAndSendsNewToken(t *testing.T) {
	f := newFakeT3(t)
	capabilitiesTool(f)
	ctx := ctxT(t)
	store := NewMemoryTokenStore()
	f.addToken("old-valid")
	_ = store.SaveToken(ctx, StoredToken{ClientID: "c", Token: "old-valid", ExpiresAt: time.Now().Add(20 * 24 * time.Hour)})
	c := newTestClient(t, f, f.adminToken, store)
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	before := c.currentSession()
	// Enter the renew window and let Run's check renew.
	c.mu.Lock()
	c.tok.ExpiresAt = time.Now().Add(time.Hour)
	c.mu.Unlock()
	if err := c.refreshIfNeeded(ctx); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	delete(f.tokens, "old-valid") // old token no longer accepted
	f.mu.Unlock()
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	if after := c.currentSession(); after == nil || after != before {
		t.Fatal("token renewal must not replace the MCP session")
	}
	if mint, _ := f.counts(); mint != 1 {
		t.Fatalf("mint = %d, want 1 (new token sent per request, no 401)", mint)
	}
}

func TestProtocolErrorKeepsSession(t *testing.T) {
	f := newFakeT3(t)
	capabilitiesTool(f)
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	ctx := ctxT(t)
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	before := c.currentSession()
	var out map[string]any
	if err := c.call(ctx, "no_such_tool", map[string]any{}, &out); err == nil {
		t.Fatal("unknown tool must fail")
	} else if IsTransportError(err) {
		t.Fatalf("protocol error classified as transport: %v", err)
	}
	if after := c.currentSession(); after == nil || after != before {
		t.Fatal("a tool/protocol error must not drop the shared session")
	}
}

func TestClosedSessionRetriesOnlyIdempotentTools(t *testing.T) {
	f := newFakeT3(t)
	capabilitiesTool(f)
	var sends atomic.Int32
	f.tool("t3_thread_send", func(map[string]any) (*mcp.CallToolResult, error) {
		sends.Add(1)
		return structured(`{"ok":true}`), nil
	})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	ctx := ctxT(t)
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	closeUnderneath := func() {
		cs := c.currentSession()
		if cs == nil {
			t.Fatal("no session")
		}
		_ = cs.Close() // connection closed while still the client's session
	}
	closeUnderneath()
	err := c.SendMessage(ctx, "th-1", "hi", SendAuto)
	if !errors.Is(err, mcp.ErrConnectionClosed) || sends.Load() != 0 {
		t.Fatalf("send on closed session: err=%v sends=%d (must not retry)", err, sends.Load())
	}
	if _, err := c.Capabilities(ctx); err != nil { // reconnects
		t.Fatal(err)
	}
	closeUnderneath()
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatalf("idempotent call must reconnect and retry: %v", err)
	}

	for _, tc := range []struct {
		tool string
		args map[string]any
		want bool
	}{
		{"t3_thread_read", nil, true},
		{"t3_thread_list", nil, true},
		{"t3_project_list", nil, true},
		{"orchestrator_capabilities", nil, true},
		{"t3_thread_launch", map[string]any{"clientRequestId": "task-1-1"}, true},
		{"t3_thread_launch", map[string]any{}, false},
		{"t3_thread_send", nil, false},
		{"t3_thread_interrupt", nil, false},
		{"t3_project_create", nil, false},
	} {
		if got := retrySafe(tc.tool, tc.args); got != tc.want {
			t.Errorf("retrySafe(%s, %v) = %v", tc.tool, tc.args, got)
		}
	}
}

func TestStaleRejectionDoesNotMarkCurrentToken(t *testing.T) {
	f := newFakeT3(t)
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	c.mu.Lock()
	c.tok, c.loaded = StoredToken{Token: "new", ExpiresAt: time.Now().Add(20 * 24 * time.Hour)}, true
	c.mu.Unlock()
	c.markRejected("old")
	if c.isRejected() {
		t.Fatal("401 for a replaced token must not mark the current token rejected")
	}
	c.markRejected("new")
	if !c.isRejected() {
		t.Fatal("401 for the current token must mark it rejected")
	}
}

func TestIsTransportError(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{ErrNotConnected, false},
		{ErrNotFound, false},
		{&ToolError{Tool: "t3_thread_launch", Message: "bad"}, false},
		{fmt.Errorf("t3 x: %w", &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "bad"}), false},
		{fmt.Errorf("t3 x: %w", &jsonrpc.Error{Code: errCodeTransportRejected, Message: "rejected"}), true},
		{fmt.Errorf("t3 x: %w", mcp.ErrConnectionClosed), true},
		{context.DeadlineExceeded, true},
		{errors.New("connection reset by peer"), true},
	} {
		if got := IsTransportError(tc.err); got != tc.want {
			t.Errorf("IsTransportError(%v) = %v", tc.err, got)
		}
	}
}

func TestUploadAndSendAttachments(t *testing.T) {
	f := newFakeT3(t)
	var mu sync.Mutex
	args := map[string]map[string]any{}
	f.tool("t3_attachment_prepare_upload", func(a map[string]any) (*mcp.CallToolResult, error) {
		mu.Lock()
		args["prepare"] = a
		mu.Unlock()
		return structured(`{"attachmentId":"pending-1","relativeUrl":"/api/attachments/upload/tok1","expiresAt":1}`), nil
	})
	f.tool("t3_thread_send_attachments", func(a map[string]any) (*mcp.CallToolResult, error) {
		mu.Lock()
		args["send"] = a
		mu.Unlock()
		return structured(`{"threadId":"t1"}`), nil
	})
	c := newTestClient(t, f, f.adminToken, NewMemoryTokenStore())
	ctx := ctxT(t)

	data := []byte("\x89PNG fake")
	a, err := c.UploadAttachment(ctx, UploadInput{Name: "shot.png", MimeType: "image/png", Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if a != (Attachment{ID: "pending-1", Name: "shot.png", MimeType: "image/png", SizeBytes: len(data)}) {
		t.Fatalf("attachment = %+v", a)
	}
	up, ok := f.uploads["tok1"]
	if !ok || string(up.body) != string(data) || up.contentType != "image/png" || up.auth != "" {
		t.Fatalf("upload = %+v (ok=%v)", up, ok)
	}
	mu.Lock()
	pu, _ := args["prepare"]["upload"].(map[string]any)
	mu.Unlock()
	if pu["type"] != "image" || pu["name"] != "shot.png" || pu["mimeType"] != "image/png" || pu["sizeBytes"] != float64(len(data)) {
		t.Fatalf("prepare args = %v", pu)
	}

	if err := c.SendAttachments(ctx, "t1", "see", []Attachment{a}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	sa := args["send"]
	mu.Unlock()
	atts, _ := sa["attachments"].([]any)
	first, _ := atts[0].(map[string]any)
	if sa["threadId"] != "t1" || sa["message"] != "see" || len(atts) != 1 || first["id"] != "pending-1" || first["type"] != "image" || first["sizeBytes"] != float64(len(data)) {
		t.Fatalf("send args = %v", sa)
	}
	if err := c.SendAttachments(ctx, "t1", "x", nil); err == nil {
		t.Fatal("send without attachments must fail")
	}
}

func TestClientNameLabelsSession(t *testing.T) {
	f := newFakeT3(t)
	capabilitiesTool(f)
	c := NewMCPClient(Options{BaseURL: f.url(), AdminToken: f.adminToken, Store: NewMemoryTokenStore(), Logger: slog.New(slog.DiscardHandler), ClientName: "stage dev"})
	t.Cleanup(c.Close)
	if _, err := c.Capabilities(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	names, labels := f.clientNames, f.pairingLabels
	f.mu.Unlock()
	if !slices.Equal(names, []string{"stage dev"}) || !slices.Equal(labels, []string{"stage dev mcp"}) {
		t.Fatalf("client names %v, pairing labels %v", names, labels)
	}
}
