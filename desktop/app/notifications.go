package main

import (
	"context"
	"errors"

	"github.com/egoist/mygo"

	"github.com/flexdinesh/stage/desktop/app/alerts"
)

// mygoSink shows the alerts service's notifications and badge through
// MyGo (docs/desktop.md#notifications).
type mygoSink struct{}

var _ alerts.Sink = mygoSink{}

// Show shows n, replacing a shown notification with the same ID. On macOS
// the first one asks for permission and waits for the answer.
func (mygoSink) Show(ctx context.Context, n alerts.Note) error {
	err := mygo.NewNotification(mygo.NotificationOptions{
		ID: n.ID, Title: n.Title, Body: n.Body, Group: "stage",
	}).ShowContext(ctx)
	if errors.Is(err, mygo.ErrNotificationsDenied) {
		return alerts.ErrDenied
	}
	return err
}

// Close removes the notification with id: on macOS also one left by an
// earlier run; on Linux only this run's.
func (mygoSink) Close(id string) { mygo.NewNotification(mygo.NotificationOptions{ID: id}).Close() }

func (mygoSink) CloseAll() { mygo.ClearNotifications() }

// SetBadge shows n on the Dock icon (macOS) or the launcher entry (Linux
// docks that read the Unity launcher API); 0 clears it.
func (mygoSink) SetBadge(n int) { mygo.App.SetBadgeCount(n) }

func (mygoSink) Supported() bool { return mygo.NotificationsSupported() }
