package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// apiHost is the synthetic Host of requests over the socket (bridge.Host);
// the server's origin policy accepts it on Unix listeners only.
const apiHost = "stage.invalid"

// Health is the part of GET /api/health the app reads.
type Health struct {
	Status     string  `json:"status"`
	Service    string  `json:"service"`
	Identity   string  `json:"identity"`
	Version    string  `json:"version"`
	APIVersion int     `json:"apiVersion"`
	InstanceID string  `json:"instanceId"`
	PublicURL  *string `json:"publicUrl"`
}

// Getter GETs a Stage API path over a Unix socket and decodes the JSON
// answer into v; a status other than 200 is an error.
type Getter func(ctx context.Context, socket, path string, v any) error

// SocketGetter is the Getter over the socket itself: no proxy, no TCP.
func SocketGetter(ctx context.Context, socket, path string, v any) error {
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		DisableKeepAlives: true,
	}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+apiHost+path, nil)
	if err != nil {
		return err
	}
	res, err := (&http.Client{Transport: tr, Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, res.Status)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	return nil
}
