package api_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/db"
)

func (e *env) note(path, text, category, requestID string) db.JournalEntry {
	e.t.Helper()
	var out db.JournalEntry
	e.must("POST", path, map[string]any{"text": text, "category": category, "requestId": requestID}, 201, &out)
	return out
}

func (e *env) journal(path string) db.JournalPage {
	e.t.Helper()
	var out db.JournalPage
	e.must("GET", path, nil, 200, &out)
	return out
}

func TestJournalAppendRetryPreviewAndFullEntry(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/journal")
	a := e.task(p.ID, "a", nil)
	path := taskPath(a.ID, "/journal")
	text := strings.Repeat("界", 700)
	entry := e.note(path, text, "finding", "write-1")
	if entry.Kind != "note" || entry.TaskID == nil || *entry.TaskID != a.ID || entry.TreeID != nil || entry.Text != text || entry.Truncated {
		t.Fatalf("entry %+v", entry)
	}
	retry := e.note(path, text, "finding", "write-1")
	if retry.ID != entry.ID || !retry.CreatedAt.Equal(entry.CreatedAt) {
		t.Fatalf("retry changed identity: %+v / %+v", entry, retry)
	}
	e.must("POST", path, map[string]any{"text": "changed", "category": "finding", "requestId": "write-1"}, 409, nil)
	e.must("POST", path, map[string]any{"text": text, "category": "outcome", "requestId": "write-1"}, 409, nil)
	page := e.journal(path)
	if len(page.Entries) != 1 || page.NextCursor != nil || page.Entries[0].ID != entry.ID || !page.Entries[0].Truncated || utf8.RuneCountInString(page.Entries[0].Text) != 600 {
		t.Fatalf("preview %+v", page)
	}
	var full db.JournalEntry
	e.must("GET", fmt.Sprintf("/api/journal/%d", entry.ID), nil, 200, &full)
	if full.Text != text || full.Truncated || full.ID != entry.ID {
		t.Fatalf("full %+v", full)
	}
	var taskJSON map[string]any
	e.must("GET", taskPath(a.ID), nil, 200, &taskJSON)
	if _, exists := taskJSON["notes"]; exists || taskJSON["deletable"] != false {
		t.Fatalf("task shape %+v", taskJSON)
	}
	e.must("DELETE", taskPath(a.ID), nil, 409, nil)
}

