//go:build postgres

package postgres

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

type connections struct{ *runner }

func (t *tx) Connections() store.Connections { return connections{t.r} }

func (r connections) Exists(ctx context.Context, id int64) (bool, error) {
	var exists bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM harness_connections WHERE id=$1)`, id).Scan(&exists)
	return exists, err
}

func (r connections) IsActive(ctx context.Context, id int64) (bool, error) {
	var active bool
	err := r.QueryRow(ctx, `SELECT active FROM harness_connections WHERE id=$1`, id).Scan(&active)
	return active, notFound(err)
}

func (r connections) MachineOf(ctx context.Context, id int64) (int64, error) {
	var machine int64
	err := r.QueryRow(ctx, `SELECT machine_id FROM harness_connections WHERE id=$1`, id).Scan(&machine)
	return machine, notFound(err)
}

func (r connections) Rows(ctx context.Context) ([]store.ConnectionRow, error) {
	rows, err := r.Query(ctx, `SELECT id,machine_id,harness,active FROM harness_connections ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.ConnectionRow, error) {
		var c store.ConnectionRow
		err := r.Scan(&c.ID, &c.Machine, &c.Harness, &c.Active)
		return c, err
	})
}

const connectionCols = `id,machine_id,harness,auto_complete,active,label,
 to_char(retired_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
 to_char(forgotten_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
 (SELECT count(*) FROM tasks t WHERE t.connection_id=c.id),
 (SELECT count(*) FROM project_bindings b WHERE b.connection_id=c.id AND b.active),new_task_config`

func scanConnection(r rowScanner) (store.HarnessConnection, error) {
	var c store.HarnessConnection
	err := r.Scan(&c.ID, &c.MachineID, &c.Harness, &c.AutoComplete, &c.Active, &c.Label, &c.RetiredAt, &c.ForgottenAt, &c.Tasks, &c.Bindings, &c.NewTaskConfig)
	return c, notFound(err)
}

func (r connections) List(ctx context.Context) ([]store.HarnessConnection, error) {
	rows, err := r.Query(ctx, `SELECT `+connectionCols+` FROM harness_connections c ORDER BY active DESC,id`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanConnection)
}

func (r connections) Get(ctx context.Context, id int64) (store.HarnessConnection, error) {
	return scanConnection(r.QueryRow(ctx, `SELECT `+connectionCols+` FROM harness_connections c WHERE id=$1`, id))
}

func (r connections) ConnectionFacts(ctx context.Context, id int64) (string, error) {
	var facts string
	err := r.QueryRow(ctx, `SELECT COALESCE(last_known_facts,'[]') FROM harness_connections WHERE id=$1 AND forgotten_at IS NULL`, id).Scan(&facts)
	return facts, notFound(err)
}

func (r connections) SaveConnectionFacts(ctx context.Context, id int64, facts string) error {
	_, err := r.Exec(ctx, `UPDATE harness_connections SET last_known_facts=$2
	    WHERE id=$1 AND active AND (last_known_facts IS NULL OR last_known_facts<>$2)`, id, facts)
	return err
}

func (r connections) SetAutoComplete(ctx context.Context, id int64, on bool) error {
	_, err := r.Exec(ctx, `UPDATE harness_connections SET auto_complete=$2 WHERE id=$1`, id, on)
	return err
}

func (r connections) SetNewTaskConfig(ctx context.Context, id int64, c *execution.Config) (bool, error) {
	res, err := r.Exec(ctx, `UPDATE harness_connections SET new_task_config=$2 WHERE id=$1`, id, c)
	return res.RowsAffected() > 0, err
}

func (r connections) Active(ctx context.Context, machine int64, harness string) (store.ActiveConnectionRow, error) {
	var cur store.ActiveConnectionRow
	err := r.QueryRow(ctx, `SELECT id,identity,auto_complete FROM harness_connections WHERE machine_id=$2 AND harness=$1 AND active`, harness, machine).Scan(&cur.ID, &cur.Identity, &cur.AutoComplete)
	return cur, notFound(err)
}

func (r connections) ActiveOnMachine(ctx context.Context, machine int64) ([]int64, error) {
	rows, err := r.Query(ctx, `SELECT id FROM harness_connections WHERE machine_id=$1 AND active`, machine)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[int64])
}

