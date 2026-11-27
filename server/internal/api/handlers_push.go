package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/notify"
)

// ---- notifications (Web Push) ----

func (s *server) pushConfig(w http.ResponseWriter, r *http.Request) error {
	if _, ok := s.pushSession(w, r); !ok {
		return nil
	}
	key, err := s.Push.PublicKey(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": key})
	return nil
}

func (s *server) pushSession(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if s.Access == nil {
		return 0, true
	}
	c, ok := credentialFrom(r.Context())
	if !ok || c.identity.Session.Audience != store.SessionBrowser {
		accessError(w, http.StatusForbidden, "principal_forbidden", "Use the Stage browser to manage web push.")
		return 0, false
	}
	return c.identity.Session.ID, true
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
	session, ok := s.pushSession(w, r)
	if !ok {
		return nil
	}
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
	if err := s.Store.UpsertPushSubscription(r.Context(), session, endpoint, p256dh, auth, r.UserAgent()); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func pushEndpoint(r *http.Request) (string, error) {
	var b struct {
		Endpoint string `json:"endpoint"`
	}
	if err := decode(r, &b); err != nil {
		return "", err
	}
	endpoint := strings.TrimSpace(b.Endpoint)
	if endpoint == "" {
		return "", db.Invalid("endpoint is required")
	}
	return endpoint, nil
}

func (s *server) unsubscribePush(w http.ResponseWriter, r *http.Request) error {
	session, ok := s.pushSession(w, r)
	if !ok {
		return nil
	}
	endpoint, err := pushEndpoint(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeletePushSubscription(r.Context(), session, endpoint); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) testPush(w http.ResponseWriter, r *http.Request) error {
	session, ok := s.pushSession(w, r)
	if !ok {
		return nil
	}
	endpoint, err := pushEndpoint(r)
	if err != nil {
		return err
	}
	if err := s.Push.SendTest(r.Context(), session, endpoint); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *server) checkPush(w http.ResponseWriter, r *http.Request) error {
	session, ok := s.pushSession(w, r)
	if !ok {
		return nil
	}
	endpoint, err := pushEndpoint(r)
	if err != nil {
		return err
	}
	_, err = s.Store.GetPushSubscription(r.Context(), session, endpoint)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]bool{"bound": err == nil})
	return nil
}

func (s *server) pushDelivery(w http.ResponseWriter, r *http.Request) error {
	session, ok := s.pushSession(w, r)
	if !ok {
		return nil
	}
	if session == 0 {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	var body struct {
		Binding string `json:"binding"`
		EventID *int64 `json:"eventId"`
	}
	if err := decodeStrict(r, &body); err != nil {
		return err
	}
	if body.Binding == "" || body.EventID == nil || *body.EventID < 0 {
		return db.Invalid("binding and non-negative eventId are required")
	}
	ref := notify.PushReference{Binding: body.Binding, EventID: *body.EventID}
	payload, err := s.Push.ResolvePush(r.Context(), session, ref)
	if errors.Is(err, db.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, payload)
	return nil
}

func (s *server) badge(w http.ResponseWriter, r *http.Request) error {
	n, err := s.Store.NeedsInputCount(r.Context())
	if err != nil {
		return err
	}
	counts := map[string]int{"needsInput": n}
	if s.Access != nil {
		unread, err := s.Store.UnreadNotificationCount(r.Context())
		if err != nil {
			return err
		}
		counts["unread"] = unread
	}
	writeJSON(w, http.StatusOK, counts)
	return nil
}

// ---- notifications inbox ----

type notificationJSON struct {
	Links execution.Links `json:"links"`
	db.Notification
}

func (s *server) listNotifications(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	limit, err := notificationLimit(r)
	if err != nil {
		return err
	}
	before, err := queryInt64(q, "before")
	if err != nil {
		return err
	}
	// One extra row tells whether an older page exists.
	var rows []db.Notification
	if key := q.Get("group"); key != "" {
		if err := validateNotificationGroupKey(key); err != nil {
			return err
		}
		rows, err = s.Store.ListNotificationHistory(r.Context(), key, limit+1, before)
	} else {
		rows, err = s.Store.ListNotifications(r.Context(), limit+1, before)
	}
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
	out := make([]notificationJSON, len(rows))
	for i, n := range rows {
		out[i] = notificationJSON{Notification: n, Links: s.sessionLinks(r.Context(), n.ConnectionID, n.ExternalSessionID)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out, "unread": unread, "nextBefore": nextBefore})
	return nil
}

func notificationLimit(r *http.Request) (int, error) {
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return 0, db.Invalid("invalid limit %q", v)
		}
		return min(n, 200), nil
	}
	return 50, nil
}

