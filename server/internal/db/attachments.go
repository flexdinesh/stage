package db

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// MaxTaskAttachments is the per-task (and per-follow-up) image limit (T3's).
const MaxTaskAttachments = 8

// MaxAttachmentBytes is the per-image size limit (T3's).
const MaxAttachmentBytes = 10 << 20

// AttachmentMimeTypes are the accepted image types (T3's).
var AttachmentMimeTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// Attachment is an image's metadata (bytes via AttachmentData).
type Attachment struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	MimeType  string `json:"mimeType"`
	SizeBytes int    `json:"sizeBytes"`
}

// AttachmentData is an image with its bytes.
type AttachmentData struct {
	Attachment
	TaskID *int64
	Data   []byte
}

// CreateAttachment stores a staged (unattached) image.
func (s *Store) CreateAttachment(ctx context.Context, name, mimeType string, data []byte) (Attachment, error) {
	a := Attachment{Name: name, MimeType: mimeType, SizeBytes: len(data)}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO task_attachments (name, mime_type, size_bytes, data) VALUES ($1, $2, $3, $4) RETURNING id`,
		name, mimeType, len(data), data).Scan(&a.ID)
	return a, err
}

// GetAttachment returns one image with its bytes.
func (s *Store) GetAttachment(ctx context.Context, id int64) (AttachmentData, error) {
	var a AttachmentData
	err := s.pool.QueryRow(ctx,
		`SELECT id, task_id, name, mime_type, size_bytes, data FROM task_attachments WHERE id = $1`, id).
		Scan(&a.ID, &a.TaskID, &a.Name, &a.MimeType, &a.SizeBytes, &a.Data)
	if err != nil {
		return AttachmentData{}, notFound(err)
	}
	return a, nil
}

// DeleteAttachment removes a staged image, or one of a backlog task's images
// (Conflict once the task has left backlog).
func (s *Store) DeleteAttachment(ctx context.Context, id int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var state *TaskState
		err := tx.QueryRow(ctx, `
SELECT t.state FROM task_attachments a LEFT JOIN tasks t ON t.id = a.task_id
WHERE a.id = $1 FOR UPDATE OF a`, id).Scan(&state)
		if err != nil {
			return notFound(err)
		}
		if state != nil && *state != StateBacklog {
			return Conflict("task is %s: images are read-only outside backlog", *state)
		}
		_, err = tx.Exec(ctx, `DELETE FROM task_attachments WHERE id = $1`, id)
		return err
	})
}

// TaskAttachments returns a task's images with bytes, in upload order.
func (s *Store) TaskAttachments(ctx context.Context, taskID int64) ([]AttachmentData, error) {
	return s.attachmentData(ctx, `WHERE task_id = $1 ORDER BY id`, taskID)
}

// StagedAttachments returns the staged images ids (in ids order); Invalid
// when any is unknown or already attached.
func (s *Store) StagedAttachments(ctx context.Context, ids []int64) ([]AttachmentData, error) {
	rows, err := s.attachmentData(ctx, `WHERE task_id IS NULL AND schedule_id IS NULL AND id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	out := make([]AttachmentData, 0, len(ids))
	for _, id := range ids {
		i := slices.IndexFunc(rows, func(a AttachmentData) bool { return a.ID == id })
		if i < 0 {
			return nil, Invalid("attachment %d is not a pending upload", id)
		}
		out = append(out, rows[i])
	}
	return out, nil
}

func (s *Store) attachmentData(ctx context.Context, cond string, arg any) ([]AttachmentData, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, task_id, name, mime_type, size_bytes, data FROM task_attachments `+cond, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AttachmentData{}
	for rows.Next() {
		var a AttachmentData
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Name, &a.MimeType, &a.SizeBytes, &a.Data); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteStagedAttachments removes staged images (after a follow-up sent them).
func (s *Store) DeleteStagedAttachments(ctx context.Context, ids []int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM task_attachments WHERE task_id IS NULL AND schedule_id IS NULL AND id = ANY($1)`, ids)
	return err
}

// SweepStagedAttachments removes staged images older than age.
func (s *Store) SweepStagedAttachments(ctx context.Context, age time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM task_attachments WHERE task_id IS NULL AND schedule_id IS NULL AND created_at < $1`, time.Now().Add(-age))
	return tag.RowsAffected(), err
}

// setAttachments makes ids the task's exact image set: staged ids are
// attached, the task's images not in ids are deleted. Invalid when an id is
// neither staged nor the task's own, or when more than MaxTaskAttachments.
func setAttachments(ctx context.Context, tx pgx.Tx, taskID int64, ids []int64) error {
	return setOwnedAttachments(ctx, tx, "task_id", taskID, ids)
}

// setOwnedAttachments is setAttachments for an owner column (task_id or
// schedule_id). Staged = no owner.
func setOwnedAttachments(ctx context.Context, tx pgx.Tx, col string, ownerID int64, ids []int64) error {
	ids = uniqueIDs(ids)
	if len(ids) > MaxTaskAttachments {
		return Invalid("at most %d images per task", MaxTaskAttachments)
	}
	const staged = `(task_id IS NULL AND schedule_id IS NULL)`
	var usable int
	err := tx.QueryRow(ctx, `
SELECT count(*) FROM task_attachments
WHERE id = ANY($2) AND (`+staged+` OR `+col+` = $1)`, ownerID, ids).Scan(&usable)
	if err != nil {
		return err
	}
	if usable != len(ids) {
		return Invalid("unknown or already attached image in attachmentIds")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM task_attachments WHERE `+col+` = $1 AND NOT (id = ANY($2))`, ownerID, ids); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE task_attachments SET `+col+` = $1 WHERE `+staged+` AND id = ANY($2)`, ownerID, ids)
	return err
}

// copyScheduleAttachments copies a schedule's images onto task dst.
func copyScheduleAttachments(ctx context.Context, tx pgx.Tx, scheduleID, dst int64) error {
	_, err := tx.Exec(ctx, `
INSERT INTO task_attachments (task_id, name, mime_type, size_bytes, data)
SELECT $2, name, mime_type, size_bytes, data FROM task_attachments WHERE schedule_id = $1 ORDER BY id`, scheduleID, dst)
	return err
}

// copyAttachments copies src's images onto dst.
func copyAttachments(ctx context.Context, tx pgx.Tx, src, dst int64) error {
	_, err := tx.Exec(ctx, `
INSERT INTO task_attachments (task_id, name, mime_type, size_bytes, data)
SELECT $2, name, mime_type, size_bytes, data FROM task_attachments WHERE task_id = $1 ORDER BY id`, src, dst)
	return err
}

func uniqueIDs(ids []int64) []int64 {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
