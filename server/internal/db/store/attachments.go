package store

import (
	"context"
	"time"
)

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

// AttachmentOwner is what an attachment can belong to. Staged = no owner.
type AttachmentOwner int

const (
	OwnerTask AttachmentOwner = iota
	OwnerSchedule
)

// Attachments is the image repository.
type Attachments interface {
	// Create stores a staged (unowned) image.
	Create(ctx context.Context, name, mimeType string, data []byte) (Attachment, error)
	Get(ctx context.Context, id int64) (AttachmentData, error)
	// TaskState is the state of the task owning image id (nil: staged or a
	// schedule's); ErrNotFound for an unknown image.
	TaskState(ctx context.Context, id int64) (*TaskState, error)
	Delete(ctx context.Context, id int64) error
	// ForTask returns a task's images with bytes, in upload order.
	ForTask(ctx context.Context, taskID int64) ([]AttachmentData, error)
	// Staged returns the staged images among ids (any order).
	Staged(ctx context.Context, ids []int64) ([]AttachmentData, error)
	DeleteStaged(ctx context.Context, ids []int64) error
	// SweepStaged deletes staged images created before t.
	SweepStaged(ctx context.Context, before time.Time) (int64, error)
	// Usable counts the ids that are staged or already the owner's.
	Usable(ctx context.Context, owner AttachmentOwner, ownerID int64, ids []int64) (int, error)
	// SetOwned deletes the owner's images not in ids and attaches the
	// staged ones among ids.
	SetOwned(ctx context.Context, owner AttachmentOwner, ownerID int64, ids []int64) error
	// CopyToTask copies the owner's images onto task dst, in upload order.
	CopyToTask(ctx context.Context, owner AttachmentOwner, ownerID, dst int64) error
}
