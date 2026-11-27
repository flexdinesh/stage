//go:build postgres

package postgres

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

const bindingWorkspaceJoin = `FROM project_bindings b
 JOIN harness_connections c ON c.id=b.connection_id
 LEFT JOIN external_projects e ON e.connection_id=b.connection_id AND e.external_id=b.external_project_id AND e.workspace=b.workspace`

const projectBindingJSON = `jsonb_build_object('id',b.id,'projectId',b.project_id,'connectionId',b.connection_id,'harness',c.harness,
 'externalProjectId',b.external_project_id,'workspace',b.workspace,'revision',b.revision,
 'status',b.status,'observedWorkspace',b.observed_workspace,'connectionActive',c.active,'connectionLabel',c.label,
 'primary',b.is_primary,'rule',b.rule,'machineId',c.machine_id,'title',COALESCE(e.title,''),
 'remote',COALESCE(e.repo,''),'subpath',COALESCE(e.repo_path,''),'fileHost',e.file_host,'evidenceAt',to_char(e.evidence_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
 'facts',CASE WHEN e.archived THEN jsonb_build_array(jsonb_build_object('id','archived','label','Archived','value','in ' || c.harness,'tone','neutral')) ELSE '[]'::jsonb END)`

func (r projects) LockIdentity(ctx context.Context) error {
	const lockID int64 = 0x5374616765496431
	_, err := r.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID)
	return err
}

func (r projects) WorkspaceProjectReferences(ctx context.Context, b store.ProjectBinding) (historical, group *int64, err error) {
	err = r.QueryRow(ctx, `SELECT historical_project_id,group_project_id FROM external_projects
 WHERE connection_id=$1 AND external_id=$2 AND workspace=$3`,
		b.ConnectionID, b.ExternalProjectID, b.Workspace).Scan(&historical, &group)
	return historical, group, notFound(err)
}

func (r projects) SetHistoricalWorkspaceProject(ctx context.Context, b store.ProjectBinding, projectID int64) error {
	_, err := r.Exec(ctx, `UPDATE external_projects SET historical_project_id=$4
 WHERE connection_id=$1 AND external_id=$2 AND workspace=$3`, b.ConnectionID, b.ExternalProjectID, b.Workspace, projectID)
	return err
}

func (r projects) WorkspaceRows(ctx context.Context) ([]store.StoredWorkspace, error) {
	rows, err := r.Query(ctx, `SELECT e.connection_id,e.external_id,e.title,e.workspace,e.available,e.importable,e.archived,e.active_at,
 b.project_id,b.id,COALESCE(b.is_primary,false),e.file_host,e.canonical_path,e.repo,e.repo_path,e.evidence_at,e.evidence_source,
 e.resolution,e.resolution_reason,e.resolution_rule,e.candidates,c.machine_id,c.active,e.checkout_key,e.inode_hint,
 e.group_project_id,e.group_revision,e.historical_project_id
 FROM external_projects e
 JOIN harness_connections c ON c.id=e.connection_id
 LEFT JOIN project_bindings b ON b.connection_id=e.connection_id AND b.external_project_id=e.external_id AND b.workspace=e.workspace AND b.active
 ORDER BY e.connection_id,e.external_id,e.workspace`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(row rowScanner) (store.StoredWorkspace, error) {
		var w store.StoredWorkspace
		err := row.Scan(&w.ConnectionID, &w.ExternalID, &w.Title, &w.Workspace.Workspace, &w.Available, &w.Importable, &w.HarnessArchived, &w.ActiveAt,
			&w.ProjectID, &w.BindingID, &w.Primary, &w.FileHost, &w.CanonicalPath, &w.Remote, &w.Subpath, &w.EvidenceAt, &w.EvidenceSource,
			&w.Resolution.Outcome, &w.Resolution.Reason, &w.Resolution.Rule, &w.Resolution.Candidates,
			&w.MachineID, &w.ConnectionActive, &w.CheckoutKey, &w.InodeHint, &w.GroupProjectID, &w.GroupRevision, &w.HistoricalProjectID)
		return w, err
	})
}

