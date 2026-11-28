package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/auth"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/notify"
)

const (
	notificationOrigin = "https://stage.test"
	ownerPassphrase    = "spindrift persimmon telescope"
	memberPassphrase   = "starfish collides tonight"
)

func mustValue[T any](t *testing.T, value T, err error) T {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func sharedNotificationEnv(t *testing.T) (*env, *notify.Hub, *auth.Service, auth.SignInResult, int64) {
	t.Helper()
	ctx := t.Context()
	hub := notify.NewHub(ctx)
	var a *auth.Service
	var owner auth.SignInResult
	var historical int64
	e := newEnv(t, func(d *api.Deps) {
		id, err := d.Store.InsertNotification(ctx, db.NewNotification{Event: db.EventDone, Title: "Existing work", URL: "/tasks/12"})
		historical = mustValue(t, id, err)
		if err := d.Store.UpsertPushSubscription(ctx, 0, "https://push.example/legacy", "key", "secret", "personal device"); err != nil {
			t.Fatal(err)
		}
		a = auth.New(d.Store, notificationOrigin, nil)
		on := true
		_, link, err := a.Start(ctx, &on)
		if err != nil {
			t.Fatal(err)
		}
		owner, err = a.ClaimSetup(ctx, notificationToken(t, link.URL()), "owner@example.test", "Owner", ownerPassphrase)
		if err != nil {
			t.Fatal(err)
		}
		state, _, err := a.Start(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		d.Access = api.NewAccess(a, notificationOrigin, state, nil)
		d.Push.SharedAccess = true
		d.Inbox = hub
	})
	return e, hub, a, owner, historical
}

func notificationToken(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	token, ok := strings.CutPrefix(mustValue(t, u, err).Fragment, "token=")
	if !ok || token == "" {
		t.Fatal("grant URL is missing its token")
	}
	return token
}

func addNotificationMember(t *testing.T, a *auth.Service) auth.SignInResult {
	t.Helper()
	invite, err := a.Invite(t.Context(), db.DefaultUser, "member@example.test")
	invite = mustValue(t, invite, err)
	member, err := a.RedeemInvite(t.Context(), notificationToken(t, invite.Link.URL()), "Member", memberPassphrase)
	return mustValue(t, member, err)
}

func notifyRequest(t *testing.T, e *env, session auth.SignInResult, method, path, body string, status int, out any) {
	t.Helper()
	req := httptest.NewRequest(method, notificationOrigin+path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:32101"
	if method != http.MethodGet {
		req.Header.Set("Origin", notificationOrigin)
		req.Header.Set("Content-Type", "application/json")
	}
	if token := session.Token.Reveal(); token != "" {
		req.AddCookie(&http.Cookie{Name: "stage_session", Value: token})
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != status {
		t.Fatalf("%s %s: got %d, want %d: %s", method, path, w.Code, status, w.Body)
	}
	if out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSharedInboxPreferencesAndReconnect(t *testing.T) {
	e, hub, accounts, owner, historical := sharedNotificationEnv(t)
	member := addNotificationMember(t, accounts)
	memberDevice, err := accounts.SignIn(t.Context(), "member@example.test", memberPassphrase, netip.MustParseAddr("127.0.0.1"), store.SessionBrowser)
	memberDevice = mustValue(t, memberDevice, err)
	notifyRequest(t, e, member, "PUT", "/api/preferences/notifications", `{"events":["needs_input"]}`, 200, nil)
	var p struct {
		Events []string `json:"notifyEvents"`
		Shared bool     `json:"sharedNotifications"`
	}
	notifyRequest(t, e, owner, "GET", "/api/preferences", "", 200, &p)
	if !p.Shared || len(p.Events) < 2 {
		t.Fatalf("owner preferences changed: %+v", p)
	}
	notifyRequest(t, e, memberDevice, "GET", "/api/preferences", "", 200, &p)
	if !p.Shared || strings.Join(p.Events, ",") != "needs_input" {
		t.Fatalf("second device preferences: %+v", p)
	}
	insert := func(event db.NotificationEvent) int64 {
		id, err := e.store.InsertNotification(t.Context(), db.NewNotification{Event: event, Title: "Shared work", URL: "/tasks/12"})
		return mustValue(t, id, err)
	}
	done, waiting := insert(db.EventDone), insert(db.EventNeedsInput)
	checkBadge := func(session auth.SignInResult, want int) {
		t.Helper()
		var badge struct{ NeedsInput, Unread int }
		notifyRequest(t, e, session, "GET", "/api/badge", "", 200, &badge)
		if badge.Unread != want || badge.NeedsInput != 0 {
			t.Fatalf("badge %+v, want %d unread with no live needs input", badge, want)
		}
	}
	checkBadge(owner, 3)
	checkBadge(member, 1)
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	open := func(session auth.SignInResult, after int64) <-chan sseEvent {
		t.Helper()
		ctx, cancel := context.WithCancel(t.Context())
		req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/api/notifications/stream?after=%d", srv.URL, after), nil)
		req = mustValue(t, req, err)
		req.Host = "stage.test"
		req.AddCookie(&http.Cookie{Name: "stage_session", Value: session.Token.Reveal()})
		res, err := srv.Client().Do(req)
		res = mustValue(t, res, err)
		t.Cleanup(func() { cancel(); res.Body.Close() })
		if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
			t.Fatalf("stream %d: %v", res.StatusCode, res.Header)
		}
		replay := sseReader(t, res)
		readHello(t, replay)
		return replay
	}
	replay := open(member, 0)
	for _, id := range []int64{historical, done, waiting} {
		row := decodeEvent[streamRow](t, nextSSE(t, replay, true), "notification")
		if row.ID != id || row.Read != (id != waiting) {
			t.Fatalf("recipient replay: %+v", row)
		}
	}
	if u := decodeEvent[unreadEvent](t, nextSSE(t, replay, true), "unread"); u.Badge != 1 {
		t.Fatalf("recipient snapshot: %+v", u)
	}
	notifyRequest(t, e, memberDevice, "POST", "/api/notifications/read", `{"groups":["task:12"]}`, 204, nil)
	if u := decodeEvent[unreadEvent](t, nextSSE(t, replay, true), "unread"); u.Badge != 0 || len(u.Tags) != 0 {
		t.Fatalf("second device failed to clear the recipient snapshot: %+v", u)
	}
	checkBadge(owner, 3)
	reconnect := open(memberDevice, done)
	if row := decodeEvent[streamRow](t, nextSSE(t, reconnect, true), "notification"); row.ID != waiting || !row.Read {
		t.Fatalf("reconnect after read: %+v", row)
	}
	decodeEvent[unreadEvent](t, nextSSE(t, reconnect, true), "unread")

	if err := accounts.Disable(t.Context(), owner.Account.ID, member.Account.ID); err != nil {
		t.Fatal(err)
	}
	// A wake with fresh private work must not bypass the stream credential check.
	insert(db.EventDone)
	hub.Signal()
	for _, stream := range []<-chan sseEvent{replay, reconnect} {
		if ev := nextSSE(t, stream, true); ev.name != "access-ended" {
			t.Fatalf("disabled recipient stream: %+v", ev)
		}
	}
	notifyRequest(t, e, member, "GET", "/api/notifications/groups", "", 401, nil)
}

func TestSharedPushBindingLifecycle(t *testing.T) {
	e, _, accounts, owner, _ := sharedNotificationEnv(t)
	member := addNotificationMember(t, accounts)
	ctx := t.Context()
	if subs, err := e.store.ListPushSubscriptions(ctx); err != nil || len(subs) != 0 {
		t.Fatalf("opt-in retained legacy endpoints: %d, %v", len(subs), err)
	}
	const endpoint = "https://push.example/a"
	const sub = `{"endpoint":"` + endpoint + `","keys":{"p256dh":"key","auth":"secret"}}`
	const endpointBody = `{"endpoint":"` + endpoint + `"}`
	bind := func(session auth.SignInResult) db.PushSubscription {
		t.Helper()
		notifyRequest(t, e, session, "POST", "/api/push/subscriptions", sub, 204, nil)
		got, err := e.store.GetPushSubscription(ctx, session.Session.ID, endpoint)
		return mustValue(t, got, err)
	}
	bound := func(session auth.SignInResult, want bool) {
		t.Helper()
		var b struct{ Bound bool }
		notifyRequest(t, e, session, "POST", "/api/push/subscriptions/check", endpointBody, 200, &b)
		if b.Bound != want {
			t.Fatalf("bound = %t, want %t", b.Bound, want)
		}
	}
	first := bind(owner)
	bound(owner, true)
	bound(member, false)
	notifyRequest(t, e, member, "POST", "/api/push/test", endpointBody, 404, nil)
	notifyRequest(t, e, member, "DELETE", "/api/push/subscriptions", endpointBody, 204, nil)
	bound(owner, true)
	id, err := e.store.InsertNotification(ctx, db.NewNotification{Event: db.EventNeedsInput, Title: "Private work title", URL: "/tasks/12"})
	id = mustValue(t, id, err)
	redeem := func(session auth.SignInResult, binding string, status int, out any) {
		t.Helper()
		notifyRequest(t, e, session, "POST", "/api/push/delivery", fmt.Sprintf(`{"binding":%q,"eventId":%d}`, binding, id), status, out)
	}
	var payload notify.Payload
	redeem(owner, first.Binding, 200, &payload)
	if payload.Body != "Private work title" || payload.Badge != 2 {
		t.Fatalf("resolved recipient payload: %+v", payload)
	}
	redeem(member, first.Binding, 204, nil)
	redeem(auth.SignInResult{}, first.Binding, 401, nil)
	notifyRequest(t, e, member, "POST", "/api/push/subscriptions",
		strings.Replace(sub, "secret", "unrelated", 1), 404, nil)

	rebound := bind(member)
	if rebound.Binding == first.Binding {
		t.Fatal("reused profile retained the original binding")
	}
	if err := e.store.DeleteExpiredPushSubscription(ctx, first); err != nil {
		t.Fatal(err)
	}
	bound(owner, false)
	bound(member, true)
	redeem(owner, first.Binding, 204, nil)
	redeem(member, first.Binding, 204, nil)
	redeem(member, rebound.Binding, 200, &payload)
	if payload.Badge != 1 {
		t.Fatalf("rebound recipient badge: %+v", payload)
	}
	second, err := accounts.SignIn(ctx, "member@example.test", memberPassphrase, netip.MustParseAddr("127.0.0.1"), store.SessionBrowser)
	second = mustValue(t, second, err)
	otherEndpoint := strings.Replace(sub, "/a", "/b", 1)
	notifyRequest(t, e, second, "POST", "/api/push/subscriptions", otherEndpoint, 204, nil)
	notifyRequest(t, e, member, "POST", "/api/auth/logout", "", 204, nil)
	redeem(member, rebound.Binding, 401, nil)
	var listed []db.PushSubscription
	listed, err = e.store.ListPushSubscriptions(ctx)
	if err != nil || len(listed) != 1 || listed[0].SessionID != second.Session.ID {
		t.Fatalf("sign-out affected a different device: %d bindings, err %v", len(listed), err)
	}
	secondBinding := listed[0].Binding
	reset, err := accounts.ResetMember(ctx, owner.Account.ID, member.Account.ID)
	reset = mustValue(t, reset, err)
	if err := accounts.RedeemReset(ctx, notificationToken(t, reset.Link.URL()), "telescope after midnight"); err != nil {
		t.Fatal(err)
	}
	redeem(second, secondBinding, 401, nil)
	if subs, err := e.store.ListPushSubscriptions(ctx); err != nil || len(subs) != 0 {
		t.Fatalf("reset retained bindings: %d, %v", len(subs), err)
	}
	restored := bind(owner)
	if err := e.store.InvalidateRestoredAccess(ctx); err != nil {
		t.Fatal(err)
	}
	redeem(owner, restored.Binding, 401, nil)
	if subs, err := e.store.ListPushSubscriptions(ctx); err != nil || len(subs) != 0 {
		t.Fatalf("restore retained bindings: %d, %v", len(subs), err)
	}
}
