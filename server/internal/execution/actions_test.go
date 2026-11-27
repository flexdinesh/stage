package execution

import (
	"reflect"
	"testing"
)

func TestApplyActionStatesOnlyRulesSessionActionsOut(t *testing.T) {
	yes := Availability{Available: true}
	declared := []Action{
		{ID: "archive", Target: TargetSession, Availability: yes},
		{ID: "settle", Target: TargetSession, Availability: yes},
		{ID: "later", Target: TargetSession, Availability: Availability{Reason: "Connect first."}},
		{ID: "open", Target: TargetProject, Availability: yes},
	}
	if got := ApplyActionStates(declared, nil); !reflect.DeepEqual(got, declared) {
		t.Fatalf("no states changed %+v", got)
	}
	got := ApplyActionStates(declared, []ActionState{
		{ID: "archive", Availability: Availability{Reason: "Already archived."}},
		{ID: "settle", Availability: Availability{Reason: "  "}},
		{ID: "later", Availability: yes},
		{ID: "open", Availability: Availability{Reason: "Not a session action."}},
		{ID: "unknown", Availability: Availability{Reason: "Ignored."}},
	})
	want := []Availability{
		{Reason: "Already archived."},
		{Reason: "The harness cannot run this action for this session."},
		{Reason: "Connect first."},
		yes,
	}
	for i, a := range got {
		if a.Availability != want[i] {
			t.Errorf("%s = %+v, want %+v", a.ID, a.Availability, want[i])
		}
	}
	if !declared[0].Availability.Available {
		t.Fatal("declarations were mutated")
	}
}
