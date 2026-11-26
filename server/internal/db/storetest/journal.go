package storetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
)

func init() {
	register("JournalNotesQueriesAndRetry", caseJournalNotesQueriesAndRetry)
	register("JournalConcurrentRetryAndPagination", caseJournalConcurrentRetryAndPagination)
	register("JournalMovesFreezeEventsAndInvalidateTreeCursors", caseJournalMovesFreezeEventsAndInvalidateTreeCursors)
	register("JournalClosedWritesAndPermanentProtection", caseJournalClosedWritesAndPermanentProtection)
	register("JournalInitialNotesScheduleDuplicateAndRollback", caseJournalInitialNotesScheduleDuplicateAndRollback)
	register("JournalLaunchInstructionsFrozenPerAttempt", caseJournalLaunchInstructionsFrozenPerAttempt)
	register("JournalCreateIntoTreeInvalidatesCursor", caseJournalCreateIntoTreeInvalidatesCursor)
	register("JournalMoveAndAppendSerialize", caseJournalMoveAndAppendSerialize)
	register("JournalMoveRollbackAndExecutionProtection", caseJournalMoveRollbackAndExecutionProtection)
	register("JournalTreeMutationEvents", caseJournalTreeMutationEvents)
}

func journal(t *testing.T, s *db.Store, ctx context.Context, owner db.JournalOwner, q db.JournalQuery) db.JournalPage {
	t.Helper()
	page, err := s.ReadJournal(ctx, owner, q)
	if err != nil {
		t.Fatal(err)
	}
	return page
}
func note(t *testing.T, s *db.Store, ctx context.Context, owner db.JournalOwner, text, key string) db.JournalEntry {
	t.Helper()
	e, err := s.AppendNote(ctx, owner, db.AppendNoteInput{Text: text, RequestID: key})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func containsEntry(page db.JournalPage, id int64) bool {
	return slices.ContainsFunc(page.Entries, func(e db.JournalEntry) bool { return e.ID == id })
}

func caseJournalNotesQueriesAndRetry(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	owner := db.JournalOwner{TaskID: &a.ID}
	input := db.AppendNoteInput{Text: strings.Repeat("界", 700) + " needle%_", Category: "finding", RequestID: "request-1", SourceTaskID: &a.ID, SourceSession: &db.SessionRef{ConnectionID: s.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: "session"}}
	first, err := s.AppendNote(ctx, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.AppendNote(ctx, owner, input)
	if err != nil || again.ID != first.ID || !again.CreatedAt.Equal(first.CreatedAt) || again.TreeID != nil || again.Attempt != nil {
		t.Fatalf("retry: %+v %v", again, err)
	}
	changed := input
	changed.SourceSession = &db.SessionRef{ConnectionID: s.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: "other"}
	var conflict *db.ConflictError
	if _, err := s.AppendNote(ctx, owner, changed); !errors.As(err, &conflict) {
		t.Fatalf("changed provenance: %v", err)
	}
	changed = input
	changed.Category = "outcome"
	if _, err := s.AppendNote(ctx, owner, changed); !errors.As(err, &conflict) {
		t.Fatalf("changed category: %v", err)
	}
	changed = input
	changed.Text = "other"
	if _, err := s.AppendNote(ctx, owner, changed); !errors.As(err, &conflict) {
		t.Fatalf("changed text: %v", err)
	}
	page := journal(t, s, ctx, owner, db.JournalQuery{Category: "finding", Q: "needle%_"})
	if len(page.Entries) != 1 || !page.Entries[0].Truncated || utf8.RuneCountInString(page.Entries[0].Text) != 600 {
		t.Fatalf("preview: %+v", page)
	}
	full, err := s.GetJournalEntry(ctx, first.ID)
	if err != nil || full.Text != input.Text || full.Truncated || full.EventType != "" {
		t.Fatalf("full: %+v %v", full, err)
	}
	if page := journal(t, s, ctx, owner, db.JournalQuery{Q: "needleXX"}); len(page.Entries) != 0 || page.Entries == nil {
		t.Fatalf("literal search: %+v", page)
	}
	b := mustTask(t, s, ctx, p.ID, "b", nil)
	if second := note(t, s, ctx, db.JournalOwner{TaskID: &b.ID}, "different", "request-1"); second.ID == first.ID {
		t.Fatal("request keys must be owner scoped")
	}
	var invalid *db.InvalidError
	for _, in := range []db.AppendNoteInput{{Text: "x"}, {Text: "x", RequestID: "bad-category", Category: "other"}, {Text: " ", RequestID: "x"}, {Text: strings.Repeat("x", 16385), RequestID: "x"}} {
		if _, err := s.AppendNote(ctx, owner, in); !errors.As(err, &invalid) {
			t.Fatalf("invalid note accepted: %v", err)
		}
	}
	note(t, s, ctx, owner, strings.Repeat("x", 16384), "max")
	for _, q := range []db.JournalQuery{{Kind: "bogus"}, {Category: "other"}, {Limit: 101}, {Limit: -1}, {TaskID: &a.ID}, {Cursor: "broken"}} {
		if _, err := s.ReadJournal(ctx, owner, q); !errors.As(err, &invalid) {
			t.Fatalf("invalid query %+v: %v", q, err)
		}
	}
	if _, err := s.ReadJournal(ctx, db.JournalOwner{}, db.JournalQuery{}); !errors.As(err, &invalid) {
		t.Fatalf("no owner: %v", err)
	}
	if _, err := s.ReadJournal(ctx, db.JournalOwner{TaskID: &a.ID, TreeID: &b.ID}, db.JournalQuery{}); !errors.As(err, &invalid) {
		t.Fatalf("two owners: %v", err)
	}
	if _, err := s.GetJournalEntry(ctx, 999999); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func caseJournalConcurrentRetryAndPagination(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	owner := db.JournalOwner{TaskID: &a.ID}
	const workers = 12
	results := make(chan db.JournalEntry, workers)
	failures := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			e, err := s.AppendNote(ctx, owner, db.AppendNoteInput{Text: "checkpoint", RequestID: "retry"})
			results <- e
			failures <- err
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id int64
	for e := range results {
		if id != 0 && e.ID != id {
			t.Fatal("retry created multiple entries")
		}
		id = e.ID
	}
	for i := range 24 {
		note(t, s, ctx, owner, fmt.Sprint(i), fmt.Sprint(i))
	}
	first := journal(t, s, ctx, owner, db.JournalQuery{})
	if len(first.Entries) != 20 || first.NextCursor == nil {
		t.Fatalf("default page: %+v", first)
	}
	inserted := note(t, s, ctx, owner, "after first page", "later")
	second := journal(t, s, ctx, owner, db.JournalQuery{Cursor: *first.NextCursor})
	if len(second.Entries) != 5 || second.NextCursor != nil || containsEntry(second, inserted.ID) {
		t.Fatalf("stable pagination: %+v", second)
	}
	for _, e := range second.Entries {
		if containsEntry(first, e.ID) {
			t.Fatal("duplicate across pages")
		}
	}
	var invalid *db.InvalidError
	if _, err := s.ReadJournal(ctx, owner, db.JournalQuery{Kind: "all", Cursor: *first.NextCursor}); !errors.As(err, &invalid) {
		t.Fatalf("cursor wrong filters: %v", err)
	}
}

func caseJournalMovesFreezeEventsAndInvalidateTreeCursors(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", nil)
	old := mustTree(t, s, ctx, "old", "", db.TreeMember{TaskID: a.ID})
	next := mustTree(t, s, ctx, "next", "", db.TreeMember{TaskID: b.ID})
	own := note(t, s, ctx, db.JournalOwner{TaskID: &a.ID}, "task work", "task-note")
	treeInput := db.AppendNoteInput{Text: "tree conclusion", RequestID: "tree-note", SourceTaskID: &a.ID, SourceSession: &db.SessionRef{ConnectionID: s.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: "thread"}}
	treeNote, err := s.AppendNote(ctx, db.JournalOwner{TreeID: &old.ID}, treeInput)
	if err != nil {
		t.Fatal(err)
	}
	setState(t, s, ctx, a.ID, db.StateDone)
	history := journal(t, s, ctx, db.JournalOwner{TaskID: &a.ID}, db.JournalQuery{Kind: "events"})
	event := history.Entries[0]
	if event.TreeID == nil || *event.TreeID != old.ID {
		t.Fatalf("state snapshot %+v", event)
	}
	page := journal(t, s, ctx, db.JournalOwner{TreeID: &old.ID}, db.JournalQuery{Kind: "all", Limit: 1})
	if page.NextCursor == nil {
		t.Fatal("expected cursor")
	}
	if _, err := s.AddToTree(ctx, next.ID, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	var conflict *db.ConflictError
	if _, err := s.ReadJournal(ctx, db.JournalOwner{TreeID: &old.ID}, db.JournalQuery{Kind: "all", Limit: 1, Cursor: *page.NextCursor}); !errors.As(err, &conflict) {
		t.Fatalf("stale cursor: %v", err)
	}
	oldNotes := journal(t, s, ctx, db.JournalOwner{TreeID: &old.ID}, db.JournalQuery{})
	newNotes := journal(t, s, ctx, db.JournalOwner{TreeID: &next.ID}, db.JournalQuery{})
	if containsEntry(oldNotes, own.ID) || !containsEntry(oldNotes, treeNote.ID) || !containsEntry(newNotes, own.ID) || containsEntry(newNotes, treeNote.ID) {
		t.Fatalf("notes did not follow ownership: old %+v new %+v", oldNotes, newNotes)
	}
	for _, treeID := range []int64{old.ID, next.ID} {
		events := journal(t, s, ctx, db.JournalOwner{TreeID: &treeID}, db.JournalQuery{Kind: "events"})
		moves := 0
		for _, e := range events.Entries {
			if e.EventType == "task_moved" && e.FromTreeID != nil && *e.FromTreeID == old.ID && e.ToTreeID != nil && *e.ToTreeID == next.ID {
				moves++
			}
		}
		if moves != 1 {
			t.Fatalf("tree %d has %d copies of move", treeID, moves)
		}
		if containsEntry(events, event.ID) != (treeID == old.ID) {
			t.Fatal("historical task event moved")
		}
	}
	oldTree, err := s.GetTree(ctx, old.ID)
	if err != nil || oldTree.Status != db.TreeArchived || len(oldTree.Tasks) != 0 || oldTree.Deletable {
		t.Fatalf("old tree: %+v %v", oldTree, err)
	}
	if err := s.DeleteTree(ctx, old.ID); !errors.As(err, &conflict) {
		t.Fatalf("recorded tree deleted: %v", err)
	}
	if e, err := s.AppendNote(ctx, db.JournalOwner{TreeID: &old.ID}, treeInput); err != nil || e.ID != treeNote.ID {
		t.Fatalf("retry after move: %+v %v", e, err)
	}
	treeInput.RequestID = "after-move"
	if _, err := s.AppendNote(ctx, db.JournalOwner{TreeID: &old.ID}, treeInput); !errors.As(err, &conflict) {
		t.Fatalf("stale tree write: %v", err)
	}
	filtered := journal(t, s, ctx, db.JournalOwner{TreeID: &old.ID}, db.JournalQuery{TaskID: &a.ID})
	if len(filtered.Entries) != 1 || filtered.Entries[0].ID != treeNote.ID {
		t.Fatalf("source task filter: %+v", filtered)
	}
	if err := s.DissolveTree(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	if e, err := s.GetJournalEntry(ctx, own.ID); err != nil || e.TaskID == nil || *e.TaskID != a.ID || e.TreeID != nil {
		t.Fatalf("immutable note owner: %+v %v", e, err)
	}
	dissolved, err := s.GetTree(ctx, next.ID)
	if err != nil || dissolved.Status != db.TreeArchived || len(dissolved.Tasks) != 0 || dissolved.Deletable {
		t.Fatalf("dissolved %+v %v", dissolved, err)
	}
}

func caseJournalClosedWritesAndPermanentProtection(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	tr := mustTree(t, s, ctx, "tree", "", db.TreeMember{TaskID: a.ID})
	if _, err := s.SetTreeStatus(ctx, tr.ID, db.TreeDone); err != nil {
		t.Fatal(err)
	}
	e := note(t, s, ctx, db.JournalOwner{TaskID: &a.ID}, "closed checkpoint", "closed")
	got, err := s.GetTree(ctx, tr.ID)
	if err != nil || got.Status != db.TreeDone || got.LastActivityAt.Before(e.CreatedAt) || got.Deletable {
		t.Fatalf("closed note: %+v %v", got, err)
	}
	if project, err := s.GetProject(ctx, p.ID); err != nil || project.LastActivityAt == nil || project.LastActivityAt.Before(e.CreatedAt) {
		t.Fatalf("project journal activity: %+v %v", project, err)
	}

	if ids, err := s.LaunchableTasks(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("closed note released tasks: %v %v", ids, err)
	}
	if _, err := s.SetTreeStatus(ctx, tr.ID, db.TreeOpen); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFromTree(ctx, tr.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	var conflict *db.ConflictError
	if err := s.DeleteTree(ctx, tr.ID); !errors.As(err, &conflict) {
		t.Fatalf("note protection lost after detach: %v", err)
	}
	if err := s.DeleteTask(ctx, a.ID); !errors.As(err, &conflict) {
		t.Fatalf("task note deleted: %v", err)
	}
	archived, err := s.ArchiveTask(ctx, a.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	note(t, s, ctx, db.JournalOwner{TaskID: &a.ID}, "archived checkpoint", "archived")
	task := getTask(t, s, ctx, a.ID)
	if task.ArchivedAt == nil || !task.ArchivedAt.Equal(*archived.ArchivedAt) || task.Deletable || task.State != db.StateBacklog {
		t.Fatalf("note changed task lifecycle: %+v", task)
	}
	treeNote := note(t, s, ctx, db.JournalOwner{TreeID: &tr.ID}, "archived tree checkpoint", "archived")
	trees, err := s.ListTrees(ctx, db.TreeFilter{View: db.TreeViewArchived})
	if err != nil || len(trees) != 1 || trees[0].LastActivityAt.Before(treeNote.CreatedAt) {
		t.Fatalf("archived activity: %+v %v", trees, err)
	}
}

func caseJournalInitialNotesScheduleDuplicateAndRollback(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	in := db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: p.ID, Title: "a", Prompt: "p", InitialNote: "initial"}
	a, err := s.CreateTask(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	notes := journal(t, s, ctx, db.JournalOwner{TaskID: &a.ID}, db.JournalQuery{})
	if len(notes.Entries) != 1 || notes.Entries[0].Text != "initial" || a.Deletable {
		t.Fatalf("initial note %+v", notes)
	}
	copy, err := s.DuplicateTask(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !copy.Deletable || len(journal(t, s, ctx, db.JournalOwner{TaskID: &copy.ID}, db.JournalQuery{}).Entries) != 0 {
		t.Fatal("duplicate copied notes")
	}
	events := journal(t, s, ctx, db.JournalOwner{TaskID: &copy.ID}, db.JournalQuery{Kind: "events"})
	if !slices.ContainsFunc(events.Entries, func(e db.JournalEntry) bool {
		return e.EventType == "task_duplicated" && e.SourceTaskID != nil && *e.SourceTaskID == a.ID
	}) {
		t.Fatalf("missing source link %+v", events)
	}
	sc := mustSchedule(t, s, ctx, p.ID, "*/5 * * * *")
	if _, err := s.UpdateSchedule(ctx, sc.ID, db.SchedulePatch{Notes: db.Optional[string]{Set: true, V: "template note"}}); err != nil {
		t.Fatal(err)
	}
	res := mustFire(t, s, ctx, sc.ID, *sc.NextRunAt)
	if res.TaskID == nil {
		t.Fatal("schedule did not spawn")
	}
	if page := journal(t, s, ctx, db.JournalOwner{TaskID: res.TaskID}, db.JournalQuery{}); len(page.Entries) != 1 || page.Entries[0].Text != "template note" {
		t.Fatalf("schedule note %+v", page)
	}
	before, err := s.ListTasks(ctx, db.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	in.InitialNote = strings.Repeat("x", 16385)
	var invalid *db.InvalidError
	if _, err := s.CreateTask(ctx, in); !errors.As(err, &invalid) {
		t.Fatalf("oversized initial: %v", err)
	}
	after, err := s.ListTasks(ctx, db.TaskFilter{})
	if err != nil || len(after) != len(before) {
		t.Fatalf("failed create left a task: %d %d %v", len(before), len(after), err)
	}
}

func caseJournalLaunchInstructionsFrozenPerAttempt(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: a.ID, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	a = getTask(t, s, ctx, a.ID)
	before := journal(t, s, ctx, db.JournalOwner{TaskID: &a.ID}, db.JournalQuery{Kind: "all"})
	if got, err := s.JournalLaunchContext(ctx, a, "exact\noriginal instructions"); err != nil || got != "exact\noriginal instructions" {
		t.Fatalf("first %q %v", got, err)
	}
	if got, err := s.JournalLaunchContext(ctx, a, "changed after move"); err != nil || got != "exact\noriginal instructions" {
		t.Fatalf("same attempt %q %v", got, err)
	}
	after := journal(t, s, ctx, db.JournalOwner{TaskID: &a.ID}, db.JournalQuery{Kind: "all"})
	if len(after.Entries) != len(before.Entries) {
		t.Fatal("launch context emitted journal entries")
	}
	setState(t, s, ctx, a.ID, db.StateFailed)
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: a.ID, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	fresh := getTask(t, s, ctx, a.ID)
	if fresh.LaunchAttempt == a.LaunchAttempt {
		t.Fatal("new attempt not incremented")
	}
	if got, err := s.JournalLaunchContext(ctx, fresh, "fresh"); err != nil || got != "fresh" {
		t.Fatalf("fresh %q %v", got, err)
	}
	var conflict *db.ConflictError
	if _, err := s.JournalLaunchContext(ctx, a, "stale"); !errors.As(err, &conflict) {
		t.Fatalf("stale attempt %v", err)
	}
}

func caseJournalCreateIntoTreeInvalidatesCursor(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	tr := mustTree(t, s, ctx, "tree", "", db.TreeMember{TaskID: a.ID})
	owner := db.JournalOwner{TreeID: &tr.ID}
	page := journal(t, s, ctx, owner, db.JournalQuery{Kind: "all", Limit: 1})
	if page.NextCursor == nil {
		t.Fatal("missing cursor")
	}
	task, err := s.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: p.ID, Title: "new member", Prompt: "p", TreeID: &tr.ID, InitialNote: "member note"})
	if err != nil {
		t.Fatal(err)
	}
	var conflict *db.ConflictError
	if _, err := s.ReadJournal(ctx, owner, db.JournalQuery{Kind: "all", Limit: 1, Cursor: *page.NextCursor}); !errors.As(err, &conflict) {
		t.Fatalf("create did not invalidate cursor: %v", err)
	}
	events := journal(t, s, ctx, db.JournalOwner{TaskID: &task.ID}, db.JournalQuery{Kind: "events"})
	if len(events.Entries) != 1 || events.Entries[0].TreeID == nil || *events.Entries[0].TreeID != tr.ID {
		t.Fatalf("creation must snapshot joined tree: %+v", events)
	}
}

func caseJournalMoveAndAppendSerialize(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", nil)
	old := mustTree(t, s, ctx, "old", "", db.TreeMember{TaskID: a.ID})
	next := mustTree(t, s, ctx, "next", "", db.TreeMember{TaskID: b.ID})
	start := make(chan struct{})
	moveResult := make(chan error, 1)
	noteResult := make(chan error, 1)
	go func() { <-start; _, err := s.AddToTree(ctx, next.ID, a.ID, nil); moveResult <- err }()
	go func() {
		<-start
		_, err := s.AppendNote(ctx, db.JournalOwner{TreeID: &old.ID}, db.AppendNoteInput{Text: "conclusion", RequestID: "race", SourceTaskID: &a.ID})
		noteResult <- err
	}()
	close(start)
	if err := <-moveResult; err != nil {
		t.Fatal(err)
	}
	err := <-noteResult
	var conflict *db.ConflictError
	if err != nil && !errors.As(err, &conflict) {
		t.Fatalf("unexpected append error: %v", err)
	}
	notes := journal(t, s, ctx, db.JournalOwner{TreeID: &old.ID}, db.JournalQuery{})
	if (err == nil && len(notes.Entries) != 1) || (err != nil && len(notes.Entries) != 0) {
		t.Fatalf("partial append after race: %+v %v", notes, err)
	}
	if task := getTask(t, s, ctx, a.ID); task.TreeID == nil || *task.TreeID != next.ID {
		t.Fatal("move lost")
	}
}

func caseJournalMoveRollbackAndExecutionProtection(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", nil)
	old := mustTree(t, s, ctx, "old", "", db.TreeMember{TaskID: a.ID})
	next := mustTree(t, s, ctx, "next", "", db.TreeMember{TaskID: b.ID})
	launch(t, s, ctx, a.ID, "session", db.StateDone)
	owner := db.JournalOwner{TreeID: &old.ID}
	page := journal(t, s, ctx, owner, db.JournalQuery{Kind: "all", Limit: 1})
	before := journal(t, s, ctx, owner, db.JournalQuery{Kind: "all"})
	var conflict *db.ConflictError
	// A changed needs set is invalid outside backlog. Joining must roll back too.
	if _, err := s.AddToTree(ctx, next.ID, a.ID, []int64{b.ID}); !errors.As(err, &conflict) {
		t.Fatalf("invalid move: %v", err)
	}
	after := journal(t, s, ctx, owner, db.JournalQuery{Kind: "all"})
	if len(before.Entries) != len(after.Entries) {
		t.Fatal("rolled-back move wrote events")
	}
	if _, err := s.ReadJournal(ctx, owner, db.JournalQuery{Kind: "all", Limit: 1, Cursor: *page.NextCursor}); err != nil {
		t.Fatalf("rolled-back move changed revision: %v", err)
	}
	if _, err := s.AddToTree(ctx, next.ID, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTree(ctx, old.ID); !errors.As(err, &conflict) {
		t.Fatalf("execution protection lost after move: %v", err)
	}
}

func caseJournalTreeMutationEvents(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", nil)
	tr := mustTree(t, s, ctx, "tree", "", db.TreeMember{TaskID: a.ID}, db.TreeMember{TaskID: b.ID})
	if _, err := s.UpdateTree(ctx, tr.ID, db.TreePatch{Title: db.Optional[string]{Set: true, V: "renamed"}, StartMode: db.Optional[db.StartMode]{Set: true, V: db.StartManual}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetNeeds(ctx, b.ID, []int64{a.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetNeeds(ctx, b.ID, []int64{a.ID}); err != nil {
		t.Fatal(err)
	}
	launch(t, s, ctx, a.ID, "execution-thread", db.StateDone)
	if _, err := s.SetTreeStatus(ctx, tr.ID, db.TreeDone); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTreeStatus(ctx, tr.ID, db.TreeDone); err != nil {
		t.Fatal(err)
	}
	page := journal(t, s, ctx, db.JournalOwner{TreeID: &tr.ID}, db.JournalQuery{Kind: "events", Limit: 100})
	counts := map[string]int{}
	for _, e := range page.Entries {
		counts[e.EventType]++
		if e.TaskID != nil && *e.TaskID == a.ID && e.SourceThreadID != nil && *e.SourceThreadID == "execution-thread" {
			if e.Attempt == nil || *e.Attempt != 1 || e.TreeID == nil || *e.TreeID != tr.ID {
				t.Fatalf("execution provenance %+v", e)
			}
			counts["thread_provenance"]++
		}
	}
	for kind, n := range map[string]int{"tree_created": 1, "tree_config_changed": 1, "needs_changed": 1, "tree_status_changed": 1, "task_moved": 2, "thread_provenance": 2} {
		if counts[kind] != n {
			t.Fatalf("%s events: %d, want %d; %+v", kind, counts[kind], n, page)
		}
	}
}
