package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/flexdinesh/t3-stage/server/internal/db"
)

// ---- notifications (Web Push) ----

func (s *server) pushConfig(w http.ResponseWriter, r *http.Request) error {
	key, err := s.Push.PublicKey(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": key})
	return nil
}

type pushSubscriptionBody struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// endpointArg validates a subscription endpoint (https URL).
func endpointArg(raw string) (string, error) {
	e := strings.TrimSpace(raw)
	if e == "" {
		return "", db.Invalid("endpoint is required")
	}
	if u, err := url.Parse(e); err != nil || u.Scheme != "https" || u.Host == "" {
		return "", db.Invalid("endpoint must be an https URL")
	}
	return e, nil
}

func (s *server) subscribePush(w http.ResponseWriter, r *http.Request) error {
	var b pushSubscriptionBody
	if err := decode(r, &b); err != nil {
		return err
	}
	endpoint, err := endpointArg(b.Endpoint)
	if err != nil {
		return err
	}
	p256dh, auth := strings.TrimSpace(b.Keys.P256dh), strings.TrimSpace(b.Keys.Auth)
	if p256dh == "" || auth == "" {
		return db.Invalid("keys.p256dh and keys.auth are required")
	}
	if err := s.Store.UpsertPushSubscription(r.Context(), endpoint, p256dh, auth, r.UserAgent()); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) unsubscribePush(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Endpoint string `json:"endpoint"`
	}
	if err := decode(r, &b); err != nil {
		return err
	}
	endpoint := strings.TrimSpace(b.Endpoint)
	if endpoint == "" {
		return db.Invalid("endpoint is required")
	}
	if err := s.Store.DeletePushSubscription(r.Context(), endpoint); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) testPush(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Endpoint string `json:"endpoint"`
	}
	if err := decode(r, &b); err != nil {
		return err
	}
	endpoint := strings.TrimSpace(b.Endpoint)
	if endpoint == "" {
		return db.Invalid("endpoint is required")
	}
	if err := s.Push.SendTest(r.Context(), endpoint); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) badge(w http.ResponseWriter, r *http.Request) error {
	n, err := s.Store.NeedsInputCount(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]int{"needsInput": n})
	return nil
}

// ---- notifications inbox ----

type notificationJSON struct {
	db.Notification
	// T3DeepLink opens the thread in the T3 desktop/mobile app.
	T3DeepLink *string `json:"t3DeepLink"`
}

func (s *server) listNotifications(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	limit := 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return db.Invalid("invalid limit %q", v)
		}
		limit = min(n, 200)
	}
	before, err := queryInt64(q, "before")
	if err != nil {
		return err
	}
	// One extra row tells whether an older page exists.
	rows, err := s.Store.ListNotifications(r.Context(), limit+1, before)
	if err != nil {
		return err
	}
	var nextBefore *int64
	if len(rows) > limit {
		rows = rows[:limit]
		id := rows[limit-1].ID
		nextBefore = &id
	}
	unread, err := s.Store.UnreadNotificationCount(r.Context())
	if err != nil {
		return err
	}
	env := s.envID(r.Context())
	out := make([]notificationJSON, len(rows))
	for i, n := range rows {
		out[i] = notificationJSON{Notification: n, T3DeepLink: deepLink(env, n.T3ThreadID)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out, "unread": unread, "nextBefore": nextBefore})
	return nil
}

func (s *server) readNotifications(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		UpTo *int64 `json:"upTo"`
	}
	if err := decode(r, &b); err != nil {
		return err
	}
	if b.UpTo == nil || *b.UpTo < 1 {
		return db.Invalid("upTo is required")
	}
	if err := s.Store.MarkNotificationsRead(r.Context(), *b.UpTo); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
