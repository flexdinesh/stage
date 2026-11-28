package execution

import (
	"context"
	"slices"
	"time"
)

// QueuedMessage is a preview of a pending message in the harness's queue.
type QueuedMessage struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Delivery  Delivery  `json:"delivery"` // queue or steer
	CreatedAt time.Time `json:"createdAt"`
	Source    string    `json:"source"` // stage, harness, or empty when unknown
}

// QueueManager reads and changes a session's native queue (OpQueueManage).
// Reads return native queue order. Writes return ErrNotFound if the message
// was consumed or removed before the action could take effect.
type QueueManager interface {
	Queued(context.Context, SessionRef) ([]QueuedMessage, error)
	CancelQueued(context.Context, SessionRef, string) error
	PromoteQueued(context.Context, SessionRef, string) error
}

// BoundQueuedMessages caps previews before returning them to clients or
// sending them across the worker channel, without modifying adapter data.
func BoundQueuedMessages(messages []QueuedMessage) []QueuedMessage {
	if messages == nil {
		return []QueuedMessage{}
	}
	out := slices.Clone(messages)
	for i := range out {
		out[i].Text = runePrefix(out[i].Text, 2000)
	}
	return out
}
