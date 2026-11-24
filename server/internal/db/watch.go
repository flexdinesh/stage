package db

import "context"

// externalNeedsInput: external threads needing input (badge,
// threads?needsInput=1). Needs th + LEFT JOIN tasks tk. Subagents excluded,
// forks included.
const externalNeedsInput = `th.observed_state = 'needs_input'
  AND th.relationship IS NOT 'subagent' AND tk.id IS NULL`

// externalRunning: external threads observed running (dashboard,
// threads?running=1). Same scope as externalNeedsInput.
const externalRunning = `th.observed_state = 'running'
  AND th.relationship IS NOT 'subagent' AND tk.id IS NULL`

// externalSettled: external threads whose mirror state is settled
// (dashboard, threads?settledFrom=). Same scope as externalNeedsInput;
// archived threads included.
const externalSettled = `(` + threadStateSQL + `) = 'settled'
  AND th.relationship IS NOT 'subagent' AND tk.id IS NULL`

// NeedsInputCount: tasks in needs_input + external threads needing input.
func (s *Store) NeedsInputCount(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM tasks WHERE state = 'needs_input')
  + (SELECT count(*) FROM threads th LEFT JOIN tasks tk ON tk.session_id = th.session_id
     WHERE `+externalNeedsInput+` AND NOT EXISTS (SELECT 1 FROM execution_observations o WHERE o.session_id=th.session_id))
  + (SELECT count(*) FROM execution_observations o WHERE json_extract(o.observation,'$.state')='needs_input'
     AND json_extract(o.observation,'$.child')=0 AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.session_id=o.session_id))`).Scan(&n)
	return n, err
}

// WatchedThreadIDs: external non-subagent threads the watcher last saw running,
// needing input or done (re-read until they leave those states; done until
// observed again, including when work resumes).
func (s *Store) WatchedThreadIDs(ctx context.Context, connection int64) ([]string, error) {
	// Scoped to the connection: a replaced T3's threads are never watched
	// against the T3 that replaced it.
	rows, err := s.pool.Query(ctx, `
SELECT th.t3_thread_id FROM threads th
WHERE (th.observed_state IN ('running', 'needs_input', 'done') OR (`+threadStateSQL+`) IN ('running','needs_input'))
  AND th.relationship IS NOT 'subagent'
  AND (th.session_id IN (SELECT id FROM session_identities WHERE connection_id=$1)
    OR (th.session_id IS NULL AND $1=`+legacyT3SQL+`))
ORDER BY th.created_at`, connection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ThreadObservation is the watcher's stored view of a thread.
type ThreadObservation struct {
	ObservedState *TaskState // nil = never observed (or not mirrored)
	TaskOwned     bool       // a task has this thread
}

// ThreadObservations returns an entry for every id.
func (s *Store) ThreadObservations(ctx context.Context, ids []string) (map[string]ThreadObservation, error) {
	rows, err := s.pool.Query(ctx, `
SELECT i.id, th.observed_state, EXISTS (SELECT 1 FROM tasks tk WHERE tk.connection_id=`+legacyT3SQL+` AND tk.t3_thread_id = i.id)
FROM (SELECT value AS id FROM json_each($1)) AS i LEFT JOIN threads th ON th.t3_thread_id = i.id`, jsonArray(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]ThreadObservation, len(ids))
	for rows.Next() {
		var id string
		var o ThreadObservation
		if err := rows.Scan(&id, &o.ObservedState, &o.TaskOwned); err != nil {
			return nil, err
		}
		out[id] = o
	}
	return out, rows.Err()
}

// SetObservedState records the watcher's mapped state for a mirrored thread
// of ref's connection (scoped like WatchedThreadIDs: another T3 connection's
// thread is never touched).
func (s *Store) SetObservedState(ctx context.Context, ref SessionRef, st TaskState) error {
	_, err := s.pool.Exec(ctx, `UPDATE threads SET observed_state = $2 WHERE t3_thread_id = $1
 AND (session_id IN (SELECT id FROM session_identities WHERE connection_id=$3)
   OR (session_id IS NULL AND $3=`+legacyT3SQL+`))`, ref.ExternalSessionID, string(st), ref.ConnectionID)
	return err
}
