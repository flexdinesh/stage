package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/t3"
)

var testDB dbtest.DB

func setup(t *testing.T) (*db.Store, context.Context) {
	return db.NewStore(testDB.Reset(t)), context.Background()
}

func mustProject(t *testing.T, s *db.Store, ctx context.Context, t3id string) db.Project {
	t.Helper()
	p, err := s.InsertProject(ctx, db.Project{Title: t3id, Bindings: []db.ProjectBinding{{ConnectionID: 1, ExternalProjectID: t3id, Workspace: "/w/" + t3id}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustTask(t *testing.T, s *db.Store, ctx context.Context, projectID int64, title string, needs []int64) db.Task {
	t.Helper()
	task, err := s.CreateTask(ctx, db.NewTask{
		ProjectID: projectID, Title: title, Prompt: "p", RuntimeMode: "full-access",
		InteractionMode: "default", Workspace: db.Workspace{Type: "root"}, Needs: needs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestPollableTasksThrottlesStaleCompleted(t *testing.T) {
	pool := testDB.Reset(t)
	s, ctx := db.NewStore(pool), context.Background()
	p := mustProject(t, s, ctx, "proj-1")
	ids := map[string]int64{}
	for i, name := range []string{"running", "fresh", "stale"} {
		task := mustTask(t, s, ctx, p.ID, name, nil)
		thread := fmt.Sprintf("thread-%d", i)
		_, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateRunning, ThreadID: &thread})
		if name != "running" {
			_, _ = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateDone})
		}
		ids[name] = task.ID
	}
	if _, err := pool.ExecContext(ctx, `UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%f000Z', 'now', '-2 hours'), finished_at = strftime('%Y-%m-%dT%H:%M:%f000Z', 'now', '-2 hours') WHERE id = $1`, ids["stale"]); err != nil {
		t.Fatal(err)
	}
	titles := func(includeStale bool) []string {
		tasks, err := s.PollableTasks(ctx, includeStale)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, task := range tasks {
			out = append(out, task.Title)
		}
		return out
	}
	if got := fmt.Sprint(titles(false)); got != "[running fresh]" {
		t.Fatalf("frequent poll %s", got)
	}
	if got := fmt.Sprint(titles(true)); got != "[running fresh stale]" {
		t.Fatalf("stale poll %s", got)
	}
	age := func() {
		if _, err := pool.ExecContext(ctx, `UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%f000Z', 'now', '-2 hours') WHERE id = $1`, ids["stale"]); err != nil {
			t.Fatal(err)
		}
	}
	// An accepted stale task has nothing left to infer.
	acceptTask(t, s, ctx, ids["stale"])
	age()
	if got := fmt.Sprint(titles(true)); got != "[running fresh]" {
		t.Fatalf("accepted stale poll %s", got)
	}
	reopenTask(t, s, ctx, ids["stale"])
	age()
	if got := fmt.Sprint(titles(true)); got != "[running fresh stale]" {
		t.Fatalf("reopened stale poll %s", got)
	}
}

func TestTaskCarriesThreadPullRequests(t *testing.T) {
	pool := testDB.Reset(t)
	s, ctx := db.NewStore(pool), t.Context()
	p := mustProject(t, s, ctx, "proj-prs")
	task := mustTask(t, s, ctx, p.ID, "t", nil)
	got, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PullRequests == nil || len(got.PullRequests) != 0 {
		t.Fatalf("threadless task prs = %#v", got.PullRequests)
	}
	now := time.Now().UTC()
	open := t3.PullRequest{Host: "github.com", Repository: "o/r", Number: 7, URL: "https://github.com/o/r/pull/7", Source: "agent", State: "open"}
	if err := s.UpsertThreads(ctx, t3.Observations([]t3.Thread{{ThreadID: "th-task-pr", ProjectID: p.Bindings[0].ExternalProjectID, Title: "t", Status: t3.StatusRunning, CreatedAt: now, UpdatedAt: now, PullRequests: []t3.PullRequest{open}}})); err != nil {
		t.Fatal(err)
	}
	// Historical records from an older Stage version remain readable.
	if _, err := pool.ExecContext(ctx, `UPDATE threads SET pull_requests = ?, recent_runs = ? WHERE t3_thread_id = ?`, `[ {"host":"github.com","repository":"o/r","number":7,"url":"https://github.com/o/r/pull/7","source":"agent","state":"open"} ]`, `[{"runId":"old","ordinal":1}]`, "th-task-pr"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertThreadDetail(ctx, t3.ThreadDetail{Thread: t3.Thread{ThreadID: "th-task-pr", ProjectID: p.Bindings[0].ExternalProjectID, Title: "updated", Status: t3.StatusCompleted, CreatedAt: now, UpdatedAt: now, PullRequests: []t3.PullRequest{{URL: "https://example.com/new"}}}}); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ThreadRuns(ctx, "th-task-pr")
	if err != nil || len(runs) != 1 || runs[0].RunID != "old" {
		t.Fatalf("historical runs: %+v %v", runs, err)
	}
	thread := "th-task-pr"
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateRunning, ThreadID: &thread}); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PullRequests) != 1 || got.PullRequests[0] != open {
		t.Fatalf("task prs = %#v", got.PullRequests)
	}
}

func TestSweepStagedAttachments(t *testing.T) {
	s, ctx := setup(t)
	old, err := s.CreateAttachment(ctx, "old.png", "image/png", []byte{1})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.CreateAttachment(ctx, "fresh.png", "image/png", []byte{2})
	if err != nil {
		t.Fatal(err)
	}
	attachedOld, err := s.CreateAttachment(ctx, "attached.png", "image/png", []byte{3})
	if err != nil {
		t.Fatal(err)
	}
	p := mustProject(t, s, ctx, "p-sweep").ID
	if _, err := s.CreateTask(ctx, db.NewTask{ProjectID: p, Title: "t", Prompt: "p", RuntimeMode: "full-access", InteractionMode: "default",
		Workspace: db.Workspace{Type: "root"}, AttachmentIDs: []int64{attachedOld.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.Pool.ExecContext(ctx, `UPDATE task_attachments SET created_at = strftime('%Y-%m-%dT%H:%M:%f000Z', 'now', '-25 hours') WHERE id IN ($1, $2)`,
		old.ID, attachedOld.ID); err != nil {
		t.Fatal(err)
	}
	n, err := s.SweepStagedAttachments(ctx, 24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("swept %d, %v; want 1", n, err)
	}
	for id, want := range map[int64]bool{old.ID: false, fresh.ID: true, attachedOld.ID: true} {
		_, err := s.GetAttachment(ctx, id)
		if (err == nil) != want {
			t.Fatalf("attachment %d exists=%v, want %v", id, err == nil, want)
		}
	}
}

func acceptTask(t *testing.T, s *db.Store, ctx context.Context, id int64) {
	t.Helper()
	p, err := s.CompletionPreview(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeAcceptance(ctx, id, db.AcceptanceRequest{RequestID: fmt.Sprintf("accept-%d-%d", id, p.Revision), Revision: p.Revision, EvidenceVersion: p.EvidenceVersion}, false); err != nil {
		t.Fatal(err)
	}
}

func reopenTask(t *testing.T, s *db.Store, ctx context.Context, id int64) {
	t.Helper()
	p, err := s.CompletionPreview(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeAcceptance(ctx, id, db.AcceptanceRequest{RequestID: fmt.Sprintf("reopen-%d-%d", id, p.Revision), Revision: p.Revision}, true); err != nil {
		t.Fatal(err)
	}
}

// needs sets id's needs (failing the test on error).
func needs(t *testing.T, s *db.Store, ctx context.Context, id int64, ids ...int64) db.Task {
	t.Helper()
	got, err := s.SetNeeds(ctx, id, ids)
	if err != nil {
		t.Fatalf("set needs of %d to %v: %v", id, ids, err)
	}
	return got
}

func note(t *testing.T, s *db.Store, ctx context.Context, owner db.JournalOwner, text, key string) db.JournalEntry {
	t.Helper()
	e, err := s.AppendNote(ctx, owner, db.AppendNoteInput{Text: text, RequestID: key})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func mustSchedule(t *testing.T, s *db.Store, ctx context.Context, projectID int64, cron string) db.Schedule {
	t.Helper()
	sc, err := s.CreateSchedule(ctx, db.NewSchedule{
		ProjectID: projectID, Title: "nightly", Prompt: "p", RuntimeMode: "full-access",
		InteractionMode: "default", Workspace: db.Workspace{Type: "root"},
		Cron: cron, Tz: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sc.NextRunAt == nil {
		t.Fatal("new schedule has no nextRunAt")
	}
	return sc
}

func mustFire(t *testing.T, s *db.Store, ctx context.Context, id int64, now time.Time) db.FireResult {
	t.Helper()
	res, err := s.FireSchedule(ctx, id, now, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
