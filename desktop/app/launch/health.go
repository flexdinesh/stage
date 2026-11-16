package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

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

// Getter GETs a Stage API path on a target and decodes the JSON answer
// into v; a status other than 200 is an *HTTPError.
type Getter func(ctx context.Context, t target.Target, path string, v any) error

// HTTPError is a Stage API answer other than 200, with the server's
// {error} message when it sent one.
type HTTPError struct {
	Path    string
	Status  int
	Message string
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("GET %s: %d %s", e.Path, e.Status, e.Message)
	}
	return fmt.Sprintf("GET %s: %d %s", e.Path, e.Status, http.StatusText(e.Status))
}

// TargetGetter is the Getter over the target's own transport: the socket
// (Host stage.invalid) or the remote over TCP/TLS (its Host), never
// through an HTTP proxy, without Origin (a non-browser client to the
// server's origin policy).
func TargetGetter(ctx context.Context, t target.Target, path string, v any) error {
	tr := t.Transport()
	tr.DisableKeepAlives = true
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.BaseURL()+path, nil)
	if err != nil {
		return err
	}
	req.Host = t.Host()
	client := &http.Client{Transport: tr, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		he := &HTTPError{Path: path, Status: res.StatusCode}
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil {
			he.Message = e.Error
		}
		return he
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	return nil
}
