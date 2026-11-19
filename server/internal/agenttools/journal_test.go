package agenttools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
)

func TestJournalMCPInputs(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The legacy T3 caller thread belongs to the active T3 connection.
		if r.URL.Path == "/api/connections" {
			_ = json.NewEncoder(w).Encode(map[string]any{"connections": []db.HarnessConnection{{ID: 1, Harness: db.HarnessT3, Active: true}}})
			return
		}
		requests++
		switch r.Method {
		case "GET":
			if r.URL.Path == "/api/journal/9" {
				_ = json.NewEncoder(w).Encode(db.JournalEntry{ID: 9, Text: "full"})
				return
			}
			if r.URL.Path != "/api/trees/2/journal" || r.URL.Query().Get("taskId") != "3" || r.URL.Query().Get("kind") != "all" {
				t.Errorf("read route %s", r.URL)
			}
			_ = json.NewEncoder(w).Encode(db.JournalPage{Entries: []db.JournalEntry{}})
		case "POST":
			var in db.AppendNoteInput
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if r.URL.Path != "/api/trees/2/journal" || in.RequestID != "key" || in.Text != "decision" ||
				in.SourceSession == nil || *in.SourceSession != (db.SessionRef{ConnectionID: 1, ExternalSessionID: "override"}) || in.SourceTaskID == nil || *in.SourceTaskID != 3 {
				t.Errorf("append %s %+v", r.URL, in)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(db.JournalEntry{ID: 9, Text: in.Text, SourceSession: in.SourceSession})
		}
	}))
	defer srv.Close()
	cs := connect(t, &agenttools.Ops{Client: client.New(srv.URL), Caller: agentctx.Caller{ThreadID: "environment"}})
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"create_task", map[string]any{"title": "T", "prompt": "P", "notes": "legacy"}},
		{"adopt_session", map[string]any{"notes": "legacy"}},
		{"update_task", map[string]any{"id": 1, "notes": "legacy"}},
		{"read_journal", map[string]any{}},
		{"read_journal", map[string]any{"taskId": 1, "treeId": 2}},
		{"read_journal", map[string]any{"taskId": 0}},
		{"read_journal", map[string]any{"taskId": 1, "filterTaskId": 2}},
		{"read_journal", map[string]any{"treeId": 2, "kind": "wrong"}},
		{"read_journal", map[string]any{"treeId": 2, "limit": 101}},
		{"append_note", map[string]any{"treeId": 2, "text": "note"}},
		{"append_note", map[string]any{"treeId": 2, "text": "note", "requestId": ""}},
		{"append_note", map[string]any{"taskId": 1, "treeId": 2, "text": "note", "requestId": "key"}},
		{"append_note", map[string]any{"treeId": 2, "text": strings.Repeat("x", 16*1024+1), "requestId": "key"}},
		{"append_note", map[string]any{"treeId": 2, "text": " ", "requestId": "key"}},
	} {
		if msg := call(t, cs, tc.tool, tc.args, nil); msg == "" {
			t.Fatalf("%s accepted invalid input", tc.tool)
		}
	}
	if requests != 0 {
		t.Fatalf("invalid inputs made %d HTTP requests", requests)
	}
	mustCall(t, cs, "read_journal", map[string]any{"treeId": 2, "filterTaskId": 3, "kind": "all"}, nil)
	var entry db.JournalEntry
	mustCall(t, cs, "append_note", map[string]any{
		"treeId": 2, "text": "decision", "requestId": "key", "sourceTaskId": 3, "callerThreadId": "override",
	}, &entry)
	if entry.ID != 9 || entry.SourceSession == nil || entry.SourceSession.ExternalSessionID != "override" {
		t.Fatalf("entry %+v", entry)
	}
	mustCall(t, cs, "get_journal_entry", map[string]any{"id": 9}, &entry)
	if entry.Text != "full" || requests != 3 {
		t.Fatalf("full entry %+v, requests %d", entry, requests)
	}
}

func TestTaskNotesSchemas(t *testing.T) {
	cs := connect(t, &agenttools.Ops{Client: client.New("http://127.0.0.1:1")})
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		switch tool.Name {
		case "create_task", "adopt_session", "update_task", "create_schedule", "update_schedule":
		default:
			continue
		}
		data, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		_, notes := schema.Properties["notes"]
		_, initial := schema.Properties["initialNote"]
		schedule := strings.HasSuffix(tool.Name, "_schedule")
		if notes != schedule || initial != (tool.Name == "create_task" || tool.Name == "adopt_session") {
			t.Fatalf("%s schema %s", tool.Name, data)
		}
	}
}

