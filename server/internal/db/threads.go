package db

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// UpsertThreadDetail stores Stage-owned observations. Legacy run/PR history remains intact.
func (s *Store) UpsertThreadDetail(ctx context.Context, source execution.Source) error {
	return s.PutObservation(ctx, source.Observation())
}
func (s *Store) UpsertThreads(ctx context.Context, observations []execution.Observation) error {
	for _, o := range observations {
		if old, e := s.Observation(ctx, o.Session); e == nil {
			o.Branch = old.Branch
			o.Directory = old.Directory
			o.StartedAt = old.StartedAt
			o.EndedAt = old.EndedAt
		}
		if err := s.PutObservation(ctx, o); err != nil {
			return err
		}
	}
	return nil
}
func nonNilPRs(prs []execution.Artifact) []execution.Artifact {
	if prs == nil {
		return []execution.Artifact{}
	}
	return prs
}
func RunSpan(source execution.Source) (*time.Time, *time.Time) {
	o := source.Observation()
	return o.StartedAt, o.EndedAt
}

const threadSelect = `
SELECT th.t3_thread_id, th.t3_project_id, COALESCE(tk.project_id,p.id), COALESCE((SELECT title FROM projects WHERE id=tk.project_id),p.title), th.title, th.status, th.settled,
  th.settled_at, th.pending_request_count, min(th.pending_question_count, th.pending_request_count), th.provider_instance_id, th.model, th.runtime_mode,
  th.interaction_mode, th.branch, th.worktree_path, th.parent_thread_id, th.relationship,
  th.creation_source, tk.id, th.link, th.created_at, th.updated_at, th.synced_at, th.archived, th.active_run_id, th.latest_run_id,
  th.run_started_at, th.run_ended_at, th.last_word_preview, th.last_word_at, th.run_count, th.pull_requests, ` + threadStateSQL + `,
  COALESCE((SELECT connection_id FROM session_identities WHERE id=th.session_id),0)
FROM threads th
LEFT JOIN projects p ON p.id = COALESCE(th.stage_project_id,(SELECT min(b.project_id) FROM project_bindings b WHERE b.connection_id=COALESCE((SELECT connection_id FROM session_identities WHERE id=th.session_id),` + legacyT3SQL + `) AND b.external_project_id=th.t3_project_id AND b.active=1 HAVING count(*)=1))
LEFT JOIN tasks tk ON tk.session_id = th.session_id`

func scanThread(row rowScanner) (Thread, error) {
	var t Thread
	err := row.Scan(&t.T3ThreadID, &t.T3ProjectID, &t.ProjectID, &t.ProjectTitle, &t.Title, &t.Status,
		&t.Settled, &t.SettledAt, &t.PendingRequestCount, &t.PendingQuestionCount, &t.ProviderInstanceID, &t.Model, &t.RuntimeMode,
		&t.InteractionMode, &t.Branch, &t.WorktreePath, &t.ParentThreadID, &t.Relationship,
		&t.CreationSource, &t.TaskID, &t.Link, &t.CreatedAt, &t.UpdatedAt, &t.SyncedAt, &t.Archived, &t.ActiveRunID, &t.LatestRunID,
		&t.RunStartedAt, &t.RunEndedAt, &t.LastWordPreview, &t.LastWordAt, &t.RunCount, &t.PullRequests, &t.State, &t.ConnectionID)
	if err != nil {
		return Thread{}, notFound(err)
	}
	t.PullRequests = nonNilPRs(t.PullRequests)
	t.Session = SessionRef{ConnectionID: t.ConnectionID, ExternalSessionID: t.T3ThreadID}
	t.Source = SourceT3
	if t.TaskID != nil {
		t.Source = SourceApp
	}
	return t, nil
}

// ThreadRuns returns a thread's mirrored recent runs (newest first, at most
// 50); [] when unread. ErrNotFound for an unknown thread.
func (s *Store) ThreadRuns(ctx context.Context, id string) ([]ThreadRun, error) {
	var runs []ThreadRun
	if err := s.pool.QueryRow(ctx, `SELECT recent_runs FROM threads WHERE t3_thread_id = $1`, id).Scan(&runs); err != nil {
		return nil, notFound(err)
	}
	if runs == nil {
		runs = []ThreadRun{}
	}
	return runs, nil
}

