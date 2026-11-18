package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
)

func TestJournalCommands(t *testing.T) {
	var requests []string
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST":
			var in db.AppendNoteInput
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.RequestID == "" || in.Text != "high signal\ncontext" || in.Category != "finding" ||
				in.SourceThreadID == nil || *in.SourceThreadID != "caller" {
				t.Errorf("note body %+v", in)
			}
			if r.URL.Path == "/api/trees/2/journal" && (in.SourceTaskID == nil || *in.SourceTaskID != 1) {
				t.Errorf("tree source %+v", in)
			}
			keys = append(keys, in.RequestID)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(db.JournalEntry{ID: 8, Kind: "note", Text: in.Text})
		case r.URL.Path == "/api/journal/8":
			_ = json.NewEncoder(w).Encode(db.JournalEntry{ID: 8, Kind: "note", Text: "complete text"})
		default:
			cursor := "next"
			_ = json.NewEncoder(w).Encode(db.JournalPage{
				Entries:    []db.JournalEntry{{ID: 8, Kind: "note", Text: "preview", Truncated: true}},
				NextCursor: &cursor,
			})
		}
	}))
	defer srv.Close()
	h := &harness{t: t, url: srv.URL, caller: agentctx.Caller{ThreadID: "caller"}}
	for _, args := range [][]string{
		{"task", "notes", "1", "--json"},
		{"tree", "notes", "2", "list", "--kind", "all", "--category", "finding", "--q", "high signal", "--task", "1", "--limit", "3", "--cursor", "old", "--json"},
		{"task", "notes", "1", "add", "--text", "-", "--category", "finding", "--json"},
		{"task", "notes", "1", "add", "--text", "-", "--category", "finding", "--json"},
		{"tree", "notes", "2", "add", "--text", "-", "--category", "finding", "--source-task", "1", "--request-id", "explicit", "--json"},
		{"task", "notes", "1", "get", "8", "--json"},
		{"tree", "notes", "2", "get", "8", "--json"},
	} {
		code, out, stderr := h.run("high signal\ncontext", args...)
		if code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, out, stderr)
		}
	}
	want := []string{
		"GET /api/tasks/1/journal?limit=20",
		"GET /api/trees/2/journal?category=finding&cursor=old&kind=all&limit=3&q=high+signal&taskId=1",
		"POST /api/tasks/1/journal", "POST /api/tasks/1/journal", "POST /api/trees/2/journal",
		"GET /api/journal/8", "GET /api/journal/8",
	}
	if fmt.Sprint(requests) != fmt.Sprint(want) {
		t.Fatalf("routes %v, want %v", requests, want)
	}
	if len(keys) != 3 || keys[0] == keys[1] || keys[2] != "explicit" {
		t.Fatalf("request keys %v", keys)
	}
	code, out, stderr := h.run("", "tree", "notes", "2")
	if code != 0 || !strings.Contains(out, "preview") || !strings.Contains(out, "next cursor: next") {
		t.Fatalf("text list: %d %s %s", code, out, stderr)
	}
	code, out, stderr = h.run("", "task", "notes", "1", "get", "8")
	if code != 0 || !strings.Contains(out, "complete text") {
		t.Fatalf("text get: %d %s %s", code, out, stderr)
	}
	before := len(requests)
	for _, args := range [][]string{
		{"task", "notes"},
		{"task", "notes", "0"},
		{"task", "notes", "1", "get"},
		{"task", "notes", "1", "add"},
		{"task", "notes", "1", "add", "--text", "a", "--request-id", ""},
		{"task", "notes", "1", "--task", "2"},
		{"tree", "notes", "2", "--kind", "invalid"},
		{"tree", "notes", "2", "--limit", "0"},
		{"tree", "notes", "2", "add", "--text", "a", "--q", "ignored"},
		{"task", "add", "--project", "1", "--title", "T", "--prompt", "P", "--notes", "legacy"},
		{"task", "edit", "1", "--notes", "legacy"},
		{"task", "edit", "1", "--initial-note", "legacy"},
		{"sessions", "adopt", "--notes", "legacy"},
	} {
		code, _, _ := h.run("", args...)
		if code == 0 {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
	if len(requests) != before {
		t.Fatalf("invalid arguments reached API: %v", requests[before:])
	}
}

func TestTaskInitialNoteAndScheduleTemplateCommands(t *testing.T) {
	h := newHarness(t)
	h.project("/w/cli-notes")
	h.caller = agentctx.Caller{Cwd: "/w/cli-notes"}
	code, out, stderr := h.run("", "task", "add", "--project", "1", "--title", "T", "--prompt", "P", "--initial-note", "initial", "--json")
	if code != 0 {
		t.Fatalf("create: %s", stderr)
	}
	task := decode[client.Task](t, out)
	code, out, stderr = h.run("", "task", "notes", fmt.Sprint(task.ID), "--json")
	if code != 0 {
		t.Fatalf("notes: %s", stderr)
	}
	page := decode[db.JournalPage](t, out)
	if len(page.Entries) != 1 || page.Entries[0].Text != "initial" {
		t.Fatalf("initial note %+v", page)
	}
	code, out, stderr = h.run("", "schedule", "add", "--project", "1", "--title", "S", "--prompt", "P", "--cron", "@daily", "--tz", "UTC", "--notes", "template", "--json")
	if code != 0 {
		t.Fatalf("schedule add: %s", stderr)
	}
	schedule := decode[db.Schedule](t, out)
	if schedule.Notes != "template" {
		t.Fatalf("template %+v", schedule)
	}
	for _, note := range []string{"edited", ""} {
		code, out, stderr = h.run("", "schedule", "edit", fmt.Sprint(schedule.ID), "--notes", note, "--json")
		if code != 0 || decode[db.Schedule](t, out).Notes != note {
			t.Fatalf("schedule edit: %d %s %s", code, out, stderr)
		}
	}
}
