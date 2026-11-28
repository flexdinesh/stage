package storetest

import (
	"context"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func init() {
	register("SteeringBriefFrozenPerAttempt", caseSteeringBriefFrozenPerAttempt)
	register("SteeringPlaybookLazy", caseSteeringPlaybookLazy)
	register("SteeringPlaybookUserOverride", caseSteeringPlaybookUserOverride)
}

// caseSteeringBriefFrozenPerAttempt: the first brief of an attempt wins,
// same-attempt retries reuse it, a new attempt freezes its own, and
// CurrentSteering reads the attempt's brief (nil without one).
func caseSteeringBriefFrozenPerAttempt(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	if got, err := s.CurrentSteering(ctx, a.ID, 0); err != nil || got != nil {
		t.Fatalf("before launch %+v %v", got, err)
	}
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: a.ID, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	a = getTask(t, s, ctx, a.ID)
	if got, err := s.CurrentSteering(ctx, a.ID, a.LaunchAttempt); err != nil || got != nil {
		t.Fatalf("no brief yet %+v %v", got, err)
	}
	if got, err := s.SteeringLaunchContext(ctx, a, "brief one"); err != nil || got != "brief one" {
		t.Fatalf("first %q %v", got, err)
	}
	if got, err := s.SteeringLaunchContext(ctx, a, "brief changed"); err != nil || got != "brief one" {
		t.Fatalf("same attempt %q %v", got, err)
	}
	if got, err := s.CurrentSteering(ctx, a.ID, a.LaunchAttempt); err != nil || got == nil || *got != (db.SteeringBrief{Attempt: a.LaunchAttempt, Brief: "brief one"}) {
		t.Fatalf("current %+v %v", got, err)
	}
	setState(t, s, ctx, a.ID, db.StateFailed)
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: a.ID, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SteeringLaunchContext(ctx, a, "stale"); err == nil {
		t.Fatal("stale attempt accepted")
	}
	fresh := getTask(t, s, ctx, a.ID)
	if got, err := s.SteeringLaunchContext(ctx, fresh, "brief two"); err != nil || got != "brief two" {
		t.Fatalf("new attempt %q %v", got, err)
	}
	if got, err := s.CurrentSteering(ctx, a.ID, a.LaunchAttempt); err != nil || got == nil || got.Brief != "brief one" {
		t.Fatalf("old attempt %+v %v", got, err)
	}
	if got, err := s.CurrentSteering(ctx, a.ID, fresh.LaunchAttempt); err != nil || got == nil || got.Brief != "brief two" {
		t.Fatalf("new attempt current %+v %v", got, err)
	}
}

func snippetNamed(t *testing.T, s *db.Store, ctx context.Context, name string) []db.Snippet {
	t.Helper()
	all, err := s.ListSnippets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []db.Snippet
	for _, sn := range all {
		if sn.Name == name {
			out = append(out, sn)
		}
	}
	return out
}

// caseSteeringPlaybookLazy: the first read creates the global playbook
// snippet from the default; later reads keep it, including the user's edit.
func caseSteeringPlaybookLazy(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	if got := snippetNamed(t, s, ctx, db.SteeringPlaybookName); len(got) != 0 {
		t.Fatalf("seeded playbook %+v", got)
	}
	if got, err := s.SteeringPlaybook(ctx, "default playbook"); err != nil || got != "default playbook" {
		t.Fatalf("first %q %v", got, err)
	}
	created := snippetNamed(t, s, ctx, db.SteeringPlaybookName)
	if len(created) != 1 || created[0].ProjectID != nil || created[0].Body != "default playbook" {
		t.Fatalf("created %+v", created)
	}
	if got, err := s.SteeringPlaybook(ctx, "newer default"); err != nil || got != "default playbook" {
		t.Fatalf("second %q %v", got, err)
	}
	if _, err := s.UpdateSnippet(ctx, created[0].ID, db.SnippetPatch{Body: db.Optional[string]{Set: true, V: "edited"}}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SteeringPlaybook(ctx, "default playbook"); err != nil || got != "edited" {
		t.Fatalf("after edit %q %v", got, err)
	}
	if err := s.DeleteSnippet(ctx, created[0].ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SteeringPlaybook(ctx, "default playbook"); err != nil || got != "default playbook" {
		t.Fatalf("after delete %q %v", got, err)
	}
}

// caseSteeringPlaybookUserOverride: a user's snippet of that name, in any
// scope, is used as is and never duplicated.
func caseSteeringPlaybookUserOverride(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	if _, err := s.CreateSnippet(ctx, db.SnippetPatch{Name: db.Optional[string]{Set: true, V: db.SteeringPlaybookName}, Body: db.Optional[string]{Set: true, V: "mine"}, ProjectID: db.Optional[int64]{Set: true, V: p.ID}}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SteeringPlaybook(ctx, "default playbook"); err != nil || got != "mine" {
		t.Fatalf("override %q %v", got, err)
	}
	if got := snippetNamed(t, s, ctx, db.SteeringPlaybookName); len(got) != 1 || got[0].Body != "mine" {
		t.Fatalf("snippets %+v", got)
	}
}
