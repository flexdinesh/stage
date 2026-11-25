package remote_test

import (
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
)

var (
	connectedKind = harness.Descriptor{Kind: "svc", Name: "Svc", Hosting: harness.Connected}
	hostedKind    = harness.Descriptor{Kind: "proc", Name: "Proc", Hosting: harness.Hosted}
)

// Before its worker described it, a machine's profile is unknown: every
// kind is allowed, so the calls themselves answer offline or starting.
func TestRemoteMachineAllowsEverythingUntilDescribed(t *testing.T) {
	m := remote.NewMachine(2, nil, nil)
	for _, d := range []harness.Descriptor{connectedKind, hostedKind} {
		if a := m.Allows(d); !a.Available {
			t.Fatalf("%s %+v", d.Kind, a)
		}
	}
}

// A described machine hosts a hosted kind as its worker's profile allows,
// with the fix in the worker's terms; connected kinds always.
func TestRemoteMachineAllowsHostedKindsByItsWorkersProfile(t *testing.T) {
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{})
	if a := rig.Machine().Allows(hostedKind); !a.Available {
		t.Fatalf("local harnesses allowed: %+v", a)
	}
	for _, c := range []struct {
		profile harness.Profile
		reason  string
	}{
		{harness.Profile{Kind: harness.ProfileServer}, "Proc runs on this machine only when its Stage worker allows local harnesses: use the worker service, or run stage worker run --local-harnesses."},
		{harness.Profile{Kind: harness.ProfileContainer}, "Proc can't run in a container: run this machine's Stage worker outside Docker."},
	} {
		rig.StopWorker()
		rig.SetProfile(c.profile)
		rig.StartWorker()
		m := rig.Machine()
		// The new worker's description replaces the last one.
		deadline := time.Now().Add(5 * time.Second)
		a := m.Allows(hostedKind)
		for (a.Available || a.Reason != c.reason) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			a = m.Allows(hostedKind)
		}
		if a.Available || a.Reason != c.reason {
			t.Fatalf("%s: %+v", c.profile.Kind, a)
		}
		if a := m.Allows(connectedKind); !a.Available {
			t.Fatalf("%s connected: %+v", c.profile.Kind, a)
		}
	}
}
