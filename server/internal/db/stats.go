package db

import (
	"context"
	"time"
)

type DayCount struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

type ProjectCount struct {
	ProjectID   *int64 `json:"projectId"`
	T3ProjectID string `json:"t3ProjectId"`
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

	rows, err := s.pool.Query(ctx, `
SELECT to_char(date_trunc('day', created_at AT TIME ZONE $3), 'YYYY-MM-DD') AS day, count(*)
FROM threads WHERE created_at >= $1 AND created_at < $2
  AND (NOT $4 OR parent_thread_id IS NULL)
GROUP BY day ORDER BY day`, from, to, tz, excludeSubagents)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var d DayCount
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			rows.Close()
			return st, err
		}
		st.ThreadsByDay = append(st.ThreadsByDay, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}

	rows, err = s.pool.Query(ctx, `
SELECT p.id, th.t3_project_id, COALESCE(p.title, th.t3_project_id), count(*) AS n
FROM threads th LEFT JOIN projects p ON p.t3_project_id = th.t3_project_id
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
