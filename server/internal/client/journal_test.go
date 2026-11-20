package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/db"
)

func TestJournalTransport(t *testing.T) {
	ctx := context.Background()
	taskID, treeID := int64(12), int64(34)
	threadID := "thread-1"
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set(buildinfo.APIVersionHeader, strconv.Itoa(buildinfo.APIVersion))
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			if r.Method != "GET" || r.URL.Path != "/api/tasks/12/journal" || r.URL.RawQuery != "" {
				t.Errorf("task read: %s %s", r.Method, r.URL)
			}
			fmt.Fprint(w, `{"entries":[],"nextCursor":null}`)
		case 2:
			q := r.URL.Query()
			if r.Method != "GET" || r.URL.Path != "/api/trees/34/journal" ||
				q.Get("kind") != "all" || q.Get("category") != "decision & result" || q.Get("q") != "a + b?" ||
				q.Get("taskId") != "12" || q.Get("cursor") != "opaque+/" || q.Get("limit") != "7" {
				t.Errorf("tree query: %s %s", r.Method, r.URL)
			}
			fmt.Fprint(w, `{"entries":[{"id":5,"kind":"note","taskId":12,"treeId":null,"text":"preview","truncated":true}],"nextCursor":"next"}`)
		case 3:
			if r.Method != "POST" || r.URL.Path != "/api/trees/34/journal" {
				t.Errorf("append: %s %s", r.Method, r.URL)
			}
			var in db.AppendNoteInput
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.Text != "full note" || in.Category != "decision" || in.RequestID != "retry-1" ||
				in.SourceTaskID == nil || *in.SourceTaskID != taskID || in.SourceSession == nil || *in.SourceSession != (db.SessionRef{ConnectionID: 3, ExternalSessionID: threadID}) {
				t.Errorf("body: %+v", in)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":6,"kind":"note","treeId":34,"taskId":null,"text":"full note","truncated":false}`)
		case 4:
			if r.Method != "GET" || r.URL.Path != "/api/journal/6" {
				t.Errorf("get: %s %s", r.Method, r.URL)
			}
			fmt.Fprint(w, `{"id":6,"kind":"note","treeId":34,"taskId":null,"text":"full note","truncated":false}`)
		default:
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"error":"stale cursor"}`)
		}
	}))
	defer srv.Close()
	c := New(srv.URL)
	if page, err := c.ReadJournal(ctx, db.JournalOwner{TaskID: &taskID}, db.JournalQuery{}); err != nil || page.Entries == nil || page.NextCursor != nil {
		t.Fatalf("empty page %+v: %v", page, err)
	}
	page, err := c.ReadJournal(ctx, db.JournalOwner{TreeID: &treeID}, db.JournalQuery{
		Kind: "all", Category: "decision & result", Q: "a + b?", TaskID: &taskID, Cursor: "opaque+/", Limit: 7,
	})
	if err != nil || len(page.Entries) != 1 || !page.Entries[0].Truncated || page.NextCursor == nil || *page.NextCursor != "next" {
		t.Fatalf("page %+v: %v", page, err)
	}
	entry, err := c.AppendNote(ctx, db.JournalOwner{TreeID: &treeID}, db.AppendNoteInput{
		Text: "full note", Category: "decision", RequestID: "retry-1", SourceTaskID: &taskID, SourceSession: &db.SessionRef{ConnectionID: 3, ExternalSessionID: threadID},
	})
	if err != nil || entry.ID != 6 || entry.TaskID != nil || entry.TreeID == nil || entry.Truncated {
		t.Fatalf("appended %+v: %v", entry, err)
	}
	entry, err = c.GetJournalEntry(ctx, 6)
	if err != nil || entry.Text != "full note" || entry.Truncated {
		t.Fatalf("full entry %+v: %v", entry, err)
	}
	if _, err := c.ReadJournal(ctx, db.JournalOwner{TreeID: &treeID}, db.JournalQuery{Cursor: "stale"}); !IsStatus(err, http.StatusConflict) {
		t.Fatalf("conflict: %v", err)
	}
	for _, owner := range []db.JournalOwner{{}, {TaskID: &taskID, TreeID: &treeID}} {
		if _, err := c.ReadJournal(ctx, owner, db.JournalQuery{}); err == nil {
			t.Fatal("invalid owner accepted")
		}
		if _, err := c.AppendNote(ctx, owner, db.AppendNoteInput{}); err == nil {
			t.Fatal("invalid append owner accepted")
		}
	}
	if _, err := c.ReadJournal(ctx, db.JournalOwner{TaskID: &taskID}, db.JournalQuery{TaskID: &taskID}); err == nil {
		t.Fatal("task filter on task journal accepted")
	}
	if _, err := c.GetJournalEntry(ctx, 0); err == nil {
		t.Fatal("invalid entry id accepted")
	}
	if requests != 5 {
		t.Fatalf("invalid inputs sent requests: %d", requests)
	}
}

func TestInitialNoteBodies(t *testing.T) {
	note := "initial context"
	for _, in := range []any{
		CreateTask{InitialNote: &note},
		AdoptSession{InitialNote: &note},
	} {
		body, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		if fields["initialNote"] != note {
			t.Fatalf("missing initialNote: %s", body)
		}
		if _, exists := fields["notes"]; exists {
			t.Fatalf("legacy notes: %s", body)
		}
	}
}
