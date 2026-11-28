package db

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type (
	Notification       = store.Notification
	NewNotification    = store.NewNotification
	NotificationGroup  = store.NotificationGroup
	NotificationCursor = store.NotificationCursor
	WaitingSession     = store.WaitingSession
	NotificationEvent  = store.NotificationEvent
)

const (
	EventNeedsInput      = store.EventNeedsInput
	EventFailed          = store.EventFailed
	EventReady           = store.EventReady
	EventCompleted       = store.EventCompleted
	EventTreeCompleted   = store.EventTreeCompleted
	EventScheduleStarted = store.EventScheduleStarted
	EventScheduleSkipped = store.EventScheduleSkipped
	EventDone            = store.EventDone
)

// NotificationEvents: the events that can notify, in display order.
var NotificationEvents = store.NotificationEvents

// DefaultNotifyEvents: the filter of an account that never saved one.
var DefaultNotifyEvents = store.DefaultNotifyEvents

// NotificationKey is a loaded row's group key (the adapter computes the
// same key in SQL). n.ExternalSessionID is the task's current session only when
// TaskID is set, where the task key wins.
func NotificationKey(n Notification) string {
	switch {
	case n.TaskID != nil:
		return "task:" + strconv.FormatInt(*n.TaskID, 10)
	case n.TreeID != nil:
		return "tree:" + strconv.FormatInt(*n.TreeID, 10)
	case n.ScheduleID != nil:
		return "schedule:" + strconv.FormatInt(*n.ScheduleID, 10)
	}
	for _, kind := range []string{"task", "tree", "schedule"} {
		if id, ok := subjectURL(n.URL, kind); ok {
			return kind + ":" + id
		}
	}
	if n.ExternalSessionID != nil {
		return "session:" + strconv.FormatInt(n.ConnectionID, 10) + ":" + *n.ExternalSessionID
	}
	return "notification:" + strconv.FormatInt(n.ID, 10)
}

// subjectURL reads the numeric identity from a stored subject destination.
func subjectURL(u, kind string) (string, bool) {
	id, ok := strings.CutPrefix(u, "/"+kind+"s/")
	if kind == "task" {
		id = strings.TrimSuffix(id, "/session")
	}
	if !ok || id == "" {
		return "", false
	}
	for _, c := range []byte(id) {
		if c < '0' || c > '9' {
			return "", false
		}
	}
	return id, true
}

// TagForKey is a group key's push tag (`task:<id>` → `task-<id>`, likewise
// tree, schedule and session); identity-less keys have none ("").
func TagForKey(key string) string {
	if kind, id, ok := strings.Cut(key, ":"); ok && (kind == "task" || kind == "tree" || kind == "schedule" || kind == "session") {
		return kind + "-" + id
	}
	return ""
}

// InsertNotification stores n and its unread receipts, and returns the event ID.
func (s *Store) InsertNotification(ctx context.Context, n NewNotification) (int64, error) {
	return write(ctx, s, func(tx store.Tx) (int64, error) {
		var sid *int64
		if ref := n.Session; ref != nil {
			value, err := ensureSession(ctx, tx, *ref)
			if err != nil {
				return 0, err
			}
			sid = &value
		}
		notifications := tx.Notifications()
		id, err := notifications.Insert(ctx, n, sid)
		if err != nil {
			return 0, err
		}
		if err := notifications.InsertRecipients(ctx, id, DefaultUser); err != nil {
			return 0, err
		}
		return id, nil
	})
}

// NotificationForDelivery returns the acting user's unread event, or ErrNotFound.
func (s *Store) NotificationForDelivery(ctx context.Context, id int64) (Notification, error) {
	return read(ctx, s, func(tx store.Tx) (Notification, error) {
		return tx.Notifications().ForDelivery(ctx, ActingUser(ctx), id)
	})
}

