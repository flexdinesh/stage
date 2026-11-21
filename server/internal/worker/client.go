package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// HTTPError is a non-2xx answer from a worker route ({error, code}).
type HTTPError struct {
	Status int
	Code   string
	Msg    string
}

func (e *HTTPError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("stage server %d %s: %s", e.Status, e.Code, e.Msg)
	}
	return fmt.Sprintf("stage server %d: %s", e.Status, e.Msg)
}

// Revoked reports whether err is the server refusing the machine token
// (the machine was removed, or the token is wrong).
func Revoked(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Status == http.StatusUnauthorized
}

// VersionMismatch reports whether err is the server refusing this worker's
// API version.
func VersionMismatch(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Status == http.StatusConflict && he.Code == "api_version_mismatch"
}

// api calls the main server's worker routes.
type api struct {
	base  string
	token string // "" for join
	http  *http.Client
}

func newAPI(base, token string, hc *http.Client) *api {
	if hc == nil {
		hc = &http.Client{}
	}
	return &api{base: base, token: token, http: hc}
}

func (a *api) request(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(buildinfo.APIVersionHeader, strconv.Itoa(buildinfo.APIVersion))
	req.Header.Set(workerchan.VersionHeader, buildinfo.Version)
	req.Header.Set("User-Agent", "stage-worker/"+buildinfo.Version)
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	return req, nil
}

// do sends a request with a bounded timeout and decodes a JSON answer into
// out (nil: discard).
func (a *api) do(ctx context.Context, method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := a.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	res, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if err := checkStatus(res); err != nil {
		return err
	}
	if out == nil || res.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		return nil
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}

// checkStatus turns a non-2xx response into an *HTTPError.
func checkStatus(res *http.Response) error {
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	var e struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if json.Unmarshal(b, &e) != nil || e.Error == "" {
		e.Error = http.StatusText(res.StatusCode)
	}
	return &HTTPError{Status: res.StatusCode, Code: e.Code, Msg: e.Error}
}

// RemoteMachine is GET /api/worker/machine: this machine as the main server
// sees it.
type RemoteMachine struct {
	ID         int64      `json:"id"`
	Label      string     `json:"label"`
	Status     string     `json:"status"`
	Online     bool       `json:"online"`
	LastSeenAt *time.Time `json:"lastSeenAt"`
	Version    *string    `json:"version"`
}
