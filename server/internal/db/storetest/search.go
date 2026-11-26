package storetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
)

// Search contract (docs/storage.md "Search"): TaskFilter.Q and
// JournalQuery.Q match case-insensitive substrings (non-ASCII included),
// every character literal, short queries too; they filter without changing
// the order or paging. Relevance is not part of the contract.

func init() {
	register("SearchTasks", caseSearchTasks)
	register("SearchTasksFollowsWrites", caseSearchTasksFollowsWrites)
	register("SearchJournal", caseSearchJournal)
	register("SearchJournalPaging", caseSearchJournalPaging)
}

// searchCorpus is text the search cases match against; each query below
// names the entries it must find (by index).
var searchCorpus = []string{
	"Needle in the haystack", // 0
	"100% done",              // 1
	"100 percent done",       // 2
	"snake a_b case",         // 3
	"snake axb case",         // 4
	`path C:\dir\file`,       // 5
	`say "hi" now`,           // 6
	"привет мир",             // 7
	"été chaud",              // 8
	"plain words",            // 9
}

// searchQueries: each query and the corpus entries (by index) it finds.
// Fillers around the corpus ("task N", "prompt") match none of them.
var searchQueries = []struct {
	q    string
	want []int
}{
	{"eedl", []int{0}},                // inside a word
	{"NEEDLE IN", []int{0}},           // ASCII case, spanning a space
	{"ПРИВЕТ", []int{7}},              // non-ASCII case
	{"ÉTÉ", []int{8}},                 // non-ASCII case, accented
	{"É", []int{8}},                   // short non-ASCII case
	{"0% d", []int{1}},                // % literal
	{"%", []int{1}},                   // short % literal
	{"a_b", []int{3}},                 // _ literal
	{"_", []int{3}},                   // short _ literal
	{`C:\d`, []int{5}},                // \ literal
	{`\`, []int{5}},                   // short \ literal
	{`"hi"`, []int{6}},                // quotes literal
	{`"`, []int{6}},                   // short quote
	{"done", []int{1, 2}},             // several hits
	{"ne", []int{0, 1, 2}},            // two characters
	{"n", []int{0, 1, 2, 3, 4, 6, 9}}, // one character
	{"ir", []int{5}},                  // Latin "ir", not Cyrillic "ир"
	{"PLAIN", []int{9}},               // whole word
	{"words plain", []int{}},          // order matters
	{"absent text", []int{}},          // no match
}

func caseSearchTasks(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	other := mustProject(t, s, ctx, "other")
	ids := make([]int64, len(searchCorpus))
	for i, text := range searchCorpus {
		// Even entries in the title, odd ones in the prompt.
		title, prompt := text, "prompt"
		if i%2 == 1 {
			title, prompt = fmt.Sprintf("task %d", i), text
		}
		ids[i] = searchTask(t, s, ctx, p.ID, title, prompt).ID
	}
	decoy := searchTask(t, s, ctx, other.ID, "Needle elsewhere", "prompt")
	for _, c := range searchQueries {
		want := c.want
		got, err := s.ListTasks(ctx, db.TaskFilter{ProjectID: &p.ID, Q: c.q})
		if err != nil {
			t.Fatalf("%q: %v", c.q, err)
		}
		// Ordering stays updated_at DESC, id DESC: the newest task first.
		wantIDs := []int64{}
		for i := len(want) - 1; i >= 0; i-- {
			wantIDs = append(wantIDs, ids[want[i]])
		}
		if gotIDs := taskIDs(got); !slices.Equal(gotIDs, wantIDs) {
			t.Fatalf("%q: got %v, want %v", c.q, gotIDs, wantIDs)
		}
	}
	// Without the project filter the other project's match shows too.
	all, err := s.ListTasks(ctx, db.TaskFilter{Q: "needle"})
	if err != nil || !slices.Equal(taskIDs(all), []int64{decoy.ID, ids[0]}) {
		t.Fatalf("unfiltered: %v %v", taskIDs(all), err)
	}
	// Other filters still apply: archived tasks are closed.
	if _, err := s.ArchiveTask(ctx, ids[0], true); err != nil {
		t.Fatal(err)
	}
	open1, err := s.ListTasks(ctx, db.TaskFilter{Q: "needle"})
	if err != nil || !slices.Equal(taskIDs(open1), []int64{decoy.ID}) {
		t.Fatalf("open only: %v %v", taskIDs(open1), err)
	}
	closed, err := s.ListTasks(ctx, db.TaskFilter{Q: "needle", Closed: "true"})
	if err != nil || !slices.Equal(taskIDs(closed), []int64{ids[0]}) {
		t.Fatalf("closed: %v %v", taskIDs(closed), err)
	}
}

// caseSearchTasksFollowsWrites: edits and deletes change what matches.
func caseSearchTasksFollowsWrites(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := searchTask(t, s, ctx, p.ID, "original title", "original prompt")
	b := searchTask(t, s, ctx, p.ID, "gone soon", "prompt")
	if _, err := s.UpdateTask(ctx, a.ID, db.TaskPatch{
		Title:  db.Optional[string]{Set: true, V: "renamed title"},
		Prompt: db.Optional[string]{Set: true, V: "rewritten prompt"},
	}); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string][]int64{
		"original": {}, "renamed": {a.ID}, "rewritten": {a.ID}, "title": {a.ID}, "gone": {b.ID},
	} {
		got, err := s.ListTasks(ctx, db.TaskFilter{Q: q})
		if err != nil || !slices.Equal(taskIDs(got), want) {
			t.Fatalf("after edit %q: %v %v, want %v", q, taskIDs(got), err, want)
		}
	}
	if err := s.DeleteTask(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	c := searchTask(t, s, ctx, p.ID, "gone again", "prompt")
	got, err := s.ListTasks(ctx, db.TaskFilter{Q: "gone"})
	if err != nil || !slices.Equal(taskIDs(got), []int64{c.ID}) {
		t.Fatalf("after delete: %v %v", taskIDs(got), err)
	}
}

func caseSearchJournal(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	task := mustTask(t, s, ctx, p.ID, "t", nil)
	other := mustTask(t, s, ctx, p.ID, "other", nil)
	owner := db.JournalOwner{TaskID: &task.ID}
	ids := make([]int64, len(searchCorpus))
	for i, text := range searchCorpus {
		ids[i] = note(t, s, ctx, owner, text, fmt.Sprintf("search-%d", i)).ID
	}
	note(t, s, ctx, db.JournalOwner{TaskID: &other.ID}, "Needle elsewhere", "decoy")
	for _, c := range searchQueries {
		want := c.want
		page := journal(t, s, ctx, owner, db.JournalQuery{Q: c.q, Limit: 100})
		// Newest entry first.
		wantIDs := []int64{}
		for i := len(want) - 1; i >= 0; i-- {
			wantIDs = append(wantIDs, ids[want[i]])
		}
		if gotIDs := entryIDs(page); !slices.Equal(gotIDs, wantIDs) || page.Entries == nil {
			t.Fatalf("%q: got %v, want %v", c.q, gotIDs, wantIDs)
		}
	}
	// Category and kind filters still apply.
	cat, err := s.AppendNote(ctx, owner, db.AppendNoteInput{Text: "needle finding", RequestID: "cat", Category: "finding"})
	if err != nil {
		t.Fatal(err)
	}
	if page := journal(t, s, ctx, owner, db.JournalQuery{Q: "needle", Category: "finding"}); !slices.Equal(entryIDs(page), []int64{cat.ID}) {
		t.Fatalf("category: %v", entryIDs(page))
	}
	if page := journal(t, s, ctx, owner, db.JournalQuery{Q: "needle", Kind: "events"}); len(page.Entries) != 0 {
		t.Fatalf("events: %v", entryIDs(page))
	}
}

// caseSearchJournalPaging: a search pages by cursor like an unfiltered read,
// matches spread across non-matching entries.
func caseSearchJournalPaging(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	task := mustTask(t, s, ctx, p.ID, "t", nil)
	owner := db.JournalOwner{TaskID: &task.ID}
	var matches []int64
	for i := range 12 {
		text := fmt.Sprintf("filler %d", i)
		if i%2 == 0 {
			text = fmt.Sprintf("Mätch %d", i)
		}
		e := note(t, s, ctx, owner, text, fmt.Sprintf("page-%d", i))
		if i%2 == 0 {
			matches = append([]int64{e.ID}, matches...)
		}
	}
	for _, q := range []string{"MÄTCH", "ä"} {
		var got []int64
		cursor := ""
		for pages := 0; ; pages++ {
			if pages > 6 {
				t.Fatalf("%q: too many pages", q)
			}
			page := journal(t, s, ctx, owner, db.JournalQuery{Q: q, Limit: 4, Cursor: cursor})
			got = append(got, entryIDs(page)...)
			if page.NextCursor == nil {
				break
			}
			cursor = *page.NextCursor
		}
		if !slices.Equal(got, matches) {
			t.Fatalf("%q: paged %v, want %v", q, got, matches)
		}
	}
	// The cursor is bound to the query.
	first := journal(t, s, ctx, owner, db.JournalQuery{Q: "mätch", Limit: 4})
	var invalid *db.InvalidError
	if _, err := s.ReadJournal(ctx, owner, db.JournalQuery{Q: "filler", Limit: 4, Cursor: *first.NextCursor}); !errors.As(err, &invalid) {
		t.Fatalf("cursor for another query: %v", err)
	}
}

func searchTask(t *testing.T, s *db.Store, ctx context.Context, projectID int64, title, prompt string) db.Task {
	t.Helper()
	task, err := s.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection,
		ProjectID: projectID, Title: title, Prompt: prompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func taskIDs(tasks []db.Task) []int64 {
	out := []int64{}
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

func entryIDs(page db.JournalPage) []int64 {
	out := []int64{}
	for _, e := range page.Entries {
		out = append(out, e.ID)
	}
	return out
}