func TestJournalFiltersPaginationAndErrors(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/filters")
	a := e.task(p.ID, "a", nil)
	b := e.task(p.ID, "b", map[string]any{"needs": []int64{a.ID}})
	path := treePath(*b.TreeID, "/journal")
	one := e.note(taskPath(a.ID, "/journal"), "First finding", "finding", "one")
	e.note(taskPath(b.ID, "/journal"), "Other finding", "finding", "two")
	e.note(path, "Tree decision", "decision", "three")
	if page := e.journal(path + "?category=finding&q=FIRST"); len(page.Entries) != 1 || page.Entries[0].ID != one.ID {
		t.Fatalf("combined filters %+v", page)
	}
	if page := e.journal(path + fmt.Sprintf("?taskId=%d", a.ID)); len(page.Entries) != 1 || page.Entries[0].ID != one.ID {
		t.Fatalf("task filter %+v", page)
	}
	seen := map[int64]bool{}
	next := path + "?limit=1"
	for i := 0; ; i++ {
		if i > 3 {
			t.Fatal("pagination did not end")
		}
		page := e.journal(next)
		if len(page.Entries) != 1 || seen[page.Entries[0].ID] {
			t.Fatalf("duplicate or missing page %+v", page)
		}
		seen[page.Entries[0].ID] = true
		if page.NextCursor == nil {
			break
		}
		next = path + "?limit=1&cursor=" + url.QueryEscape(*page.NextCursor)
	}
	if len(seen) != 3 {
		t.Fatalf("paged %d entries", len(seen))
	}
	if events := e.journal(path + "?kind=events"); len(events.Entries) == 0 {
		t.Fatal("missing tree events")
	} else {
		for _, entry := range events.Entries {
			if entry.Kind != "event" {
				t.Fatalf("event filter %+v", entry)
			}
		}
	}
	if all := e.journal(path + "?kind=all&limit=100"); len(all.Entries) <= 3 {
		t.Fatalf("all omitted events %+v", all)
	}
	for _, query := range []string{"?category=checkpoint", "?kind=nope", "?limit=0", "?limit=101", "?limit=x", "?taskId=-1", "?taskId=x", "?cursor=garbage"} {
		e.must("GET", path+query, nil, 400, nil)
	}
	e.must("GET", taskPath(a.ID, "/journal?taskId=1"), nil, 400, nil)
	for _, path := range []string{"/api/tasks/99999/journal", "/api/trees/99999/journal", "/api/journal/99999"} {
		e.must("GET", path, nil, 404, nil)
	}
	e.must("GET", "/api/journal/bad", nil, 400, nil)
	for _, body := range []map[string]any{
		{"text": "note", "requestId": "x", "category": "checkpoint"}, {"text": "note"}, {"text": " ", "requestId": "x"},
		{"text": strings.Repeat("x", 16385), "requestId": "x"},
		{"text": "note", "requestId": "x", "taskId": a.ID},
		{"text": "note", "requestId": "x", "sourceTaskId": -1},
	} {
		e.must("POST", path, body, 400, nil)
	}
	e.must("POST", "/api/tasks/99999/journal", map[string]any{"text": "note", "requestId": "missing"}, 404, nil)
}

