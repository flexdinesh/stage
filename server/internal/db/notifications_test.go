package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestNotificationsInbox(t *testing.T) {
	pool := testDB.Reset(t)
	s, ctx := db.NewStore(pool), context.Background()
	p := mustProject(t, s, ctx, "proj-1")
	task := mustTask(t, s, ctx, p.ID, "Say OK", nil)
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateNeedsInput}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertThreads(ctx, t3.Observations([]t3.Thread{
		{ThreadID: "ext-1", ProjectID: "proj-1", Title: "Ext", Status: t3.StatusWaiting, CreatedAt: time.Now().UTC()},
	})); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertThreadDetail(ctx, t3.ThreadDetail{Thread: t3.Thread{ThreadID: "ext-1", ProjectID: "proj-1", Title: "Fix bug & test", Status: t3.StatusWaiting, UpdatedAt: time.Now().UTC()}, PendingRequestCount: 1}); err != nil {
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

	// Current state follows normalized execution; historical settlement cannot hide activity.
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateDone}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(ctx, `UPDATE threads SET settled = 1 WHERE t3_thread_id = 'ext-1'`); err != nil {
		t.Fatal(err)
	}
	all, err = s.ListNotifications(ctx, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if state(all[2]) != db.StateDone || state(all[1]) != db.StateNeedsInput {
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

	// Archiving recorded work keeps its notification and current state.
	if _, err := s.ArchiveTask(ctx, task.ID, true); err != nil {
		t.Fatal(err)
	}
	all, err = s.ListNotifications(ctx, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[2].TaskID == nil || all[2].CurrentState == nil || *all[2].CurrentState != db.StateDone {
		t.Fatalf("after task archive %+v", all[2])
	}

	// Prune drops entries older than the age.
	if _, err := pool.ExecContext(ctx, `UPDATE notifications SET created_at = strftime('%Y-%m-%dT%H:%M:%f000Z', 'now', '-31 days') WHERE id = $1`, id1); err != nil {
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
	if err != nil || len(older) != 2 || older[0].Key != "thread:1:ext-2" || older[1].Key != "thread:1:ext-1" {
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
	if _, err := s.ArchiveTask(ctx, active.ID, true); err != nil {
		t.Fatal(err)
	}
	groups, err = s.ListNotificationGroups(ctx, 10, nil, futureID)
	if err != nil || len(groups) != 4 || groups[1].Key != key || groups[1].EventCount != 56 ||
		groups[1].UnreadCount != 1 || groups[1].CurrentState == nil || *groups[1].CurrentState != db.StateFailed {
		t.Fatalf("archived/restarted task must retain its group: %+v %v", groups, err)
	}
	history, err = s.ListNotificationHistory(ctx, key, 100, nil)
	if err != nil || len(history) != 56 {
		t.Fatalf("archived task history: %d %v", len(history), err)
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
	if got := fmt.Sprint(tags()); got != fmt.Sprint([]string{taskTag, "task-99", "thread-1:ext-1"}) {
		t.Fatalf("unread tags %s", got)
	}

	// Marking groups read covers every event of the task and the deleted task's URL key only.
	if err := s.MarkNotificationGroupsRead(ctx, []string{fmt.Sprintf("task:%d", task.ID), "task:99"}); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(tags()); got != fmt.Sprint([]string{"thread-1:ext-1"}) {
		t.Fatalf("tags after group read %s", got)
	}
	if n, err := s.UnreadNotificationCount(ctx); err != nil || n != 2 {
		t.Fatalf("unread after group read %d %v", n, err)
	}

	// A new event makes a read group unread again.
	insert(db.NewNotification{Event: db.StateFailed, TaskID: &task.ID, Title: "Say OK", URL: taskURL})
	if got := fmt.Sprint(tags()); got != fmt.Sprint([]string{taskTag, "thread-1:ext-1"}) {
		t.Fatalf("tags after new event %s", got)
	}
}

// NotificationKey must agree with the SQL group key for every identity kind.
func TestNotificationKeyParity(t *testing.T) {
	pool := testDB.Reset(t)
	s, ctx := db.NewStore(pool), context.Background()
	p := mustProject(t, s, ctx, "proj-1")
	task := mustTask(t, s, ctx, p.ID, "Say OK", nil)
	deleted := mustTask(t, s, ctx, p.ID, "Gone", nil)
	thread, other := "ext-1", "ext-2"
	for _, n := range []db.NewNotification{
		{Event: db.StateNeedsInput, TaskID: &task.ID, T3ThreadID: &other, Title: "Say OK", URL: fmt.Sprintf("/tasks/%d", task.ID)},
		{Event: db.StateDone, TaskID: &deleted.ID, Title: "Gone", URL: fmt.Sprintf("/tasks/%d", deleted.ID)},
		{Event: db.StateDone, Title: "Old", URL: "/tasks/007"},
		{Event: db.StateDone, Title: "Not a task", URL: "/tasks/7x"},
		{Event: db.StateNeedsInput, T3ThreadID: &thread, Title: "Ext", URL: "/sessions/ext-1"},
		{Event: db.StateFailed, Title: "No identity", URL: "/"},
	} {
		if _, err := s.InsertNotification(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteTask(ctx, deleted.ID); err != nil {
		t.Fatal(err)
	}
	latest, _, err := s.NotificationReadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := s.ListNotificationGroups(ctx, 50, nil, latest)
	if err != nil || len(groups) != 6 {
		t.Fatalf("groups %+v %v", groups, err)
	}
	want := map[string]bool{
		fmt.Sprintf("task:%d", task.ID): true, fmt.Sprintf("task:%d", deleted.ID): true, "task:007": true,
		"thread:1:ext-1": true,
	}
	identityless := 0
	for _, g := range groups {
		if got := db.NotificationKey(g.Notification); got != g.Key {
			t.Errorf("row %d: NotificationKey %q, SQL key %q", g.ID, got, g.Key)
		}
		if !want[g.Key] {
			identityless++
			if g.Key != fmt.Sprintf("notification:%d", g.ID) {
				t.Errorf("identity-less key %q for row %d", g.Key, g.ID)
			}
		}
	}
	if identityless != 2 {
		t.Fatalf("identity-less groups %d, want 2 (bad task URL, none)", identityless)
	}
}

func TestTagForKey(t *testing.T) {
	for key, want := range map[string]string{
		"task:7":         "task-7",
		"thread:1:ext-1": "thread-1:ext-1",
		"thread:1:a:b":   "thread-1:a:b",
		"notification:3": "",
		"task":           "",
		"":               "",
	} {
		if got := db.TagForKey(key); got != want {
			t.Errorf("TagForKey(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestNotificationsAfter(t *testing.T) {
	pool := testDB.Reset(t)
	s, ctx := db.NewStore(pool), context.Background()
	p := mustProject(t, s, ctx, "proj-1")
	task := mustTask(t, s, ctx, p.ID, "Say OK", nil)
	thread := "session-2"
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateNeedsInput, ThreadID: &thread}); err != nil {
		t.Fatal(err)
	}
	stale := "session-1"
	var ids []int64
	for i := range 5 {
		id, err := s.InsertNotification(ctx, db.NewNotification{Event: db.StateDone, TaskID: &task.ID, T3ThreadID: &stale,
			Title: fmt.Sprintf("n%d", i), URL: fmt.Sprintf("/tasks/%d", task.ID)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	got := func(after int64, limit int, newest bool) []int64 {
		t.Helper()
		rows, err := s.NotificationsAfter(ctx, after, limit, newest)
		if err != nil {
			t.Fatal(err)
		}
		out := []int64{}
		for _, n := range rows {
			out = append(out, n.ID)
		}
		return out
	}
	if r := fmt.Sprint(got(ids[0], 2, false)); r != fmt.Sprint(ids[1:3]) {
		t.Fatalf("oldest after %d: %s", ids[0], r)
	}
	if r := fmt.Sprint(got(ids[0], 2, true)); r != fmt.Sprint(ids[3:5]) {
		t.Fatalf("newest after %d: %s", ids[0], r)
	}
	if r := fmt.Sprint(got(0, 10, true)); r != fmt.Sprint(ids) {
		t.Fatalf("all: %s", r)
	}
	if r := got(ids[4], 10, false); len(r) != 0 {
		t.Fatalf("after latest: %v", r)
	}
	rows, err := s.NotificationsAfter(ctx, ids[3], 1, false)
	if err != nil || len(rows) != 1 {
		t.Fatalf("row: %+v %v", rows, err)
	}
	// The task's current thread, live state and stored fields.
	n := rows[0]
	if n.T3ThreadID == nil || *n.T3ThreadID != thread || n.CurrentState == nil || *n.CurrentState != db.StateNeedsInput ||
		n.Title != "n4" || n.URL != fmt.Sprintf("/tasks/%d", task.ID) || n.ReadAt != nil || n.CreatedAt.IsZero() {
		t.Fatalf("row %+v", n)
	}
}
