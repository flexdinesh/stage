//go:build postgres

package postgres

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type access struct{ *runner }

func (t *tx) Access() store.Access { return access{t.r} }

const installationColumns = "COALESCE(installation_id, ''), mode, claimed_at"

func (r access) installation(ctx context.Context, lock string) (store.InstallationAccess, error) {
	var a store.InstallationAccess
	err := r.QueryRow(ctx, "SELECT "+installationColumns+" FROM installation_access WHERE id = 1"+lock).
		Scan(&a.ID, &a.Mode, &a.ClaimedAt)
	return a, notFound(err)
}

func (r access) Installation(ctx context.Context) (store.InstallationAccess, error) {
	return r.installation(ctx, "")
}

func (r access) InstallationForUpdate(ctx context.Context) (store.InstallationAccess, error) {
	return r.installation(ctx, " FOR NO KEY UPDATE")
}

func (r access) InitializeInstallation(ctx context.Context, id string, mode store.AccessMode) error {
	res, err := r.Exec(ctx, `UPDATE installation_access SET installation_id = COALESCE(installation_id, $1), mode = $2
		WHERE id = 1 AND mode = 'personal' AND $2 IN ('personal', 'setup_pending')`, id, mode)
	if err == nil && res.RowsAffected() != 1 {
		return store.ErrNotFound
	}
	if err != nil || mode != store.AccessPending {
		return err
	}
	_, err = r.Exec(ctx, `DELETE FROM push_subscriptions WHERE auth_session_id IS NULL`)
	return err
}

func (r access) MarkClaimed(ctx context.Context, at time.Time) error {
	res, err := r.Exec(ctx, `UPDATE installation_access SET mode = 'claimed', claimed_at = $1
		WHERE id = 1 AND mode = 'setup_pending'`, at)
	if err == nil && res.RowsAffected() != 1 {
		return store.ErrNotFound
	}
	return err
}

const accountColumns = "id, display_name, created_at, email, email_verified_at, role, status"

func scanAccount(row rowScanner) (store.Account, error) {
	var a store.Account
	err := row.Scan(&a.ID, &a.DisplayName, &a.CreatedAt, &a.Email, &a.EmailVerifiedAt, &a.Role, &a.Status)
	return a, notFound(err)
}

func (r access) Account(ctx context.Context, id int64) (store.Account, error) {
	return scanAccount(r.QueryRow(ctx, "SELECT "+accountColumns+" FROM users WHERE id = $1", id))
}

func (r access) AccountByEmail(ctx context.Context, email string) (store.Account, error) {
	return scanAccount(r.QueryRow(ctx, "SELECT "+accountColumns+" FROM users WHERE email_key = $1", store.EmailKey(email)))
}

func (r access) ListAccounts(ctx context.Context, beforeID int64, limit int) ([]store.Account, error) {
	rows, err := r.Query(ctx, `SELECT `+accountColumns+` FROM users
		WHERE email IS NOT NULL AND ($1::bigint = 0 OR id < $1)
		ORDER BY id DESC LIMIT $2`, beforeID, limit)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanAccount)
}

func (r access) ClaimOwner(ctx context.Context, a store.AccountInput) error {
	res, err := r.Exec(ctx, `UPDATE users SET display_name = $1, email = $2, email_key = $3
		WHERE id = 1 AND email IS NULL AND role = 'admin' AND status = 'active'
		AND EXISTS (SELECT 1 FROM installation_access WHERE id = 1 AND mode = 'setup_pending')`,
		strings.TrimSpace(a.DisplayName), strings.TrimSpace(a.Email), store.EmailKey(a.Email))
	if err == nil && res.RowsAffected() != 1 {
		return store.ErrNotFound
	}
	return err
}

func (r access) InsertMember(ctx context.Context, a store.AccountInput) (int64, error) {
	id, err := rowTo[int64](r.QueryRow(ctx, `INSERT INTO users(display_name, email, email_key)
		SELECT $1::text, $2::text, $3::text FROM installation_access WHERE id = 1 AND mode = 'claimed' RETURNING id`,
		strings.TrimSpace(a.DisplayName), strings.TrimSpace(a.Email), store.EmailKey(a.Email)))
	return id, notFound(err)
}

func (r access) InsertLocalCredential(ctx context.Context, userID int64, verifier string) error {
	id, err := rowTo[int64](r.QueryRow(ctx, `INSERT INTO identities(user_id, provider, subject)
		SELECT id, 'local', $1::text FROM users WHERE id = $2 AND email IS NOT NULL
		AND EXISTS (SELECT 1 FROM installation_access WHERE id = 1 AND mode = 'claimed') RETURNING id`,
		"user:"+strconv.FormatInt(userID, 10), userID))
	if err != nil {
		return notFound(err)
	}
	_, err = r.Exec(ctx, `INSERT INTO local_credentials(identity_id, password_verifier) VALUES ($1, $2)`, id, verifier)
	return err
}

