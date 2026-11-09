package db_test

import (
	"context"
	"fmt"
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

func TestNotificationGroups(t *testing.T) {
	pool := testDB.Reset(t)
	s, ctx := db.NewStore(pool), context.Background()
	p := mustProject(t, s, ctx, "proj-1")
	waiting := mustTask(t, s, ctx, p.ID, "Same title", nil)
	active := mustTask(t, s, ctx, p.ID, "Same title", nil)
	for _, change := range []db.Transition{
		{TaskID: waiting.ID, To: db.StateNeedsInput},
		{TaskID: active.ID, To: db.StateRunning},
	} {
		if _, err := s.TransitionTask(ctx, change); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(n db.NewNotification) int64 {
		t.Helper()
		id, err := s.InsertNotification(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	waitingID := insert(db.NewNotification{Event: db.StateNeedsInput, TaskID: &waiting.ID,
		Title: waiting.Title, URL: fmt.Sprintf("/tasks/%d", waiting.ID)})
	thread1, thread2 := "ext-1", "ext-2"
	insert(db.NewNotification{Event: db.StateDone, T3ThreadID: &thread1, Title: "Same title", URL: "/history?q=Same"})
	insert(db.NewNotification{Event: db.StateFailed, T3ThreadID: &thread2, Title: "Same title", URL: "/history?q=Same"})
	var activeIDs []int64
	for i := range 55 {
		thread := fmt.Sprintf("attempt-%d", i)
		activeIDs = append(activeIDs, insert(db.NewNotification{Event: db.StateFailed, TaskID: &active.ID,
			T3ThreadID: &thread, Title: fmt.Sprintf("Renamed %d", i), URL: fmt.Sprintf("/tasks/%d", active.ID)}))
	}
	latest, unread, err := s.NotificationReadState(ctx)
	if err != nil || latest != activeIDs[len(activeIDs)-1] || unread != 58 {
		t.Fatalf("read boundary: %d %d %v", latest, unread, err)
	}
	// An insert after capturing the boundary must not leak into this snapshot.
	futureID := insert(db.NewNotification{Event: db.StateDone, TaskID: &active.ID, Title: "Future",
		URL: fmt.Sprintf("/tasks/%d", active.ID)})
	groups, err := s.ListNotificationGroups(ctx, 2, nil, latest)
	if err != nil || len(groups) != 2 {
		t.Fatalf("first page: %+v %v", groups, err)
	}
	if groups[0].ID != waitingID || groups[0].EventCount != 1 ||
		groups[1].ID != latest || groups[1].EventCount != 55 || groups[1].UnreadCount != 55 ||
		groups[1].CurrentState == nil || *groups[1].CurrentState != db.StateRunning {
		t.Fatalf("priority, aggregation or live state: %+v", groups)
	}
	cursor := &db.NotificationCursor{Waiting: false, ID: groups[1].ID}
	older, err := s.ListNotificationGroups(ctx, 2, cursor, latest)
	if err != nil || len(older) != 2 || older[0].Key != "thread:ext-2" || older[1].Key != "thread:ext-1" {
		t.Fatalf("group pagination/identity: %+v %v", older, err)
	}
	waitingCursor := &db.NotificationCursor{Waiting: true, ID: waitingID}
	afterWaiting, err := s.ListNotificationGroups(ctx, 1, waitingCursor, latest)
	if err != nil || len(afterWaiting) != 1 || afterWaiting[0].ID != latest {
		t.Fatalf("priority cursor must reach newer recent items: %+v %v", afterWaiting, err)
	}
	key := fmt.Sprintf("task:%d", active.ID)
	history, err := s.ListNotificationHistory(ctx, key, 2, &futureID)
	if err != nil || len(history) != 2 || history[0].ID != latest || history[1].ID != activeIDs[len(activeIDs)-2] {
		t.Fatalf("history pagination: %+v %v", history, err)
	}
	if err := s.MarkNotificationsRead(ctx, latest); err != nil {
		t.Fatal(err)
	}
	if n, err := s.UnreadNotificationCount(ctx); err != nil || n != 1 {
		t.Fatalf("new event must remain unread: %d %v", n, err)
	}
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: active.ID, To: db.StateFailed}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTask(ctx, active.ID); err != nil {
		t.Fatal(err)
	}
	groups, err = s.ListNotificationGroups(ctx, 10, nil, futureID)
	if err != nil || len(groups) != 4 || groups[1].Key != key || groups[1].EventCount != 56 ||
		groups[1].UnreadCount != 1 || groups[1].CurrentState != nil {
		t.Fatalf("deleted/restarted task must retain its group: %+v %v", groups, err)
	}
	history, err = s.ListNotificationHistory(ctx, key, 100, nil)
	if err != nil || len(history) != 56 {
		t.Fatalf("deleted task history: %d %v", len(history), err)
	}
	// Attention follows current state, even without another notification.
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: waiting.ID, To: db.StateDone}); err != nil {
		t.Fatal(err)
	}
	groups, err = s.ListNotificationGroups(ctx, 10, nil, futureID)
	if err != nil || groups[0].Key != key || groups[len(groups)-1].ID != waitingID {
		t.Fatalf("resolved input must leave priority: %+v %v", groups, err)
	}
}

func TestNotificationGroupReadAndTags(t *testing.T) {
	pool := testDB.Reset(t)
	s, ctx := db.NewStore(pool), context.Background()
	p := mustProject(t, s, ctx, "proj-1")
	task := mustTask(t, s, ctx, p.ID, "Say OK", nil)
	ext, other := "ext-1", "ext-2"
	insert := func(n db.NewNotification) {
		t.Helper()
		if _, err := s.InsertNotification(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	taskURL := fmt.Sprintf("/tasks/%d", task.ID)
	insert(db.NewNotification{Event: db.StateNeedsInput, TaskID: &task.ID, T3ThreadID: &other, Title: "Say OK", URL: taskURL})
	insert(db.NewNotification{Event: db.StateDone, TaskID: &task.ID, Title: "Say OK", URL: taskURL})
	insert(db.NewNotification{Event: db.StateDone, Title: "Deleted task", URL: "/tasks/99"})
	insert(db.NewNotification{Event: db.StateNeedsInput, T3ThreadID: &ext, Title: "Ext", URL: "/sessions/ext-1"})
	insert(db.NewNotification{Event: db.StateFailed, Title: "No identity", URL: "/"})

	tags := func() []string {
		t.Helper()
		got, err := s.UnreadNotificationTags(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	taskTag := fmt.Sprintf("task-%d", task.ID)
	// A task-owned thread notification is tagged by its task, never its thread.
	if got := fmt.Sprint(tags()); got != fmt.Sprint([]string{taskTag, "task-99", "thread-ext-1"}) {
		t.Fatalf("unread tags %s", got)
	}

	// Marking groups read covers every event of the task and the deleted task's URL key only.
	if err := s.MarkNotificationGroupsRead(ctx, []string{fmt.Sprintf("task:%d", task.ID), "task:99"}); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(tags()); got != fmt.Sprint([]string{"thread-ext-1"}) {
		t.Fatalf("tags after group read %s", got)
	}
	if n, err := s.UnreadNotificationCount(ctx); err != nil || n != 2 {
		t.Fatalf("unread after group read %d %v", n, err)
	}

	// A new event makes a read group unread again.
	insert(db.NewNotification{Event: db.StateFailed, TaskID: &task.ID, Title: "Say OK", URL: taskURL})
	if got := fmt.Sprint(tags()); got != fmt.Sprint([]string{taskTag, "thread-ext-1"}) {
		t.Fatalf("tags after new event %s", got)
	}
}
