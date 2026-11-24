package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
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

func (r connections) HarnessIs(ctx context.Context, id int64, harness string) (bool, error) {
	var ok bool
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM harness_connections WHERE id=$1 AND harness=$2)`, id, harness).Scan(&ok)
	return ok, err
}

func (r connections) LegacyT3(ctx context.Context) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `SELECT COALESCE(`+legacyT3SQL+`,0)`).Scan(&id)
	return id, err
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

const connectionCols = `id,machine_id,harness,auto_complete,active,label,retired_at,forgotten_at,
 (SELECT count(*) FROM tasks t WHERE t.connection_id=c.id),
 (SELECT count(*) FROM project_bindings b WHERE b.connection_id=c.id AND b.active=1),new_task_config`

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
	err := r.QueryRow(ctx, `SELECT id,identity,auto_complete FROM harness_connections WHERE machine_id=$2 AND harness=$1 AND active=1`, harness, machine).Scan(&cur.ID, &cur.Identity, &cur.AutoComplete)
	return cur, notFound(err)
}

func (r connections) ActiveOnMachine(ctx context.Context, machine int64) ([]int64, error) {
	rows, err := r.Query(ctx, `SELECT id FROM harness_connections WHERE machine_id=$1 AND active=1`, machine)
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
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM harness_connections WHERE id=$1 AND machine_id=$3 AND harness=$2 AND active=0 AND forgotten_at IS NULL)`, id, harness, machine).Scan(&ok)
	return ok, err
}

func (r connections) RetiredWithIdentity(ctx context.Context, machine int64, harness, identity string) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `SELECT id FROM harness_connections WHERE machine_id=$3 AND harness=$2 AND active=0 AND identity=$1`, identity, harness, machine).Scan(&id)
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
	_, err := r.Exec(ctx, `UPDATE harness_connections SET active=0,retired_at=stage_now() WHERE id=$1`, id)
	return err
}

func (r connections) Revive(ctx context.Context, id int64, label, identity string) error {
	_, err := r.Exec(ctx, `UPDATE harness_connections SET active=1,retired_at=NULL,label=CASE WHEN $2<>'' THEN $2 ELSE label END,identity=CASE WHEN $3<>'' THEN $3 ELSE identity END WHERE id=$1`, id, label, identity)
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
		`UPDATE tasks SET connection_id=$2,updated_at=stage_now() WHERE connection_id=$1 AND state='backlog' AND session_id IS NULL`,
		`UPDATE schedules SET connection_id=$2,updated_at=stage_now() WHERE connection_id=$1`,
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
	return r.execAll(ctx, id, `DELETE FROM external_projects WHERE connection_id=$1`)
}

