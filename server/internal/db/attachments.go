package db

import (
	"context"
	"slices"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// MaxTaskAttachments is the per-task (and per-follow-up) image limit (T3's).
const MaxTaskAttachments = 8

// MaxAttachmentBytes is the per-image size limit (T3's).
const MaxAttachmentBytes = 10 << 20

// AttachmentMimeTypes are the accepted image types (T3's).
var AttachmentMimeTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

type (
	Attachment     = store.Attachment
	AttachmentData = store.AttachmentData
)

// CreateAttachment stores a staged (unattached) image.
func (s *Store) CreateAttachment(ctx context.Context, name, mimeType string, data []byte) (Attachment, error) {
	return write(ctx, s, func(tx store.Tx) (Attachment, error) { return tx.Attachments().Create(ctx, name, mimeType, data) })
}

// GetAttachment returns one image with its bytes.
func (s *Store) GetAttachment(ctx context.Context, id int64) (AttachmentData, error) {
	return read(ctx, s, func(tx store.Tx) (AttachmentData, error) { return tx.Attachments().Get(ctx, id) })
}

// DeleteAttachment removes a staged image, or one of a backlog task's images
// (Conflict once the task has left backlog).
func (s *Store) DeleteAttachment(ctx context.Context, id int64) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		state, err := tx.Attachments().TaskState(ctx, id)
		if err != nil {
			return err
		}
		if state != nil && *state != StateBacklog {
			return Conflict("task is %s: images are read-only outside backlog", *state)
		}
		return tx.Attachments().Delete(ctx, id)
	})
}

// TaskAttachments returns a task's images with bytes, in upload order.
func (s *Store) TaskAttachments(ctx context.Context, taskID int64) ([]AttachmentData, error) {
	return read(ctx, s, func(tx store.Tx) ([]AttachmentData, error) { return tx.Attachments().ForTask(ctx, taskID) })
}

// StagedAttachments returns the staged images ids (in ids order); Invalid
// when any is unknown or already attached.
func (s *Store) StagedAttachments(ctx context.Context, ids []int64) ([]AttachmentData, error) {
	rows, err := read(ctx, s, func(tx store.Tx) ([]AttachmentData, error) { return tx.Attachments().Staged(ctx, ids) })
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

// DeleteStagedAttachments removes staged images (after a follow-up sent them).
func (s *Store) DeleteStagedAttachments(ctx context.Context, ids []int64) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Attachments().DeleteStaged(ctx, ids) })
}

// SweepStagedAttachments removes staged images older than age.
func (s *Store) SweepStagedAttachments(ctx context.Context, age time.Duration) (int64, error) {
	return write(ctx, s, func(tx store.Tx) (int64, error) { return tx.Attachments().SweepStaged(ctx, time.Now().Add(-age)) })
}

// setOwned makes ids the owner's exact image set: staged ids are attached,
// the owner's images not in ids are deleted. Invalid when an id is neither
// staged nor the owner's, or when more than MaxTaskAttachments.
func setOwned(ctx context.Context, tx store.Tx, owner store.AttachmentOwner, ownerID int64, ids []int64) error {
	ids = uniqueIDs(ids)
	if len(ids) > MaxTaskAttachments {
		return Invalid("at most %d images per task", MaxTaskAttachments)
	}
	usable, err := tx.Attachments().Usable(ctx, owner, ownerID, ids)
	if err != nil {
		return err
	}
	if usable != len(ids) {
		return Invalid("unknown or already attached image in attachmentIds")
	}
	return tx.Attachments().SetOwned(ctx, owner, ownerID, ids)
}

// setAttachments (transitional raw SQL for tasks and schedules) makes ids the task's exact image set: staged ids are
// attached, the task's images not in ids are deleted. Invalid when an id is
// neither staged nor the task's own, or when more than MaxTaskAttachments.
func setAttachments(ctx context.Context, tx *Tx, taskID int64, ids []int64) error {
	return setOwnedAttachments(ctx, tx, "task_id", taskID, ids)
}

// setOwnedAttachments is setAttachments for an owner column (task_id or
// schedule_id). Staged = no owner.
func setOwnedAttachments(ctx context.Context, tx *Tx, col string, ownerID int64, ids []int64) error {
	ids = uniqueIDs(ids)
	if len(ids) > MaxTaskAttachments {
		return Invalid("at most %d images per task", MaxTaskAttachments)
	}
	const staged = `(task_id IS NULL AND schedule_id IS NULL)`
	var usable int
	err := tx.QueryRow(ctx, `
SELECT count(*) FROM task_attachments
WHERE id IN (SELECT value FROM json_each($2)) AND (`+staged+` OR `+col+` = $1)`, ownerID, jsonArray(ids)).Scan(&usable)
	if err != nil {
		return err
	}
	if usable != len(ids) {
		return Invalid("unknown or already attached image in attachmentIds")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM task_attachments WHERE `+col+` = $1 AND id NOT IN (SELECT value FROM json_each($2))`, ownerID, jsonArray(ids)); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE task_attachments SET `+col+` = $1 WHERE `+staged+` AND id IN (SELECT value FROM json_each($2))`, ownerID, jsonArray(ids))
	return err
}

// copyScheduleAttachments copies a schedule's images onto task dst.
func copyScheduleAttachments(ctx context.Context, tx *Tx, scheduleID, dst int64) error {
	_, err := tx.Exec(ctx, `
INSERT INTO task_attachments (task_id, name, mime_type, size_bytes, data)
SELECT $2, name, mime_type, size_bytes, data FROM task_attachments WHERE schedule_id = $1 ORDER BY id`, scheduleID, dst)
	return err
}

// copyAttachments copies src's images onto dst.
func copyAttachments(ctx context.Context, tx *Tx, src, dst int64) error {
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
