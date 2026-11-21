package worker_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/worker"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// seen is one request the fake main server received.
type seen struct {
	Method, Path, Query, Host, Body string
	Header                          http.Header
}

func fakeMain(t *testing.T) (*httptest.Server, chan seen) {
	t.Helper()
	got := make(chan seen, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := new(strings.Builder)
		_, _ = bufio.NewReader(r.Body).WriteTo(b)
		got <- seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Host, b.String(), r.Header.Clone()}
		if strings.HasSuffix(r.URL.Path, "/stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: hello\ndata: {}\n\n"))
			w.(http.Flusher).Flush()
			<-r.Context().Done() // a stream stays open
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func gatewayServer(t *testing.T, mainURL string) *httptest.Server {
	t.Helper()
	h, err := worker.NewGateway(worker.Config{URL: mainURL, Token: "stage_mt_1_secret"}, nil, quiet())
	if err != nil {
		t.Fatal(err)
	}
	gw := httptest.NewServer(h)
	t.Cleanup(gw.Close)
	return gw
}

func TestGatewayForwardsWithMachineToken(t *testing.T) {
	main, got := fakeMain(t)
	gw := gatewayServer(t, main.URL)
	req, _ := http.NewRequest("POST", gw.URL+"/api/tasks/1/journal?x=1", strings.NewReader(`{"text":"n"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stage-Caller-Session", "1:agent")
	req.Header.Set("Authorization", "Bearer something-else")
	req.Header.Set("Cookie", "session=abc")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := gw.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("status %d", res.StatusCode)
	}
	r := <-got
	mainHost := strings.TrimPrefix(main.URL, "http://")
	switch {
	case r.Method != "POST" || r.Path != workerchan.GatewayPath+"/api/tasks/1/journal" || r.Query != "x=1" || r.Body != `{"text":"n"}`:
		t.Fatalf("forwarded %+v", r)
	case r.Host != mainHost:
		t.Fatalf("host %q, want the main server's %q", r.Host, mainHost)
	case r.Header.Get("Authorization") != "Bearer stage_mt_1_secret":
		t.Fatalf("authorization %q", r.Header.Get("Authorization"))
	case r.Header.Get(buildinfo.APIVersionHeader) == "" || r.Header.Get(workerchan.VersionHeader) == "":
		t.Fatalf("version headers %v", r.Header)
	case r.Header.Get("Stage-Caller-Session") != "1:agent":
		t.Fatalf("caller header dropped: %v", r.Header)
	case r.Header.Get("Cookie") != "" || r.Header.Get("Sec-Fetch-Site") != "" || r.Header.Get("Origin") != "":
		t.Fatalf("browser headers forwarded: %v", r.Header)
	}
	// /mcp is forwarded the same way.
	req, _ = http.NewRequest("POST", gw.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	if res, err = gw.Client().Do(req); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if r := <-got; r.Path != workerchan.GatewayPath+"/mcp" {
		t.Fatalf("mcp path %q", r.Path)
	}
}

// The gateway applies a Stage listener's origin policy before forwarding.
func TestGatewayOriginPolicy(t *testing.T) {
	main, got := fakeMain(t)
	gw := gatewayServer(t, main.URL)
	send := func(method, path, host, ctype string, extra map[string]string) int {
		var body *strings.Reader
		if method == "POST" {
			body = strings.NewReader(`{}`)
		} else {
			body = strings.NewReader("")
		}
		req, _ := http.NewRequest(method, gw.URL+path, body)
		if host != "" {
			req.Host = host
		}
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		for k, v := range extra {
			req.Header.Set(k, v)
		}
		res, err := gw.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if c := send("GET", "/api/tasks", "evil.example", "", nil); c != 403 {
		t.Errorf("rebinding host: %d", c)
	}
	if c := send("GET", "/api/tasks", "stage", "", nil); c != 403 {
		t.Errorf("host stage over tcp: %d", c)
	}
	if c := send("POST", "/api/tasks", "", "application/json", map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}); c != 403 {
		t.Errorf("cross-site: %d", c)
	}
	if c := send("POST", "/api/tasks", "", "text/plain", nil); c != 415 {
		t.Errorf("text/plain body: %d", c)
	}
	if c := send("GET", "/", "", "", nil); c != 404 {
		t.Errorf("root: %d", c)
	}
	select {
	case r := <-got:
		t.Fatalf("a refused request reached the main server: %+v", r)
	default:
	}
	if c := send("GET", "/api/tasks", "", "", nil); c != 201 {
		t.Errorf("loopback cli: %d", c)
	}
}

func TestGatewayStreamsAndReportsUnreachableMain(t *testing.T) {
	main, _ := fakeMain(t)
	gw := gatewayServer(t, main.URL)
	res, err := gw.Client().Get(gw.URL + "/api/notifications/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(res.Body).ReadString('\n')
		line <- s
	}()
	select {
	case s := <-line:
		if s != "event: hello\n" {
			t.Fatalf("first line %q", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream not flushed through the gateway")
	}

	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	gw2 := gatewayServer(t, down.URL)
	res2, err := gw2.Client().Get(gw2.URL + "/api/tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	var e struct{ Error, Code string }
	if err := json.NewDecoder(res2.Body).Decode(&e); err != nil || res2.StatusCode != 502 || e.Code != "gateway_unreachable" {
		t.Fatalf("main down: %d %+v %v", res2.StatusCode, e, err)
	}
}
