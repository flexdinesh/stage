package db

import (
	"errors"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/sqlite"

	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// Migration 0018: T3 and OpenCode settings move to per-machine rows on the
// local machine, unchanged; connection identities stay.
func seedV0018MachineSettings(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `UPDATE harness_connections SET identity='env-a',label='box a' WHERE id=1`)
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness,identity,label) VALUES(7,'opencode','http://oc:4096','oc')`)
	mustExec(t, pool, `UPDATE t3_connection SET client_id='c',token='keep',t3_url='http://t3:3773',last_full_sync_at='2026-11-01T00:00:00Z',last_error='boom' WHERE id=1`)
	mustExec(t, pool, `UPDATE opencode_settings SET endpoint='http://oc:4096',username='u',password='p',auth='basic' WHERE id=1`)
}

func checkV0018MachineSettings(t *testing.T, pool *sqlite.DB) {
	if n := foreignKeyViolations(t, pool); n != 0 {
		t.Fatalf("%d foreign key violations", n)
	}
	wantRows(t, pool, `SELECT machine_id,client_id,token,t3_url,last_full_sync_at,last_error FROM t3_connection`,
		"1|c|keep|http://t3:3773|2026-11-01T00:00:00Z|boom")
	wantRows(t, pool, `SELECT machine_id,endpoint,username,password,auth FROM opencode_settings`, "1|http://oc:4096|u|p|basic")
	wantRows(t, pool, `SELECT id,machine_id,harness,identity,active FROM harness_connections ORDER BY id`,
		"1|1|t3|env-a|1", "7|1|opencode|http://oc:4096|1")
	mustFail(t, pool, "settings for an unknown machine", `INSERT INTO t3_connection(machine_id) VALUES(9)`)
}

// After 0018, the store reads per-machine settings at the latest schema.
func TestMachineSettingsMigrationKeepsSingleMachineDataInPlace(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 17)
	seedV0018MachineSettings(t, pool)
	migrateTo(t, pool, sqlite.MaxVersion())
	if n := foreignKeyViolations(t, pool); n != 0 {
		t.Fatalf("%d foreign key violations", n)
	}
	s := NewStore(pool)
	ctx := t.Context()
	if s.ActiveID(LocalMachine, "t3") != 1 || s.ActiveID(LocalMachine, "opencode") != 7 {
		t.Fatalf("connection ids t3=%d opencode=%d", s.ActiveID(LocalMachine, "t3"), s.ActiveID(LocalMachine, "opencode"))
	}
	wantRows(t, pool, `SELECT settings FROM harness_settings WHERE machine_id=1 AND kind='t3'`,
		`{"url":"http://t3:3773","publicUrl":null}`)
	wantRows(t, pool, `SELECT value FROM harness_secrets WHERE machine_id=1 AND kind='t3'`,
		`{"clientId":"c","token":"keep","expiresAt":null}`)
	wantRows(t, pool, `SELECT last_full_sync_at,last_error FROM harness_sync_status WHERE machine_id=1 AND kind='t3'`,
		"2026-11-01T00:00:00Z|boom")
	wantRows(t, pool, `SELECT settings FROM harness_settings WHERE machine_id=1 AND kind='opencode'`,
		`{"endpoint":"http://oc:4096","browserEndpoint":"","webClient":"","auth":"basic"}`)
	wantRows(t, pool, `SELECT value FROM harness_secrets WHERE machine_id=1 AND kind='opencode'`,
		`{"username":"u","password":"p"}`)
	if _, identity, err := s.ActiveConnectionIdentity(ctx, LocalMachine, "opencode"); err != nil || identity != "http://oc:4096" {
		t.Fatalf("opencode identity %q %v", identity, err)
	}
}

func TestTwoMachinesEachHaveTheirOwnActiveConnections(t *testing.T) {
	s, ctx, pool := connectionStore(t)
	mustExec(t, pool, `INSERT INTO machines(id,label) VALUES(2,'Laptop')`)
	const laptop int64 = 2
	local := s.ActiveID(LocalMachine, "t3")
	if _, err := s.SwitchInstance(ctx, LocalMachine, t3.Descriptor, "env-a", "box a", 0); err != nil {
		t.Fatal(err)
	}
	// The laptop's first T3 is a new connection; the local one is untouched.
	sw, err := s.SwitchInstance(ctx, laptop, t3.Descriptor, "env-b", "laptop b", 0)
	if err != nil || sw.Changed || sw.Old != 0 || sw.New == 0 || sw.New == local {
		t.Fatalf("laptop T3 %+v %v", sw, err)
	}
	remote := sw.New
	if s.ActiveID(LocalMachine, "t3") != local || s.ActiveID(laptop, "t3") != remote || s.ActiveID(LocalMachine, "t3") != local {
		t.Fatalf("active local=%d laptop=%d legacy=%d", s.ActiveID(LocalMachine, "t3"), s.ActiveID(laptop, "t3"), s.ActiveID(LocalMachine, "t3"))
	}
	if s.ConnectionMachine(remote) != laptop || s.ConnectionMachine(local) != LocalMachine || !s.ConnectionActive(local) || !s.ConnectionActive(remote) {
		t.Fatal("connection machines")
	}
	if got := s.ActiveConnections(); !slices.Contains(got, ActiveConnection{ID: local, Machine: LocalMachine, Harness: "t3"}) || !slices.Contains(got, ActiveConnection{ID: remote, Machine: laptop, Harness: "t3"}) {
		t.Fatalf("active connections %+v", got)
	}

	// Replacing the laptop's instance retires only the laptop's connection.
	projectWithBinding(t, s, remote, "/w/laptop")
	sw, err = s.SwitchInstance(ctx, laptop, t3.Descriptor, "env-c", "laptop c", 0)
	if err != nil || !sw.Changed || sw.Old != remote {
		t.Fatalf("laptop replace %+v %v", sw, err)
	}
	replacement := sw.New
	if s.ActiveID(LocalMachine, "t3") != local || !s.ConnectionActive(local) || s.ConnectionActive(remote) {
		t.Fatal("local T3 changed with the laptop's")
	}
	if ids, err := s.RetiredConnections(ctx, laptop, "t3"); err != nil || len(ids) != 1 || ids[0] != remote {
		t.Fatalf("laptop retired %v %v", ids, err)
	}
	if ids, err := s.RetiredConnections(ctx, LocalMachine, "t3"); err != nil || len(ids) != 0 {
		t.Fatalf("local retired %v %v", ids, err)
	}
	// Even with continuity proof, a connection of another machine cannot be revived.
	proof := t3.Descriptor
	proof.ContinuityProof = true
	var conflict *ConflictError
	if _, err := s.SwitchInstance(ctx, LocalMachine, proof, "", "", remote); !errors.As(err, &conflict) {
		t.Fatalf("revived another machine's connection: %v", err)
	}
	// The same environment id on two machines is two connections (the
	// laptop's unused replacement is restamped in place).
	if sw, err := s.SwitchInstance(ctx, laptop, t3.Descriptor, "env-a", "laptop a", 0); err != nil || sw.New != replacement || sw.New == local {
		t.Fatalf("same identity on two machines %+v %v", sw, err)
	}

	oc := harness.Descriptor{Kind: "opencode"}
	ocLocal, err := s.SwitchInstance(ctx, LocalMachine, oc, "local", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	ocLaptop, err := s.SwitchInstance(ctx, laptop, oc, "laptop", "", 0)
	if err != nil || ocLaptop.New == ocLocal.New || s.ConnectionMachine(ocLaptop.New) != laptop {
		t.Fatalf("laptop opencode %+v (local %+v) %v", ocLaptop, ocLocal, err)
	}

	if n := foreignKeyViolations(t, pool); n != 0 {
		t.Fatalf("%d foreign key violations", n)
	}
}
