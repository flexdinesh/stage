// Package transcript reads a T3 session's conversation (user and assistant
// messages, plans) through t3.Client and fans live changes out to streams
// (contract: docs/api.md "Transcript").
package transcript

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/flexdinesh/stage/server/internal/t3"
)

const (
	// PreviewChars caps item text in pages and stream events.
	PreviewChars = 2000
	// DefaultLimit and MaxLimit bound a REST page.
	DefaultLimit = 50
	MaxLimit     = 200
	// MaxFullChars caps the assembled full text of one item.
	MaxFullChars = 1_000_000
	// maxScanPages bounds one forward scan (100 messages per T3 page).
	maxScanPages = 500
)

// Item is a TranscriptItem.
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
func fromT3(it t3.TimelineItem, activeRunID string) (Item, bool) {
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

var activeStatuses = []t3.ThreadStatus{t3.StatusPreparing, t3.StatusQueued, t3.StatusStarting, t3.StatusRunning, t3.StatusWaiting}

// ActiveStatus reports whether a T3 thread status means a run is in flight.
func ActiveStatus(s t3.ThreadStatus) bool { return slices.Contains(activeStatuses, s) }

// active: a run is in flight.
func active(d t3.ThreadDetail) bool { return d.ActiveRunID != "" || ActiveStatus(d.Status) }

// advance moves cursor over ascending items until the first streaming one.
func advance(cursor int, items []Item) int {
	for _, it := range items {
		if it.Position <= cursor {
			continue
		}
		if it.Streaming {
			break
		}
		cursor = it.Position
	}
	return cursor
}

// Reader reads transcripts from T3.
type Reader struct{ T3 t3.Client }

// scan pages forward from after (exclusive) until T3 reports no more, or
// stop returns true for a page's last position. Returns the latest thread
// detail.
func (r Reader) scan(ctx context.Context, threadID string, after int, stop func(last int) bool, each func(Item)) (t3.ThreadDetail, error) {
	var thread t3.ThreadDetail
	for range maxScanPages {
		in := t3.ReadItemsInput{ThreadID: threadID, Limit: t3.MaxItemsPerRead, MaxChars: PreviewChars}
		if after >= 0 {
			pos := after
			in.AfterPosition = &pos
		}
		page, err := r.T3.ReadItems(ctx, in)
		if err != nil {
			return t3.ThreadDetail{}, err
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
			return t3.ThreadDetail{}, fmt.Errorf("t3 t3_thread_read: position did not advance (%d -> %d)", after, *page.NextPosition)
		}
		after = *page.NextPosition
		if stop != nil && stop(after) {
			return thread, nil
		}
	}
	return t3.ThreadDetail{}, fmt.Errorf("t3 t3_thread_read: more than %d pages", maxScanPages)
}

// After reads every item after cursor and the advanced cursor.
func (r Reader) After(ctx context.Context, threadID string, cursor int) ([]Item, int, t3.ThreadDetail, error) {
	var items []Item
	thread, err := r.scan(ctx, threadID, cursor, nil, func(it Item) { items = append(items, it) })
	if err != nil {
		return nil, cursor, t3.ThreadDetail{}, err
	}
	return items, advance(cursor, items), thread, nil
}

// Page is a REST transcript page.
type Page struct {
	Items      []Item          `json:"items"`
	HasEarlier bool            `json:"hasEarlier"`
	Cursor     *int            `json:"cursor"`
	Active     bool            `json:"active"`
	Status     t3.ThreadStatus `json:"status"`
}

// Tail returns the newest limit items (below before when set). T3 pages
// forward only, so it scans from the start.
func (r Reader) Tail(ctx context.Context, threadID string, before *int, limit int) (Page, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	var window []Item
	earlier := false
	var stop func(int) bool
	if before != nil {
		stop = func(last int) bool { return last >= *before }
	}
	all := []Item{}
	thread, err := r.scan(ctx, threadID, -1, stop, func(it Item) {
		if before != nil && it.Position >= *before {
			return
		}
		all = append(all, it)
	})
	if err != nil {
		return Page{}, err
	}
	window = all
	if len(all) > limit {
		window = all[len(all)-limit:]
		earlier = true
	}
	page := Page{Items: slices.Clone(window), HasEarlier: earlier, Active: active(thread), Status: thread.Status}
	if before == nil {
		c := advance(-1, all)
		page.Cursor = &c
	}
	return page, nil
}

// ErrItemNotFound: the item is not in the session's messages view.
var ErrItemNotFound = errors.New("transcript item not found")

// FullText assembles an item's text from T3 text pages (capped at
// MaxFullChars; truncated reports the cap was hit).
func (r Reader) FullText(ctx context.Context, threadID, itemID string) (text string, truncated bool, err error) {
	var buf []byte
	offset, chars := 0, 0
	for chars < MaxFullChars {
		page, err := r.T3.ReadItems(ctx, t3.ReadItemsInput{ThreadID: threadID, ItemID: itemID, TextOffset: offset, MaxChars: t3.MaxItemChars})
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
