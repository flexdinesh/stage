package t3

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Fixtures follow live t3 0.1.7 results (ids/titles changed), including
// fields we ignore, to prove tolerant decoding.

const threadListJSON = `{
 "currentThreadId": null,
 "nextCursor": 3,
 "projectId": null,
 "threads": [
  {
   "createdAt": "2026-11-08T00:55:49.692Z",
   "createdBy": "agent",
   "creationSource": "provider",
   "interactionMode": "default",
   "itemCount": 66,
   "latestRunId": null,
   "link": "https://app.t3.codes/threads/env-1/thread%3Aprovider%3AclaudeAgent%3Anative-thread%3Aabc%253Ax",
   "linkedPullRequest": null,
   "model": "claude-opus-5-5",
   "parentThreadId": "parent-1",
   "projectId": "proj-1",
   "providerInstanceId": "claudeAgent",
   "pullRequests": [],
   "relationshipToParent": "subagent",
   "runtimeMode": "full-access",
   "settled": false,
   "settledAt": null,
   "snoozed": false,
   "snoozedUntil": null,
   "status": "idle",
   "threadId": "thread:provider:claudeAgent:native-thread:abc%3Ax",
   "title": "Sub task",
   "updatedAt": "2026-11-08T01:04:25.816Z"
  },
  {
   "createdAt": "2026-11-07T23:16:00.600Z",
   "createdBy": "user",
   "creationSource": "web",
   "interactionMode": "plan",
   "itemCount": 85,
   "latestRunId": "run:thread:t2:ordinal:2",
   "model": "gpt-6.1-sol",
   "parentThreadId": null,
   "projectId": "proj-2",
   "providerInstanceId": "codex",
   "relationshipToParent": null,
   "runtimeMode": "approval-required",
   "settled": true,
   "settledAt": "2026-11-08T02:00:00.000Z",
   "snoozed": true,
   "status": "completed",
   "threadId": "t2",
   "title": "Root task",
   "updatedAt": "2026-11-08T02:00:00.000Z",
   "someFutureField": {"x": 1}
  }
 ],
 "total": 781
}`

const threadReadJSON = `{
 "hasMore": true,
 "items": [{"createdBy":"user","itemId":"turn-item:message:m1","position":0,"status":"completed","text":"hello","textTruncated":true,"title":null,"type":"user_message","updatedAt":"2026-11-07T23:16:00.654Z","visibility":"local"}],
 "nextPosition": 0,
 "recentRuns": [
  {"completedAt": null, "model": "claude-opus-5-5", "ordinal": 2, "providerInstanceId": "claudeAgent",
   "requestedAt": "2026-11-08T00:03:40.338Z", "runId": "run:thread:t1:ordinal:2", "startedAt": "2026-11-08T00:03:40.365Z", "status": "running"}
 ],
 "thread": {
  "activeRunId": "run:thread:t1:ordinal:2",
  "archived": false,
  "branch": "main",
  "createdAt": "2026-11-07T23:16:00.600Z",
  "createdBy": "user",
  "creationSource": "web",
  "interactionMode": "default",
  "itemCount": 85,
  "latestRunId": "run:thread:t1:ordinal:2",
  "link": "https://app.t3.codes/threads/38506b05-b4b9-4dc1-ad0c-ae95a74cd13e/t1",
  "linkedPullRequest": null,
  "model": "claude-opus-5-5",
  "parentThreadId": null,
  "pendingRequestCount": 2,
  "projectId": "proj-1",
  "providerInstanceId": "claudeAgent",
  "pullRequests": [],
  "relationshipToParent": null,
  "runCount": 2,
  "runtimeMode": "full-access",
  "settled": false,
  "settledAt": null,
  "snoozed": false,
  "snoozedUntil": null,
  "status": "running",
  "threadId": "t1",
  "title": "Build it",
  "titleRegeneration": null,
  "updatedAt": "2026-11-08T01:04:25.813Z",
  "worktreePath": null
 }
}`