func (r access) LocalVerifier(ctx context.Context, userID int64) (string, error) {
	v, err := rowTo[string](r.QueryRow(ctx, `SELECT c.password_verifier FROM local_credentials c
		JOIN identities i ON i.id = c.identity_id WHERE i.user_id = $1 AND i.provider = 'local' AND i.subject = $2`,
		userID, "user:"+strconv.FormatInt(userID, 10)))
	return v, notFound(err)
}

func (r access) UpdateLocalVerifier(ctx context.Context, userID int64, verifier string) error {
	res, err := r.Exec(ctx, `UPDATE local_credentials SET password_verifier = $1
		WHERE identity_id = (SELECT id FROM identities WHERE user_id = $2 AND provider = 'local' AND subject = $3)`,
		verifier, userID, "user:"+strconv.FormatInt(userID, 10))
	if err == nil && res.RowsAffected() != 1 {
		return store.ErrNotFound
	}
	return err
}

func (r access) UpdateDisplayName(ctx context.Context, userID int64, name string) error {
	res, err := r.Exec(ctx, `UPDATE users SET display_name = $1
		WHERE id = $2 AND email IS NOT NULL AND status = 'active'`, strings.TrimSpace(name), userID)
	if err == nil && res.RowsAffected() != 1 {
		return store.ErrNotFound
	}
	return err
}

func (r access) CountActiveAdmins(ctx context.Context) (int, error) {
	return rowTo[int](r.QueryRow(ctx, `SELECT count(*) FROM users
		WHERE role = 'admin' AND status = 'active' AND email IS NOT NULL`))
}

func (r access) SetAccountRole(ctx context.Context, userID int64, role store.AccountRole) error {
	res, err := r.Exec(ctx, `UPDATE users SET role = $1
		WHERE id = $2 AND email IS NOT NULL AND status = 'active'`, role, userID)
	if err == nil && res.RowsAffected() != 1 {
		return store.ErrNotFound
	}
	return err
}

func (r access) DisableAccount(ctx context.Context, userID int64) error {
	res, err := r.Exec(ctx, `UPDATE users SET status = 'disabled'
		WHERE id = $1 AND email IS NOT NULL AND status = 'active'`, userID)
	if err == nil && res.RowsAffected() != 1 {
		return store.ErrNotFound
	}
	return err
}

