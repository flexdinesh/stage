package codex_test

import (
	"errors"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestRenameSessionSetsTheThreadName(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	var cps checkpoints
	o, err := r.adapter.Launch(ctx, intent("/w/app"), execution.Checkpoint{}, cps.save)
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.adapter.Features(ctx)
	if err != nil || !c.Operations[execution.OpSessionRename].Available || c.DeclaresOperations() != nil {
		t.Fatalf("rename declaration: %+v, %v", c.Operations[execution.OpSessionRename], err)
	}
	before := r.srv.Calls("thread/name/set")
	if err := r.adapter.RenameSession(ctx, o.Session, "  Renamed task  "); err != nil {
		t.Fatal(err)
	}
	if got := r.srv.Thread(o.Session.ExternalSessionID).Name; got != "Renamed task" || r.srv.Calls("thread/name/set") != before+1 {
		t.Fatalf("thread name %q after %d calls", got, r.srv.Calls("thread/name/set")-before)
	}
	if err := r.adapter.RenameSession(ctx, o.Session, " "); err == nil {
		t.Fatal("empty name accepted")
	}
	other := execution.SessionRef{ConnectionID: o.Session.ConnectionID + 1, ExternalSessionID: o.Session.ExternalSessionID}
	if err := r.adapter.RenameSession(ctx, other, "x"); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("another connection's session: %v", err)
	}
	if r.srv.Calls("thread/name/set") != before+1 {
		t.Fatal("refused renames reached Codex")
	}
}