func TestJournalLifecycleMCP(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/journal")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{Cwd: p.Bindings[0].Workspace}})
	var task client.Task
	mustCall(t, cs, "create_task", map[string]any{"project": "1", "title": "Journal", "prompt": "work", "initialNote": "initial"}, &task)
	var page db.JournalPage
	mustCall(t, cs, "read_journal", map[string]any{"taskId": task.ID}, &page)
	if len(page.Entries) != 1 || page.Entries[0].Text != "initial" {
		t.Fatalf("initial note %+v", page)
	}
	args := map[string]any{"taskId": task.ID, "text": strings.Repeat("界", 700), "requestId": "retry", "category": "finding"}
	var first, retry db.JournalEntry
	mustCall(t, cs, "append_note", args, &first)
	mustCall(t, cs, "append_note", args, &retry)
	if first.ID != retry.ID {
		t.Fatalf("retry duplicated: %d != %d", first.ID, retry.ID)
	}
	mustCall(t, cs, "read_journal", map[string]any{"taskId": task.ID, "category": "finding"}, &page)
	if len(page.Entries) != 1 || !page.Entries[0].Truncated || len([]rune(page.Entries[0].Text)) != 600 {
		t.Fatalf("preview %+v", page)
	}
	mustCall(t, cs, "get_journal_entry", map[string]any{"id": first.ID}, &retry)
	if retry.Truncated || retry.Text != first.Text {
		t.Fatalf("full entry %+v", retry)
	}
	args["text"] = "changed"
	if msg := call(t, cs, "append_note", args, nil); !strings.Contains(msg, "409") {
		t.Fatalf("changed retry: %q", msg)
	}
}

func TestContextTracksCurrentTreeAndNoteSource(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/context-journal")
	threadID := e.session(p, "Contributor")
	cs := connect(t, &agenttools.Ops{Client: e.client, Caller: agentctx.Caller{ThreadID: threadID}})
	var adopted agenttools.AdoptOut
	mustCall(t, cs, "adopt_session", nil, &adopted)
	var first client.TreeDetail
	mustCall(t, cs, "create_tree", map[string]any{
		"title": "First", "startMode": "manual", "tasks": []map[string]any{{"id": adopted.Task.ID}},
	}, &first)
	var contextOut agenttools.ContextOut
	mustCall(t, cs, "get_context", nil, &contextOut)
	if contextOut.TreeID == nil || *contextOut.TreeID != first.ID || contextOut.TreeTitle == nil || *contextOut.TreeTitle != "First" {
		t.Fatalf("first tree context %+v", contextOut)
	}
	var entry db.JournalEntry
	mustCall(t, cs, "append_note", map[string]any{
		"treeId": first.ID, "sourceTaskId": adopted.Task.ID, "requestId": "tree-first", "text": "conclusion",
	}, &entry)
	if entry.TreeID == nil || *entry.TreeID != first.ID || entry.TaskID != nil ||
		entry.SourceThreadID == nil || *entry.SourceThreadID != threadID ||
		entry.SourceTaskID == nil || *entry.SourceTaskID != adopted.Task.ID {
		t.Fatalf("provenance %+v", entry)
	}
	var second client.TreeDetail
	mustCall(t, cs, "create_tree", map[string]any{
		"title": "Second", "startMode": "manual", "tasks": []map[string]any{{"id": adopted.Task.ID}},
	}, &second)
	mustCall(t, cs, "get_context", nil, &contextOut)
	if contextOut.TreeID == nil || *contextOut.TreeID != second.ID || contextOut.TreeTitle == nil || *contextOut.TreeTitle != "Second" {
		t.Fatalf("moved tree context %+v", contextOut)
	}
	if msg := call(t, cs, "append_note", map[string]any{
		"treeId": first.ID, "sourceTaskId": adopted.Task.ID, "requestId": "stale-membership", "text": "stale",
	}, nil); !strings.Contains(msg, "409") {
		t.Fatalf("stale tree source: %q", msg)
	}
	mustCall(t, cs, "remove_from_tree", map[string]any{"taskId": adopted.Task.ID}, nil)
	mustCall(t, cs, "get_context", nil, &contextOut)
	if contextOut.CallerTaskID == nil || *contextOut.CallerTaskID != adopted.Task.ID || contextOut.TreeID != nil || contextOut.TreeTitle != nil {
		t.Fatalf("detached context %+v", contextOut)
	}
}