func (r projects) SetWorkspaceGroup(ctx context.Context, w store.StoredWorkspace, group int64, attribution store.Attribution) error {
	_, err := r.Exec(ctx, `UPDATE external_projects SET group_project_id=$4,group_revision=group_revision+1,group_attribution=$5
 WHERE connection_id=$1 AND external_id=$2 AND workspace=$3`, w.ConnectionID, w.ExternalID, w.Workspace.Workspace, group, attribution)
	return err
}

func (r projects) IdentityBindings(ctx context.Context) ([]store.IdentityBinding, error) {
	rows, err := r.Query(ctx, `SELECT `+projectBindingJSON+`,
 COALESCE(e.available,b.status='available'), COALESCE(e.canonical_path,''), COALESCE(e.checkout_key,''), COALESCE(e.inode_hint,'')
 `+bindingWorkspaceJoin+` WHERE b.active ORDER BY b.id`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(row rowScanner) (store.IdentityBinding, error) {
		var b store.IdentityBinding
		err := row.Scan(&b.ProjectBinding, &b.Available, &b.CanonicalPath, &b.CheckoutKey, &b.InodeHint)
		return b, err
	})
}

func (r projects) WorkspaceDeclines(ctx context.Context) ([]store.WorkspaceDecline, error) {
	rows, err := r.Query(ctx, `SELECT connection_id,identity_key FROM workspace_declines ORDER BY connection_id,identity_key`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(row rowScanner) (store.WorkspaceDecline, error) {
		var decline store.WorkspaceDecline
		err := row.Scan(&decline.ConnectionID, &decline.Key)
		return decline, err
	})
}

func (r projects) SetWorkspaceDecline(ctx context.Context, connection int64, key string, decline bool) error {
	if decline {
		_, err := r.Exec(ctx, `INSERT INTO workspace_declines(connection_id,identity_key) VALUES($1,$2)
 ON CONFLICT(connection_id,identity_key) DO NOTHING`, connection, key)
		return err
	}
	_, err := r.Exec(ctx, `DELETE FROM workspace_declines WHERE connection_id=$1 AND identity_key=$2`, connection, key)
	return err
}

func (r projects) SetWorkspaceResolution(ctx context.Context, connection int64, externalID, path string, v store.WorkspaceResolution) error {
	_, err := r.Exec(ctx, `UPDATE external_projects SET resolution=$4,resolution_reason=$5,resolution_rule=$6,candidates=$7::jsonb
 WHERE connection_id=$1 AND external_id=$2 AND workspace=$3`, connection, externalID, path, v.Outcome, v.Reason, v.Rule, jsonArray(v.Candidates))
	return err
}

func scanBindingState(row rowScanner) (store.BindingState, error) {
	var b store.BindingState
	err := row.Scan(&b.ProjectBinding, &b.Active)
	return b, notFound(err)
}

func (r projects) Binding(ctx context.Context, id int64) (store.BindingState, error) {
	return scanBindingState(r.QueryRow(ctx, `SELECT `+projectBindingJSON+`,b.active `+bindingWorkspaceJoin+` WHERE b.id=$1`, id))
}

func (r projects) ActiveWorkspaceBinding(ctx context.Context, connection int64, externalID, path string) (store.BindingState, error) {
	return scanBindingState(r.QueryRow(ctx, `SELECT `+projectBindingJSON+`,b.active `+bindingWorkspaceJoin+`
 WHERE b.connection_id=$1 AND b.external_project_id=$2 AND b.workspace=$3 AND b.active`, connection, externalID, path))
}

func (r projects) DeactivateBinding(ctx context.Context, id int64) error {
	_, err := r.Exec(ctx, `UPDATE project_bindings SET active=false,is_primary=false WHERE id=$1 AND active`, id)
	return err
}