func TestJournalMoveInvalidatesCursorAndPreservesOwnership(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/move-journal")
	a := e.task(p.ID, "a", nil)
	b := e.task(p.ID, "b", map[string]any{"needs": []int64{a.ID}})
	c := e.task(p.ID, "c", nil)
	d := e.task(p.ID, "d", map[string]any{"needs": []int64{c.ID}})
	oldTree, newTree := *b.TreeID, *d.TreeID
	oldPath, newPath := treePath(oldTree, "/journal"), treePath(newTree, "/journal")
	personal := e.note(taskPath(a.ID, "/journal"), "personal", "", "personal")
	var shared db.JournalEntry
	e.must("POST", oldPath, map[string]any{"text": "shared", "requestId": "shared", "sourceTaskId": a.ID}, 201, &shared)
	page := e.journal(oldPath + "?limit=1")
	if page.NextCursor == nil {
		t.Fatal("missing cursor")
	}
	e.must("POST", treePath(newTree, "/tasks"), map[string]any{"taskId": a.ID}, 200, nil)
	var retry db.JournalEntry
	e.must("POST", oldPath, map[string]any{"text": "shared", "requestId": "shared", "sourceTaskId": a.ID}, 201, &retry)
	if retry.ID != shared.ID || retry.SourceTaskID == nil || *retry.SourceTaskID != a.ID {
		t.Fatalf("moved provenance retry %+v", retry)
	}
	e.must("POST", oldPath, map[string]any{"text": "new stale contribution", "requestId": "stale", "sourceTaskId": a.ID}, 409, nil)
	e.must("GET", oldPath+"?limit=1&cursor="+url.QueryEscape(*page.NextCursor), nil, 409, nil)
	oldNotes, newNotes := e.journal(oldPath), e.journal(newPath)
	if len(oldNotes.Entries) != 1 || oldNotes.Entries[0].ID != shared.ID || len(newNotes.Entries) != 1 || newNotes.Entries[0].ID != personal.ID {
		t.Fatalf("ownership after move: old %+v new %+v", oldNotes, newNotes)
	}
	for _, path := range []string{oldPath, newPath} {
		found := false
		for _, entry := range e.journal(path + "?kind=events&limit=100").Entries {
			if entry.FromTreeID != nil && *entry.FromTreeID == oldTree && entry.ToTreeID != nil && *entry.ToTreeID == newTree {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing move provenance in %s", path)
		}
	}
	e.must("DELETE", treePath(oldTree, fmt.Sprintf("/tasks/%d", b.ID)), nil, 204, nil)
	var archived closeTree
	e.must("GET", treePath(oldTree), nil, 200, &archived)
	if archived.Status != "archived" || archived.Deletable {
		t.Fatalf("emptied recorded tree %+v", archived)
	}
	e.must("DELETE", treePath(oldTree), nil, 409, nil)
	e.note(oldPath, "late conclusion", "outcome", "late")
	e.must("GET", treePath(oldTree), nil, 200, &archived)
	if archived.Status != "archived" {
		t.Fatal("note reopened tree")
	}
}

func TestJournalDefaultPageAndAppendDuringPagination(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/journal-pages")
	a := e.task(p.ID, "a", nil)
	path := taskPath(a.ID, "/journal")
	var first db.JournalEntry
	for i := range 21 {
		entry := e.note(path, fmt.Sprintf("note %d", i), "", fmt.Sprint(i))
		if i == 0 {
			first = entry
		}
	}
	page := e.journal(path)
	if len(page.Entries) != 20 || page.NextCursor == nil {
		t.Fatalf("default page %+v", page)
	}
	e.note(path, "arrived after first page", "", "later")
	next := e.journal(path + "?cursor=" + url.QueryEscape(*page.NextCursor))
	if len(next.Entries) != 1 || next.Entries[0].ID != first.ID || next.NextCursor != nil {
		t.Fatalf("append disturbed pagination %+v", next)
	}
	e.must("GET", path+"?category=decision&cursor="+url.QueryEscape(*page.NextCursor), nil, 400, nil)
	if all := e.journal(path + "?limit=100"); len(all.Entries) != 22 || all.NextCursor != nil {
		t.Fatalf("larger page %+v", all)
	}
}

func TestInitialNoteAndRemovedTaskNotes(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/initial-note")
	a := e.task(p.ID, "a", map[string]any{"initialNote": "initial context"})
	page := e.journal(taskPath(a.ID, "/journal"))
	if len(page.Entries) != 1 || page.Entries[0].Text != "initial context" {
		t.Fatalf("initial note %+v", page)
	}
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "legacy", "prompt": "go", "notes": "removed"}, 400, nil)
	e.must("PATCH", taskPath(a.ID), map[string]any{"notes": "removed"}, 400, nil)
	e.must("PATCH", taskPath(a.ID), map[string]any{"initialNote": "not editable"}, 400, nil)
	var duplicate task
	e.must("POST", taskPath(a.ID, "/duplicate"), nil, 201, &duplicate)
	if page := e.journal(taskPath(duplicate.ID, "/journal")); len(page.Entries) != 0 {
		t.Fatalf("duplicate copied notes %+v", page)
	}
	thread := runningSession(t, e, p.T3ProjectID, "external")
	e.must("POST", "/api/threads/"+thread+"/adopt", map[string]any{"notes": "removed"}, 400, nil)
	var adopted task
	e.must("POST", "/api/threads/"+thread+"/adopt", map[string]any{"initialNote": "adopted context"}, 201, &adopted)
	if page := e.journal(taskPath(adopted.ID, "/journal")); len(page.Entries) != 1 || page.Entries[0].Text != "adopted context" {
		t.Fatalf("adopt note %+v", page)
	}
	if len(e.fake.Sent) != 0 {
		t.Fatal("adoption sent retroactive instructions")
	}
	e.must("POST", taskPath(a.ID, "/archive"), nil, 200, nil)
	e.note(taskPath(a.ID, "/journal"), "archived update", "", "archived")
	var closed closeTask
	e.must("GET", taskPath(a.ID), nil, 200, &closed)
	if closed.ArchivedAt == nil {
		t.Fatal("note unarchived task")
	}
}
