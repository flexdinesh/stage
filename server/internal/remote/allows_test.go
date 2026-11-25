package remote_test

import (
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/remote"
)

// A remote machine hosts connected harnesses only, whatever its worker's
// profile, until workers set up hosted harnesses (#374).
func TestRemoteMachineAllowsConnectedKindsOnly(t *testing.T) {
	m := remote.NewMachine(2, nil, nil)
	if a := m.Allows(harness.Descriptor{Kind: "svc", Name: "Svc", Hosting: harness.Connected}); !a.Available {
		t.Fatalf("connected %+v", a)
	}
	a := m.Allows(harness.Descriptor{Kind: "proc", Name: "Proc", Hosting: harness.Hosted})
	if a.Available || !strings.HasPrefix(a.Reason, "Proc can't run on a worker machine yet") {
		t.Fatalf("hosted %+v", a)
	}
}
