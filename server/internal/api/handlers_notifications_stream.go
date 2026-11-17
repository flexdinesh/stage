package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/notify"
)

// Shared by the retained notification stream.
var streamPing = 15 * time.Second

func lastEventID(r *http.Request) (string, string) {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		return v, "Last-Event-ID"
	}
	return r.URL.Query().Get("after"), "after"
}

// notificationReplayMax caps the rows a (re)connecting stream replays.
const notificationReplayMax = 100

// streamNotification is one inbox row on GET /api/notifications/stream.
type streamNotification struct {
	ID        int64        `json:"id"`
	Event     db.TaskState `json:"event"`
	CreatedAt time.Time    `json:"createdAt"`
	Read      bool         `json:"read"`
	Tag       string       `json:"tag"`
	Title     string       `json:"title"`
	Body      string       `json:"body"`
	URL       string       `json:"url"`
}

type streamHello struct {
	LatestID int64 `json:"latestId"`
	Reset    bool  `json:"reset"`
}

// streamUnread: push tags of groups with unread entries and the badge count.
type streamUnread struct {
	Tags  []string `json:"tags"`
	Badge int      `json:"badge"`
}

// notificationStreamCursor: the lastEventID notification id (>= 0), ok false
// when none.
func notificationStreamCursor(r *http.Request) (cursor int64, ok bool, err error) {
	raw, name := lastEventID(r)
	if raw == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, false, db.Invalid("invalid %s %q", name, raw)
	}
	return n, true, nil
}

// streamNotifications is GET /api/notifications/stream (docs/api.md): the
// inbox rows after a cursor, then live rows and unread snapshots, re-read
// from the database on every Inbox wake.
func (s *server) streamNotifications(w http.ResponseWriter, r *http.Request) error {
	cursor, hasCursor, err := notificationStreamCursor(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	// Before any read, so a change racing the replay still wakes the loop.
	wake, unsubscribe := s.Inbox.Subscribe()
	defer unsubscribe()
	latest, _, err := s.Store.NotificationReadState(ctx)
	if err != nil {
		return err
	}
	reset := false
	switch {
	case !hasCursor:
		cursor = latest
	case cursor > latest:
		cursor, reset = latest, true
	}

	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	write := func(format string, args ...any) bool {
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	event := func(name string, id int64, v any) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return false
		}
		return write("event: %s\nid: %d\ndata: %s\n\n", name, id, b)
	}
	if !write("retry: 3000\n\n") || !event("hello", cursor, streamHello{LatestID: latest, Reset: reset}) {
		return nil
	}

	lastSent := cursor
	send := func(rows []db.Notification) bool {
		for _, n := range rows {
			p := notify.PayloadFor(n, 0)
			if !event("notification", n.ID, streamNotification{
				ID: n.ID, Event: n.Event, CreatedAt: n.CreatedAt, Read: n.ReadAt != nil,
				Tag: p.Tag, Title: p.Title, Body: p.Body, URL: p.URL,
			}) {
				return false
			}
			lastSent = n.ID
		}
		return true
	}
	// drain sends every row after lastSent, oldest first.
	drain := func() bool {
		for {
			rows, err := s.Store.NotificationsAfter(ctx, lastSent, notificationReplayMax, false)
			if err != nil || !send(rows) {
				return false
			}
			if len(rows) < notificationReplayMax {
				return true
			}
		}
	}
	lastSnapshot := ""
	// snapshot sends `unread` when it differs from the last one sent.
	snapshot := func() bool {
		tags, err := s.Store.UnreadNotificationTags(ctx)
		if err != nil {
			return false
		}
		badge, err := s.Store.NeedsInputCount(ctx)
		if err != nil {
			return false
		}
		b, err := json.Marshal(streamUnread{Tags: tags, Badge: badge})
		if err != nil {
			return false
		}
		if string(b) == lastSnapshot {
			return true
		}
		lastSnapshot = string(b)
		return write("event: unread\ndata: %s\n\n", b)
	}

	// Replay: the newest notificationReplayMax rows after the cursor.
	rows, err := s.Store.NotificationsAfter(ctx, cursor, notificationReplayMax+1, true)
	if err != nil {
		return nil
	}
	if len(rows) > notificationReplayMax {
		if !write("event: gap\ndata: {}\n\n") {
			return nil
		}
		rows = rows[1:]
	}
	if !send(rows) || !drain() || !snapshot() {
		return nil
	}

	ping := time.NewTicker(streamPing)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ping.C:
			if !write(": ping\n\n") || !snapshot() {
				return nil
			}
		case _, ok := <-wake:
			if !ok || !drain() || !snapshot() {
				return nil
			}
		}
	}
}
