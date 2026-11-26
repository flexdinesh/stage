package db_test

import (
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// A fresh install has no connection and so no default harness (ADR 0007
// decision 12): nothing is T3 until a T3 connection is set up, and a task
// needs an explicit connection.
func TestFreshInstallHasNoConnection(t *testing.T) {
	s := db.NewStore(dbtest.FreshEngine(t))
	conns, err := s.Connections(t.Context())
	if err != nil || len(conns) != 0 {
		t.Fatalf("fresh install connections %+v %v", conns, err)
	}
	if id := s.ActiveID(db.LocalMachine, "t3"); id != 0 {
		t.Fatalf("fresh install T3 connection %d", id)
	}
	if active := s.ActiveConnections(); len(active) != 0 {
		t.Fatalf("fresh install active connections %+v", active)
	}
	p, err := s.InsertProject(t.Context(), db.Project{Title: "P"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTask(t.Context(), db.NewTask{ProjectID: p.ID, Title: "T", Prompt: "p"}); err == nil {
		t.Fatal("task created without a connection")
	}
	id, err := s.EnsureOpenCode(t.Context(), db.LocalMachine)
	if err != nil || s.Harness(id) != "opencode" {
		t.Fatalf("first connection %d %v", id, err)
	}
}

// Inbox titles come from the observation (no T3 mirror): a waiting session
// on any harness carries its observed title and its bound project's title.
func TestWaitingSessionTitlesFromObservation(t *testing.T) {
	s := db.NewStore(dbtest.FreshEngine(t))
	ctx := t.Context()
	conn, err := s.EnsureOpenCode(ctx, db.LocalMachine)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertProject(ctx, db.Project{Title: "Stage P", Bindings: []db.ProjectBinding{{ConnectionID: conn, ExternalProjectID: "ext-p", Workspace: "/w"}}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ref := execution.SessionRef{ConnectionID: conn, ExternalSessionID: "ses-1"}
	if err := s.PutObservation(ctx, execution.Observation{Session: ref, Project: "ext-p", Title: "Plan the release", State: "needs_input", Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.WaitingWithoutNotifications(ctx, 0)
	if err != nil || len(waiting) != 1 {
		t.Fatalf("waiting %+v %v", waiting, err)
	}
	w := waiting[0]
	if w.ConnectionID != conn || w.ThreadID != "ses-1" || w.Title != "Plan the release" || w.ProjectTitle == nil || *w.ProjectTitle != "Stage P" {
		t.Fatalf("waiting session %+v", w)
	}
}