func (s *Store) GetThread(ctx context.Context, id string) (Thread, error) {
	return scanThread(s.pool.QueryRow(ctx, threadSelect+` WHERE th.t3_thread_id = $1`, id))
}

type ThreadFilter struct {
	ProjectID    *int64
	States       []string
	Active       bool
	SortActivity bool
	Parent       *string
	Statuses     []string
	Settled      *bool
	Provider     string
	Source       string // app | t3 | ""
	Q            string
	// IDs keeps these T3 thread ids only (nil = no id filter).
	IDs      []string
	From, To *time.Time
	// ExcludeSubagents keeps root threads only (parent_thread_id IS NULL).
	ExcludeSubagents bool
	// NeedsInput keeps external threads needing input (see NeedsInputCount).
	NeedsInput bool
	// Running keeps external threads observed running (see externalRunning).
	Running bool
	// SettledFrom keeps external threads settled at or after it (see
	// externalSettled), newest settle first.
	SettledFrom *time.Time
	Limit       int
	Offset      int
}

// ListThreads returns a page of mirrored threads (createdAt desc) and the
// total match count.
func (s *Store) ListThreads(ctx context.Context, f ThreadFilter) ([]Thread, int, error) {
	var w where
	if len(f.States) > 0 {
		w.add("(" + threadStateSQL + ") IN (SELECT value FROM json_each(" + w.arg(jsonArray(f.States)) + "))")
	}
	if f.Active {
		w.add("NOT th.archived AND (" + threadStateSQL + ") IN ('running','needs_input')")
	}
	if f.Parent != nil {
		w.add("th.parent_thread_id = " + w.arg(*f.Parent))
	}
	if f.ProjectID != nil {
		w.add("p.id = " + w.arg(*f.ProjectID))
	}
	if len(f.Statuses) > 0 {
		w.add("th.status IN (SELECT value FROM json_each(" + w.arg(jsonArray(f.Statuses)) + "))")
	}
	if f.Settled != nil {
		w.add("th.settled = " + w.arg(*f.Settled))
	}
	if f.Provider != "" {
		w.add("th.provider_instance_id = " + w.arg(f.Provider))
	}
	switch f.Source {
	case SourceApp:
		w.add("tk.id IS NOT NULL")
	case SourceT3:
		w.add("tk.id IS NULL")
	}
	if f.Q != "" {
		w.add(likeSQL("th.title", w.arg(likePattern(f.Q))))
	}
	if f.IDs != nil {
		w.add("th.t3_thread_id IN (SELECT value FROM json_each(" + w.arg(jsonArray(f.IDs)) + "))")
	}
	if f.From != nil {
		w.add("th.created_at >= " + w.arg(*f.From))
	}
	if f.To != nil {
		w.add("th.created_at < " + w.arg(*f.To))
	}
	if f.ExcludeSubagents {
		w.add("th.parent_thread_id IS NULL")
	}
	if f.NeedsInput {
		w.add(externalNeedsInput)
	}
	if f.Running {
		w.add(externalRunning)
	}
	if f.SettledFrom != nil {
		w.add(externalSettled + " AND th.settled_at >= " + w.arg(*f.SettledFrom))
	}
	var total int
	countSQL := `SELECT count(*) FROM threads th
LEFT JOIN projects p ON p.id = COALESCE(th.stage_project_id,(SELECT min(b.project_id) FROM project_bindings b WHERE b.connection_id=COALESCE((SELECT connection_id FROM session_identities WHERE id=th.session_id),` + legacyT3SQL + `) AND b.external_project_id=th.t3_project_id AND b.active=1 HAVING count(*)=1))
LEFT JOIN tasks tk ON tk.session_id = th.session_id` + w.sql()
	if err := s.pool.QueryRow(ctx, countSQL, w.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "th.created_at"
	switch {
	case f.SettledFrom != nil:
		order = "th.settled_at"
	case f.SortActivity:
		order = "th.updated_at"
	}
	q := threadSelect + w.sql() + ` ORDER BY ` + order + ` DESC, th.t3_thread_id DESC LIMIT ` +
		w.arg(f.Limit) + ` OFFSET ` + w.arg(f.Offset)
	rows, err := s.pool.Query(ctx, q, w.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Thread{}
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

// The mirror's state follows T3 independently of frozen legacy task state.
// Input, activity, and failure outrank a stale settled flag.
const threadStateSQL = `COALESCE(th.execution_state,'done')`
