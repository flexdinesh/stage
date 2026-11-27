package observe

import (
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

const (
	KindWatch = "observe.watch"
	KindBatch = "observe.batch"
)

// Watch replaces the complete observation set for one machine.
type Watch struct {
	Epoch       string            `json:"epoch"`
	Generation  int64             `json:"generation"`
	Connections []WatchConnection `json:"connections"`
	Resample    bool              `json:"resample,omitempty"`
}

type WatchConnection struct {
	ID        int64          `json:"id"`
	Attention bool           `json:"attention"`
	Sessions  []WatchSession `json:"sessions"`
}

type WatchSession struct {
	ID        string `json:"id"`
	Stale     bool   `json:"stale,omitempty"`
	Attention bool   `json:"attention,omitempty"`
}

type WatchAck struct {
	Producer       string `json:"producer"`
	ReleasedCursor int64  `json:"releasedCursor,omitempty"`
}

// Record is one adapter sample, read/stream status, or report of lost samples.
type Record struct {
	Cursor            int64                  `json:"cursor,omitempty"`
	ConnectionID      int64                  `json:"connectionId,omitempty"`
	ExternalSessionID string                 `json:"externalSessionId,omitempty"`
	ObservedAt        time.Time              `json:"observedAt"`
	Observation       *execution.Observation `json:"observation,omitempty"`
	Gap               *Gap                   `json:"gap,omitempty"`
	Attention         bool                   `json:"attention,omitempty"`
	Quiet             bool                   `json:"quiet,omitempty"`
	Unavailable       bool                   `json:"unavailable,omitempty"`
	StreamError       *string                `json:"streamError,omitempty"`

	// A bridge preserves its harness event time in ObservedAt. Stage knows
	// when it received the push and uses that time for ordering snapshots.
	capturedAt time.Time
}

func (r Record) captureTime() time.Time {
	if r.capturedAt.IsZero() {
		return r.ObservedAt
	}
	return r.capturedAt
}

type Gap struct {
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	Count uint64    `json:"count"`
}

type Batch struct {
	Producer string   `json:"producer"`
	Cursor   int64    `json:"cursor"`
	Records  []Record `json:"records"`
}

type BatchAck struct {
	Cursor   int64 `json:"cursor"`
	Resample bool  `json:"resample,omitempty"`
}