func (r connections) Insert(ctx context.Context, machine int64, harness, identity, label string, stableIdentity bool) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO harness_connections(machine_id,harness,identity,label,stable_identity) VALUES($4,$1,$2,$3,$5) RETURNING id`, harness, identity, label, machine, stableIdentity).Scan(&id)
	return id, err
}

func (r connections) InsertReplacement(ctx context.Context, machine int64, harness, identity, label string, autoComplete, stableIdentity bool, from int64) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO harness_connections(machine_id,harness,identity,label,auto_complete,stable_identity,new_task_config) VALUES($5,$1,$2,$3,$4,$6,(SELECT new_task_config FROM harness_connections WHERE id=$7)) RETURNING id`, harness, identity, label, autoComplete, machine, stableIdentity, from).Scan(&id)
	return id, err
}

func (r connections) Restorable(ctx context.Context, id, machine int64, harness string) (bool, error) {
	var ok bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM harness_connections WHERE id=$1 AND machine_id=$3 AND harness=$2 AND NOT active AND forgotten_at IS NULL)`, id, harness, machine).Scan(&ok)
	return ok, err
}

func (r connections) RetiredWithIdentity(ctx context.Context, machine int64, harness, identity string) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `SELECT id FROM harness_connections WHERE machine_id=$3 AND harness=$2 AND NOT active AND identity=$1`, identity, harness, machine).Scan(&id)
	return id, notFound(err)
}

func (r connections) SetLabel(ctx context.Context, id int64, label string) error {
	_, err := r.Exec(ctx, `UPDATE harness_connections SET label=$2 WHERE id=$1`, id, label)
	return err
}

func (r connections) SetIdentity(ctx context.Context, id int64, identity, label string) error {
	_, err := r.Exec(ctx, `UPDATE harness_connections SET identity=$2,label=$3 WHERE id=$1`, id, identity, label)
	return err
}

func (r connections) Retire(ctx context.Context, id int64) error {
	_, err := r.Exec(ctx, `UPDATE harness_connections SET active=false,retired_at=`+nowSQL+` WHERE id=$1`, id)
	return err
}

func (r connections) Revive(ctx context.Context, id int64, label, identity string) error {
	_, err := r.Exec(ctx, `UPDATE harness_connections SET active=true,retired_at=NULL,label=CASE WHEN $2::text<>'' THEN $2::text ELSE label END,identity=CASE WHEN $3::text<>'' THEN $3::text ELSE identity END WHERE id=$1`, id, label, identity)
	return err
}

func (r connections) HasRelations(ctx context.Context, id int64) (bool, error) {
	var related bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_bindings WHERE connection_id=$1)
 OR EXISTS(SELECT 1 FROM session_identities WHERE connection_id=$1)
 OR EXISTS(SELECT 1 FROM task_launch_destinations WHERE connection_id=$1)
 OR EXISTS(SELECT 1 FROM tasks WHERE connection_id=$1 AND NOT (state='backlog' AND session_id IS NULL))`, id).Scan(&related)
	return related, err
}

func (r connections) HasWork(ctx context.Context, id int64) (bool, error) {
	var pending bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE connection_id=$1) OR EXISTS(SELECT 1 FROM schedules WHERE connection_id=$1)`, id).Scan(&pending)
	return pending, err
}

func (r connections) Referenced(ctx context.Context, id int64) (bool, error) {
	var referenced bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE connection_id=$1)
 OR EXISTS(SELECT 1 FROM schedules WHERE connection_id=$1)
 OR EXISTS(SELECT 1 FROM session_identities WHERE connection_id=$1)
 OR EXISTS(SELECT 1 FROM task_launch_destinations WHERE connection_id=$1)`, id).Scan(&referenced)
	return referenced, err
}

func (r connections) execAll(ctx context.Context, id any, qs ...string) error {
	for _, q := range qs {
		if _, err := r.Exec(ctx, q, id); err != nil {
			return err
		}
	}
	return nil
}

func (r connections) FollowReplacement(ctx context.Context, from, to int64) error {
	for _, q := range []string{
		`UPDATE preferences SET default_connection_id=$2 WHERE default_connection_id=$1`,
		`UPDATE tasks SET connection_id=$2,binding_id=NULL,updated_at=` + nowSQL + ` WHERE connection_id=$1 AND state='backlog' AND session_id IS NULL`,
		`UPDATE schedules SET connection_id=$2,binding_id=NULL,updated_at=` + nowSQL + ` WHERE connection_id=$1`,
	} {
		if _, err := r.Exec(ctx, q, from, to); err != nil {
			return err
		}
	}
	return nil
}

