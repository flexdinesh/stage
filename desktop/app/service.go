package main

import (
	"context"
	"errors"

	"github.com/egoist/mygo"

	"github.com/flexdinesh/stage/desktop/app/alerts"
	"github.com/flexdinesh/stage/desktop/app/launch"
	"github.com/flexdinesh/stage/desktop/app/nav"
)

// Desktop is the host service the UI calls through the generated client
// (ui/src/mygo.ts). Only the app's own top frame can call it (MyGo); the
// navigation lock keeps other content out of that frame.
type Desktop struct {
	h *host
}

var errNotRunning = errors.New("the desktop host is not running")

// State is the current connection state.
func (d *Desktop) State() launch.State {
	if d.h.l == nil {
		return launch.State{}
	}
	return d.h.l.State()
}

// Retry runs the start sequence again; it resolves once it settled.
func (d *Desktop) Retry() error {
	if d.h.l == nil {
		return errNotRunning
	}
	return d.h.l.Retry()
}

// StartServer starts the Stage service (when stopped) and connects; this
// machine only (an error while a remote is active).
func (d *Desktop) StartServer() error {
	if d.h.l == nil {
		return errNotRunning
	}
	return d.h.l.StartServer()
}

// RestartServer restarts the Stage service onto the server bundled with
// this app; this machine only.
func (d *Desktop) RestartServer() error {
	if d.h.l == nil {
		return errNotRunning
	}
	return d.h.l.RestartServer()
}

// JoinWorker makes this machine a worker of the active remote server with
// link, a pairing link the page created there: this machine's own Stage
// server service is uninstalled (its data stays) and the worker service
// started (docs/desktop.md, Worker).
func (d *Desktop) JoinWorker(ctx context.Context, link string) error {
	if d.h.l == nil {
		return errNotRunning
	}
	return d.h.l.JoinWorker(ctx, link)
}

// LeaveWorker stops this machine's worker and leaves its main server,
// which removes the machine there; local only deletes the local worker
// config (the main server is unreachable or already removed it).
func (d *Desktop) LeaveWorker(ctx context.Context, local bool) error {
	if d.h.l == nil {
		return errNotRunning
	}
	return d.h.l.LeaveWorker(ctx, local)
}

// RestartWorker restarts the worker service onto the stage CLI bundled
// with this app.
func (d *Desktop) RestartWorker(ctx context.Context) error {
	if d.h.l == nil {
		return errNotRunning
	}
	return d.h.l.RestartWorker(ctx)
}

// Diagnostics is a plain-text report for support: versions, identity,
// service status (without secrets) and the last server log lines.
func (d *Desktop) Diagnostics(ctx context.Context) string {
	if d.h.l == nil {
		return ""
	}
	return d.h.l.Diagnostics(ctx)
}

// CopyText puts text on the clipboard.
func (d *Desktop) CopyText(text string) error {
	mygo.Clipboard.WriteText(text)
	return nil
}

// OpenExternal opens an http(s) page in the default browser.
func (d *Desktop) OpenExternal(url string) error {
	dec := d.h.policy().External(url)
	if dec.Action != nav.External {
		return errors.New(dec.Reason)
	}
	return mygo.Shell.OpenExternal(url)
}

// OpenApp opens an app link: a t3code:// thread in T3 (its web page when
// no T3 app handles it), or a Stage deep link in this window.
func (d *Desktop) OpenApp(url string) error {
	dec := d.h.policy().App(url)
	switch dec.Action {
	case nav.OpenApp:
		return d.h.openApp(url)
	case nav.InApp:
		d.h.navigate(dec.Path)
		return nil
	}
	return errors.New(dec.Reason)
}

// LoginStartup reports whether Stage opens at login.
func (d *Desktop) LoginStartup() bool {
	if d.h.l == nil {
		return false
	}
	return d.h.l.LoginStartup()
}

// SetLoginStartup turns "Open Stage at login" on or off: the app (hidden)
// and this machine's service (the worker service while it is a worker);
// an error while a remote is active and this machine is not its worker.
func (d *Desktop) SetLoginStartup(ctx context.Context, on bool) error {
	if d.h.l == nil {
		return errNotRunning
	}
	return d.h.l.SetLoginStartup(ctx, on)
}

// TakeNavigation returns the app path of a deep link the UI has not shown
// yet, and forgets it; "" when there is none.
func (d *Desktop) TakeNavigation() string { return d.h.takeNavigation() }

// NotificationState is this device's native notification state:
// "unsupported", "off", "blocked" or "on".
func (d *Desktop) NotificationState() string {
	if d.h.alerts == nil {
		return string(alerts.StateUnsupported)
	}
	return string(d.h.alerts.State())
}

// EnableNotifications turns native notifications on and shows a
// confirmation (on macOS the first one asks for permission).
func (d *Desktop) EnableNotifications(ctx context.Context) error {
	if d.h.alerts == nil {
		return errNotRunning
	}
	return d.h.alerts.Enable(ctx)
}

// DisableNotifications turns native notifications off and closes shown
// ones; the badge keeps updating.
func (d *Desktop) DisableNotifications() error {
	if d.h.alerts == nil {
		return errNotRunning
	}
	d.h.alerts.Disable()
	return nil
}

// TestNotification shows a test notification on this device.
func (d *Desktop) TestNotification(ctx context.Context) error {
	if d.h.alerts == nil {
		return errNotRunning
	}
	return d.h.alerts.Test(ctx)
}

// CloseNotifications closes the active server's shown notification with
// tag (task-12, session-x), or all of its notifications for "".
func (d *Desktop) CloseNotifications(tag string) {
	if d.h.alerts != nil {
		d.h.alerts.CloseShown(d.h.activeID(), tag)
	}
}

// SyncNotifications closes shown notifications whose groups were read on
// any device (every server).
func (d *Desktop) SyncNotifications() {
	if d.h.alerts != nil {
		d.h.alerts.Sync("")
	}
}

// OpenNotificationSettings opens the OS notification settings for this
// app; false when this system has no known place to open.
func (d *Desktop) OpenNotificationSettings() (bool, error) {
	return alerts.OpenSettings(d.h.bundleID(), mygo.Shell.OpenExternal)
}