func (r connections) Delete(ctx context.Context, id int64) error {
	return r.execAll(ctx, id,
		`DELETE FROM external_projects WHERE connection_id=$1`,
		`DELETE FROM shared_paths WHERE connection_id=$1`,
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
	err := r.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM harness_connections WHERE id=$1 AND harness=$2 AND active=1 AND id<>$3)`, replacement, harness, id).Scan(&ok)
	return ok, err
}

func (r connections) ForgetState(ctx context.Context, id int64) (bool, bool, error) {
	var active bool
	var forgotten *string
	err := r.QueryRow(ctx, `SELECT active,forgotten_at FROM harness_connections WHERE id=$1`, id).Scan(&active, &forgotten)
	return active, forgotten != nil, notFound(err)
}

func (r connections) DeleteBindings(ctx context.Context, id int64) error {
	return r.execAll(ctx, id, `DELETE FROM project_bindings WHERE connection_id=$1`)
}

func (r connections) Forget(ctx context.Context, id int64) error {
	return r.execAll(ctx, id,
		`UPDATE harness_connections SET identity='',forgotten_at=stage_now() WHERE id=$1`,
		`UPDATE project_bindings SET active=0 WHERE connection_id=$1`,
		`DELETE FROM external_projects WHERE connection_id=$1`,
		`DELETE FROM shared_paths WHERE connection_id=$1`)
}

func (r connections) Moved(ctx context.Context) ([]store.Move, error) {
	rows, err := r.Query(ctx, `SELECT o.id,n.id FROM harness_connections o JOIN harness_connections n
 ON n.harness=o.harness AND n.identity=o.identity AND n.machine_id<>o.machine_id AND n.active=1 AND n.stable_identity=1
 WHERE o.active=1 AND o.identity<>'' AND o.stable_identity=1 ORDER BY o.id,n.id`)
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
	rows, err := r.Query(ctx, `SELECT id FROM harness_connections WHERE machine_id=$2 AND harness=$1 AND active=0 AND forgotten_at IS NULL ORDER BY retired_at DESC,id DESC`, harness, machine)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[int64])
}

func (r connections) EnsureOpenCode(ctx context.Context, machine int64) (int64, error) {
	if _, err := r.Exec(ctx, `INSERT INTO harness_connections(machine_id,harness) SELECT $1,'opencode' WHERE NOT EXISTS(SELECT 1 FROM harness_connections WHERE machine_id=$1 AND harness='opencode' AND active=1)`, machine); err != nil {
		return 0, err
	}
	var id int64
	err := r.QueryRow(ctx, `SELECT id FROM harness_connections WHERE machine_id=$1 AND harness='opencode' AND active=1`, machine).Scan(&id)
	return id, err
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

func (r connections) SharedPaths(ctx context.Context, machine int64) ([]store.SharedPath, error) {
	rows, err := r.Query(ctx, `SELECT p.connection_id,p.harness_root,p.stage_root FROM shared_paths p JOIN harness_connections c ON c.id=p.connection_id
 WHERE $1=0 OR c.machine_id=$1 ORDER BY p.connection_id,length(p.harness_root) DESC,p.harness_root`, machine)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.SharedPath, error) {
		var p store.SharedPath
		err := r.Scan(&p.ConnectionID, &p.HarnessRoot, &p.StageRoot)
		return p, err
	})
}

func (r connections) DeleteSharedPath(ctx context.Context, connectionID int64, harnessRoot string) error {
	_, err := r.Exec(ctx, `DELETE FROM shared_paths WHERE connection_id=$1 AND harness_root=$2`, connectionID, harnessRoot)
	return err
}

func (r connections) PutSharedPath(ctx context.Context, p store.SharedPath) error {
	_, err := r.Exec(ctx, `INSERT INTO shared_paths(connection_id,harness_root,stage_root) VALUES($1,$2,$3) ON CONFLICT(connection_id,harness_root) DO UPDATE SET stage_root=excluded.stage_root`, p.ConnectionID, p.HarnessRoot, p.StageRoot)
	return err
}

func (r connections) T3Credential(ctx context.Context, machine int64) (execution.Credential, error) {
	var clientID, token *string
	var expires *time.Time
	err := r.QueryRow(ctx,
		`SELECT client_id, token, expires_at FROM t3_connection WHERE machine_id = $1`, machine,
	).Scan(&clientID, &token, &expires)
	if err != nil {
		return execution.Credential{}, notFound(err)
	}
	var out execution.Credential
	if clientID != nil {
		out.ClientID = *clientID
	}
	if token != nil {
		out.Token = *token
	}
	if expires != nil {
		out.ExpiresAt = *expires
	}
	return out, nil
}

func (r connections) SaveT3Credential(ctx context.Context, machine int64, tok execution.Credential) error {
	var expires *time.Time
	if !tok.ExpiresAt.IsZero() {
		expires = &tok.ExpiresAt
	}
	_, err := r.Exec(ctx,
		`INSERT INTO t3_connection(machine_id, client_id, token, expires_at) VALUES($1, $2, $3, $4)
 ON CONFLICT(machine_id) DO UPDATE SET client_id = excluded.client_id, token = excluded.token, expires_at = excluded.expires_at, updated_at = `+nowSQL,
		machine, nullIfEmpty(tok.ClientID), nullIfEmpty(tok.Token), expires)
	return err
}

func (r connections) SetT3LastError(ctx context.Context, machine int64, msg string) error {
	_, err := r.Exec(ctx,
		`INSERT INTO t3_connection(machine_id, last_error) VALUES($1, $2)
 ON CONFLICT(machine_id) DO UPDATE SET last_error = excluded.last_error, updated_at = `+nowSQL, machine, nullIfEmpty(msg))
	return err
}

func (r connections) T3Settings(ctx context.Context, machine int64) (store.T3Connection, error) {
	var c store.T3Connection
	err := r.QueryRow(ctx,
		`SELECT (SELECT last_error FROM t3_connection WHERE machine_id=$1), (SELECT last_full_sync_at FROM t3_connection WHERE machine_id=$1), (SELECT t3_url FROM t3_connection WHERE machine_id=$1), (SELECT public_url FROM t3_connection WHERE machine_id=$1),
 (SELECT NULLIF(identity,'') FROM harness_connections WHERE machine_id=$1 AND harness='t3' AND active=1),
 COALESCE((SELECT id FROM harness_connections WHERE machine_id=$1 AND harness='t3' AND active=1),0)`, machine,
	).Scan(&c.LastError, &c.LastFullSyncAt, &c.T3URL, &c.T3PublicURL, &c.EnvironmentID, &c.ConnectionID)
	return c, err
}

func (r connections) SetT3URL(ctx context.Context, machine int64, u string) error {
	_, err := r.Exec(ctx,
		`INSERT INTO t3_connection(machine_id, t3_url) VALUES($1, $2)
 ON CONFLICT(machine_id) DO UPDATE SET t3_url = excluded.t3_url, updated_at = `+nowSQL, machine, nullIfEmpty(u))
	return err
}

func (r connections) SetT3PublicURL(ctx context.Context, machine int64, u string) error {
	_, err := r.Exec(ctx,
		`INSERT INTO t3_connection(machine_id, public_url) VALUES($1, $2)
 ON CONFLICT(machine_id) DO UPDATE SET public_url = excluded.public_url, updated_at = `+nowSQL, machine, nullIfEmpty(u))
	return err
}

func (r connections) T3AdminToken(ctx context.Context, machine int64) (string, error) {
	var tok *string
	err := r.QueryRow(ctx, `SELECT admin_token FROM t3_connection WHERE machine_id = $1`, machine).Scan(&tok)
	if errors.Is(err, sql.ErrNoRows) || tok == nil {
		return "", nil
	}
	return *tok, err
}

func (r connections) SaveT3AdminToken(ctx context.Context, machine int64, tok string) error {
	_, err := r.Exec(ctx,
		`INSERT INTO t3_connection(machine_id, admin_token) VALUES($1, $2)
 ON CONFLICT(machine_id) DO UPDATE SET admin_token = excluded.admin_token, updated_at = `+nowSQL, machine, nullIfEmpty(tok))
	return err
}

func (r connections) SetT3LastFullSync(ctx context.Context, machine int64, at time.Time) error {
	_, err := r.Exec(ctx,
		`INSERT INTO t3_connection(machine_id, last_full_sync_at) VALUES($1, $2)
 ON CONFLICT(machine_id) DO UPDATE SET last_full_sync_at = excluded.last_full_sync_at, updated_at = `+nowSQL, machine, at)
	return err
}

func (r connections) OpenCodeSettings(ctx context.Context, machine int64) (config.OpenCodeSettings, bool, error) {
	var c config.OpenCodeSettings
	err := r.QueryRow(ctx, `SELECT endpoint,browser_endpoint,web_client,username,password,auth,token_issued_at FROM opencode_settings WHERE machine_id=$1`, machine).Scan(&c.Endpoint, &c.BrowserEndpoint, &c.WebClient, &c.Username, &c.Password, &c.Auth, &c.TokenIssuedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return config.OpenCodeSettings{}, false, nil
	}
	return c, err == nil, err
}

func (r connections) OpenCodeIdentity(ctx context.Context, machine int64) (string, error) {
	var verified string
	err := r.QueryRow(ctx, `SELECT COALESCE((SELECT identity FROM harness_connections WHERE machine_id=$1 AND harness='opencode' AND active=1),'')`, machine).Scan(&verified)
	return verified, err
}

func (r connections) SaveOpenCodeSettings(ctx context.Context, machine int64, c config.OpenCodeSettings) error {
	_, err := r.Exec(ctx, `INSERT INTO opencode_settings(machine_id,endpoint,browser_endpoint,web_client,username,password,auth,token_issued_at) VALUES($8,$1,$2,$3,$4,$5,$6,$7)
 ON CONFLICT(machine_id) DO UPDATE SET endpoint=excluded.endpoint,browser_endpoint=excluded.browser_endpoint,web_client=excluded.web_client,username=excluded.username,password=excluded.password,auth=excluded.auth,token_issued_at=excluded.token_issued_at`, c.Endpoint, c.BrowserEndpoint, c.WebClient, c.Username, c.Password, c.Auth, c.TokenIssuedAt, machine)
	return err
}

// Lock is a no-op: BEGIN IMMEDIATE already serializes writes.
func (connections) Lock(context.Context, int64) error { return nil }
