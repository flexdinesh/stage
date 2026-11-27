package remote_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
)

// renameProbe declares sessionRename and renames on the worker.
type renameProbe struct {
	probe
	declare bool
	mu      sync.Mutex
	renamed []string
}

func (p *renameProbe) Features(ctx context.Context) (execution.Capabilities, error) {
	c, err := p.probe.Features(ctx)
	if p.declare {
		c.Operations[execution.OpSessionRename] = execution.Availability{Available: true}
	}
	return c, err
}

func (p *renameProbe) RenameSession(_ context.Context, ref execution.SessionRef, title string) error {
	if title == "refuse" {
		return errors.New("the harness refused the name")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.renamed = append(p.renamed, ref.ExternalSessionID+"="+title)
	return nil
}

// availableOnly declares sessionRename without the trait.
type availableOnly struct{ probe }

func (p *availableOnly) Features(ctx context.Context) (execution.Capabilities, error) {
	c, err := p.probe.Features(ctx)
	c.Operations[execution.OpSessionRename] = execution.Availability{Available: true}
	return c, err
}

func TestSessionRenameThroughWorker(t *testing.T) {
	p := &renameProbe{declare: true}
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{5: p})
	a := rig.Adapter(5)
	ref := execution.SessionRef{ConnectionID: 5, ExternalSessionID: "s1"}
	c, err := a.Features(t.Context())
	if err != nil || !c.Operations[execution.OpSessionRename].Available {
		t.Fatalf("declared rename lost: %+v, %v", c.Operations[execution.OpSessionRename], err)
	}
	namer, ok := execution.As[execution.SessionNamer](a)
	if !ok {
		t.Fatal("worker's session namer hidden")
	}
	if err := namer.RenameSession(t.Context(), ref, "New title"); err != nil {
		t.Fatal(err)
	}
	if err := namer.RenameSession(t.Context(), ref, "refuse"); err == nil || !strings.Contains(err.Error(), "refused the name") {
		t.Fatalf("worker error lost: %v", err)
	}
	p.mu.Lock()
	got := strings.Join(p.renamed, ",")
	p.mu.Unlock()
	if got != "s1=New title" {
		t.Fatalf("worker renames: %q", got)
	}

	// An older worker has no handler: the operation and trait are withdrawn.
	rig.StopWorker()
	rig.SetMissingRequestKinds(remote.KindSessionRename)
	rig.StartWorker()
	if has[execution.SessionNamer](a) {
		t.Fatal("trait offered without its request kind")
	}
	c, err = a.Features(t.Context())
	if op := c.Operations[execution.OpSessionRename]; err != nil || op.Available || op.Reason != "Update Stage on the worker machine to rename sessions." {
		t.Fatalf("older worker rename: %+v, %v", op, err)
	}
	// Nor can it declare the operation at all.
	rig.ReplaceResult(remote.KindFeatures, []byte(`{"operations":{"launch":{"available":true}}}`))
	c, err = a.Features(t.Context())
	if op, ok := c.Operations[execution.OpSessionRename]; err != nil || !ok || op.Available || op.Reason == "" {
		t.Fatalf("undeclared rename on an older worker: %+v, %v", c.Operations, err)
	}
}

func TestSessionRenameNeedsTheWorkersTrait(t *testing.T) {
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{5: &availableOnly{}, 6: &probe{}})
	if has[execution.SessionNamer](rig.Adapter(5)) || has[execution.SessionNamer](rig.Adapter(6)) {
		t.Fatal("proxy offers rename the worker's adapter lacks")
	}
	c, err := rig.Adapter(5).Features(t.Context())
	if op := c.Operations[execution.OpSessionRename]; err != nil || op.Available || op.Reason != "This worker connection does not offer session rename." {
		t.Fatalf("declared without trait: %+v, %v", op, err)
	}
	c, err = rig.Adapter(6).Features(t.Context())
	if op := c.Operations[execution.OpSessionRename]; err != nil || op.Available || op.Reason != "probe lacks sessionRename" {
		t.Fatalf("declared unavailable: %+v, %v", op, err)
	}
}
