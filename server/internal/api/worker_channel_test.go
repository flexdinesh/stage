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

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

type statusOut struct {
	ID           int64  `json:"id"`
	Online       bool   `json:"online"`
	Status       string `json:"status"`
	StatusReason string `json:"statusReason"`
}

// stream opens GET /api/worker/channel on srv and returns the hello and a
// reader of the following events.
func stream(t *testing.T, srv *httptest.Server, token string) (workerchan.Hello, *workerchan.Reader, func()) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+"/api/worker/channel", nil)
	for k, v := range workerHeaders(token) {
		req.Header.Set(k, v)
	}
	req.Header.Set(buildinfo.APIVersionHeader, "42")
	req.Header.Set(workerchan.VersionHeader, "7.7.7")
	req.Header.Set(workerchan.ProtocolMinHeader, "1")
	req.Header.Set(workerchan.ProtocolMaxHeader, "3")
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
	req.Header.Set(buildinfo.APIVersionHeader, "42")
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
	if hello.MachineID != j.MachineID || hello.ProtocolVersion != workerchan.ProtocolVersion || hello.ProtocolRange != workerchan.CurrentRange() {
		t.Fatalf("hello %+v", hello)
	}
	e.must("GET", path, nil, 200, &m)
	if m.Online || m.Status != "negotiating" || m.StatusReason == "" {
		t.Fatalf("before ready %+v", m)
	}
	ready := workerchan.Envelope{Kind: workerchan.KindReady,
		Body: json.RawMessage(`{"kinds":[],"channelKinds":["ping"],"connections":[]}`)}
	if res, body := post(t, srv, "bad token", hello.Session, ready); res.StatusCode != 401 {
		t.Fatalf("unauthenticated ready %d %s", res.StatusCode, body)
	}
	e.must("GET", path, nil, 200, &m)
	if m.Online || m.Status != "negotiating" {
		t.Fatalf("unauthenticated ready changed state %+v", m)
	}
	if res, body := post(t, srv, j.Token, hello.Session, ready); res.StatusCode != 204 {
		t.Fatalf("ready %d %s", res.StatusCode, body)
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
	if res, _ := post(t, srv, j.Token, hello.Session, ready); res.StatusCode != 409 {
		t.Fatalf("replaced ready %d", res.StatusCode)
	}
	e.must("GET", path, nil, 200, &m)
	if m.Online || m.Status != "negotiating" {
		t.Fatalf("replaced ready changed state %+v", m)
	}
	if res, _ := post(t, srv, j.Token, hello2.Session, workerchan.Envelope{Kind: workerchan.KindHeartbeat}); res.StatusCode != 204 {
		t.Fatalf("new session %d", res.StatusCode)
	}
}

func TestWorkerChannelRangeRejection(t *testing.T) {
	server := workerchan.CurrentRange()
	for _, tc := range []struct {
		name, min, max, reason, code string
		want                         int
		server                       workerchan.VersionRange
	}{
		{"legacy", "", "", "Update Stage on laptop", workerchan.CodeProtocolMismatch, 409, workerchan.CurrentRange()},
		{"server older", fmt.Sprint(server.Max + 1), fmt.Sprint(server.Max + 2), "Update Stage on the main machine", workerchan.CodeProtocolMismatch, 409, server},
		{"worker older", "1", "1", "Update Stage on laptop", workerchan.CodeProtocolMismatch, 409, workerchan.CurrentRange()},
		{"non-integer", "a", "2", "", "invalid_channel_protocol", 400, workerchan.CurrentRange()},
		{"zero", "0", "2", "", "invalid_channel_protocol", 400, workerchan.CurrentRange()},
		{"inverted", "2", "1", "", "invalid_channel_protocol", 400, workerchan.CurrentRange()},
		{"partial", "1", "", "", "invalid_channel_protocol", 400, workerchan.CurrentRange()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, withPublicURL, func(d *api.Deps) {
				d.Channels = workerchan.NewHub(t.Context(), workerchan.Options{ProtocolRange: tc.server})
			})
			j := e.join("laptop")
			h := map[string]string{buildinfo.APIVersionHeader: "9", workerchan.VersionHeader: "0.1.0"}
			if tc.min != "" {
				h[workerchan.ProtocolMinHeader] = tc.min
			}
			if tc.max != "" {
				h[workerchan.ProtocolMaxHeader] = tc.max
			}
			path := fmt.Sprintf("/api/machines/%d", j.MachineID)
			var m statusOut
			h["Authorization"] = "Bearer stage_mt_999_x"
			if rec := raw(t, e.h, "GET", "/api/worker/channel", h, nil, nil); rec.Code != 401 {
				t.Fatalf("unauthenticated opening: %d", rec.Code)
			}
			e.must("GET", path, nil, 200, &m)
			if m.Status != "offline" {
				t.Fatalf("unauthenticated opening changed presence %+v", m)
			}
			h["Authorization"] = "Bearer " + j.Token
			var out errOut
			if rec := raw(t, e.h, "GET", "/api/worker/channel", h, nil, &out); rec.Code != tc.want || out.Code != tc.code {
				t.Fatalf("opening %d %+v", rec.Code, out)
			}
			e.must("GET", path, nil, 200, &m)
			if m.Online || (tc.reason == "" && m.Status != "offline") ||
				(tc.reason != "" && (m.Status != "needs_update" || !strings.Contains(m.StatusReason, tc.reason))) {
				t.Fatalf("status %+v", m)
			}
		})
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
