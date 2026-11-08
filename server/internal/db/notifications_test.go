package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/flexdinesh/t3-stage/server/internal/db"
	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

func TestNotificationsInbox(t *testing.T) {
	pool := testDB.Reset(t)
	s, ctx := db.NewStore(pool), context.Background()
	p := mustProject(t, s, ctx, "proj-1")
	task := mustTask(t, s, ctx, p.ID, "Say OK", nil)
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateNeedsInput}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertThreads(ctx, []t3.Thread{
		{ThreadID: "ext-1", ProjectID: "proj-1", Title: "Ext", Status: t3.StatusWaiting, CreatedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetObservedState(ctx, "ext-1", db.StateNeedsInput); err != nil {
		t.Fatal(err)
	}
	proj := "proj-1"
	thread := "ext-1"
	insert := func(n db.NewNotification) int64 {
		t.Helper()
		id, err := s.InsertNotification(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	id1 := insert(db.NewNotification{Event: db.StateNeedsInput, TaskID: &task.ID, Title: "Say OK", ProjectTitle: &proj, URL: "/tasks/1"})
	id2 := insert(db.NewNotification{Event: db.StateNeedsInput, T3ThreadID: &thread, Title: "Ext", ProjectTitle: &proj, URL: "/history?q=Ext"})
	id3 := insert(db.NewNotification{Event: db.StateFailed, T3ThreadID: &thread, Title: "Ext", URL: "/history?q=Ext"})

	all, err := s.ListNotifications(ctx, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID != id3 || all[2].ID != id1 {
		t.Fatalf("order %+v", all)
	}
	state := func(n db.Notification) db.TaskState {
		if n.CurrentState == nil {
			return ""
		}
		return *n.CurrentState
	}
	if state(all[2]) != db.StateNeedsInput || state(all[1]) != db.StateNeedsInput {
		t.Fatalf("current states %q %q", state(all[2]), state(all[1]))
	}

	// Current state follows the live task / thread.
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateDone}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE threads SET settled = true WHERE t3_thread_id = 'ext-1'`); err != nil {
		t.Fatal(err)
	}
	all, err = s.ListNotifications(ctx, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if state(all[2]) != db.StateDone || state(all[1]) != db.StateSettled {
		t.Fatalf("live states %q %q", state(all[2]), state(all[1]))
	}

	// Pagination: before an id.
	page, err := s.ListNotifications(ctx, 1, &id3)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].ID != id2 {
		t.Fatalf("page %+v", page)
	}

	// Read up to an id leaves newer ones unread.
	if n, err := s.UnreadNotificationCount(ctx); err != nil || n != 3 {
		t.Fatalf("unread %d %v", n, err)
	}
	if err := s.MarkNotificationsRead(ctx, id2); err != nil {
		t.Fatal(err)
	}
	if n, err := s.UnreadNotificationCount(ctx); err != nil || n != 1 {
		t.Fatalf("unread after read %d %v", n, err)
	}

	// Deleting the task keeps the entry, without a state.
	if err := s.DeleteTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	all, err = s.ListNotifications(ctx, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[2].TaskID != nil || all[2].CurrentState != nil {
		t.Fatalf("after task delete %+v", all[2])
	}

	// Prune drops entries older than the age.
	if _, err := pool.Exec(ctx, `UPDATE notifications SET created_at = now() - interval '31 days' WHERE id = $1`, id1); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneNotifications(ctx, 30*24*time.Hour); err != nil || n != 1 {
		t.Fatalf("pruned %d %v", n, err)
	}
}