const capabilitiesJSON = `{
 "features": {"appOwnedSubagents": true, "maxBatchThreads": 20},
 "inheritedModel": null, "inheritedOptions": [], "inheritedProviderInstanceId": null,
 "interactionMode": "default", "parentThreadId": null, "runtimeMode": "full-access",
 "providers": [
  {
   "canRunChildTask": true, "canRunCrossProviderChildTask": true, "constraints": [],
   "displayName": "Claude", "driverKind": "claudeAgent", "providerInstanceId": "claudeAgent",
   "omittedModels": 1,
   "models": [
    {"id": "claude-opus-5-5", "label": "Claude Opus 5.5",
     "options": [
      {"default": "medium", "id": "effort", "values": ["low", "medium", "high", "xhigh", "max"]},
      {"id": "fastMode", "values": [false, true]}
     ]},
    {"id": "claude-haiku-5-5"}
   ]
  },
  {
   "canRunChildTask": false, "canRunCrossProviderChildTask": false,
   "constraints": ["Provider instance is disabled."],
   "displayName": null, "driverKind": "grok", "providerInstanceId": "grok",
   "models": [{"id": "grok-build", "label": "Grok Build"}],
   "runtimeModes": ["approval-required", "auto", "full-access"]
  }
 ]
}`

func structuredResult(t *testing.T, raw string) *mcp.CallToolResult {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return &mcp.CallToolResult{StructuredContent: v, Content: []mcp.Content{&mcp.TextContent{Text: "ignored when structured"}}}
}

func TestDecodeThreadList(t *testing.T) {
	var w wireThreadList
	if err := decodeToolResult("t3_thread_list", structuredResult(t, threadListJSON), &w); err != nil {
		t.Fatal(err)
	}
	page := w.toPage()
	if page.Total != 781 || page.NextCursor == nil || *page.NextCursor != 3 || len(page.Threads) != 2 {
		t.Fatalf("page = %+v", page)
	}
	sub := page.Threads[0]
	if sub.Status != StatusIdle || sub.LatestRunID != "" || sub.SettledAt != nil || sub.RelationshipToParent != "subagent" ||
		sub.ParentThreadID != "parent-1" || sub.CreatedBy != "agent" || sub.CreationSource != "provider" || sub.ItemCount != 66 {
		t.Fatalf("sub = %+v", sub)
	}
	if want := time.Date(2026, 11, 8, 0, 55, 49, 692e6, time.UTC); !sub.CreatedAt.Equal(want) {
		t.Fatalf("createdAt = %v", sub.CreatedAt)
	}
	root := page.Threads[1]
	if root.Link != "" || !root.Settled || root.SettledAt == nil || root.ParentThreadID != "" || root.RelationshipToParent != "" ||
		root.InteractionMode != InteractionPlan || root.RuntimeMode != RuntimeApprovalRequired || !root.Snoozed || root.Status != StatusCompleted {
		t.Fatalf("root = %+v", root)
	}
}

func TestDecodeThreadRead(t *testing.T) {
	var w wireThreadRead
	if err := decodeToolResult("t3_thread_read", structuredResult(t, threadReadJSON), &w); err != nil {
		t.Fatal(err)
	}
	d := w.toDetail()
	if d.ThreadID != "t1" || d.Status != StatusRunning || d.PendingRequestCount != 2 || d.RunCount != 2 || d.Branch != "main" ||
		d.WorktreePath != "" || d.ActiveRunID != "run:thread:t1:ordinal:2" || d.Archived {
		t.Fatalf("detail = %+v", d)
	}
	if len(d.RecentRuns) != 1 {
		t.Fatalf("runs = %+v", d.RecentRuns)
	}
	r := d.RecentRuns[0]
	if r.Ordinal != 2 || r.Status != StatusRunning || r.StartedAt == nil || r.CompletedAt != nil || r.RequestedAt.IsZero() {
		t.Fatalf("run = %+v", r)
	}
	if env := environmentFromLink(d.Link); env != "38506b05-b4b9-4dc1-ad0c-ae95a74cd13e" {
		t.Fatalf("env = %q", env)
	}
}

