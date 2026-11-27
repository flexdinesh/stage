package storetest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

func init() {
	register("TagForKey", caseTagForKey)
	register("NotificationRecipients", caseNotificationRecipients)
}

func caseTagForKey(t *testing.T, open Factory) {
	for key, want := range map[string]string{
		"task:7":          "task-7",
		"session:1:ext-1": "session-1:ext-1",
		"session:1:a:b":   "session-1:a:b",
		"notification:3":  "",
		"task":            "",
		"":                "",
	} {
		if got := db.TagForKey(key); got != want {
			t.Errorf("TagForKey(%q) = %q, want %q", key, got, want)
		}
	}
}

func caseNotificationRecipients(t *testing.T, open Factory) {
	s, owner := setup(t, open)
	const group = "task:7"
	insert := func(ctx context.Context, event db.TaskState, at *time.Time) int64 {
		t.Helper()
		id, err := s.InsertNotification(ctx, db.NewNotification{
			Event: event, Title: "Task", URL: "/tasks/7", ObservedAt: at,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	checkDelivery := func(ctx context.Context, id int64, want bool) {
		t.Helper()
		n, err := s.NotificationForDelivery(ctx, id)
		if want && (err != nil || n.ID != id || n.ReadAt != nil) || !want && !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("delivery for user %d event %d: %+v, %v, want eligible %t", db.ActingUser(ctx), id, n, err, want)
		}
	}
	checkState := func(ctx context.Context, id int64, count int) {
		t.Helper()
		if latest, unread, err := s.NotificationReadState(ctx); err != nil || latest != id || unread != count {
			t.Fatalf("read state for user %d: %d, %d, %v; want %d, %d", db.ActingUser(ctx), latest, unread, err, id, count)
		}
		if unread, err := s.UnreadNotificationCount(ctx); err != nil || unread != count {
			t.Fatalf("unread for user %d: %d, %v; want %d", db.ActingUser(ctx), unread, err, count)
		}
	}

	old := time.Now().Add(-31 * 24 * time.Hour)
	personal := insert(owner, db.StateDone, &old)
	checkDelivery(owner, personal, true)
	var memberID, disabledID int64
	if err := s.WriteAccess(owner, func(r store.Access) error {
		if _, err := db.ConfigureAccessTx(owner, r, ptr(true)); err != nil {
			return err
		}
		now, digest := time.Now(), store.TokenDigest{42}
		if _, err := r.InsertGrant(owner, digest, store.AuthGrant{
			Purpose: store.GrantSetup, CreatedAt: now, ExpiresAt: now.Add(time.Minute),
		}); err != nil {
			return err
		}
		if _, err := db.ClaimInstallationTx(owner, r, digest, store.AccountInput{DisplayName: "Owner", Email: "owner@example.test"}, "verifier", now); err != nil {
			return err
		}
		var err error
		memberID, err = r.InsertMember(owner, store.AccountInput{DisplayName: "Member", Email: "member@example.test"})
		if err != nil {
			return err
		}
		disabledID, err = r.InsertMember(owner, store.AccountInput{DisplayName: "Disabled", Email: "disabled@example.test"})
		if err != nil {
			return err
		}
		return r.DisableAccount(owner, disabledID)
	}); err != nil {
		t.Fatal(err)
	}
	member, disabled := db.WithUser(owner, memberID), db.WithUser(owner, disabledID)
	checkState(member, personal, 0)
	checkDelivery(member, personal, false)
	if p, err := s.GetPreferences(member); err != nil || !slices.Equal(p.NotifyEvents, db.NotificationEvents) {
		t.Fatalf("new member defaults: %+v, %v", p, err)
	}
	if _, err := s.SetNotifyEvents(owner, []db.TaskState{db.StateFailed, db.StateDone}); err != nil {
		t.Fatal(err)
	}
	memberEvents := []db.TaskState{db.StateNeedsInput, db.StateDone}
	if _, err := s.SetNotifyEvents(member, memberEvents); err != nil {
		t.Fatal(err)
	}
	if p, err := s.SetDefaultConnection(owner, ptr(dbtest.T3Connection)); err != nil || !slices.Equal(p.NotifyEvents, []db.TaskState{db.StateFailed, db.StateDone}) {
		t.Fatalf("owner preferences: %+v, %v", p, err)
	}
	if p, err := s.GetPreferences(member); err != nil || p.DefaultConnectionID == nil || *p.DefaultConnectionID != dbtest.T3Connection || !slices.Equal(p.NotifyEvents, memberEvents) {
		t.Fatalf("member preferences: %+v, %v", p, err)
	}

	failed := insert(member, db.StateFailed, nil)
	done := insert(owner, db.StateDone, nil)
	muted := insert(owner, db.EventReady, nil)
	checkDelivery(owner, failed, true)
	checkDelivery(member, failed, false)
	checkDelivery(member, done, true)
	checkDelivery(disabled, done, false)
	checkDelivery(owner, muted, false)
	checkState(owner, muted, 3)
	checkState(member, muted, 1)
	checkState(disabled, muted, 0)

	history, err := s.ListNotificationHistory(member, group, 10, nil)
	if err != nil || len(history) != 4 || history[0].ID != muted || history[1].ID != done || history[2].ID != failed || history[3].ID != personal || history[1].ReadAt != nil {
		t.Fatalf("member history: %+v, %v", history, err)
	}
	for _, n := range []db.Notification{history[0], history[2], history[3]} {
		if n.ReadAt == nil || !n.ReadAt.Equal(n.CreatedAt) {
			t.Fatalf("event %d: read time %v, want creation time %v", n.ID, n.ReadAt, n.CreatedAt)
		}
	}
	groups, err := s.ListNotificationGroups(member, 10, nil, muted)
	if err != nil || len(groups) != 1 || groups[0].Key != group || groups[0].ID != muted || groups[0].EventCount != 4 || groups[0].UnreadCount != 1 || groups[0].ReadAt == nil {
		t.Fatalf("member groups: %+v, %v", groups, err)
	}
	if err := s.MarkNotificationsRead(owner, failed); err != nil {
		t.Fatal(err)
	}
	checkState(owner, muted, 1)
	checkState(member, muted, 1)
	if err := s.MarkNotificationGroupsRead(member, []string{group}); err != nil {
		t.Fatal(err)
	}
	checkDelivery(owner, done, true)
	checkDelivery(member, done, false)
	if tags, err := s.UnreadNotificationTags(member); err != nil || len(tags) != 0 {
		t.Fatalf("member unread tags: %v, %v", tags, err)
	}
	if tags, err := s.UnreadNotificationTags(owner); err != nil || !slices.Equal(tags, []string{"task-7"}) {
		t.Fatalf("owner unread tags: %v, %v", tags, err)
	}
	for _, ctx := range []context.Context{owner, member} {
		rows, err := s.NotificationsAfter(ctx, failed, 10, false)
		if err != nil || len(rows) != 2 || rows[0].ID != done || rows[1].ID != muted || (rows[0].ReadAt == nil) != (ctx == owner) || rows[1].ReadAt == nil {
			t.Fatalf("replay for user %d: %+v, %v", db.ActingUser(ctx), rows, err)
		}
	}
	if _, err := s.SetNotifyEvents(member, db.NotificationEvents); err != nil {
		t.Fatal(err)
	}
	checkState(member, muted, 0)
	var lateID int64
	if err := s.WriteAccess(owner, func(r store.Access) error {
		var err error
		lateID, err = r.InsertMember(owner, store.AccountInput{DisplayName: "Late member", Email: "late@example.test"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	late := db.WithUser(owner, lateID)
	checkState(late, muted, 0)
	newReady := insert(owner, db.EventReady, nil)
	checkDelivery(member, newReady, true)
	checkDelivery(owner, newReady, false)
	checkDelivery(late, newReady, true)

	if n, err := s.PruneNotifications(owner, 30*24*time.Hour); err != nil || n != 1 {
		t.Fatalf("prune: %d, %v", n, err)
	}
	if rows, err := s.ListNotifications(member, 10, nil); err != nil || len(rows) != 4 || rows[len(rows)-1].ID != failed {
		t.Fatalf("retained history: %+v, %v", rows, err)
	}
}
