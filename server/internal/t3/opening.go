// Package opening reads only the first own prompt of an adopted task.
package t3

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// PreviewChars caps the opening prompt preview.
	PreviewChars = 2000
	// MaxFullChars caps the assembled full text of one item.
	MaxFullChars = 1_000_000
	// maxScanPages bounds one forward scan (100 messages per T3 page).
	maxScanPages = 500
)

// Item describes the first own user prompt returned by T3.
type Item struct {
	Position       int       `json:"position"`
	ItemID         string    `json:"itemId"`
	SourceThreadID string    `json:"sourceThreadId"`
	Inherited      bool      `json:"inherited"`
	Kind           string    `json:"kind"` // user|assistant|plan
	Status         string    `json:"status"`
	Streaming      bool      `json:"streaming"`
	CreatedBy      *string   `json:"createdBy"`
	RunID          *string   `json:"runId"`
	Text           string    `json:"text"`
	Truncated      bool      `json:"truncated"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

var kinds = map[string]string{"user_message": "user", "assistant_message": "assistant", "proposed_plan": "plan"}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// fromT3 maps a timeline item; false for types outside the messages view.
// Only items of the active run can still stream: a stale non-final status
// on an older run must not pin the cursor.
func fromT3(it TimelineItem, activeRunID string) (Item, bool) {
	kind, ok := kinds[it.Type]
	if !ok {
		return Item{}, false
	}
	return Item{
		Position:       it.Position,
		ItemID:         it.ItemID,
		SourceThreadID: it.SourceThreadID,
		Inherited:      it.Visibility == "inherited",
		Kind:           kind,
		Status:         it.Status,
		Streaming:      !it.Settled() && activeRunID != "" && it.RunID == activeRunID,
		CreatedBy:      optional(it.CreatedBy),
		RunID:          optional(it.RunID),
		Text:           it.Text,
		Truncated:      it.TextTruncated,
		UpdatedAt:      it.UpdatedAt,
	}, true
}

// OpeningReader locates and expands an adopted task's opening prompt.
type OpeningReader struct{ T3 Client }

// scan pages forward from after (exclusive) until T3 reports no more, or
// stop returns true for a page's last position. Returns the latest thread
// detail.
func (r OpeningReader) scan(ctx context.Context, threadID string, after int, stop func(last int) bool, each func(Item)) (ThreadDetail, error) {
	var thread ThreadDetail
	for range maxScanPages {
		in := ReadItemsInput{ThreadID: threadID, Limit: MaxItemsPerRead, MaxChars: PreviewChars}
		if after >= 0 {
			pos := after
			in.AfterPosition = &pos
		}
		page, err := r.T3.ReadItems(ctx, in)
		if err != nil {
			return ThreadDetail{}, err
		}
		thread = page.Thread
		for _, it := range page.Items {
			if item, ok := fromT3(it, thread.ActiveRunID); ok {
				each(item)
			}
		}
		if !page.HasMore || page.NextPosition == nil {
			return thread, nil
		}
		if *page.NextPosition <= after {
			return ThreadDetail{}, fmt.Errorf("t3 t3_thread_read: position did not advance (%d -> %d)", after, *page.NextPosition)
		}
		after = *page.NextPosition
		if stop != nil && stop(after) {
			return thread, nil
		}
	}
	return ThreadDetail{}, fmt.Errorf("t3 t3_thread_read: more than %d pages", maxScanPages)
}

// Opening returns the session's first own user message (inherited fork
// history skipped): the prompt that started it. Nil when there is none yet.
func (r OpeningReader) Opening(ctx context.Context, threadID string) (*Item, error) {
	var found *Item
	_, err := r.scan(ctx, threadID, -1, func(int) bool { return found != nil }, func(it Item) {
		if found == nil && it.Kind == "user" && !it.Inherited {
			found = &it
		}
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// ErrItemNotFound: the item is not in the session's messages view.
var ErrItemNotFound = errors.New("opening prompt item not found")

// FullText assembles an item's text from T3 text pages (capped at
// MaxFullChars; truncated reports the cap was hit).
func (r OpeningReader) FullText(ctx context.Context, threadID, itemID string) (text string, truncated bool, err error) {
	var buf []byte
	offset, chars := 0, 0
	for chars < MaxFullChars {
		page, err := r.T3.ReadItems(ctx, ReadItemsInput{ThreadID: threadID, ItemID: itemID, TextOffset: offset, MaxChars: MaxItemChars})
		if err != nil {
			return "", false, err
		}
		if len(page.Items) == 0 {
			return "", false, ErrItemNotFound
		}
		it := page.Items[0]
		if _, ok := kinds[it.Type]; !ok {
			return "", false, ErrItemNotFound
		}
		buf = append(buf, it.Text...)
		chars += len([]rune(it.Text))
		if !it.TextTruncated || it.NextTextOffset == nil {
			return string(buf), false, nil
		}
		if *it.NextTextOffset <= offset {
			return "", false, fmt.Errorf("t3 t3_thread_read: text offset did not advance (%d -> %d)", offset, *it.NextTextOffset)
		}
		offset = *it.NextTextOffset
	}
	return string(buf), true, nil
}
