//go:build postgres

package postgres

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type attachments struct{ *runner }

func (t *tx) Attachments() store.Attachments { return attachments{t.r} }

// ownerCol is the column of an attachment owner.
func ownerCol(o store.AttachmentOwner) string {
	if o == store.OwnerSchedule {
		return "schedule_id"
	}
	return "task_id"
}

func (r attachments) Create(ctx context.Context, name, mimeType string, data []byte) (store.Attachment, error) {
	a := store.Attachment{Name: name, MimeType: mimeType, SizeBytes: len(data)}
	err := r.QueryRow(ctx,
		`INSERT INTO task_attachments (name, mime_type, size_bytes, data) VALUES ($1, $2, $3, $4) RETURNING id`,
		name, mimeType, len(data), data).Scan(&a.ID)
	return a, err
}

func (r attachments) Get(ctx context.Context, id int64) (store.AttachmentData, error) {
	var a store.AttachmentData
	err := r.QueryRow(ctx,
		`SELECT id, task_id, name, mime_type, size_bytes, data FROM task_attachments WHERE id = $1`, id).
		Scan(&a.ID, &a.TaskID, &a.Name, &a.MimeType, &a.SizeBytes, &a.Data)
	if err != nil {
		return store.AttachmentData{}, notFound(err)
	}
	return a, nil
}

func (r attachments) TaskState(ctx context.Context, id int64) (*store.TaskState, error) {
	var state *store.TaskState
	err := r.QueryRow(ctx, `
SELECT t.state FROM task_attachments a LEFT JOIN tasks t ON t.id = a.task_id
WHERE a.id = $1`, id).Scan(&state)
	return state, notFound(err)
}

func (r attachments) Delete(ctx context.Context, id int64) error {
	_, err := r.Exec(ctx, `DELETE FROM task_attachments WHERE id = $1`, id)
	return err
}

func (r attachments) ForTask(ctx context.Context, taskID int64) ([]store.AttachmentData, error) {
	return r.data(ctx, `WHERE task_id = $1 ORDER BY id`, taskID)
}

func (r attachments) Staged(ctx context.Context, ids []int64) ([]store.AttachmentData, error) {
	return r.data(ctx, `WHERE task_id IS NULL AND schedule_id IS NULL AND id = ANY($1::bigint[])`, ids)
}

func (r attachments) data(ctx context.Context, cond string, arg any) ([]store.AttachmentData, error) {
	rows, err := r.Query(ctx, `SELECT id, task_id, name, mime_type, size_bytes, data FROM task_attachments `+cond, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.AttachmentData{}
	for rows.Next() {
		var a store.AttachmentData
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Name, &a.MimeType, &a.SizeBytes, &a.Data); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r attachments) DeleteStaged(ctx context.Context, ids []int64) error {
	_, err := r.Exec(ctx, `DELETE FROM task_attachments WHERE task_id IS NULL AND schedule_id IS NULL AND id = ANY($1::bigint[])`, ids)
	return err
}

func (r attachments) SweepStaged(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.Exec(ctx,
		`DELETE FROM task_attachments WHERE task_id IS NULL AND schedule_id IS NULL AND created_at < $1`, before)
	return tag.RowsAffected(), err
}

const stagedSQL = `(task_id IS NULL AND schedule_id IS NULL)`

func (r attachments) Usable(ctx context.Context, owner store.AttachmentOwner, ownerID int64, ids []int64) (int, error) {
	var usable int
	err := r.QueryRow(ctx, `
SELECT count(*) FROM task_attachments
WHERE id = ANY($2::bigint[]) AND (`+stagedSQL+` OR `+ownerCol(owner)+` = $1)`, ownerID, ids).Scan(&usable)
	return usable, err
}

func (r attachments) SetOwned(ctx context.Context, owner store.AttachmentOwner, ownerID int64, ids []int64) error {
	col := ownerCol(owner)
	if _, err := r.Exec(ctx, `DELETE FROM task_attachments WHERE `+col+` = $1 AND NOT (id = ANY($2::bigint[]))`, ownerID, ids); err != nil {
		return err
	}
	_, err := r.Exec(ctx, `UPDATE task_attachments SET `+col+` = $1 WHERE `+stagedSQL+` AND id = ANY($2::bigint[])`, ownerID, ids)
	return err
}

func (r attachments) CopyToTask(ctx context.Context, owner store.AttachmentOwner, ownerID, dst int64) error {
	_, err := r.Exec(ctx, `
INSERT INTO task_attachments (task_id, name, mime_type, size_bytes, data)
SELECT $2, name, mime_type, size_bytes, data FROM task_attachments WHERE `+ownerCol(owner)+` = $1 ORDER BY id`, ownerID, dst)
	return err
}
