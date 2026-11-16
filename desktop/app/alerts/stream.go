package alerts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// apiHost is the synthetic Host of requests over the socket (bridge.Host,
// launch.SocketGetter).
const apiHost = "stage.invalid"

// streamPath is the server's notification stream (docs/api.md).
const streamPath = "/api/notifications/stream"

// errNoStream: the server has no notification stream (404, an older
// server).
var errNoStream = errors.New("server has no " + streamPath)

// openStream GETs the notification stream over the Unix socket, sending
// Last-Event-ID when cursor is set. No client timeout: ctx ends it. The
// caller closes the body.
func openStream(ctx context.Context, socket string, cursor *int64) (io.ReadCloser, error) {
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+apiHost+streamPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	if cursor != nil {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(*cursor, 10))
	}
	res, err := (&http.Client{Transport: tr}).Do(req)
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
