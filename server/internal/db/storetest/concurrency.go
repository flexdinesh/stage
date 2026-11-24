package storetest

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// Concurrency cases: each races one read-check-write use case of the
// docs/storage.md "Transactions and locking" table and asserts the invariant
// it keeps. SQLite keeps them by its single writer; an engine without a
// global lock must keep them with its own row locks or constraints. Also:
// ConcurrentNeedsSerialize (trees.go), JournalConcurrentRetryAndPagination
// and JournalMoveAndAppendSerialize (journal.go),
// RedeemPairingConcurrentSingleWinner (machineauth.go).
func init() {
	register("ConcurrentLaunchClaim", caseConcurrentLaunchClaim)
	register("ConcurrentAcceptance", caseConcurrentAcceptance)
	register("ConcurrentAcceptanceRequestID", caseConcurrentAcceptanceRequestID)
	register("ConcurrentTreeMembership", caseConcurrentTreeMembership)
	register("ConcurrentNeedsAndRemoval", caseConcurrentNeedsAndRemoval)
	register("ConcurrentScheduleFire", caseConcurrentScheduleFire)
	register("ConcurrentConnectionSwitch", caseConcurrentConnectionSwitch)
	register("ConcurrentNoteRequestIDConflict", caseConcurrentNoteRequestIDConflict)
}

// racers is how many goroutines each case releases at once.
const racers = 8

func isConflict(err error) bool {
	var conflict *db.ConflictError
	return errors.As(err, &conflict)
}

// Launch claim: one attempt begins per task and attempt number.
func caseConcurrentLaunchClaim(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	task := mustTask(t, s, ctx, p.ID, "claimed", nil)
	results := make([]db.TransitionResult, racers)
	errs := race(racers, func(i int) error {
		var err error
		results[i], err = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching, BeginAttempt: true})
		return err
	})
	changed := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v", i, err)
		}
		if results[i].Changed {
			changed++
			if results[i].Attempt != 1 {
				t.Fatalf("winner began attempt %d, want 1", results[i].Attempt)
			}
		}
	}
	if changed != 1 {
		t.Fatalf("%d racers began an attempt, want 1", changed)
	}
	got := getTask(t, s, ctx, task.ID)
	if got.State != db.StateLaunching {
		t.Fatalf("state %s, want launching", got.State)
	}
	if _, err := s.Attempt(ctx, task.ID, 1); err != nil {
		t.Fatalf("attempt 1: %v", err)
	}
	if _, err := s.Attempt(ctx, task.ID, 2); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("attempt 2 = %v, want not found", err)
	}
}