func (r connections) ClearDefault(ctx context.Context, id int64) error {
	return r.execAll(ctx, id, `UPDATE preferences SET default_connection_id=NULL WHERE default_connection_id=$1`)
}

func (r connections) ClearDiscovery(ctx context.Context, id int64) error {
	return r.execAll(ctx, id, `DELETE FROM external_projects WHERE connection_id=$1`,
		`DELETE FROM workspace_declines WHERE connection_id=$1`)
}

func (r connections) Delete(ctx context.Context, id int64) error {
	return r.execAll(ctx, id,
		`DELETE FROM external_projects WHERE connection_id=$1`,
		`DELETE FROM workspace_declines WHERE connection_id=$1`,
		`DELETE FROM harness_connections WHERE id=$1`)
}

func (r connections) RetireState(ctx context.Context, id int64) (string, bool, error) {
	var harness string
	var active bool
	err := r.QueryRow(ctx, `SELECT harness,active FROM harness_connections WHERE id=$1 AND forgotten_at IS NULL`, id).Scan(&harness, &active)
	return harness, active, notFound(err)
}

func (r connections) IsActiveReplacement(ctx context.Context, replacement int64, harness string, id int64) (bool, error) {
	var ok bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM harness_connections WHERE id=$1 AND harness=$2 AND active AND id<>$3)`, replacement, harness, id).Scan(&ok)
	return ok, err
}

func (r connections) ForgetState(ctx context.Context, id int64) (bool, bool, error) {
	var active bool
	var forgotten bool
	err := r.QueryRow(ctx, `SELECT active,forgotten_at IS NOT NULL FROM harness_connections WHERE id=$1`, id).Scan(&active, &forgotten)
	return active, forgotten, notFound(err)
}

func (r connections) DeleteBindings(ctx context.Context, id int64) error {
	return r.execAll(ctx, id, `DELETE FROM project_bindings WHERE connection_id=$1`)
}

func (r connections) Forget(ctx context.Context, id int64) error {
	return r.execAll(ctx, id,
		`UPDATE harness_connections SET identity='',forgotten_at=`+nowSQL+` WHERE id=$1`,
		`UPDATE project_bindings SET active=false WHERE connection_id=$1`,
		`DELETE FROM external_projects WHERE connection_id=$1`,
		`DELETE FROM workspace_declines WHERE connection_id=$1`)
}

func (r connections) Moved(ctx context.Context) ([]store.Move, error) {
	rows, err := r.Query(ctx, `SELECT o.id,n.id FROM harness_connections o JOIN harness_connections n
 ON n.harness=o.harness AND n.identity=o.identity AND n.machine_id<>o.machine_id AND n.active AND n.stable_identity
 WHERE o.active AND o.identity<>'' AND o.stable_identity ORDER BY o.id,n.id`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.Move, error) {
		var m store.Move
		err := r.Scan(&m.Old, &m.New)
		return m, err
	})
}

func (r connections) Retired(ctx context.Context, machine int64, harness string) ([]int64, error) {
	rows, err := r.Query(ctx, `SELECT id FROM harness_connections WHERE machine_id=$2 AND harness=$1 AND NOT active AND forgotten_at IS NULL ORDER BY retired_at DESC NULLS LAST,id DESC`, harness, machine)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[int64])
}

func (r connections) Evidence(ctx context.Context, id int64) ([]store.ProjectBinding, []*execution.Observation, bool, error) {
	rows, err := r.Query(ctx, `SELECT DISTINCT external_project_id,workspace FROM project_bindings WHERE connection_id=$1`, id)
	if err != nil {
		return nil, nil, false, err
	}
	bindings, err := collectRows(rows, func(r rowScanner) (store.ProjectBinding, error) {
		var b store.ProjectBinding
		err := r.Scan(&b.ExternalProjectID, &b.Workspace)
		return b, err
	})
	if err != nil {
		return nil, nil, false, err
	}
	rows, err = r.Query(ctx, `SELECT o.observation FROM session_identities i LEFT JOIN execution_observations o ON i.id=o.session_id WHERE i.connection_id=$1`, id)
	if err != nil {
		return nil, nil, false, err
	}
	observations, err := collectRows(rows, rowTo[*execution.Observation])
	if err != nil {
		return nil, nil, false, err
	}
	var pending bool
	err = r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE connection_id=$1 AND state='launching')`, id).Scan(&pending)
	return bindings, observations, pending, err
}
