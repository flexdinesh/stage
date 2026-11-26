package storetest

import (
	"reflect"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func init() {
	register("ObservationUsage", caseObservationUsage)
}

// caseObservationUsage: usage is cumulative per session, so an observation
// without it (an unreadable transcript) keeps the stored usage, across
// turns too; a new usage replaces it.
func caseObservationUsage(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	sw, err := s.SwitchInstance(ctx, db.LocalMachine, claudecode.Descriptor, "/home/u/.claude", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	ref := execution.SessionRef{ConnectionID: sw.New, ExternalSessionID: "c6b773af-da92-49f8-a4dc-a5f304d5ba0c"}
	at := time.Date(2026, 11, 25, 3, 20, 0, 0, time.UTC)
	put := func(turn string, u *execution.Usage) {
		t.Helper()
		at = at.Add(time.Second)
		o := execution.Observation{Session: ref, Project: "/w/a", Title: "t", State: "done", ExecutionID: turn,
			CreatedAt: at, UpdatedAt: at, Usage: u}
		if err := s.PutObservation(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	read := func() *execution.Usage {
		t.Helper()
		o, err := s.Observation(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		return o.Usage
	}

	put("turn-1", nil)
	if u := read(); u != nil {
		t.Fatalf("usage before any %+v, want nil", u)
	}
	cost := 0.25
	first := &execution.Usage{Turns: 1, InputTokens: 2, CacheReadTokens: 27618, CacheWriteTokens: 16652, OutputTokens: 4875, WorkingMs: 23015, CostUSD: &cost}
	put("turn-1", first)
	if u := read(); !reflect.DeepEqual(u, first) {
		t.Fatalf("stored %+v, want %+v", u, first)
	}
	put("turn-2", nil)
	if u := read(); !reflect.DeepEqual(u, first) {
		t.Fatalf("after a read without usage %+v, want kept %+v", u, first)
	}
	second := &execution.Usage{Turns: 2, InputTokens: 4, OutputTokens: 5000}
	put("turn-2", second)
	if u := read(); !reflect.DeepEqual(u, second) {
		t.Fatalf("replaced %+v, want %+v", u, second)
	}
}
