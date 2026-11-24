package db

// Raw task reads for aggregates not yet behind repositories (transitional;
// removed once every aggregate is).

// closedSQL: task t is archived or its tree tr is not open.
const closedSQL = `(t.archived_at IS NOT NULL OR COALESCE(tr.status, 'open') <> 'open')`

const taskSelect = `
SELECT t.id, t.project_id, p.title, COALESCE((SELECT external_project_id FROM task_launch_destinations d WHERE d.task_id=t.id AND d.attempt=t.launch_attempt),(SELECT external_project_id FROM project_bindings b WHERE b.project_id=t.project_id AND b.connection_id=t.connection_id AND b.active=1),''), t.title, t.prompt, t.provider_instance_id,
  t.model, t.model_options, t.runtime_mode, t.interaction_mode, t.workspace,
  t.state, t.t3_thread_id, t.tree_id, tr.title, tr.start_mode, t.start_mode, ` + effectiveModeSQL + `, ` + readinessSQL + `,
  COALESCE((SELECT json_group_array(n.need_id ORDER BY n.need_id) FROM task_needs n WHERE n.task_id = t.id), '[]'),
  COALESCE((SELECT json_group_array(n.task_id ORDER BY n.task_id) FROM task_needs n WHERE n.need_id = t.id), '[]'),
  t.adopted, t.ready_at, t.launch_attempt, t.last_error,
  t.launched_at, t.finished_at, t.settled_at, th.run_started_at, th.run_ended_at, coalesce(th.last_word_preview, substr(json_extract(obs.observation,'$.reply.text'),1,200)), coalesce(th.last_word_at,json_extract(obs.observation,'$.reply.updatedAt')),
  COALESCE(th.pull_requests, '[]'), t.created_at, t.updated_at, th.link,
  t.start_at, t.start_tz, t.schedule_id, sc.title,
  COALESCE((SELECT external_id FROM session_identities WHERE id=t.origin_session_id AND connection_id IN ` + t3ConnectionsSQL + `),t.origin_thread_id), oth.title, t.handoff,
  COALESCE(min(th.pending_question_count, th.pending_request_count), 0),
  COALESCE((SELECT json_group_array(json_object('id', a.id, 'name', a.name, 'mimeType', a.mime_type,
    'sizeBytes', a.size_bytes) ORDER BY a.id) FROM task_attachments a WHERE a.task_id = t.id), '[]'),
  (SELECT max(e.at) FROM task_events e WHERE e.task_id = t.id AND e.to_state = t.state), th.updated_at,
  t.archived_at, tr.status, ` + unstartedSQL + `,
  t.connection_id,t.session_id,t.origin_session_id,
 (SELECT json_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=t.session_id),
 (SELECT json_object('connectionId',connection_id,'externalSessionId',external_id) FROM session_identities WHERE id=t.origin_session_id),t.acceptance_id,t.acceptance_revision,(SELECT accepted_at FROM task_acceptances a WHERE a.id=t.acceptance_id),(SELECT provenance FROM task_acceptances a WHERE a.id=t.acceptance_id),t.execution_config,
 hc.harness,hc.active,hc.label,t.created_by_user_id,t.source_machine_id
FROM tasks t
JOIN projects p ON p.id = t.project_id
JOIN harness_connections hc ON hc.id = t.connection_id
LEFT JOIN task_trees tr ON tr.id = t.tree_id
LEFT JOIN execution_observations obs ON obs.session_id=t.session_id
LEFT JOIN threads th ON th.session_id = t.session_id
LEFT JOIN schedules sc ON sc.id = t.schedule_id
LEFT JOIN threads oth ON oth.session_id = t.origin_session_id`

func scanTask(row rowScanner) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.ProjectID, &t.ProjectTitle, &t.T3ProjectID, &t.Title, &t.Prompt,
		&t.ProviderInstanceID, &t.Model, &t.ModelOptions, &t.RuntimeMode, &t.InteractionMode,
		&t.Workspace, &t.State, &t.T3ThreadID, &t.TreeID, &t.TreeTitle, &t.TreeStartMode,
		&t.StartMode, &t.EffectiveStartMode, &t.Readiness, &t.Needs, &t.Dependents, &t.Adopted, &t.ReadyAt,
		&t.LaunchAttempt, &t.LastError, &t.LaunchedAt, &t.FinishedAt, &t.SettledAt, &t.RunStartedAt, &t.RunEndedAt,
		&t.LastWordPreview, &t.LastWordAt, &t.PullRequests, &t.CreatedAt,
		&t.UpdatedAt, &t.ThreadLink, &t.StartAt, &t.StartTz, &t.ScheduleID, &t.ScheduleTitle,
		&t.OriginThreadID, &t.OriginThreadTitle, &t.Handoff, &t.PendingQuestionCount, &t.Attachments,
		&t.StateSince, &t.ThreadUpdatedAt, &t.ArchivedAt, &t.TreeStatus, &t.Deletable,
		&t.ConnectionID, &t.SessionIdentityID, &t.OriginSessionIdentityID, &t.Session, &t.OriginSession, &t.AcceptanceID, &t.AcceptanceRevision, &t.CompletedAt, &t.CompletionProvenance, &t.ExecutionConfig, &t.Harness, &t.ConnectionActive, &t.ConnectionLabel, &t.CreatedBy, &t.SourceMachineID)
	if err != nil {
		return Task{}, notFound(err)
	}
	if t.Session != nil {
		t.T3ThreadID = &t.Session.ExternalSessionID
	}
	t.ModelOptions = nonNilOptions(t.ModelOptions)
	t.PullRequests = nonNilPRs(t.PullRequests)
	t.Needs = nonNilIDs(t.Needs)
	t.Dependents = nonNilIDs(t.Dependents)
	if t.Workspace.Type == "" {
		t.Workspace.Type = "root"
	}
	return t, nil
}

func nonNilIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

func collectTasks(rows *rowSet, err error) ([]Task, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanTaskEvent(r rowScanner) (TaskEvent, error) {
	var e TaskEvent
	err := r.Scan(&e.ID, &e.TaskID, &e.FromState, &e.ToState, &e.Detail, &e.At)
	return e, err
}