func (r access) InsertSession(ctx context.Context, digest store.TokenDigest, s store.AuthSession) (int64, error) {
	return rowTo[int64](r.QueryRow(ctx, `INSERT INTO auth_sessions(token_digest, user_id, audience, created_at, expires_at, idle_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, digest[:], s.UserID, s.Audience, s.CreatedAt, s.ExpiresAt, s.IdleExpiresAt))
}

func (r access) ActiveSession(ctx context.Context, digest store.TokenDigest, audience store.SessionAudience, now time.Time) (store.AuthSession, error) {
	var s store.AuthSession
	err := r.QueryRow(ctx, `SELECT s.id, s.user_id, s.audience, s.created_at, s.expires_at, s.idle_expires_at
		FROM auth_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_digest = $1 AND s.audience = $2 AND s.revoked_at IS NULL AND u.status = 'active'
		AND s.expires_at > $3 AND (s.idle_expires_at IS NULL OR s.idle_expires_at > $3)`, digest[:], audience, now).
		Scan(&s.ID, &s.UserID, &s.Audience, &s.CreatedAt, &s.ExpiresAt, &s.IdleExpiresAt)
	return s, notFound(err)
}

func (r access) RevokeSession(ctx context.Context, id, userID int64, at time.Time) error {
	_, err := r.Exec(ctx, `UPDATE auth_sessions SET revoked_at = $1 WHERE id = $2 AND user_id = $3 AND revoked_at IS NULL`, at, id, userID)
	if err != nil {
		return err
	}
	_, err = r.Exec(ctx, `DELETE FROM push_subscriptions WHERE auth_session_id = $1`, id)
	return err
}

func (r access) ExtendSessionIdle(ctx context.Context, id int64, now, deadline time.Time) error {
	_, err := r.Exec(ctx, `UPDATE auth_sessions SET idle_expires_at = CASE WHEN $1 < expires_at THEN $1 ELSE expires_at END
		WHERE id = $2 AND idle_expires_at < $1 AND idle_expires_at > $3 AND revoked_at IS NULL
		AND EXISTS (SELECT 1 FROM users WHERE id = auth_sessions.user_id AND status = 'active')`, deadline, id, now)
	return err
}

func (r access) RevokeUserSessions(ctx context.Context, userID int64, at time.Time) error {
	_, err := r.Exec(ctx, `UPDATE auth_sessions SET revoked_at = $1 WHERE user_id = $2 AND revoked_at IS NULL`, at, userID)
	if err != nil {
		return err
	}
	_, err = r.Exec(ctx, `DELETE FROM push_subscriptions WHERE auth_session_id IN (SELECT id FROM auth_sessions WHERE user_id = $1)`, userID)
	return err
}

func (r access) RevokeUserAudience(ctx context.Context, userID int64, audience store.SessionAudience, at time.Time) error {
	_, err := r.Exec(ctx, `UPDATE auth_sessions SET revoked_at = $1 WHERE user_id = $2 AND audience = $3 AND revoked_at IS NULL`, at, userID, audience)
	return err
}

const grantColumns = "id, purpose, issuer_user_id, email, target_user_id, created_at, expires_at, consumed_at, revoked_at"

func scanGrant(row rowScanner) (store.AuthGrant, error) {
	var g store.AuthGrant
	err := row.Scan(&g.ID, &g.Purpose, &g.IssuerUserID, &g.Email, &g.TargetUserID, &g.CreatedAt, &g.ExpiresAt, &g.ConsumedAt, &g.RevokedAt)
	return g, notFound(err)
}

func (r access) InsertGrant(ctx context.Context, digest store.TokenDigest, g store.AuthGrant) (int64, error) {
	var key *string
	if g.Email != nil {
		email := strings.TrimSpace(*g.Email)
		k := store.EmailKey(email)
		g.Email, key = &email, &k
	}
	return rowTo[int64](r.QueryRow(ctx, `INSERT INTO auth_grants(token_digest, purpose, issuer_user_id, email, email_key, target_user_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, digest[:], g.Purpose, g.IssuerUserID, g.Email, key, g.TargetUserID, g.CreatedAt, g.ExpiresAt))
}

func (r access) Grant(ctx context.Context, digest store.TokenDigest, purpose store.GrantPurpose) (store.AuthGrant, error) {
	return scanGrant(r.QueryRow(ctx, "SELECT "+grantColumns+" FROM auth_grants WHERE token_digest = $1 AND purpose = $2", digest[:], purpose))
}

func (r access) GrantByID(ctx context.Context, id int64) (store.AuthGrant, error) {
	return scanGrant(r.QueryRow(ctx, "SELECT "+grantColumns+" FROM auth_grants WHERE id = $1", id))
}

func (r access) OpenGrant(ctx context.Context, purpose store.GrantPurpose, email string, targetUserID int64) (store.AuthGrant, error) {
	const open = "SELECT " + grantColumns + " FROM auth_grants WHERE consumed_at IS NULL AND revoked_at IS NULL"
	switch purpose {
	case store.GrantSetup:
		return scanGrant(r.QueryRow(ctx, open+" AND purpose = 'setup'"))
	case store.GrantInvite:
		return scanGrant(r.QueryRow(ctx, open+" AND purpose = 'invite' AND email_key = $1", store.EmailKey(email)))
	case store.GrantPasswordReset:
		return scanGrant(r.QueryRow(ctx, open+" AND purpose = 'password_reset' AND target_user_id = $1", targetUserID))
	default:
		return store.AuthGrant{}, store.ErrNotFound
	}
}

func (r access) ListInvites(ctx context.Context, beforeID int64, limit int) ([]store.AuthGrant, error) {
	rows, err := r.Query(ctx, `SELECT `+grantColumns+` FROM auth_grants
		WHERE purpose = 'invite' AND ($1::bigint = 0 OR id < $1)
		ORDER BY id DESC LIMIT $2`, beforeID, limit)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanGrant)
}

func (r access) ConsumeGrant(ctx context.Context, digest store.TokenDigest, purpose store.GrantPurpose, now time.Time) (store.AuthGrant, error) {
	return scanGrant(r.QueryRow(ctx, `UPDATE auth_grants SET consumed_at = $1
		WHERE token_digest = $2 AND purpose = $3 AND created_at <= $1 AND expires_at > $1
		AND consumed_at IS NULL AND revoked_at IS NULL RETURNING `+grantColumns, now, digest[:], purpose))
}

func (r access) RevokeGrant(ctx context.Context, id int64, at time.Time) error {
	_, err := r.Exec(ctx, `UPDATE auth_grants SET revoked_at = $1 WHERE id = $2 AND consumed_at IS NULL AND revoked_at IS NULL`, at, id)
	return err
}

func (r access) InsertAccessAudit(ctx context.Context, a store.AccessAudit) error {
	_, err := r.Exec(ctx, `INSERT INTO access_audit(action, actor_user_id, target_user_id, grant_id, created_at)
		VALUES ($1, $2, $3, $4, $5)`, a.Action, a.ActorUserID, a.TargetUserID, a.GrantID, a.CreatedAt)
	return err
}

func (r access) DiscardTransientAccess(ctx context.Context) error {
	if _, err := r.Exec(ctx, `DELETE FROM auth_sessions`); err != nil {
		return err
	}
	_, err := r.Exec(ctx, `DELETE FROM auth_grants`)
	return err
}
