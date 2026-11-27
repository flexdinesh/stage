package db

import (
	"context"
	"slices"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// MaxTaskAttachments is the per-task (and per-follow-up) image limit.
const MaxTaskAttachments = 8

// MaxAttachmentBytes is the per-image size limit.
const MaxAttachmentBytes = 10 << 20

// AttachmentMimeTypes are the accepted image types.
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

func attachDuplicateImages(ctx context.Context, tx store.Tx, source, target int64, ids []int64) error {
	ids = uniqueIDs(ids)
	if len(ids) > MaxTaskAttachments {
		return Invalid("at most %d images per task", MaxTaskAttachments)
	}
	attachments := tx.Attachments()
	usable, err := attachments.Usable(ctx, store.OwnerTask, source, ids)
	if err != nil {
		return err
	}
	if usable != len(ids) {
		return Invalid("unknown or already attached image in attachmentIds")
	}
	for i, id := range ids {
		image, err := attachments.Get(ctx, id)
		if err != nil {
			return err
		}
		if image.TaskID == nil {
			continue
		}
		if *image.TaskID != source {
			return Invalid("image %d does not belong to the source task", id)
		}
		copy, err := attachments.Create(ctx, image.Name, image.MimeType, image.Data)
		if err != nil {
			return err
		}
		ids[i] = copy.ID
	}
	return setOwned(ctx, tx, store.OwnerTask, target, ids)
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
