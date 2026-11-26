package remote_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
)

// probe is a harness whose launch runs launchFn. It resolves links and has
// no other optional interface.
type probe struct {
	mu       sync.Mutex
	launchFn func(ctx context.Context, in execution.Intent, cp execution.Checkpoint, save execution.SaveCheckpoint) (execution.Observation, error)
	effects  int
	got      execution.Intent
}

func (p *probe) Health(context.Context) execution.Health {
	return execution.Health{Availability: execution.Availability{Available: true}, Identity: "probe-1"}
}
func (p *probe) Features(context.Context) (execution.Capabilities, error) {
	ops := map[execution.Op]execution.Availability{}
	for _, op := range execution.Ops {
		ops[op] = execution.Availability{Reason: "probe lacks " + string(op)}
	}
	ops[execution.OpLaunch] = execution.Availability{Available: true}
	return execution.Capabilities{Operations: ops}, nil
}
func (p *probe) Workspaces(context.Context) ([]execution.WorkspaceInfo, error) {
	return []execution.WorkspaceInfo{{ID: "w", WorkspaceRoot: "/srv/repo/app"}}, nil
}
func (p *probe) Observe(_ context.Context, ref execution.SessionRef) (execution.Observation, error) {
	if ref.ExternalSessionID != "s1" {
		return execution.Observation{}, execution.ErrNotFound
	}
	return execution.Observation{Session: ref, Project: "w", State: "running", Active: true, Origin: "wrapper"}, nil
}
func (p *probe) Launch(ctx context.Context, in execution.Intent, cp execution.Checkpoint, save execution.SaveCheckpoint) (execution.Observation, error) {
	return p.launchFn(ctx, in, cp, save)
}
func (p *probe) Stop(context.Context, execution.SessionRef) error { return nil }
func (p *probe) Attention(context.Context) ([]execution.SessionRef, error) {
	return nil, execution.ErrUnavailable
}
func (p *probe) Opening(context.Context, execution.SessionRef, bool) (*execution.Reply, error) {
	return nil, execution.ErrNoReply
}
func (p *probe) Destinations(_ context.Context, ref execution.SessionRef) execution.Links {
	return execution.Links{Web: "https://probe.invalid/" + ref.ExternalSessionID}
}
func (p *probe) ResolveLink(_ context.Context, link string) (execution.SessionRef, error) {
	return execution.SessionRef{ConnectionID: 5, ExternalSessionID: strings.TrimPrefix(link, "probe:")}, nil
}

func (p *probe) effect() {
	p.mu.Lock()
	p.effects++
	p.mu.Unlock()
}

func (p *probe) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.effects
}

func ok(ref execution.SessionRef) execution.Observation {
	return execution.Observation{Session: ref, Project: "w", State: "running", Active: true}
}

func TestLaunchSaveFailureStopsTheWorkersAdapter(t *testing.T) {
	p := &probe{}
	p.launchFn = func(ctx context.Context, in execution.Intent, _ execution.Checkpoint, save execution.SaveCheckpoint) (execution.Observation, error) {
		if err := save(ctx, execution.Checkpoint{Version: 1, Data: []byte(`{}`)}); err != nil {
			return execution.Observation{}, err
		}
		p.effect()
		return ok(execution.SessionRef{ConnectionID: in.ConnectionID, ExternalSessionID: "s1"}), nil
	}
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{5: p})
	a := rig.Adapter(5)
	_, err := a.Launch(t.Context(), execution.Intent{ConnectionID: 5, Images: []execution.Image{}}, execution.Checkpoint{}, func(context.Context, execution.Checkpoint) error {
		return errors.New("launch changed; reload the task")
	})
	var rejected *execution.Rejected
	if err == nil || errors.As(err, &rejected) || !strings.Contains(err.Error(), "launch changed") {
		t.Fatal("save failure", err)
	}
	if p.count() != 0 {
		t.Fatal("effect after a failed save")
	}
	var saved []execution.Checkpoint
	o, err := a.Launch(t.Context(), execution.Intent{ConnectionID: 5, Images: []execution.Image{}}, execution.Checkpoint{}, func(_ context.Context, c execution.Checkpoint) error {
		saved = append(saved, c)
		return nil
	})
	if err != nil || o.Session.ExternalSessionID != "s1" || p.count() != 1 || len(saved) != 1 || saved[0].Version != 1 {
		t.Fatal(o, err, saved)
	}
}