// Acceptance: one revision sequence per task; racers at the same revision
// with their own requestIds see one winner and stale conflicts.
func caseConcurrentAcceptance(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	task := mustTask(t, s, ctx, p.ID, "accepted", nil)
	preview, err := s.CompletionPreview(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	results := make([]db.AcceptanceResult, racers)
	errs := race(racers, func(i int) error {
		var err error
		results[i], err = s.ChangeAcceptance(ctx, task.ID, db.AcceptanceRequest{
			RequestID: fmt.Sprintf("accept-%d", i), Revision: preview.Revision, EvidenceVersion: preview.EvidenceVersion,
		}, false)
		return err
	})
	won := -1
	for i, err := range errs {
		switch {
		case err == nil && won >= 0:
			t.Fatalf("racers %d and %d both accepted", won, i)
		case err == nil:
			won = i
		case !isConflict(err):
			t.Fatalf("racer %d: %v", i, err)
		}
	}
	if won < 0 {
		t.Fatal("no racer accepted")
	}
	if results[won].Revision != preview.Revision+1 || results[won].AcceptanceID == nil {
		t.Fatalf("winner result %+v (preview revision %d)", results[won], preview.Revision)
	}
	list, err := s.Acceptances(ctx, task.ID)
	if err != nil || len(list) != 1 || list[0].ID != *results[won].AcceptanceID {
		t.Fatalf("acceptances %+v %v", list, err)
	}
	after, err := s.CompletionPreview(ctx, task.ID)
	if err != nil || after.Revision != preview.Revision+1 {
		t.Fatalf("revision after %d %v, want %d", after.Revision, err, preview.Revision+1)
	}
}

// Acceptance retry: racers with one requestId and one body all get the one
// recorded result.
func caseConcurrentAcceptanceRequestID(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	task := mustTask(t, s, ctx, p.ID, "accepted", nil)
	preview, err := s.CompletionPreview(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	req := db.AcceptanceRequest{RequestID: "retry", Revision: preview.Revision, EvidenceVersion: preview.EvidenceVersion, Note: "looks good"}
	results := make([]db.AcceptanceResult, racers)
	errs := race(racers, func(i int) error {
		var err error
		results[i], err = s.ChangeAcceptance(ctx, task.ID, req, false)
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v", i, err)
		}
		if results[i].Revision != results[0].Revision || results[i].AcceptanceID == nil || *results[i].AcceptanceID != *results[0].AcceptanceID {
			t.Fatalf("racer %d result %+v, racer 0 %+v", i, results[i], results[0])
		}
	}
	list, err := s.Acceptances(ctx, task.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("acceptances %+v %v", list, err)
	}
	if _, err := s.ChangeAcceptance(ctx, task.ID, db.AcceptanceRequest{RequestID: "retry", Revision: preview.Revision, Note: "other"}, false); !isConflict(err) {
		t.Fatalf("same requestId, other body = %v, want conflict", err)
	}
}

// Tree membership: a task joining two trees at once ends in exactly one.
func caseConcurrentTreeMembership(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p").ID
	for round := range 5 {
		x := mustTask(t, s, ctx, p, fmt.Sprintf("x-%d", round), nil)
		y := mustTask(t, s, ctx, p, fmt.Sprintf("y-%d", round), nil)
		mover := mustTask(t, s, ctx, p, fmt.Sprintf("mover-%d", round), nil)
		trees := []int64{
			mustTree(t, s, ctx, fmt.Sprintf("a-%d", round), "", db.TreeMember{TaskID: x.ID}).ID,
			mustTree(t, s, ctx, fmt.Sprintf("b-%d", round), "", db.TreeMember{TaskID: y.ID}).ID,
		}
		errs := race(len(trees), func(i int) error {
			_, err := s.AddToTree(ctx, trees[i], mover.ID, nil)
			return err
		})
		for i, err := range errs {
			if err != nil && !isConflict(err) {
				t.Fatalf("round %d racer %d: %v", round, i, err)
			}
		}
		got := getTask(t, s, ctx, mover.ID)
		if got.TreeID == nil || !slices.Contains(trees, *got.TreeID) {
			t.Fatalf("round %d: mover tree %v, want one of %v", round, got.TreeID, trees)
		}
		listed := 0
		for _, id := range trees {
			tree, err := s.GetTree(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if slices.ContainsFunc(tree.Tasks, func(m db.Task) bool { return m.ID == mover.ID }) {
				listed++
				if id != *got.TreeID {
					t.Fatalf("round %d: tree %d lists the mover, task says tree %d", round, id, *got.TreeID)
				}
			}
		}
		if listed != 1 {
			t.Fatalf("round %d: mover listed in %d trees", round, listed)
		}
	}
}

// Tree needs: a need set while its target leaves the tree never points
// outside the tree. Rounds swap which goroutine sets the need, so both
// orders occur.
func caseConcurrentNeedsAndRemoval(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p").ID
	for round := range 10 {
		a := mustTask(t, s, ctx, p, fmt.Sprintf("a-%d", round), nil)
		b := mustTask(t, s, ctx, p, fmt.Sprintf("b-%d", round), []int64{a.ID})
		c := mustTask(t, s, ctx, p, fmt.Sprintf("c-%d", round), nil)
		tree := *b.TreeID
		if _, err := s.AddToTree(ctx, tree, c.ID, nil); err != nil {
			t.Fatal(err)
		}
		errs := race(2, func(i int) error {
			if i == round%2 {
				_, err := s.SetNeeds(ctx, c.ID, []int64{b.ID})
				return err
			}
			return s.RemoveFromTree(ctx, tree, b.ID)
		})
		for i, err := range errs {
			if err != nil && !isConflict(err) && !errors.Is(err, db.ErrNotFound) {
				t.Fatalf("round %d racer %d: %v", round, i, err)
			}
		}
		for _, id := range []int64{a.ID, b.ID, c.ID} {
			task := getTask(t, s, ctx, id)
			for _, need := range task.Needs {
				dep := getTask(t, s, ctx, need)
				if task.TreeID == nil || dep.TreeID == nil || *dep.TreeID != *task.TreeID {
					t.Fatalf("round %d: task %d (tree %v) needs %d (tree %v)", round, id, task.TreeID, need, dep.TreeID)
				}
			}
		}
	}
}

// Schedule fire: racers firing one due time spawn one task.
func caseConcurrentScheduleFire(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	sc := mustSchedule(t, s, ctx, p.ID, "*/5 * * * *")
	now := sc.NextRunAt.Add(30 * time.Second)
	results := make([]db.FireResult, racers)
	errs := race(racers, func(i int) error {
		var err error
		results[i], err = s.FireSchedule(ctx, sc.ID, now, 2*time.Minute)
		return err
	})
	started := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v", i, err)
		}
		if results[i].TaskID != nil {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("%d racers spawned a task, want 1: %+v", started, results)
	}
	runs, err := s.ScheduleRuns(ctx, sc.ID, 100)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs %+v %v", runs, err)
	}
	spawned, err := s.ListTasks(ctx, db.TaskFilter{ProjectID: &p.ID, Closed: "all"})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, task := range spawned {
		if task.ScheduleID != nil && *task.ScheduleID == sc.ID {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d tasks spawned by the schedule, want 1", n)
	}
	got, err := s.GetSchedule(ctx, sc.ID)
	if err != nil || got.NextRunAt == nil || !got.NextRunAt.After(now) {
		t.Fatalf("next run %v %v, want after %v", got.NextRunAt, err, now)
	}
}

// Connection switch: at most one active connection per machine and kind,
// and one connection per identity.
func caseConcurrentConnectionSwitch(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	// A binding on the active connection makes each switch retire it and
	// insert a replacement rather than restamp it.
	mustProject(t, s, ctx, "bound")
	activeT3 := func() []db.HarnessConnection {
		t.Helper()
		all, err := s.Connections(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []db.HarnessConnection
		for _, c := range all {
			if c.MachineID == db.LocalMachine && c.Harness == db.HarnessT3 && c.Active {
				out = append(out, c)
			}
		}
		return out
	}

	errs := race(racers, func(i int) error {
		_, err := s.SwitchInstance(ctx, db.LocalMachine, t3.Descriptor, fmt.Sprintf("env-%d", i), "", 0)
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v", i, err)
		}
	}
	active := activeT3()
	if len(active) != 1 {
		t.Fatalf("active t3 connections after distinct switches: %+v", active)
	}

	// Bound again, so a switch to another identity would retire it.
	if _, err := s.InsertProject(ctx, db.Project{Title: "rebound", Bindings: []db.ProjectBinding{{ConnectionID: active[0].ID, ExternalProjectID: "rebound", Workspace: "/w/rebound"}}}); err != nil {
		t.Fatal(err)
	}
	results := make([]db.Switch, racers)
	errs = race(racers, func(i int) error {
		var err error
		results[i], err = s.SwitchInstance(ctx, db.LocalMachine, t3.Descriptor, "env-same", "", 0)
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("same identity racer %d: %v", i, err)
		}
		if results[i].New != results[0].New {
			t.Fatalf("same identity racers got connections %d and %d", results[0].New, results[i].New)
		}
	}
	active = activeT3()
	if len(active) != 1 || active[0].ID != results[0].New || active[0].Label != "env-same" {
		t.Fatalf("active t3 connections after same-identity switches: %+v (want %d)", active, results[0].New)
	}
}

// Journal requestId: racers with one key and different text see one entry;
// the rest are conflicts (same key and text: JournalConcurrentRetryAndPagination).
func caseConcurrentNoteRequestIDConflict(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	owner := db.JournalOwner{TaskID: &a.ID}
	entries := make([]db.JournalEntry, racers)
	errs := race(racers, func(i int) error {
		var err error
		entries[i], err = s.AppendNote(ctx, owner, db.AppendNoteInput{Text: fmt.Sprintf("version %d", i), RequestID: "same-key"})
		return err
	})
	won := -1
	for i, err := range errs {
		switch {
		case err == nil && won >= 0:
			t.Fatalf("racers %d and %d both appended", won, i)
		case err == nil:
			won = i
		case !isConflict(err):
			t.Fatalf("racer %d: %v", i, err)
		}
	}
	if won < 0 {
		t.Fatal("no racer appended")
	}
	page, err := s.ReadJournal(ctx, owner, db.JournalQuery{})
	if err != nil {
		t.Fatal(err)
	}
	notes := 0
	for _, e := range page.Entries {
		if e.Kind == "note" {
			notes++
			if e.ID != entries[won].ID || e.Text != fmt.Sprintf("version %d", won) {
				t.Fatalf("note %+v, winner %+v", e, entries[won])
			}
		}
	}
	if notes != 1 {
		t.Fatalf("%d notes, want 1", notes)
	}
}
