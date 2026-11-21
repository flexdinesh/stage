package db

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestMachineSettingsMigrationKeepsSingleMachineDataInPlace(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 17)
	mustExec(t, pool, `UPDATE harness_connections SET identity='env-a',label='box a' WHERE id=1`)
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness,identity,label) VALUES(7,'opencode','http://oc:4096','oc')`)
	mustExec(t, pool, `UPDATE t3_connection SET client_id='c',token='keep',t3_url='http://t3:3773',last_full_sync_at='2026-11-01T00:00:00Z',last_error='boom' WHERE id=1`)
	mustExec(t, pool, `UPDATE opencode_settings SET endpoint='http://oc:4096',username='u',password='p',auth='basic' WHERE id=1`)
	migrateTo(t, pool, MaxVersion())
	if n := foreignKeyViolations(t, pool); n != 0 {
		t.Fatalf("%d foreign key violations", n)
	}
	s := NewStore(pool)
	ctx := t.Context()
	if s.LegacyT3ID() != 1 || s.ActiveID(LocalMachine, HarnessOpenCode) != 7 {
		t.Fatalf("connection ids t3=%d opencode=%d", s.LegacyT3ID(), s.ActiveID(LocalMachine, HarnessOpenCode))
	}
	tok, err := s.LoadToken(ctx, LocalMachine)
	if err != nil || tok.ClientID != "c" || tok.Token != "keep" {
		t.Fatalf("token %+v %v", tok, err)
	}
	c, err := s.GetConnection(ctx, LocalMachine)
	if err != nil || c.T3URL == nil || *c.T3URL != "http://t3:3773" || c.LastFullSyncAt == nil || c.LastError == nil || *c.LastError != "boom" || c.EnvironmentID == nil || *c.EnvironmentID != "env-a" || c.ConnectionID != 1 {
		t.Fatalf("t3 setup %+v %v", c, err)
	}
	oc, cred, verified, err := s.OpenCodeSettings(ctx, LocalMachine)
	if err != nil || oc.Endpoint != "http://oc:4096" || cred.Username != "u" || cred.Password != "p" || oc.Auth != config.OpenCodeAuthBasic || verified != "http://oc:4096" {
		t.Fatalf("opencode %+v %q %v", oc, verified, err)
	}
}

func TestTwoMachinesEachHaveTheirOwnActiveConnectionsAndSettings(t *testing.T) {
	s, ctx, pool := connectionStore(t)
	mustExec(t, pool, `INSERT INTO machines(id,label) VALUES(2,'Laptop')`)
	const laptop int64 = 2
	local := s.LegacyT3ID()
	if _, err := s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "env-a", "box a", 0); err != nil {
		t.Fatal(err)
	}
	// The laptop's first T3 is a new connection; the local one is untouched.
	sw, err := s.SwitchInstance(ctx, laptop, t3.Descriptor, "env-b", "laptop b", 0)
	if err != nil || sw.Changed || sw.Old != 0 || sw.New == 0 || sw.New == local {
		t.Fatalf("laptop T3 %+v %v", sw, err)
	}
	remote := sw.New
	if s.ActiveID(LocalMachine, HarnessT3) != local || s.ActiveID(laptop, HarnessT3) != remote || s.LegacyT3ID() != local {
		t.Fatalf("active local=%d laptop=%d legacy=%d", s.ActiveID(LocalMachine, HarnessT3), s.ActiveID(laptop, HarnessT3), s.LegacyT3ID())
	}
	if s.ConnectionMachine(remote) != laptop || s.ConnectionMachine(local) != LocalMachine || !s.ConnectionActive(local) || !s.ConnectionActive(remote) {
		t.Fatal("connection machines")
	}
	if got := s.ActiveConnections(); !slices.Contains(got, ActiveConnection{ID: local, Machine: LocalMachine, Harness: HarnessT3}) || !slices.Contains(got, ActiveConnection{ID: remote, Machine: laptop, Harness: HarnessT3}) {
		t.Fatalf("active connections %+v", got)
	}

	// Legacy thread ids keep mapping to the local machine's T3.
	project := projectWithBinding(t, s, local, "/w/local")
	mustExec(t, pool, `INSERT INTO tasks(project_id,title,prompt,state,t3_thread_id,origin_thread_id,connection_id) VALUES($1,'t','p','running','thread-l','origin-l',$2)`, project, local)
	var origin int64
	if err := pool.QueryRow(`SELECT (SELECT connection_id FROM session_identities WHERE id=t.origin_session_id) FROM tasks t WHERE t3_thread_id='thread-l'`).Scan(&origin); err != nil || origin != local {
		t.Fatalf("legacy origin on %d, want %d (%v)", origin, local, err)
	}
	if err := s.UpsertThreads(ctx, t3.Observations([]t3.Thread{{ThreadID: "mirror-1", ProjectID: "p", Title: "Mirror", Status: t3.StatusRunning, CreatedAt: time.Now().UTC()}})); err != nil {
		t.Fatal(err)
	}
	// Another T3 connection never writes the local mirror's watcher state.
	if err := s.SetObservedState(ctx, SessionRef{ConnectionID: remote, ExternalSessionID: "mirror-1"}, StateDone); err != nil {
		t.Fatal(err)
	}
	var observed *string
	if err := pool.QueryRow(`SELECT observed_state FROM threads WHERE t3_thread_id='mirror-1'`).Scan(&observed); err != nil || observed != nil {
		t.Fatalf("remote wrote mirror state %v %v", observed, err)
	}
	if err := s.SetObservedState(ctx, SessionRef{ConnectionID: local, ExternalSessionID: "mirror-1"}, StateDone); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(`SELECT observed_state FROM threads WHERE t3_thread_id='mirror-1'`).Scan(&observed); err != nil || observed == nil || *observed != string(StateDone) {
		t.Fatalf("local mirror state %v %v", observed, err)
	}

	// Replacing the laptop's instance retires only the laptop's connection.
	projectWithBinding(t, s, remote, "/w/laptop")
	sw, err = s.SwitchInstance(ctx, laptop, t3.Descriptor, "env-c", "laptop c", 0)
	if err != nil || !sw.Changed || sw.Old != remote {
		t.Fatalf("laptop replace %+v %v", sw, err)
	}
	replacement := sw.New
	if s.ActiveID(LocalMachine, HarnessT3) != local || !s.ConnectionActive(local) || s.ConnectionActive(remote) {
		t.Fatal("local T3 changed with the laptop's")
	}
	if ids, err := s.RetiredConnections(ctx, laptop, HarnessT3); err != nil || len(ids) != 1 || ids[0] != remote {
		t.Fatalf("laptop retired %v %v", ids, err)
	}
	if ids, err := s.RetiredConnections(ctx, LocalMachine, HarnessT3); err != nil || len(ids) != 0 {
		t.Fatalf("local retired %v %v", ids, err)
	}
	// A connection of another machine is never revived.
	var conflict *ConflictError
	if _, err := s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "", "", remote); !errors.As(err, &conflict) {
		t.Fatalf("revived another machine's connection: %v", err)
	}
	// The same environment id on two machines is two connections (the
	// laptop's unused replacement is restamped in place).
	if sw, err := s.SwitchInstance(ctx, laptop, t3.Descriptor, "env-a", "laptop a", 0); err != nil || sw.New != replacement || sw.New == local {
		t.Fatalf("same identity on two machines %+v %v", sw, err)
	}

	// Settings are per machine.
	if err := s.SetT3URL(ctx, laptop, "http://laptop:3773"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveToken(ctx, laptop, execution.Credential{ClientID: "lc", Token: "laptop-token"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLastFullSync(ctx, laptop, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if c, err := s.GetConnection(ctx, LocalMachine); err != nil || c.T3URL != nil || c.LastFullSyncAt != nil || c.ConnectionID != local {
		t.Fatalf("local T3 setup %+v %v", c, err)
	}
	if tok, err := s.LoadToken(ctx, LocalMachine); err != nil || tok.Token != "" {
		t.Fatalf("local token %+v %v", tok, err)
	}
	m := s.OnMachine(laptop)
	if c, err := m.GetConnection(ctx); err != nil || c.T3URL == nil || *c.T3URL != "http://laptop:3773" || c.LastFullSyncAt == nil || c.ConnectionID != replacement || m.ActiveID(HarnessT3) != replacement || c.EnvironmentID == nil || *c.EnvironmentID != "env-a" {
		t.Fatalf("laptop T3 setup %+v %v", c, err)
	}
	if tok, err := m.LoadToken(ctx); err != nil || tok.Token != "laptop-token" {
		t.Fatalf("laptop token %+v %v", tok, err)
	}

	// OpenCode: one active connection and one settings row per machine.
	ocLocal, err := s.EnsureOpenCode(ctx, LocalMachine)
	if err != nil {
		t.Fatal(err)
	}
	ocLaptop, err := m.EnsureOpenCode(ctx)
	if err != nil || ocLaptop == ocLocal || s.ConnectionMachine(ocLaptop) != laptop {
		t.Fatalf("laptop opencode %d (local %d) %v", ocLaptop, ocLocal, err)
	}
	if again, _ := m.EnsureOpenCode(ctx); again != ocLaptop {
		t.Fatalf("second ensure %d", again)
	}
	if err := m.SaveOpenCodeSettings(ctx, config.OpenCodeSetup{Endpoint: "http://laptop:4096", Auth: config.OpenCodeAuthBasic}, config.OpenCodeCredential{Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	if c, cred, _, err := s.OpenCodeSettings(ctx, LocalMachine); err != nil || c.Endpoint != "" || cred.Password != "" || c.Auth != config.OpenCodeAuthNone {
		t.Fatalf("local opencode %+v %v", c, err)
	}
	if c, cred, _, err := m.OpenCodeSettings(ctx); err != nil || c.Endpoint != "http://laptop:4096" || cred.Password != "secret" {
		t.Fatalf("laptop opencode %+v %v", c, err)
	}

	// Shared paths translate within their machine only.
	if err := s.SetSharedPath(ctx, SharedPath{ConnectionID: ocLaptop, HarnessRoot: "/srv", StageRoot: "/home"}); err != nil {
		t.Fatal(err)
	}
	if paths, err := s.MachineSharedPaths(ctx, LocalMachine); err != nil || len(paths) != 0 {
		t.Fatalf("local paths %+v %v", paths, err)
	}
	if paths, err := s.MachineSharedPaths(ctx, laptop); err != nil || len(paths) != 1 || paths[0].ConnectionID != ocLaptop {
		t.Fatalf("laptop paths %+v %v", paths, err)
	}
	if paths, err := s.SharedPaths(ctx); err != nil || len(paths) != 1 {
		t.Fatalf("all paths %+v %v", paths, err)
	}
	if n := foreignKeyViolations(t, pool); n != 0 {
		t.Fatalf("%d foreign key violations", n)
	}
}
