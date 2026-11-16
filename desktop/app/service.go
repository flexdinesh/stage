package main

import (
	"context"
	"errors"

	"github.com/egoist/mygo"

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

// StartServer starts the Stage service (when stopped) and connects.
func (d *Desktop) StartServer() error {
	if d.h.l == nil {
		return errNotRunning
	}
	return d.h.l.StartServer()
}

// RestartServer restarts the Stage service onto the server bundled with
// this app.
func (d *Desktop) RestartServer() error {
	if d.h.l == nil {
		return errNotRunning
	}
	return d.h.l.RestartServer()
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
// and the Stage service.
func (d *Desktop) SetLoginStartup(ctx context.Context, on bool) error {
	if d.h.l == nil {
		return errNotRunning
	}
	return d.h.l.SetLoginStartup(ctx, on)
}

// TakeNavigation returns the app path of a deep link the UI has not shown
// yet, and forgets it; "" when there is none.
func (d *Desktop) TakeNavigation() string { return d.h.takeNavigation() }