// ListNotifications returns up to limit entries, newest first, with id <
// beforeID when beforeID is set.
func (s *Store) ListNotifications(ctx context.Context, limit int, beforeID *int64) ([]Notification, error) {
	return read(ctx, s, func(tx store.Tx) ([]Notification, error) {
		return tx.Notifications().List(ctx, ActingUser(ctx), limit, beforeID, nil)
	})
}

// ListNotificationHistory pages only events belonging to key.
func (s *Store) ListNotificationHistory(ctx context.Context, key string, limit int, beforeID *int64) ([]Notification, error) {
	return read(ctx, s, func(tx store.Tx) ([]Notification, error) {
		return tx.Notifications().List(ctx, ActingUser(ctx), limit, beforeID, &key)
	})
}

// NotificationsAfter returns up to limit entries with id > after in ascending
// order: the oldest ones, or with newest the newest ones.
func (s *Store) NotificationsAfter(ctx context.Context, after int64, limit int, newest bool) ([]Notification, error) {
	return read(ctx, s, func(tx store.Tx) ([]Notification, error) {
		return tx.Notifications().After(ctx, ActingUser(ctx), after, limit, newest)
	})
}

// NotificationReadState captures the read boundary before listing groups.
func (s *Store) NotificationReadState(ctx context.Context) (latestID int64, unread int, err error) {
	err = s.e.Read(ctx, func(tx store.Tx) error {
		var err error
		latestID, unread, err = tx.Notifications().ReadState(ctx, ActingUser(ctx))
		return err
	})
	return
}

// ListNotificationGroups aggregates before applying the cursor/limit. upToID
// prevents a racing insert from being included in the initial read boundary.
func (s *Store) ListNotificationGroups(ctx context.Context, limit int, cursor *NotificationCursor, upToID int64) ([]NotificationGroup, error) {
	return read(ctx, s, func(tx store.Tx) ([]NotificationGroup, error) {
		return tx.Notifications().Groups(ctx, ActingUser(ctx), limit, cursor, upToID)
	})
}

// UnreadNotificationCount counts entries never read.
func (s *Store) UnreadNotificationCount(ctx context.Context) (int, error) {
	return read(ctx, s, func(tx store.Tx) (int, error) { return tx.Notifications().UnreadCount(ctx, ActingUser(ctx)) })
}

// MarkNotificationsRead marks every unread entry with id <= upTo as read.
func (s *Store) MarkNotificationsRead(ctx context.Context, upTo int64) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Notifications().MarkRead(ctx, ActingUser(ctx), upTo) })
}

// MarkNotificationGroupsRead marks every unread entry in the given groups read.
func (s *Store) MarkNotificationGroupsRead(ctx context.Context, groupKeys []string) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Notifications().MarkGroupsRead(ctx, ActingUser(ctx), groupKeys) })
}

// UnreadNotificationTags returns the push tags (`task-<id>`, `session-<id>`) of
// groups with unread entries. Identity-less groups have no tag.
func (s *Store) UnreadNotificationTags(ctx context.Context) ([]string, error) {
	keys, err := read(ctx, s, func(tx store.Tx) ([]string, error) {
		return tx.Notifications().UnreadKeys(ctx, ActingUser(ctx))
	})
	if err != nil {
		return nil, err
	}
	tags := []string{}
	for _, key := range keys {
		if tag := TagForKey(key); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags, nil
}

// PruneNotifications deletes entries older than age.
func (s *Store) PruneNotifications(ctx context.Context, age time.Duration) (int64, error) {
	return write(ctx, s, func(tx store.Tx) (int64, error) { return tx.Notifications().Prune(ctx, time.Now().Add(-age)) })
}

func (s *Store) WaitingWithoutNotifications(ctx context.Context, upToID int64) ([]WaitingSession, error) {
	return read(ctx, s, func(tx store.Tx) ([]WaitingSession, error) { return tx.Notifications().Waiting(ctx, upToID) })
}