const threadSearchJSON = `{"matches":[
 {"threadId":"t1","projectId":"proj-1","title":"Ctrl K palette","link":"[Ctrl K palette](t3-thread://v2/env-1/t1)","source":"title"},
 {"threadId":"t2","projectId":"proj-1","title":"what more?","link":"[what more?](t3-thread://v2/env-1/t2)","source":"assistant","snippet":"…from the Ctrl K palette. Where I'd push back…","messageCreatedAt":"2026-11-14T08:50:07.904Z"},
 {"threadId":"","projectId":"proj-1","title":"dropped","source":"user"}
]}`

func TestDecodeThreadSearch(t *testing.T) {
	var w wireThreadSearch
	if err := decodeToolResult("t3_thread_search", structuredResult(t, threadSearchJSON), &w); err != nil {
		t.Fatal(err)
	}
	ms := w.toMatches()
	if len(ms) != 2 {
		t.Fatalf("matches = %+v", ms)
	}
	if m := ms[0]; m.ThreadID != "t1" || m.Source != SearchTitle || m.Snippet != "" || m.MessageCreatedAt != nil {
		t.Fatalf("title match = %+v", m)
	}
	m := ms[1]
	if m.Source != SearchAssistant || m.Snippet != "…from the Ctrl K palette. Where I'd push back…" || m.Title != "what more?" || m.ProjectID != "proj-1" {
		t.Fatalf("message match = %+v", m)
	}
	if want := time.Date(2026, 11, 14, 8, 50, 7, 904e6, time.UTC); m.MessageCreatedAt == nil || !m.MessageCreatedAt.Equal(want) {
		t.Fatalf("messageCreatedAt = %v", m.MessageCreatedAt)
	}
}

func TestDecodeFallsBackToTextContent(t *testing.T) {
	res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"projects":[{"id":"p","title":"T","workspaceRoot":"/w","createdAt":"bogus","deletedAt":null}],"nextCursor":null}`}}}
	var w wireProjectList
	if err := decodeToolResult("t3_project_list", res, &w); err != nil {
		t.Fatal(err)
	}
	if len(w.Projects) != 1 || w.NextCursor != nil {
		t.Fatalf("w = %+v", w)
	}
	p := w.Projects[0].toProject()
	if p.ID != "p" || !p.CreatedAt.IsZero() || p.DeletedAt != nil || p.ArchivedAt != nil {
		t.Fatalf("project = %+v", p)
	}
}

func TestDecodeCapabilities(t *testing.T) {
	var w wireCapabilities
	if err := decodeToolResult("orchestrator_capabilities", structuredResult(t, capabilitiesJSON), &w); err != nil {
		t.Fatal(err)
	}
	cat := w.toCatalog()
	if len(cat.Providers) != 2 {
		t.Fatalf("providers = %+v", cat.Providers)
	}
	claude := cat.Providers[0]
	if claude.RuntimeModes != nil || len(claude.Constraints) != 0 || !claude.CanRunChildTask || len(claude.Models) != 2 {
		t.Fatalf("claude = %+v", claude)
	}
	opus := claude.Models[0]
	if opus.Label != "Claude Opus 5.5" || len(opus.Options) != 2 {
		t.Fatalf("opus = %+v", opus)
	}
	effort, fast := opus.Options[0], opus.Options[1]
	if effort.ID != "effort" || len(effort.Values) != 5 || effort.Default == nil || effort.Default.Str != "medium" {
		t.Fatalf("effort = %+v", effort)
	}
	if fast.Default != nil || len(fast.Values) != 2 || !fast.Values[1].IsBool || !fast.Values[1].Bool {
		t.Fatalf("fastMode = %+v", fast)
	}
	if haiku := claude.Models[1]; haiku.Label != "" || haiku.Options != nil {
		t.Fatalf("haiku = %+v", haiku)
	}
	grok := cat.Providers[1]
	if grok.DisplayName != "" || len(grok.Constraints) != 1 || len(grok.RuntimeModes) != 3 || grok.RuntimeModes[2] != RuntimeFullAccess {
		t.Fatalf("grok = %+v", grok)
	}
}

func TestDecodeLaunchModelSelectionShapes(t *testing.T) {
	for name, raw := range map[string]string{
		"array":  `{"threadId":"t","projectId":"p","modelSelection":{"instanceId":"claude","model":"m","options":[{"id":"effort","value":"low"},{"id":"fastMode","value":true}]},"runId":"r","status":"starting"}`,
		"record": `{"threadId":"t","projectId":"p","modelSelection":{"provider":"claude","model":"m","options":{"effort":"low","fastMode":true}},"runId":"r","status":"starting"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var w wireLaunch
			if err := decodeToolResult("t3_thread_launch", structuredResult(t, raw), &w); err != nil {
				t.Fatal(err)
			}
			r := w.toResult()
			if r.ModelSelection.InstanceID != "claude" || r.Status != StatusStarting || r.RunID != "r" || len(r.ModelSelection.Options) != 2 {
				t.Fatalf("result = %+v", r)
			}
			byID := map[string]OptionValue{}
			for _, o := range r.ModelSelection.Options {
				byID[o.ID] = o.Value
			}
			if byID["effort"].Str != "low" || !byID["fastMode"].IsBool || !byID["fastMode"].Bool {
				t.Fatalf("options = %+v", r.ModelSelection.Options)
			}
		})
	}
}