type notificationGroupJSON struct {
	Links execution.Links `json:"links"`
	db.NotificationGroup
}

func notificationGroupCursor(g db.NotificationGroup) string {
	priority := "recent"
	if g.CurrentState != nil && *g.CurrentState == db.StateNeedsInput {
		priority = "waiting"
	}
	return priority + ":" + strconv.FormatInt(g.ID, 10)
}

type waitingSessionJSON struct {
	Links execution.Links `json:"links"`
	db.WaitingSession
}

func (s *server) listNotificationGroups(w http.ResponseWriter, r *http.Request) error {
	limit, err := notificationLimit(r)
	if err != nil {
		return err
	}
	var cursor *db.NotificationCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		priority, rawID, _ := strings.Cut(raw, ":")
		id, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || id < 1 || (priority != "waiting" && priority != "recent") {
			return db.Invalid("invalid cursor %q", raw)
		}
		cursor = &db.NotificationCursor{Waiting: priority == "waiting", ID: id}
	}
	// Capture this first: priority order does not imply the newest event ID.
	latestID, unread, err := s.Store.NotificationReadState(r.Context())
	if err != nil {
		return err
	}
	rows, err := s.Store.ListNotificationGroups(r.Context(), limit+1, cursor, latestID)
	if err != nil {
		return err
	}
	var nextCursor *string
	if len(rows) > limit {
		rows = rows[:limit]
		value := notificationGroupCursor(rows[limit-1])
		nextCursor = &value
	}
	out := make([]notificationGroupJSON, len(rows))
	for i, g := range rows {
		out[i] = notificationGroupJSON{NotificationGroup: g, Links: s.sessionLinks(r.Context(), g.ConnectionID, g.ExternalSessionID)}
	}
	waiting, err := s.Store.WaitingWithoutNotifications(r.Context(), latestID)
	if err != nil {
		return err
	}
	silent := make([]waitingSessionJSON, len(waiting))
	for i, row := range waiting {
		silent[i] = waitingSessionJSON{WaitingSession: row, Links: s.sessionLinks(r.Context(), row.ConnectionID, &row.ExternalSessionID)}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"waitingSessions": silent,
		"groups":          out, "unread": unread, "latestId": latestID, "nextCursor": nextCursor,
	})
	return nil
}

func (s *server) readNotifications(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		UpTo   *int64   `json:"upTo"`
		Groups []string `json:"groups"`
	}
	if err := decode(r, &b); err != nil {
		return err
	}
	switch {
	case b.UpTo != nil && b.Groups != nil:
		return db.Invalid("send either upTo or groups, not both")
	case b.Groups != nil:
		if len(b.Groups) < 1 || len(b.Groups) > maxReadGroups {
			return db.Invalid("groups must have 1 to %d keys", maxReadGroups)
		}
		for _, g := range b.Groups {
			if strings.TrimSpace(g) == "" {
				return db.Invalid("groups must not contain empty keys")
			}
			if err := validateNotificationGroupKey(g); err != nil {
				return err
			}
		}
		if err := s.Store.MarkNotificationGroupsRead(r.Context(), b.Groups); err != nil {
			return err
		}
	case b.UpTo != nil && *b.UpTo >= 1:
		if err := s.Store.MarkNotificationsRead(r.Context(), *b.UpTo); err != nil {
			return err
		}
	default:
		return db.Invalid("upTo or groups is required")
	}
	s.Inbox.Signal()
	w.WriteHeader(http.StatusNoContent)
	return nil
}

const maxReadGroups = 20

func validateNotificationGroupKey(key string) error {
	if strings.HasPrefix(key, "thread:") {
		return db.Invalid("group key thread:... was renamed in API v9; use session:<connectionId>:<externalSessionId>")
	}
	return nil
}

func (s *server) unreadNotificationTags(w http.ResponseWriter, r *http.Request) error {
	tags, err := s.Store.UnreadNotificationTags(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string][]string{"tags": tags})
	return nil
}
