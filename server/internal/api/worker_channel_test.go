package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

type statusOut struct {
	ID     int64  `json:"id"`
	Online bool   `json:"online"`
	Status string `json:"status"`
}

// stream opens GET /api/worker/channel on srv and returns the hello and a
// reader of the following events.
func stream(t *testing.T, srv *httptest.Server, token string) (workerchan.Hello, *workerchan.Reader, func()) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+"/api/worker/channel", nil)
	for k, v := range workerHeaders(token) {
		req.Header.Set(k, v)
	}
	req.Header.Set(workerchan.VersionHeader, "7.7.7")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream %d %v", res.StatusCode, res.Header)
	}
	rd := workerchan.NewReader(res.Body)
	ev, err := rd.Next()
	if err != nil || ev.Name != workerchan.EventHello {
		t.Fatalf("hello %+v %v", ev, err)
	}
	var h workerchan.Hello
	if err := json.Unmarshal(ev.Data, &h); err != nil || h.Session == "" || h.HeartbeatSec != 15 || h.TimeoutSec != 45 {
		t.Fatalf("hello %s", ev.Data)
	}
	return h, rd, func() { _ = res.Body.Close() }
}

// post sends one up message of session.
func post(t *testing.T, srv *httptest.Server, token, session string, env workerchan.Envelope) (*http.Response, []byte) {
	t.Helper()
	b, _ := json.Marshal(env)
	req, _ := http.NewRequest("POST", srv.URL+"/api/worker/channel", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range workerHeaders(token) {
		req.Header.Set(k, v)
	}
	if session != "" {
		req.Header.Set(workerchan.SessionHeader, session)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(res.Body)
	return res, buf.Bytes()
}

func TestWorkerChannelRoundTrip(t *testing.T) {
	e := newEnv(t, withPublicURL)
	j := e.join("laptop")
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	path := fmt.Sprintf("/api/machines/%d", j.MachineID)
	var m statusOut
	e.must("GET", path, nil, 200, &m)
	if m.Online || m.Status != "offline" {
		t.Fatalf("before connect %+v", m)
	}
	var out errOut
	e.must("POST", path+"/ping", nil, 409, &out)
	if out.Code != "machine_offline" {
		t.Fatalf("offline ping %+v", out)
	}

	hello, rd, closeStream := stream(t, srv, j.Token)
	defer closeStream()
	if hello.MachineID != j.MachineID {
		t.Fatalf("hello %+v", hello)
	}
	var mv machineOut
	e.must("GET", path, nil, 200, &mv)
	if !mv.Online || mv.Version == nil || *mv.Version != "7.7.7" {
		t.Fatalf("connected %+v", mv)
	}

	// Up messages: heartbeat 204, worker-initiated ping answered inline,
	// a missing or stale session refused.
	if res, _ := post(t, srv, j.Token, hello.Session, workerchan.Envelope{Kind: workerchan.KindHeartbeat}); res.StatusCode != 204 {
		t.Fatalf("heartbeat %d", res.StatusCode)
	}
	res, body := post(t, srv, j.Token, hello.Session, workerchan.Envelope{ID: "w1", Kind: workerchan.KindPing})
	var reply workerchan.Envelope
	if res.StatusCode != 200 || json.Unmarshal(body, &reply) != nil || reply.ID != "w1" || reply.Kind != workerchan.KindResult {
		t.Fatalf("worker ping %d %s", res.StatusCode, body)
	}
	if res, _ := post(t, srv, j.Token, "", workerchan.Envelope{Kind: workerchan.KindHeartbeat}); res.StatusCode != 400 {
		t.Fatalf("no session %d", res.StatusCode)
	}
	res, body = post(t, srv, j.Token, "stale", workerchan.Envelope{Kind: workerchan.KindHeartbeat})
	if res.StatusCode != 409 || !strings.Contains(string(body), "channel_replaced") {
		t.Fatalf("stale session %d %s", res.StatusCode, body)
	}

	// Main -> worker ping through the stream.
	type pingRes struct {
		code int
		body string
	}
	done := make(chan pingRes, 1)
	go func() {
		rec := raw(t, e.h, "POST", path+"/ping", nil, nil, nil)
		done <- pingRes{rec.Code, rec.Body.String()}
	}()
	ev, err := rd.Next()
	if err != nil || ev.Name != workerchan.EventRPC {
		t.Fatalf("rpc %+v %v", ev, err)
	}
	var req workerchan.Envelope
	if err := json.Unmarshal(ev.Data, &req); err != nil || req.Kind != workerchan.KindPing || req.Deadline == nil {
		t.Fatalf("request %s", ev.Data)
	}
	pong, _ := json.Marshal(workerchan.Pong{Version: "7.7.7", Time: time.Now()})
	if res, _ := post(t, srv, j.Token, hello.Session, workerchan.Envelope{ID: req.ID, Kind: workerchan.KindResult, Body: pong}); res.StatusCode != 204 {
		t.Fatalf("result %d", res.StatusCode)
	}
	pr := <-done
	if pr.code != 200 || !strings.Contains(pr.body, `"version":"7.7.7"`) {
		t.Fatalf("ping %d %s", pr.code, pr.body)
	}

	// A newer stream replaces this one.
	hello2, _, close2 := stream(t, srv, j.Token)
	defer close2()
	if _, err := rd.Next(); err == nil {
		t.Fatal("replaced stream still open")
	}
	if res, _ := post(t, srv, j.Token, hello.Session, workerchan.Envelope{Kind: workerchan.KindHeartbeat}); res.StatusCode != 409 {
		t.Fatalf("replaced session %d", res.StatusCode)
	}
	if res, _ := post(t, srv, j.Token, hello2.Session, workerchan.Envelope{Kind: workerchan.KindHeartbeat}); res.StatusCode != 204 {
		t.Fatalf("new session %d", res.StatusCode)
	}
}

func TestWorkerChannelV8NeedsUpdate(t *testing.T) {
	e := newEnv(t, withPublicURL)
	j := e.join("old")
	h := map[string]string{"Authorization": "Bearer " + j.Token, buildinfo.APIVersionHeader: "8", workerchan.VersionHeader: "0.1.0"}
	var out errOut
	if rec := raw(t, e.h, "GET", "/api/worker/channel", h, nil, &out); rec.Code != 409 || out.Code != "api_version_mismatch" || rec.Header().Get(buildinfo.APIVersionHeader) != "9" {
		t.Fatalf("mismatch %d %+v", rec.Code, out)
	}
	var m statusOut
	e.must("GET", fmt.Sprintf("/api/machines/%d", j.MachineID), nil, 200, &m)
	if m.Status != "needs_update" || m.Online {
		t.Fatalf("status %+v", m)
	}
	// Without a valid token, a mismatch marks nothing.
	k := e.join("other")
	h["Authorization"] = "Bearer stage_mt_999_x"
	raw(t, e.h, "GET", "/api/worker/channel", h, nil, nil)
	e.must("GET", fmt.Sprintf("/api/machines/%d", k.MachineID), nil, 200, &m)
	if m.Status != "offline" {
		t.Fatalf("unrelated machine %+v", m)
	}
}

func TestRemoveAndLeaveCloseChannel(t *testing.T) {
	e := newEnv(t, withPublicURL)
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	for _, how := range []string{"remove", "leave"} {
		j := e.join(how)
		_, rd, closeStream := stream(t, srv, j.Token)
		switch how {
		case "remove":
			e.must("DELETE", fmt.Sprintf("/api/machines/%d", j.MachineID), nil, 204, nil)
		default:
			if rec := raw(t, e.h, "POST", "/api/worker/leave", workerHeaders(j.Token), nil, nil); rec.Code != 204 {
				t.Fatalf("leave %d %s", rec.Code, rec.Body)
			}
		}
		ended := make(chan error, 1)
		go func() { _, err := rd.Next(); ended <- err }()
		select {
		case err := <-ended:
			if err == nil {
				t.Fatalf("%s: stream sent an event instead of ending", how)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: stream still open", how)
		}
		closeStream()
		if rec := raw(t, e.h, "GET", "/api/worker/machine", workerHeaders(j.Token), nil, nil); rec.Code != 401 {
			t.Fatalf("%s: token still valid: %d", how, rec.Code)
		}
	}
}

func TestPingLocalAndUnknown(t *testing.T) {
	e := newEnv(t)
	var p struct {
		MachineID int64  `json:"machineId"`
		LatencyMs int64  `json:"latencyMs"`
		Version   string `json:"version"`
	}
	e.must("POST", "/api/machines/1/ping", nil, 200, &p)
	if p.MachineID != 1 || p.Version != buildinfo.Version {
		t.Fatalf("local ping %+v", p)
	}
	e.must("POST", "/api/machines/99/ping", nil, 404, nil)
}
