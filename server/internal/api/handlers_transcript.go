package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/transcript"
)

// streamPing keeps proxies from closing an idle event stream.
var streamPing = 15 * time.Second

// MirrorActive is the transcript hub's idle check: the mirrored session
// status (refreshed by sync), no T3 call.
func MirrorActive(store *db.Store) transcript.MirrorActive {
	return func(ctx context.Context, threadID string) (bool, error) {
		th, err := store.GetThread(ctx, threadID)
		if err != nil {
			return false, err
		}
		return th.ActiveRunID != nil || transcript.ActiveStatus(t3.ThreadStatus(th.Status)), nil
	}
}

// transcriptReady: the hub is wired, the session is mirrored and T3 is
// connected.
func (s *server) transcriptReady(r *http.Request) (string, error) {
	if s.Transcript == nil {
		return "", errors.New("transcript hub not configured")
	}
	id := r.PathValue("id")
	if _, err := s.Store.GetThread(r.Context(), id); err != nil {
		return "", err
	}
	if !s.T3.Status(r.Context()).Connected {
		return "", t3.ErrNotConnected
	}
	return id, nil
}

// transcriptErr maps T3 read errors onto contract statuses.
func transcriptErr(err error) error {
	switch {
	case errors.Is(err, t3.ErrNotFound), errors.Is(err, transcript.ErrItemNotFound):
		return fmt.Errorf("%w: %v", db.ErrNotFound, err)
	case errors.Is(err, t3.ErrNotConnected):
		return err
	}
	return &db.T3Error{Err: err}
}

func (s *server) getTranscript(w http.ResponseWriter, r *http.Request) error {
	id, err := s.transcriptReady(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	limit, err := queryInt64(q, "limit")
	if err != nil {
		return err
	}
	n := transcript.DefaultLimit
	if limit != nil {
		if *limit < 1 || *limit > transcript.MaxLimit {
			return db.Invalid("limit must be 1..%d", transcript.MaxLimit)
		}
		n = int(*limit)
	}
	var before *int
	if b, err := queryInt64(q, "before"); err != nil {
		return err
	} else if b != nil {
		if *b < 0 {
			return db.Invalid("before must be >= 0")
		}
		v := int(*b)
		before = &v
	}
	page, err := s.Transcript.Reader().Tail(r.Context(), id, before, n)
	if err != nil {
		return transcriptErr(err)
	}
	writeJSON(w, http.StatusOK, page)
	return nil
}

func (s *server) getTranscriptItem(w http.ResponseWriter, r *http.Request) error {
	id, err := s.transcriptReady(r)
	if err != nil {
		return err
	}
	itemID := r.PathValue("itemId")
	text, truncated, err := s.Transcript.Reader().FullText(r.Context(), id, itemID)
	if err != nil {
		return transcriptErr(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"itemId": itemID, "text": text, "truncated": truncated})
	return nil
}

// streamCursor: Last-Event-ID (browser reconnect) wins over ?after.
func streamCursor(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	name := "Last-Event-ID"
	if raw == "" {
		raw, name = strings.TrimSpace(r.URL.Query().Get("after")), "after"
	}
	if raw == "" {
		return -1, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < -1 {
		return 0, db.Invalid("invalid %s %q", name, raw)
	}
	return n, nil
}

func (s *server) streamTranscript(w http.ResponseWriter, r *http.Request) error {
	id, err := s.transcriptReady(r)
	if err != nil {
		return err
	}
	after, err := streamCursor(r)
	if err != nil {
		return err
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
	if !write("retry: 3000\n\n") {
		return nil
	}
	sub := s.Transcript.Subscribe(id, after)
	defer sub.Close()
	ping := time.NewTicker(streamPing)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-ping.C:
			if !write(": ping\n\n") {
				return nil
			}
		case ev, ok := <-sub.Events:
			if !ok {
				if sub.Reset() {
					write("event: reset\ndata: {}\n\n")
				}
				return nil
			}
			name, data := "item", any(ev.Item)
			if ev.State != nil {
				name, data = "state", ev.State
			}
			b, err := json.Marshal(data)
			if err != nil {
				return nil
			}
			if !write("event: %s\nid: %d\ndata: %s\n\n", name, ev.Cursor, b) {
				return nil
			}
		}
	}
}
