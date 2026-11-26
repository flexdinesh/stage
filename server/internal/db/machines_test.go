package db

import (
	"errors"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/sqlite"

	"github.com/flexdinesh/stage/server/internal/harness"
)

// Migration 0015: every connection moves to the local machine (seeded as
// "This computer"); T3 identities are stable; harness kinds are open text;
// one active connection per machine and kind.
func seedV0015Machines(t *testing.T, pool *sqlite.DB) {
	mustExec(t, pool, `UPDATE harness_connections SET identity='env-a',label='box a' WHERE id=1`)
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness,identity,label,active,retired_at) VALUES(4,'t3','env-old','old',0,stage_now())`)
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness,identity,label,auto_complete) VALUES(7,'opencode','http://oc:4096','oc',0)`)
	mustExec(t, pool, `INSERT INTO projects(id,t3_project_id,title,workspace_root,source) VALUES(1,'p','P','/w','app')`)
	mustExec(t, pool, `INSERT INTO project_bindings(project_id,connection_id,external_project_id,workspace) VALUES(1,7,'oc','/w')`)
	mustExec(t, pool, `INSERT INTO session_identities(id,connection_id,external_id) VALUES(5,7,'ses_x')`)
}

func checkV0015Machines(t *testing.T, pool *sqlite.DB) {
	if n := foreignKeyViolations(t, pool); n != 0 {
		t.Fatalf("%d foreign key violations", n)
	}
	wantRows(t, pool, `SELECT id,label FROM machines`, "1|This computer")
	wantRows(t, pool, `SELECT id,machine_id,harness,identity,stable_identity,label,active,auto_complete FROM harness_connections ORDER BY id`,
		"1|1|t3|env-a|1|box a|1|1", "4|1|t3|env-old|1|old|0|1", "7|1|opencode|http://oc:4096|0|oc|1|0")
	wantRows(t, pool, `SELECT connection_id,external_project_id FROM project_bindings`, "7|oc")
	wantRows(t, pool, `SELECT id,connection_id,external_id FROM session_identities WHERE id=5`, "5|7|ses_x")
	// The registry, not the schema, decides which kinds exist.
	mustExec(t, pool, `INSERT INTO harness_connections(harness) VALUES('another')`)
	mustFail(t, pool, "second active connection of a kind on one machine", `INSERT INTO harness_connections(harness) VALUES('opencode')`)
	// New AUTOINCREMENT ids never reuse an old one.
	var next int64
	if err := pool.QueryRow(`INSERT INTO harness_connections(harness,active,retired_at) VALUES('t3',0,stage_now()) RETURNING id`).Scan(&next); err != nil || next <= 8 {
		t.Fatalf("next id %d %v", next, err)
	}
}

// After 0015, the store reads machine-scoped connections at the latest
// schema.
func TestMachinesMigrationKeepsConnectionsAndOpensKinds(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 14)
	seedV0015Machines(t, pool)
	migrateTo(t, pool, sqlite.MaxVersion())
	if n := foreignKeyViolations(t, pool); n != 0 {
		t.Fatalf("%d foreign key violations", n)
	}
	s := NewStore(pool)
	ctx := t.Context()
	cs, err := s.Connections(ctx)
	if err != nil || len(cs) != 3 {
		t.Fatalf("connections %+v %v", cs, err)
	}
	for _, c := range cs {
		if c.MachineID != LocalMachine {
			t.Fatalf("machine %+v", c)
		}
	}
	if c, _ := s.GetHarnessConnection(ctx, 7); c.Harness != "opencode" || c.AutoComplete || c.Label != "oc" || c.Bindings != 1 {
		t.Fatalf("opencode %+v", c)
	}
}

func TestSwitchInstanceRevivesByIdentityOnlyForStableKinds(t *testing.T) {
	s, ctx, _ := connectionStore(t)
	stable := harness.Descriptor{Kind: "stable", Name: "Stable", Hosting: harness.Connected, StableIdentity: true}
	loose := harness.Descriptor{Kind: "loose", Name: "Loose", Hosting: harness.Connected}
	for _, d := range []harness.Descriptor{stable, loose} {
		first, err := s.SwitchInstance(ctx, LocalMachine, d, "id-a", "a", 0)
		if err != nil || first.New == 0 {
			t.Fatalf("%s first %+v %v", d.Kind, first, err)
		}
		projectWithBinding(t, s, first.New, "/w/"+d.Kind)
		second, err := s.SwitchInstance(ctx, LocalMachine, d, "id-b", "b", 0)
		if err != nil || !second.Changed || second.Old != first.New {
			t.Fatalf("%s replace %+v %v", d.Kind, second, err)
		}
		projectWithBinding(t, s, second.New, "/w/"+d.Kind+"-b")
		back, err := s.SwitchInstance(ctx, LocalMachine, d, "id-a", "a", 0)
		if err != nil || !back.Changed {
			t.Fatalf("%s back %+v %v", d.Kind, back, err)
		}
		// A stable identity names the same instance: its connection returns.
		// Otherwise the same address may be another instance: a new one.
		if revived := back.New == first.New; revived != d.StableIdentity || back.Revived != d.StableIdentity {
			t.Fatalf("%s back %+v (first %d)", d.Kind, back, first.New)
		}
		if s.ActiveID(LocalMachine, d.Kind) != back.New {
			t.Fatalf("%s active %d", d.Kind, s.ActiveID(LocalMachine, d.Kind))
		}
	}
	var conflict *ConflictError
	if _, err := s.SwitchInstance(ctx, LocalMachine, loose, "", "", 99); !errors.As(err, &conflict) {
		t.Fatalf("unknown revive: %v", err)
	}
}

// Migration 0022: the local machine's seeded label is unset; a label the
// user chose stays.
func seedV0022LocalMachineLabel(label string) func(*testing.T, *sqlite.DB) {
	return func(t *testing.T, pool *sqlite.DB) {
		mustExec(t, pool, `UPDATE machines SET label=$1 WHERE id=1`, label)
	}
}

func checkV0022LocalMachineLabel(label string) func(*testing.T, *sqlite.DB) {
	return func(t *testing.T, pool *sqlite.DB) {
		wantRows(t, pool, `SELECT id,label FROM machines`, "1|"+label)
	}
}
