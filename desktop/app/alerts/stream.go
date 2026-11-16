package alerts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// streamPath is the server's notification stream (docs/api.md).
const streamPath = "/api/notifications/stream"

// errNoStream: the server has no notification stream (404, an older
// server).
var errNoStream = errors.New("server has no " + streamPath)

// openStream GETs the notification stream of t (over the socket, or TCP
// to a remote, with t's Host and no Origin), sending Last-Event-ID when
// cursor is set. No client timeout: ctx ends it. The caller closes the
// body.
func openStream(ctx context.Context, t target.Target, cursor *int64) (io.ReadCloser, error) {
	tr := t.Transport()
	tr.DisableKeepAlives = true
	tr.ResponseHeaderTimeout = 30 * time.Second
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.BaseURL()+streamPath, nil)
	if err != nil {
		return nil, err
	}
	req.Host = t.Host()
	req.Header.Set("Accept", "text/event-stream")
	if cursor != nil {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(*cursor, 10))
	}
	client := &http.Client{Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	switch {
	case res.StatusCode == http.StatusNotFound:
		res.Body.Close()
		return nil, errNoStream
	case res.StatusCode != http.StatusOK:
		res.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", streamPath, res.Status)
	}
	return res.Body, nil
}
