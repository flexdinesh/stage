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
)

// NotificationKey is a loaded row's group key (the adapter computes the
// same key in SQL). n.ExternalSessionID is the task's current session only when
// TaskID is set, where the task key wins.
func NotificationKey(n Notification) string {
	switch {
	case n.TaskID != nil:
		return "task:" + strconv.FormatInt(*n.TaskID, 10)
	case isTaskURL(n.URL):
		return "task:" + strings.TrimPrefix(n.URL, "/tasks/")
	case n.ExternalSessionID != nil:
		return "session:" + strconv.FormatInt(n.ConnectionID, 10) + ":" + *n.ExternalSessionID
	}
	return "notification:" + strconv.FormatInt(n.ID, 10)
}

// isTaskURL: `/tasks/` followed by one or more ASCII digits only.
func isTaskURL(u string) bool {
	id, ok := strings.CutPrefix(u, "/tasks/")
	if !ok || id == "" {
		return false
	}
	for _, c := range []byte(id) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// TagForKey is a group key's push tag (`task:<id>` → `task-<id>`,
// `session:<id>` → `session-<id>`); identity-less keys have none ("").
func TagForKey(key string) string {
	if kind, id, ok := strings.Cut(key, ":"); ok && (kind == "task" || kind == "session") {
		return kind + "-" + id
	}
	return ""
}

// InsertNotification stores n and returns its id.
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
		return tx.Notifications().Insert(ctx, n, sid)
	})
}

// ListNotifications returns up to limit entries, newest first, with id <
// beforeID when beforeID is set.
func (s *Store) ListNotifications(ctx context.Context, limit int, beforeID *int64) ([]Notification, error) {
	return read(ctx, s, func(tx store.Tx) ([]Notification, error) { return tx.Notifications().List(ctx, limit, beforeID, nil) })
}

// ListNotificationHistory pages only events belonging to key.
func (s *Store) ListNotificationHistory(ctx context.Context, key string, limit int, beforeID *int64) ([]Notification, error) {
	return read(ctx, s, func(tx store.Tx) ([]Notification, error) { return tx.Notifications().List(ctx, limit, beforeID, &key) })
}

// NotificationsAfter returns up to limit entries with id > after in ascending
// order: the oldest ones, or with newest the newest ones.
func (s *Store) NotificationsAfter(ctx context.Context, after int64, limit int, newest bool) ([]Notification, error) {
	return read(ctx, s, func(tx store.Tx) ([]Notification, error) { return tx.Notifications().After(ctx, after, limit, newest) })
}

// NotificationReadState captures the read boundary before listing groups.
func (s *Store) NotificationReadState(ctx context.Context) (latestID int64, unread int, err error) {
	err = s.e.Read(ctx, func(tx store.Tx) error {
		var err error
		latestID, unread, err = tx.Notifications().ReadState(ctx)
		return err
	})
	return
}

// ListNotificationGroups aggregates before applying the cursor/limit. upToID
// prevents a racing insert from being included in the initial read boundary.
func (s *Store) ListNotificationGroups(ctx context.Context, limit int, cursor *NotificationCursor, upToID int64) ([]NotificationGroup, error) {
	return read(ctx, s, func(tx store.Tx) ([]NotificationGroup, error) {
		return tx.Notifications().Groups(ctx, limit, cursor, upToID)
	})
}

// UnreadNotificationCount counts entries never read.
func (s *Store) UnreadNotificationCount(ctx context.Context) (int, error) {
	return read(ctx, s, func(tx store.Tx) (int, error) { return tx.Notifications().UnreadCount(ctx) })
}

// MarkNotificationsRead marks every unread entry with id <= upTo as read.
func (s *Store) MarkNotificationsRead(ctx context.Context, upTo int64) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Notifications().MarkRead(ctx, upTo) })
}

// MarkNotificationGroupsRead marks every unread entry in the given groups read.
func (s *Store) MarkNotificationGroupsRead(ctx context.Context, groupKeys []string) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Notifications().MarkGroupsRead(ctx, groupKeys) })
}

// UnreadNotificationTags returns the push tags (`task-<id>`, `session-<id>`) of
// groups with unread entries. Identity-less groups have no tag.
func (s *Store) UnreadNotificationTags(ctx context.Context) ([]string, error) {
	keys, err := read(ctx, s, func(tx store.Tx) ([]string, error) { return tx.Notifications().UnreadKeys(ctx) })
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