func TestToolErrorDecoding(t *testing.T) {
	cases := []struct {
		name     string
		res      *mcp.CallToolResult
		code     string
		notFound bool
	}{
		{"failure json", &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: `{"_tag":"OrchestratorMcpFailure","code":"thread_not_found","message":"gone"}`}}}, "thread_not_found", true},
		{"plain text", &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Preview snapshot failed"}}}, "", false},
		{"structured error", &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{"error": map[string]any{"code": "invalid_request", "message": "bad"}}}, "invalid_request", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out map[string]any
			err := decodeToolResult("x", tc.res, &out)
			var te *ToolError
			if !errors.As(err, &te) || te.Code != tc.code || errors.Is(err, ErrNotFound) != tc.notFound || te.Message == "" {
				t.Fatalf("err = %#v", err)
			}
		})
	}
}

func TestEnvironmentFromLink(t *testing.T) {
	cases := map[string]string{
		"t3-thread://v2/env-a/thread-1":                     "env-a",
		"<t3-thread://v2/env-a/thread-1>":                   "env-a",
		"t3-thread://v1/thread-1":                           "",
		"https://app.t3.codes/threads/env%2Eb/t%253Ax":      "env.b",
		"https://app.t3.codes/threads/env-c/thread%3Aa%3Ab": "env-c",
		"https://app.t3.codes/other/env-c/thread":           "",
		"":           "",
		"not a link": "",
	}
	for link, want := range cases {
		if got := environmentFromLink(link); got != want {
			t.Errorf("environmentFromLink(%q) = %q, want %q", link, got, want)
		}
	}
}

func TestWebThreadURL(t *testing.T) {
	if got := WebThreadURL("http://controlbox:3773/", "env", "t:1"); got != "http://controlbox:3773/env/t:1" {
		t.Fatalf("got %q", got)
	}
	if got := WebThreadURL("http://controlbox:3773", "", "t"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestAppThreadURL(t *testing.T) {
	if got := AppThreadURL("env", "codex:t%1"); got != "t3code://threads/env/codex:t%251" {
		t.Fatalf("got %q", got)
	}
	if got := AppThreadURL("", "t"); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := AppThreadURL("env", ""); got != "" {
		t.Fatalf("got %q", got)
	}
}
