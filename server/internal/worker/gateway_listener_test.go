package worker

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

func TestPrivateGatewayHeader(t *testing.T) {
	dir, err := os.MkdirTemp("", "wk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	main := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, v := range r.Header.Values(workerchan.PrivateGatewayHeader) {
			w.Header().Add(workerchan.PrivateGatewayHeader, v)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(main.Close)
	log := slog.New(slog.DiscardHandler)
	h, err := NewGateway(Config{URL: main.URL, Token: "machine-token"}, main.Client().Transport, log)
	if err != nil {
		t.Fatal(err)
	}
	addrs, err := listen.ParseList("unix,unix:" + filepath.Join(dir, "explicit.sock"))
	if err != nil {
		t.Fatal(err)
	}
	addrs = append(addrs, listen.Addr{Network: "tcp", Address: "127.0.0.1:0"})
	getenv := map[string]string{"STAGE_DATA_DIR": dir}
	g, err := openGateway(addrs, func(k string) string { return getenv[k] }, h, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.close)

	for _, tc := range []struct {
		name     string
		listener int
		spoof    bool
		want     []string
	}{
		{name: "private socket", listener: 0, want: []string{workerchan.PrivateGatewayProof("machine-token")}},
		{name: "private socket replaces caller values", listener: 0, spoof: true, want: []string{workerchan.PrivateGatewayProof("machine-token")}},
		{name: "explicit Unix socket strips caller values", listener: 1, spoof: true},
		{name: "loopback TCP strips caller values", listener: 2, spoof: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln := g.lns[tc.listener].Addr()
			transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, ln.Network(), ln.String())
			}}
			t.Cleanup(transport.CloseIdleConnections)
			cli := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/api/tasks", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.spoof {
				req.Header[strings.ToLower(workerchan.PrivateGatewayHeader)] = []string{"1", "0"}
			}
			res, err := cli.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusNoContent {
				t.Fatalf("status %d", res.StatusCode)
			}
			if got := res.Header.Values(workerchan.PrivateGatewayHeader); !slices.Equal(got, tc.want) {
				t.Fatalf("forwarded attestation %q, want %q", got, tc.want)
			}
		})
	}
}