func TestLateCheckpointSaveIsRefused(t *testing.T) {
	p := &probe{}
	refused := make(chan error, 1)
	p.launchFn = func(ctx context.Context, _ execution.Intent, _ execution.Checkpoint, save execution.SaveCheckpoint) (execution.Observation, error) {
		// Outlive the main server's wait, then try the next effect.
		time.Sleep(400 * time.Millisecond)
		err := save(context.WithoutCancel(ctx), execution.Checkpoint{Version: 2})
		refused <- err
		if err != nil {
			return execution.Observation{}, err
		}
		p.effect()
		return execution.Observation{}, errors.New("unreachable")
	}
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{5: p})
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	saves := 0
	_, err := rig.Adapter(5).Launch(ctx, execution.Intent{ConnectionID: 5, Images: []execution.Image{}}, execution.Checkpoint{}, func(context.Context, execution.Checkpoint) error {
		saves++
		return nil
	})
	if err == nil {
		t.Fatal("launch past its deadline succeeded")
	}
	select {
	case err := <-refused:
		if err == nil || !strings.Contains(err.Error(), "call_ended") {
			t.Fatal("late save accepted", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker never tried its save")
	}
	if saves != 0 || p.count() != 0 {
		t.Fatal("late save reached the main server or the effect ran", saves, p.count())
	}
}

func TestLaunchImagesAreFetchedByTheWorker(t *testing.T) {
	p := &probe{}
	p.launchFn = func(_ context.Context, in execution.Intent, _ execution.Checkpoint, _ execution.SaveCheckpoint) (execution.Observation, error) {
		p.mu.Lock()
		p.got = in
		p.mu.Unlock()
		return ok(execution.SessionRef{ConnectionID: in.ConnectionID, ExternalSessionID: "s1"}), nil
	}
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{5: p})
	big := bytes.Repeat([]byte{0xab}, 9<<20)
	in := execution.Intent{ConnectionID: 5, Images: []execution.Image{{Name: "a.png", MimeType: "image/png", Data: []byte{1, 2, 3}}, {Name: "b.jpg", MimeType: "image/jpeg", Data: big}}}
	if _, err := rig.Adapter(5).Launch(t.Context(), in, execution.Checkpoint{}, func(context.Context, execution.Checkpoint) error { return nil }); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	got := p.got.Images
	p.mu.Unlock()
	if len(got) != 2 || got[0].Name != "a.png" || !bytes.Equal(got[0].Data, []byte{1, 2, 3}) || got[1].MimeType != "image/jpeg" || !bytes.Equal(got[1].Data, big) {
		t.Fatalf("images %d", len(got))
	}
	if !bytes.Equal(in.Images[1].Data, big) {
		t.Fatal("caller's intent changed")
	}
}

func TestProxyTraitsDestinationsAndErrors(t *testing.T) {
	p := &probe{}
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{5: p})
	a := rig.Adapter(5)
	if _, ok := execution.As[execution.LinkResolver](a); !ok {
		t.Fatal("worker's link resolver hidden")
	}
	for name, offered := range map[string]bool{
		"correlator":  has[execution.Correlator](a),
		"events":      has[execution.EventSource](a),
		"interactive": has[execution.Interactive](a),
		"workspace":   has[execution.WorkspaceFeatures](a),
	} {
		if offered {
			t.Fatal("proxy offers what the worker's adapter lacks:", name)
		}
	}
	lr, _ := execution.As[execution.LinkResolver](a)
	if ref, err := lr.ResolveLink(t.Context(), "probe:s1"); err != nil || ref.ExternalSessionID != "s1" {
		t.Fatal(ref, err)
	}
	if links := a.Destinations(t.Context(), execution.SessionRef{ConnectionID: 5, ExternalSessionID: "s1"}); links != (execution.Links{}) {
		t.Fatal("destinations made a call", links)
	}
	if h := a.Health(t.Context()); !h.Available || h.Identity != "probe-1" {
		t.Fatal(h)
	}
	if _, err := a.Observe(t.Context(), execution.SessionRef{ConnectionID: 5, ExternalSessionID: "gone"}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("not found lost", err)
	}
	if _, err := a.Opening(t.Context(), execution.SessionRef{ConnectionID: 5, ExternalSessionID: "s1"}, true); !errors.Is(err, execution.ErrNoReply) {
		t.Fatal("no reply lost", err)
	}
	if _, err := a.Attention(t.Context()); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatal("unavailable lost", err)
	}
	// Every operation and its reason, and a session's origin, pass through.
	want, _ := p.Features(t.Context())
	if got, err := a.Features(t.Context()); err != nil || !reflect.DeepEqual(got.Operations, want.Operations) || got.DeclaresOperations() != nil {
		t.Fatal("operations", got.Operations, err)
	}
	if o, err := a.Observe(t.Context(), execution.SessionRef{ConnectionID: 5, ExternalSessionID: "s1"}); err != nil || o.Origin != "wrapper" {
		t.Fatal("origin", o, err)
	}
	if got := rig.Machine().Profile().Kind; got != "server" {
		t.Fatal("profile", got)
	}
	rig.StopWorker()
	if h := a.Health(t.Context()); h.Available || !strings.Contains(h.Reason, "offline") || h.Identity != "probe-1" {
		t.Fatal("offline health", h)
	}
	if _, err := a.Workspaces(t.Context()); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatal("offline call", err)
	}
	var rejected *execution.Rejected
	if _, err := a.Launch(t.Context(), execution.Intent{ConnectionID: 5}, execution.Checkpoint{}, nil); errors.As(err, &rejected) || !errors.Is(err, execution.ErrUnavailable) {
		t.Fatal("offline launch must be unavailable, never rejected", err)
	}
}

func has[T any](a execution.Adapter) bool {
	_, ok := execution.As[T](a)
	return ok
}

func TestOpsRunOnTheWorker(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "repo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "repo", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "repo", ".git", "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:acme/widgets.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{5: &probe{}})
	ws, err := rig.Machine().Ops().Inventory(t.Context(), []execution.WorkspaceInfo{{ID: "w", WorkspaceRoot: "/srv/repo/app"}}, []machine.SharedRoot{{HarnessRoot: "/srv", StageRoot: root}})
	if err != nil || len(ws) != 1 || ws[0].Repo != "github.com/acme/widgets" || ws[0].RepoPath != "app" {
		t.Fatal(ws, err)
	}
	want, _ := machine.LocalOps{}.Checkout(t.Context(), []machine.SharedRoot{{HarnessRoot: "/srv", StageRoot: root}}, "/srv/repo/app")
	got, err := rig.Machine().Ops().Checkout(t.Context(), []machine.SharedRoot{{HarnessRoot: "/srv", StageRoot: root}}, "/srv/repo/app")
	if err != nil || got != want {
		t.Fatal(got, want, err)
	}
}
