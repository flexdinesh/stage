//go:build postgres

package postgres

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type machines struct{ *runner }

func (t *tx) Machines() store.Machines { return machines{t.r} }

func (r machines) Get(ctx context.Context, id int64) (store.Machine, error) {
	var m store.Machine
	err := r.QueryRow(ctx, `SELECT id,label,created_by_user_id FROM machines WHERE id=$1`, id).Scan(&m.ID, &m.Label, &m.CreatedBy)
	return m, notFound(err)
}

func (r machines) List(ctx context.Context, id int64) ([]store.MachineInfo, error) {
	rows, err := r.Query(ctx, `SELECT id,label,created_by_user_id,created_at,last_seen_at,version FROM machines WHERE removed_at IS NULL AND ($1::bigint=0 OR id=$1::bigint) ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	ms, err := collectRows(rows, func(r rowScanner) (store.MachineInfo, error) {
		var m store.MachineInfo
		err := r.Scan(&m.ID, &m.Label, &m.CreatedBy, &m.CreatedAt, &m.LastSeenAt, &m.Version)
		m.Connections = []store.MachineConnection{}
		return m, err
	})
	if err != nil || len(ms) == 0 {
		return ms, err
	}
	rows, err = r.Query(ctx, `SELECT machine_id,id,harness,label FROM harness_connections WHERE active AND ($1::bigint=0 OR machine_id=$1::bigint) ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	type conn struct {
		machine int64
		c       store.MachineConnection
	}
	cs, err := collectRows(rows, func(r rowScanner) (conn, error) {
		var c conn
		err := r.Scan(&c.machine, &c.c.ID, &c.c.Harness, &c.c.Label)
		return c, err
	})
	if err != nil {
		return nil, err
	}
	for i := range ms {
		for _, c := range cs {
			if c.machine == ms[i].ID {
				ms[i].Connections = append(ms[i].Connections, c.c)
			}
		}
	}
	return ms, nil
}

func (r machines) PrunePairings(ctx context.Context, before time.Time) error {
	_, err := r.Exec(ctx, `DELETE FROM pairing_codes WHERE COALESCE(redeemed_at,expires_at)<$1`, before)
	return err
}

func (r machines) InsertPairing(ctx context.Context, hash []byte, label string, createdBy int64, expiresAt time.Time) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO pairing_codes(secret_hash,label,created_by_user_id,expires_at) VALUES($1,$2,$3,$4) RETURNING id`,
		hash, label, createdBy, expiresAt).Scan(&id)
	return id, err
}

func (r machines) Pairing(ctx context.Context, id int64) (store.PairingRow, error) {
	var p store.PairingRow
	err := r.QueryRow(ctx, `SELECT expires_at,machine_id,expires_at>`+nowSQL+` FROM pairing_codes WHERE id=$1`, id).Scan(&p.ExpiresAt, &p.MachineID, &p.Live)
	return p, notFound(err)
}

func (r machines) DeleteUnredeemedPairing(ctx context.Context, id int64) error {
	_, err := r.Exec(ctx, `DELETE FROM pairing_codes WHERE id=$1 AND redeemed_at IS NULL`, id)
	return err
}

func (r machines) RedeemablePairing(ctx context.Context, id int64) (store.PairingSecret, error) {
	var p store.PairingSecret
	err := r.QueryRow(ctx, `SELECT secret_hash,label,created_by_user_id FROM pairing_codes WHERE id=$1 AND redeemed_at IS NULL AND expires_at>`+nowSQL, id).Scan(&p.Hash, &p.Label, &p.CreatedBy)
	return p, notFound(err)
}

func (r machines) MarkRedeemed(ctx context.Context, id, machine int64) (bool, error) {
	res, err := r.Exec(ctx, `UPDATE pairing_codes SET redeemed_at=`+nowSQL+`,machine_id=$2 WHERE id=$1 AND redeemed_at IS NULL`, id, machine)
	return res.RowsAffected() == 1, err
}

func (r machines) InsertMachine(ctx context.Context, label string, createdBy int64, version string) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO machines(label,created_by_user_id,last_seen_at,version) VALUES($1,$2,`+nowSQL+`,$3) RETURNING id`, label, createdBy, version).Scan(&id)
	return id, err
}

func (r machines) SetLabel(ctx context.Context, id int64, label string) error {
	_, err := r.Exec(ctx, `UPDATE machines SET label=$2 WHERE id=$1`, id, label)
	return err
}

func (r machines) SetLabelIfEmpty(ctx context.Context, id int64, label string) (bool, error) {
	res, err := r.Exec(ctx, `UPDATE machines SET label=$2 WHERE id=$1 AND label=''`, id, label)
	return res.RowsAffected() > 0, err
}

func (r machines) Rename(ctx context.Context, id int64, label string) (bool, error) {
	res, err := r.Exec(ctx, `UPDATE machines SET label=$2 WHERE id=$1 AND removed_at IS NULL`, id, label)
	return res.RowsAffected() > 0, err
}

func (r machines) InsertToken(ctx context.Context, machine int64, hash []byte) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO machine_tokens(machine_id,secret_hash) VALUES($1,$2) RETURNING id`, machine, hash).Scan(&id)
	return id, err
}

func (r machines) Token(ctx context.Context, id int64) (int64, []byte, error) {
	var machine int64
	var stored []byte
	err := r.QueryRow(ctx, `SELECT t.machine_id,t.secret_hash FROM machine_tokens t JOIN machines m ON m.id=t.machine_id WHERE t.id=$1 AND m.removed_at IS NULL`, id).Scan(&machine, &stored)
	return machine, stored, notFound(err)
}

func (r machines) Touch(ctx context.Context, id int64, at time.Time, version string) error {
	_, err := r.Exec(ctx, `UPDATE machines SET last_seen_at=CASE WHEN last_seen_at IS NULL OR last_seen_at<$2::timestamptz THEN $2::timestamptz ELSE last_seen_at END, version=CASE WHEN $3::text<>'' THEN $3::text ELSE version END WHERE id=$1 AND id<>1 AND removed_at IS NULL`, id, at, version)
	return err
}

func (r machines) MarkRemoved(ctx context.Context, id int64) (bool, error) {
	res, err := r.Exec(ctx, `UPDATE machines SET removed_at=`+nowSQL+` WHERE id=$1 AND removed_at IS NULL`, id)
	return res.RowsAffected() > 0, err
}

func (r machines) DeleteTokens(ctx context.Context, machine int64) error {
	_, err := r.Exec(ctx, `DELETE FROM machine_tokens WHERE machine_id=$1`, machine)
	return err
}
