package main

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/auth"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/instance"
)

// Restore a snapshot with shared accounts and credentials, taken before
// the attribution migration.
const restoredVersion = 43

const restoreEmail = "restore@example.test"
const restorePassword = "lavender opens winter shutters"

type restoredAccessFixture struct {
	state       store.InstallationAccess
	project     db.Project
	machine     db.MachineGrant
	invite      auth.IssuedGrant
	reset       auth.Link
	credentials []auth.SignInResult
}

func seedRestoredAccess(t *testing.T, engine store.Engine) restoredAccessFixture {
	t.Helper()
	ctx := t.Context()
	database := db.NewStore(engine)
	project, err := database.InsertProject(ctx, db.Project{Title: "Project from backup"})
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := database.CreatePairing(ctx, "Restored worker")
	if err != nil {
		t.Fatal(err)
	}
	machine, err := database.RedeemPairing(ctx, pairing.Code, "Restored worker", "test-worker")
	if err != nil {
		t.Fatal(err)
	}
	svc := auth.New(database, "https://stage.example.test", nil)
	shared := true
	_, setup, err := svc.Start(ctx, &shared)
	if err != nil {
		t.Fatal(err)
	}
	browser, err := svc.ClaimSetup(ctx, restoreLinkToken(t, setup), restoreEmail, "Restored Owner", restorePassword)
	if err != nil || browser.Account.ID != db.DefaultUser {
		t.Fatalf("claim user 1: %v", err)
	}
	cli, err := svc.SignIn(ctx, restoreEmail, restorePassword, netip.Addr{}, store.SessionCLI)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := svc.RotateAgent(ctx, cli.Token.Reveal(), store.SessionCLI)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := svc.Invite(ctx, browser.Account.ID, "invited@example.test")
	if err != nil {
		t.Fatal(err)
	}
	reset, err := svc.RecoverAdmin(ctx, restoreEmail)
	if err != nil {
		t.Fatal(err)
	}
	state, err := database.ConfigureAccess(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return restoredAccessFixture{
		state: state, project: project, machine: machine, invite: invite, reset: reset,
		credentials: []auth.SignInResult{browser, cli, agent},
	}
}

func (fixture restoredAccessFixture) verify(t *testing.T, eng store.Engine, invalidated bool) {
	t.Helper()
	ctx := t.Context()
	database := db.NewStore(eng)
	state, err := database.ConfigureAccess(ctx, nil)
	if err != nil || state.ID != fixture.state.ID || state.Mode != store.AccessClaimed {
		t.Fatalf("restored installation: %+v %v", state, err)
	}
	project, err := database.GetProject(ctx, fixture.project.ID)
	if err != nil || project.Title != fixture.project.Title {
		t.Fatalf("restored project: %+v %v", project, err)
	}
	machine, err := database.AuthenticateMachine(ctx, fixture.machine.Token)
	if err != nil || machine != fixture.machine.MachineID {
		t.Fatalf("restored worker pairing: %d %v", machine, err)
	}
	svc := auth.New(database, "https://stage.example.test", nil)
	for _, credential := range fixture.credentials {
		_, err := svc.Authenticate(ctx, credential.Token.Reveal(), credential.Session.Audience, false)
		if !invalidated && err != nil || invalidated && !errors.Is(err, auth.ErrSession) {
			t.Fatalf("restored %s session: %v", credential.Session.Audience, err)
		}
	}
	_, err = svc.InspectGrant(ctx, restoreLinkToken(t, fixture.invite.Link), store.GrantInvite)
	if !invalidated && err != nil || invalidated && !errors.Is(err, auth.ErrInvite) {
		t.Fatalf("restored invitation: %v", err)
	}
	_, err = svc.InspectGrant(ctx, restoreLinkToken(t, fixture.reset), store.GrantPasswordReset)
	if !invalidated && err != nil || invalidated && !errors.Is(err, auth.ErrReset) {
		t.Fatalf("restored reset: %v", err)
	}
	if invalidated {
		user, err := svc.SignIn(ctx, restoreEmail, restorePassword, netip.Addr{}, store.SessionBrowser)
		if err != nil || user.Account.ID != db.DefaultUser || user.Account.Role != store.RoleAdmin {
			t.Fatalf("restored admin sign-in: %v", err)
		}
	}
}

func restoreLinkToken(t *testing.T, link auth.Link) string {
	t.Helper()
	_, token, ok := strings.Cut(link.URL(), "#token=")
	if !ok || token == "" {
		t.Fatal("missing token in account link")
	}
	return token
}

func TestDBInvalidateAccessSQLite(t *testing.T) {
	ctx := t.Context()
	source := t.TempDir()
	database, err := sqlite.Open(ctx, filepath.Join(source, sqlite.DBFile))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := sqlite.MigrateTo(ctx, database, restoredVersion); err != nil {
		t.Fatal(err)
	}
	fixture := seedRestoredAccess(t, database)
	backup, err := sqlite.Backup(ctx, database, source, restoredVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	restoredDir := t.TempDir()
	restoredPath := filepath.Join(restoredDir, sqlite.DBFile)
	if err := os.Rename(backup, restoredPath); err != nil {
		t.Fatal(err)
	}
	_, environ := env(t)
	environ = append(environ, "STAGE_DATA_DIR="+restoredDir)
	holder, err := instance.Lock(restoredDir)
	if err != nil {
		t.Fatal(err)
	}
	code, stderr := runMain(t, environ, "db", "invalidate-access")
	holder.Release()
	if code != 75 || !strings.Contains(stderr, "in use by another stage server") {
		t.Fatalf("held SQLite directory: %d %s", code, stderr)
	}
	if code, stderr := runMain(t, environ, "db", "invalidate-access"); code != 0 {
		t.Fatalf("invalidate restored SQLite access: %d %s", code, stderr)
	}
	restored, err := sqlite.Open(ctx, restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	version, err := restored.Migrator().Current(ctx)
	if err != nil || version != restored.Migrator().Max() {
		t.Fatalf("restored schema version: %d %v", version, err)
	}
	fixture.verify(t, restored, true)
	fixture.verify(t, database, false)
}

func TestDBInvalidateAccessRefusesMissingOrNewerDatabase(t *testing.T) {
	_, environ := env(t)
	if code, stderr := runMain(t, environ, "db", "invalidate-access"); code != 78 || !strings.Contains(stderr, "Set STAGE_DATA_DIR") {
		t.Fatalf("ambient data directory: %d %s", code, stderr)
	}
	dir := t.TempDir()
	environ = append(environ, "STAGE_DATA_DIR="+dir)
	path := filepath.Join(dir, sqlite.DBFile)
	if code, stderr := runMain(t, environ, "db", "invalidate-access"); code != 78 || !strings.Contains(stderr, "Restore stage.db") {
		t.Fatalf("missing database: %d %s", code, stderr)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing database was created: %v", err)
	}
	database, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrator().Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), "INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)", database.Migrator().Max()+1); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if code, stderr := runMain(t, environ, "db", "invalidate-access"); code != 78 || !strings.Contains(stderr, "is newer than this Stage") {
		t.Fatalf("newer database: %d %s", code, stderr)
	}
}
