package db

import (
	"context"
	"maps"
	"slices"
	"time"
)

type DayCount struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

type ProjectCount struct {
	ProjectID   *int64 `json:"projectId"`
	T3ProjectID string `json:"externalProjectId"`
	Title       string `json:"title"`
	Count       int    `json:"count"`
}

type StatusCount struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

type StateCount struct {
	State TaskState `json:"state"`
	Count int       `json:"count"`
}

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
	// Day buckets in tz, computed here (SQLite has no time zones).
	rows, err := s.pool.Query(ctx, `
SELECT created_at FROM threads WHERE created_at >= $1 AND created_at < $2
  AND (NOT $3 OR parent_thread_id IS NULL)`, from, to, excludeSubagents)
	if err != nil {
		return st, err
	}
	byDay := map[string]int{}
	for rows.Next() {
		var at time.Time
		if err := rows.Scan(&at); err != nil {
			rows.Close()
			return st, err
		}
		byDay[at.In(loc).Format(time.DateOnly)]++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}
	for _, day := range slices.Sorted(maps.Keys(byDay)) {
		st.ThreadsByDay = append(st.ThreadsByDay, DayCount{Day: day, Count: byDay[day]})
	}

	rows, err = s.pool.Query(ctx, `
SELECT p.id, th.t3_project_id, COALESCE(p.title, th.t3_project_id), count(*) AS n
FROM threads th LEFT JOIN projects p ON p.id = COALESCE(th.stage_project_id,(SELECT min(b.project_id) FROM project_bindings b WHERE b.connection_id=COALESCE((SELECT connection_id FROM session_identities WHERE id=th.session_id),`+activeT3SQL+`) AND b.external_project_id=th.t3_project_id AND b.active=1 HAVING count(*)=1))
WHERE th.created_at >= $1 AND th.created_at < $2
  AND (NOT $3 OR th.parent_thread_id IS NULL)
GROUP BY p.id, th.t3_project_id, p.title ORDER BY n DESC, th.t3_project_id`, from, to, excludeSubagents)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var c ProjectCount
		if err := rows.Scan(&c.ProjectID, &c.T3ProjectID, &c.Title, &c.Count); err != nil {
			rows.Close()
			return st, err
		}
		st.ThreadsByProject = append(st.ThreadsByProject, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}

	rows, err = s.pool.Query(ctx, `
SELECT status, count(*) AS n FROM threads WHERE created_at >= $1 AND created_at < $2
  AND (NOT $3 OR parent_thread_id IS NULL)
GROUP BY status ORDER BY n DESC, status`, from, to, excludeSubagents)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var c StatusCount
		if err := rows.Scan(&c.Status, &c.Count); err != nil {
			rows.Close()
			return st, err
		}
		st.ThreadsByStatus = append(st.ThreadsByStatus, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}

	rows, err = s.pool.Query(ctx, `SELECT state, count(*) FROM tasks GROUP BY state ORDER BY state`)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var c StateCount
		if err := rows.Scan(&c.State, &c.Count); err != nil {
			rows.Close()
			return st, err
		}
		if c.State == StateNeedsInput {
			st.NeedsInput = c.Count
		}
		st.TasksByState = append(st.TasksByState, c)
	}
	rows.Close()
	return st, rows.Err()
}
