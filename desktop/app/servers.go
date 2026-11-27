package main

import (
	"context"
	"log"

	"github.com/flexdinesh/stage/desktop/app/launch"
	"github.com/flexdinesh/stage/desktop/app/target"
)

// ServerEntry is one Stage server as the Settings page and the top-bar
// menu list it (docs/desktop.md, Servers).
type ServerEntry struct {
	ID   string      `json:"id"`
	Name string      `json:"name"`
	Kind target.Kind `json:"kind"`
	// URL is a remote's base URL, "" for this machine.
	URL string `json:"url"`
	// Active: the app shows this server.
	Active bool `json:"active"`
	// NeedsInput is the server's needs-input count from its notification
	// stream, null until its stream answered.
	NeedsInput *int `json:"needsInput"`
}

// ServerInput is a remote server to save.
type ServerInput struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// activeID is the active server's id ("local" before the launcher runs).
func (h *host) activeID() string {
	if h.l == nil {
		return target.LocalID
	}
	return h.l.Active().ID
}

// serverTargets is every saved server for the notification streams, this
// machine's with its socket once known. While this machine is a worker its
// own server is off and the default socket is the worker's agent gateway
// (which forwards to the main server), so it has no stream.
func (h *host) serverTargets() []target.Target {
	if h.servers == nil {
		return nil
	}
	ts := h.servers.List()
	if h.l != nil {
		if h.l.Worker() != nil {
			return ts[1:]
		}
		ts[0] = h.l.LocalTarget()
	}
	return ts
}

// serverEntries is the server list for the page.
func (h *host) serverEntries() []ServerEntry {
	if h.servers == nil {
		return []ServerEntry{}
	}
	active := h.activeID()
	var badges map[string]int
	if h.alerts != nil {
		badges = h.alerts.NeedsInput()
	}
	out := []ServerEntry{}
	for _, t := range h.servers.List() {
		e := ServerEntry{ID: t.ID, Name: t.Name, Kind: t.Kind, URL: t.URL, Active: t.ID == active}
		if n, ok := badges[t.ID]; ok {
			e.NeedsInput = &n
		}
		out = append(out, e)
	}
	return out
}

func (h *host) entry(id string) ServerEntry {
	for _, e := range h.serverEntries() {
		if e.ID == id {
			return e
		}
	}
	return ServerEntry{}
}

// serversChanged broadcasts the list (servers:changed).
func (h *host) serversChanged() { _ = ServersChanged.Broadcast(h.serverEntries()) }

// listChanged follows a saved-list edit: the notification streams, then
// the page.
func (h *host) listChanged() {
	if h.alerts != nil {
		h.alerts.SetServers(h.serverTargets())
	}
	h.serversChanged()
}

func (h *host) accountChanged(t target.Target) {
	if h.api.Target().Same(t) {
		h.closeAttachments()
	}
	h.l.AccountChanged(t)
	h.alerts.SetSession(t, h.api.Sessions.AlertSession(t))
}

// switchTo makes server id active. The new state is broadcast before
// switchTo returns, so a navigation that follows (a notification click)
// reaches the page after the server change.
func (h *host) switchTo(id string) error {
	if h.l == nil {
		return errNotRunning
	}
	if err := h.l.Switch(id); err != nil {
		return err
	}
	_ = StateChanged.Broadcast(h.l.State())
	h.serversChanged()
	return nil
}

// clickNotification shows a notification's target: its server first
// (switching when another one is active), then its page.
func (h *host) clickNotification(id string) {
	server, path := h.alerts.Target(id)
	if server != "" && server != h.activeID() {
		if err := h.switchTo(server); err != nil {
			log.Printf("stage desktop: notification %s: switch to %s: %v", id, server, err)
			path = "/"
		}
	}
	h.navigate(path)
}

// Servers lists the Stage servers: this machine first, then the saved
// remotes, with the active one and each one's needs-input count.
func (d *Desktop) Servers() []ServerEntry { return d.h.serverEntries() }

// AddServer saves a remote Stage server (it does not switch to it).
func (d *Desktop) AddServer(input ServerInput) (ServerEntry, error) {
	if d.h.servers == nil {
		return ServerEntry{}, errNotRunning
	}
	t, err := d.h.servers.Add(input.Name, input.URL)
	if err != nil {
		return ServerEntry{}, err
	}
	d.h.listChanged()
	return d.h.entry(t.ID), nil
}

// UpdateServer renames a saved server or changes its URL; the active one
// reconnects when its URL changed. This machine's cannot be changed.
func (d *Desktop) UpdateServer(id string, input ServerInput) (ServerEntry, error) {
	if d.h.servers == nil {
		return ServerEntry{}, errNotRunning
	}
	old, _ := d.h.servers.Get(id)
	t, err := d.h.servers.Update(id, input.Name, input.URL)
	if err != nil {
		return ServerEntry{}, err
	}
	if id == d.h.activeID() {
		if err := d.h.switchTo(id); err != nil {
			return ServerEntry{}, err
		}
	}
	d.h.listChanged()
	if !old.Same(t) && d.h.api.Sessions != nil {
		if err := d.h.api.Sessions.Forget(old); err != nil {
			return d.h.entry(t.ID), err
		}
	}
	return d.h.entry(t.ID), nil
}

// RemoveServer deletes a saved server and its notification state; when it
// was active, the app switches to this machine first. This machine's
// cannot be removed.
func (d *Desktop) RemoveServer(id string) error {
	if d.h.servers == nil {
		return errNotRunning
	}
	if id == target.LocalID {
		return target.ErrLocal
	}
	t, ok := d.h.servers.Get(id)
	if !ok {
		return target.ErrNotFound
	}
	if id == d.h.activeID() {
		if err := d.h.switchTo(target.LocalID); err != nil {
			return err
		}
	}
	if err := d.h.servers.Remove(id); err != nil {
		return err
	}
	d.h.listChanged()
	if d.h.api.Sessions != nil {
		return d.h.api.Sessions.Forget(t)
	}
	return nil
}

// SwitchServer makes a server active: open requests and streams end, the
// state starts over for that server (serverEpoch 0) and its connection
// follows in host:state.
func (d *Desktop) SwitchServer(id string) error { return d.h.switchTo(id) }

// TestServer checks a remote server URL before saving it: whether a Stage
// server answers there over TCP, its version and API, and what to change
// when it refuses this app (STAGE_ALLOWED_ORIGINS).
func (d *Desktop) TestServer(ctx context.Context, url string) launch.ServerTest {
	if d.h.l == nil {
		return launch.ServerTest{Error: errNotRunning.Error()}
	}
	return d.h.l.TestServer(ctx, url)
}
