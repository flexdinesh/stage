package engine_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestT3SessionSurface(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		ctx := t.Context()
		pool := testDB.Reset(t)
		const connectionID = dbtest.T3Connection
		if _, err := pool.ExecContext(ctx, "UPDATE harness_connections SET machine_id=$1 WHERE id=$2", p.machine(t, pool), connectionID); err != nil {
			t.Fatal(err)
		}
		store := db.NewStore(pool)
		fake := t3.NewFake()
		project := fake.AddProject("Scratch", "/scratch")
		proj, err := store.InsertProject(ctx, db.Project{Title: "Scratch", Bindings: []db.ProjectBinding{
			{ConnectionID: connectionID, ExternalProjectID: project.ID, Workspace: project.WorkspaceRoot},
		}})
		if err != nil {
			t.Fatal(err)
		}
		svc := p.engine(t, store, t3Harnesses, map[int64]execution.Adapter{connectionID: t3.NewConnectionAdapter(fake, connectionID)}, nil)
		task, err := store.CreateTask(ctx, db.NewTask{ProjectID: proj.ID, ConnectionID: connectionID, Title: "T3 work", Prompt: "Start"})
		if err != nil {
			t.Fatal(err)
		}
		running, err := svc.Coordinator.StartTask(ctx, task.ID)
		if err != nil || running.Session == nil || running.State != db.StateRunning {
			t.Fatal(running, err)
		}
		thread := running.Session.ExternalSessionID
		fake.AddItem(thread, t3.TimelineItem{Type: "user_message", Text: task.Prompt})
		fake.AddItem(thread, t3.TimelineItem{Type: "file_change", Text: "scratch.txt"})
		activity, err := svc.Coordinator.Activity(ctx, task.ID, nil, execution.ActivityQuery{Limit: 1})
		if err != nil || len(activity.Items) != 1 || activity.Items[0].Kind != "file_change" ||
			!reflect.DeepEqual(activity.Files, []execution.FileChange{{Path: "scratch.txt", CountsUnknown: true}}) || !activity.Truncated {
			t.Fatal(activity, err)
		}
		approval, err := fake.AddApproval(thread)
		if err != nil {
			t.Fatal(err)
		}
		if requests, err := svc.Coordinator.Requests(ctx, task.ID); err != nil || len(requests) != 0 {
			t.Fatal("approval listed as a question", requests, err)
		}
		if err := fake.ResolveRequest(thread, approval, "resolved"); err != nil {
			t.Fatal(err)
		}
		question, err := fake.AddUserInput(thread, []t3.Question{{
			ID: "choice", Question: "Which checks?", MultiSelect: true,
			Options: []t3.QuestionOption{{Label: "Unit"}, {Label: "Integration"}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		requests, err := svc.Coordinator.Requests(ctx, task.ID)
		if err != nil || len(requests) != 1 || requests[0].ID != question || len(requests[0].Questions) != 1 || !requests[0].Questions[0].Multi {
			t.Fatal(requests, err)
		}
		answer := execution.Response{Answers: map[string][]string{"choice": {"Unit", "Integration"}}}
		if _, err := svc.Coordinator.Respond(ctx, task.ID, question, answer, nil); err != nil {
			t.Fatal(err)
		}
		for _, delivery := range []execution.Delivery{execution.DeliverySteer, execution.DeliveryQueue} {
			if _, err := svc.Coordinator.Send(ctx, task.ID, execution.Message{Text: string(delivery), Delivery: delivery}, nil); err != nil {
				t.Fatal(delivery, err)
			}
		}
		digest := execution.Message{Text: "tree update", RequestID: "steering-1-2"}
		if _, err := svc.Coordinator.SendRecorded(ctx, task.ID, digest, func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if err := fake.SetStatus(thread, t3.StatusCompleted); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := svc.Coordinator.Send(ctx, task.ID, execution.Message{Text: "follow up"}, nil); err != nil {
			t.Fatal(err)
		}
		if len(fake.Sent) != 4 || fake.Sent[0].Mode != t3.SendSteer || fake.Sent[1].Mode != t3.SendQueue ||
			fake.Sent[2].Mode != t3.SendQueue || fake.Sent[2].ClientRequestID != digest.RequestID || fake.Sent[3].Mode != t3.SendQueue {
			t.Fatalf("T3 delivery %+v", fake.Sent)
		}
	})
}