func (r projects) PromoteBinding(ctx context.Context, project, connection, id int64) error {
	const activeBindings = `project_id=$1 AND connection_id=$2 AND active`
	if _, err := r.Exec(ctx, `UPDATE project_bindings SET is_primary=false WHERE `+activeBindings+` AND is_primary
 AND ($3::bigint=0 OR EXISTS(SELECT 1 FROM project_bindings WHERE `+activeBindings+` AND id=$3))`, project, connection, id); err != nil {
		return err
	}
	result, err := r.Exec(ctx, `UPDATE project_bindings SET is_primary=true WHERE `+activeBindings+`
 AND id=COALESCE(NULLIF($3::bigint,0),(SELECT min(id) FROM project_bindings WHERE `+activeBindings+`))`, project, connection, id)
	if err == nil && id != 0 && result.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return err
}

func (r projects) MarkUnavailable(ctx context.Context, connectionID int64) error {
	_, err := r.Exec(ctx, `UPDATE external_projects SET available=false WHERE connection_id=$1`, connectionID)
	return err
}

func (r projects) UpsertDiscovered(ctx context.Context, connectionID int64, w store.DiscoveredWorkspace) error {
	if w.EvidenceSource == "harness" {
		w.FileHost = nil
		w.CanonicalPath, w.CheckoutKey, w.InodeHint = "", "", ""
	}
	// An available report refreshes legacy status even when it supplies no new
	// evidence. A gone report retains both the previous evidence and its time.
	_, err := r.Exec(ctx, `INSERT INTO external_projects(connection_id,external_id,title,workspace,available,importable,archived,active_at,
 repo,repo_path,file_host,canonical_path,checkout_key,inode_hint,evidence_source,evidence_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,CASE WHEN $5 OR $15<>'' THEN `+nowSQL+` END)
 ON CONFLICT(connection_id,external_id,workspace) DO UPDATE SET title=excluded.title,available=excluded.available,
 importable=excluded.importable,archived=excluded.archived,active_at=excluded.active_at,
 repo=CASE WHEN excluded.evidence_source<>'' THEN excluded.repo ELSE external_projects.repo END,
 repo_path=CASE WHEN excluded.evidence_source<>'' THEN excluded.repo_path ELSE external_projects.repo_path END,
 file_host=CASE WHEN excluded.evidence_source<>'' THEN excluded.file_host ELSE external_projects.file_host END,
 canonical_path=CASE WHEN excluded.evidence_source<>'' THEN excluded.canonical_path ELSE external_projects.canonical_path END,
 checkout_key=CASE WHEN excluded.evidence_source<>'' THEN excluded.checkout_key ELSE external_projects.checkout_key END,
 inode_hint=CASE WHEN excluded.evidence_source<>'' THEN excluded.inode_hint ELSE external_projects.inode_hint END,
 evidence_source=COALESCE(NULLIF(excluded.evidence_source,''),external_projects.evidence_source),
 evidence_at=CASE WHEN excluded.evidence_source<>'' THEN excluded.evidence_at
   ELSE COALESCE(external_projects.evidence_at,excluded.evidence_at) END`,
		connectionID, w.ExternalID, w.Title, w.Workspace, w.Available, w.Importable, w.Archived, w.ActiveAt,
		w.Repo, w.RepoPath, w.FileHost, w.CanonicalPath, w.CheckoutKey, w.InodeHint, w.EvidenceSource)
	return err
}

func (r projects) RefreshBindingStatus(ctx context.Context, connectionID int64) error {
	_, err := r.Exec(ctx, `UPDATE project_bindings SET
 status=CASE WHEN EXISTS(SELECT 1 FROM external_projects e WHERE e.connection_id=$1 AND e.external_id=project_bindings.external_project_id AND e.workspace=project_bindings.workspace AND e.available) THEN 'available'
 WHEN EXISTS(SELECT 1 FROM external_projects e WHERE e.connection_id=$1 AND e.external_id=project_bindings.external_project_id AND e.available) THEN 'changed' ELSE 'missing' END,
 observed_workspace=(SELECT workspace FROM external_projects e WHERE e.connection_id=$1 AND e.external_id=project_bindings.external_project_id AND e.available ORDER BY workspace LIMIT 1)
 WHERE connection_id=$1 AND active`, connectionID)
	return err
}
