package db

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type DayCount struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

type (
	ProjectCount = store.ProjectCount
	StatusCount  = store.StatusCount
	StateCount   = store.StateCount
)

type Stats struct {
	From             time.Time      `json:"from"`
	To               time.Time      `json:"to"`
	TZ               string         `json:"tz"`
	ThreadsByDay     []DayCount     `json:"threadsByDay"`
	ThreadsByProject []ProjectCount `json:"threadsByProject"`
	ThreadsByStatus  []StatusCount  `json:"threadsByStatus"`
	TasksByState     []StateCount   `json:"tasksByState"`
	NeedsInput       int            `json:"needsInput"`
}

// Stats: thread counts over mirrored threads with created_at in [from, to)
// (day buckets in tz, a valid IANA zone); excludeSubagents keeps root threads
// only. Task counts are current totals.
func (s *Store) Stats(ctx context.Context, from, to time.Time, tz string, excludeSubagents bool) (Stats, error) {
	st := Stats{From: from, To: to, TZ: tz,
		ThreadsByDay: []DayCount{}, ThreadsByProject: []ProjectCount{},
		ThreadsByStatus: []StatusCount{}, TasksByState: []StateCount{}}

	loc, err := time.LoadLocation(tz)
	if err != nil {
		return st, Invalid("invalid tz %q", tz)
	}
	err = s.e.Read(ctx, func(tx store.Tx) error {
		r := tx.Stats()
		// Day buckets in tz, computed here (SQLite has no time zones).
		times, err := r.ThreadTimes(ctx, from, to, excludeSubagents)
		if err != nil {
			return err
		}
		byDay := map[string]int{}
		for _, at := range times {
			byDay[at.In(loc).Format(time.DateOnly)]++
		}
		for _, day := range slices.Sorted(maps.Keys(byDay)) {
			st.ThreadsByDay = append(st.ThreadsByDay, DayCount{Day: day, Count: byDay[day]})
		}
		projects, err := r.ThreadsByProject(ctx, from, to, excludeSubagents)
		if err != nil {
			return err
		}
		st.ThreadsByProject = append(st.ThreadsByProject, projects...)
		statuses, err := r.ThreadsByStatus(ctx, from, to, excludeSubagents)
		if err != nil {
			return err
		}
		st.ThreadsByStatus = append(st.ThreadsByStatus, statuses...)
		states, err := r.TasksByState(ctx)
		if err != nil {
			return err
		}
		for _, c := range states {
			if c.State == StateNeedsInput {
				st.NeedsInput = c.Count
			}
		}
		st.TasksByState = append(st.TasksByState, states...)
		return nil
	})
	return st, err
}
