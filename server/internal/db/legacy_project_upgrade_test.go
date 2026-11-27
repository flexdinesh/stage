package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/dbtest/migrationfixture"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestLegacyProjectUpgradeSQLite(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 46)
	checkLegacyProjectUpgrade(t, pool.DB, pool)
}

// checkLegacyProjectUpgrade starts at 0046. The same legacy installation
// upgrades to head and then goes through the owner claim on both engines.
func checkLegacyProjectUpgrade(t *testing.T, pool *sql.DB, engine store.Engine) {
	t.Helper()
	ctx := t.Context()
	s := NewStore(engine)
	personal, err := s.ConfigureAccess(ctx, nil)
	if err != nil || personal.Mode != store.AccessPersonal || personal.ID == "" {
		t.Fatalf("legacy personal installation: %+v, %v", personal, err)
	}
	pairing, err := s.CreatePairing(ctx, "Worker")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.RedeemPairing(ctx, pairing.Code, "", "legacy")
	if err != nil || worker.MachineID != 2 {
		t.Fatalf("pair legacy worker: machine %d, %v", worker.MachineID, err)
	}
	migrationfixture.SeedLegacyProjectGroups(t, func(query string) error {
		_, err := pool.ExecContext(ctx, query)
		return err
	})
	if err := engine.Migrator().Up(ctx); err != nil {
		t.Fatal(err)
	}
	checkUpgradedProjectHistory(t, s)

	shared := true
	pending, err := s.ConfigureAccess(ctx, &shared)
	if err != nil || pending.Mode != store.AccessPending || pending.ID != personal.ID || pending.ClaimedAt != nil {
		t.Fatalf("upgraded setup-pending installation: %+v, %v", pending, err)
	}
	digest := store.TokenDigest{47, 48}
	now := time.Date(2026, 11, 27, 1, 0, 0, 0, time.UTC)
	if err := s.WriteAccess(ctx, func(r store.Access) error {
		_, err := r.InsertGrant(ctx, digest, store.AuthGrant{Purpose: store.GrantSetup, CreatedAt: now, ExpiresAt: now.Add(time.Minute)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	owner, err := s.ClaimInstallation(ctx, digest, store.AccountInput{DisplayName: "Owner", Email: "owner@example.test"}, "test verifier", now)
	if err != nil || owner.ID != 1 || owner.Role != store.RoleAdmin || owner.DisplayName != "Owner" {
		t.Fatalf("claim legacy owner: %+v, %v", owner, err)
	}
	claimed, err := s.ConfigureAccess(ctx, nil)
	if err != nil || claimed.Mode != store.AccessClaimed || claimed.ID != personal.ID || claimed.ClaimedAt == nil || !claimed.ClaimedAt.Equal(now) {
		t.Fatalf("claimed upgraded installation: %+v, %v", claimed, err)
	}
	if got, err := s.AuthenticateMachine(ctx, worker.Token); err != nil || got != 2 {
		t.Errorf("authenticate pre-upgrade worker: machine %d, %v", got, err)
	}
	joined, err := s.PairingStatus(ctx, pairing.ID)
	if err != nil || joined.State != "joined" || joined.MachineID == nil || *joined.MachineID != 2 {
		t.Errorf("pre-upgrade pairing: %+v, %v", joined, err)
	}
	if got, err := s.GetMachineInfo(ctx, 2); err != nil || got.CreatedBy != owner.ID || got.Label != "Worker" || got.Version != "legacy" {
		t.Errorf("pre-upgrade worker: %+v, %v", got, err)
	}
	checkUpgradedProjectHistory(t, s)
	migrationfixture.CheckProjectGroups(t, func(query string) (int, error) {
		var n int
		err := pool.QueryRowContext(ctx, query).Scan(&n)
		return n, err
	})

	_, taskErr := s.TransitionTask(ctx, Transition{TaskID: 7, To: StateLaunching, BeginAttempt: true})
	_, scheduleErr := s.RunScheduleNow(ctx, 7)
	for operation, err := range map[string]error{"launch unresolved task": taskErr, "run unresolved schedule": scheduleErr} {
		var conflict *ConflictError
		if !errors.As(err, &conflict) || conflict.Code != "destination_required" {
			t.Errorf("%s: %v, want destination_required", operation, err)
		}
	}
}

func checkUpgradedProjectHistory(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	group, err := s.GetProjectGroup(ctx, 39)
	if err != nil || group.ID != 4 || !slices.ContainsFunc(group.Members, func(m GroupWorkspace) bool {
		return m.MachineID == 2 && m.ConnectionID == 5 && m.WorkspaceID == WorkspaceID(5, "clone", "/clone") && m.CatalogState == "retired"
	}) {
		t.Errorf("group through old alias: %+v, %v", group, err)
	}
	if archived, err := s.GetProjectGroup(ctx, 5); err != nil || archived.ID != 5 || archived.ArchivedAt == nil {
		t.Errorf("archived project: %+v, %v", archived, err)
	}
	for id, project := range map[int64]int64{12: 4, 13: 5} {
		if snippet, err := s.GetSnippet(ctx, id); err != nil || snippet.ProjectID == nil || *snippet.ProjectID != project {
			t.Errorf("snippet %d: %+v, %v, want project %d", id, snippet, err, project)
		}
	}

	for _, want := range []struct {
		id, project, connection, creator int64
		workspace                        string
	}{
		{6, 4, 4, 7, WorkspaceID(4, "root", "/root")},
		{7, 4, 5, 1, ""},
		{8, 4, 5, 1, WorkspaceID(5, "clone", "/original-clone")},
		{9, 5, 5, 1, ""},
	} {
		task, err := s.GetTask(ctx, want.id)
		if err != nil || task.ProjectID != want.project || task.ConnectionID != want.connection || task.CreatedBy != want.creator || task.WorkspaceID != want.workspace {
			t.Errorf("task %d: %+v, %v, want %+v", want.id, task, err, want)
		}
		if want.id == 9 && (task.ArchivedAt == nil || task.ConnectionActive || task.Session == nil || *task.Session != (SessionRef{ConnectionID: 5, ExternalSessionID: "old-session"})) {
			t.Errorf("archived task lost its disconnected session: %+v", task)
		}
	}
	for _, want := range []struct {
		id, connection, creator int64
		workspace               string
	}{
		{6, 4, 7, WorkspaceID(4, "root", "/root")},
		{7, 5, 1, ""},
	} {
		schedule, err := s.GetSchedule(ctx, want.id)
		if err != nil || schedule.ProjectID != 4 || schedule.ConnectionID != want.connection || schedule.CreatedBy != want.creator || schedule.WorkspaceID != want.workspace {
			t.Errorf("schedule %d: %+v, %v, want %+v", want.id, schedule, err, want)
		}
	}

	dest, err := s.LaunchDestination(ctx, 8, 1)
	wantDest := LaunchDestination{ConnectionID: 5, ExternalProjectID: "clone", Workspace: "/original-clone", BindingID: 9, BindingRevision: 3}
	if err != nil || dest != wantDest {
		t.Errorf("frozen destination: %+v, %v, want %+v", dest, err, wantDest)
	}
	attempt, err := s.Attempt(ctx, 8, 1)
	wantIntent := execution.Intent{RequestID: "old-launch", ConnectionID: 5, Project: "clone", Directory: "/original-clone",
		Title: "Frozen title", Prompt: "Frozen prompt ✓", Config: execution.Config{Model: "old-model"}}
	var checkpoint map[string]string
	if err != nil || attempt.Phase != "unconfirmed" || attempt.Revision != 2 || attempt.RetryCount != 1 ||
		attempt.Intent == nil || !reflect.DeepEqual(*attempt.Intent, wantIntent) || attempt.Checkpoint.Version != 1 ||
		json.Unmarshal(attempt.Checkpoint.Data, &checkpoint) != nil || !reflect.DeepEqual(checkpoint, map[string]string{"sessionId": "pending-session"}) ||
		attempt.Actor.Kind != "human" || attempt.Actor.UserID != 7 || attempt.SourceMachine == nil || *attempt.SourceMachine != 2 ||
		attempt.Cause == nil || attempt.Cause.Kind != "accepted" || attempt.Cause.RequestID != "old-caüse" {
		t.Errorf("frozen attempt: %+v, intent %+v, checkpoint %s, %v", attempt, attempt.Intent, attempt.Checkpoint.Data, err)
	}
}
