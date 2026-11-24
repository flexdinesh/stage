package db

import (
	"errors"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"testing"

	"github.com/flexdinesh/stage/server/internal/harness"
)

func TestMachinesMigrationKeepsConnectionsAndOpensKinds(t *testing.T) {
	pool := openUnmigrated(t)
	migrateTo(t, pool, 14)
	mustExec(t, pool, `UPDATE harness_connections SET identity='env-a',label='box a' WHERE id=1`)
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness,identity,label,active,retired_at) VALUES(4,'t3','env-old','old',0,stage_now())`)
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness,identity,label,auto_complete) VALUES(7,'opencode','http://oc:4096','oc',0)`)
	mustExec(t, pool, `INSERT INTO projects(id,t3_project_id,title,workspace_root,source) VALUES(1,'p','P','/w','app')`)
	mustExec(t, pool, `INSERT INTO project_bindings(project_id,connection_id,external_project_id,workspace) VALUES(1,7,'oc','/w')`)
	mustExec(t, pool, `INSERT INTO session_identities(id,connection_id,external_id) VALUES(5,7,'ses_x')`)
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
	// The seeded label is unset by 0022 until the server names the machine.
	if m, err := s.GetMachine(ctx, LocalMachine); err != nil || m.Label != "" {
		t.Fatalf("machine %+v %v", m, err)
	}
	var stable []int64
	rows, err := pool.Query(`SELECT id FROM harness_connections WHERE stable_identity=1 ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		stable = append(stable, id)
	}
	if rows.Close(); len(stable) != 2 || stable[0] != 1 || stable[1] != 4 {
		t.Fatalf("stable identities %v", stable)
	}
	// The registry, not the schema, decides which kinds exist.
	mustExec(t, pool, `INSERT INTO harness_connections(harness) VALUES('another')`)
	if _, err := pool.Exec(`INSERT INTO harness_connections(harness) VALUES('opencode')`); err == nil {
		t.Fatal("second active connection of a kind on one machine")
	}
	// New AUTOINCREMENT ids never reuse an old one.
	var next int64
	if err := pool.QueryRow(`INSERT INTO harness_connections(harness,active,retired_at) VALUES('t3',0,stage_now()) RETURNING id`).Scan(&next); err != nil || next <= 8 {
		t.Fatalf("next id %d %v", next, err)
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

func TestLocalMachineLabelMigrationUnsetsOnlyTheSeed(t *testing.T) {
	for _, tc := range []struct{ before, after string }{
		{"This computer", ""},
		{"desk", "desk"},
	} {
		pool := openUnmigrated(t)
		migrateTo(t, pool, 21)
		mustExec(t, pool, `UPDATE machines SET label=$1 WHERE id=1`, tc.before)
		migrateTo(t, pool, sqlite.MaxVersion())
		m, err := NewStore(pool).GetMachine(t.Context(), LocalMachine)
		if err != nil || m.Label != tc.after {
			t.Fatalf("%q: machine %+v %v, want label %q", tc.before, m, err, tc.after)
		}
	}
}
