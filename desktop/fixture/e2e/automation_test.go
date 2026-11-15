//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// automation speaks MyGo's automation protocol (docs/plugins/automation.md):
// one JSON request per line over the app's MYGO_AUTOMATION socket, each
// answered on a line of its own, in order.
type automation struct {
	mu   sync.Mutex
	conn net.Conn
	r    *bufio.Reader
	next int
}

// dialAutomation connects once the app listens, failing early if the app
// exits first (no display, no WebKitGTK, ...).
func dialAutomation(path string, timeout time.Duration, appAlive func() bool) (*automation, error) {
	deadline := time.Now().Add(timeout)
	for {
		c, err := net.Dial("unix", path)
		if err == nil {
			return &automation{conn: c, r: bufio.NewReaderSize(c, 1<<20)}, nil
		}
		if !appAlive() {
			return nil, fmt.Errorf("the app exited before its automation socket opened (see its log above)")
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("automation socket %s: %w", path, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type automationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *automationError) Error() string { return e.Code + ": " + e.Message }

// call sends one request and decodes its result into out (nil to drop it).
func (a *automation) call(method string, params any, out any, timeout time.Duration) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.next++
	req := map[string]any{"id": a.next, "method": method}
	if params != nil {
		req["params"] = params
	}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if err := a.conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	if _, err := a.conn.Write(append(b, '\n')); err != nil {
		return err
	}
	line, err := a.r.ReadBytes('\n')
	if err != nil {
		return err
	}
	var res struct {
		ID     int              `json:"id"`
		Result json.RawMessage  `json:"result"`
		Error  *automationError `json:"error"`
	}
	if err := json.Unmarshal(line, &res); err != nil {
		return fmt.Errorf("automation answer %q: %w", line, err)
	}
	if res.Error != nil {
		return res.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(res.Result, out)
}

type window struct {
	ID      int    `json:"id"`
	Kind    string `json:"kind"`
	URL     string `json:"url"`
	Visible bool   `json:"visible"`
	Focused bool   `json:"focused"`
}

func (a *automation) windows() ([]window, error) {
	var out struct {
		Windows []window `json:"windows"`
	}
	err := a.call("windows", nil, &out, 10*time.Second)
	return out.Windows, err
}

// eval evaluates script in the page (promises awaited) and decodes its
// value into out.
func (a *automation) eval(script string, out any, timeout time.Duration) error {
	var res struct {
		Value json.RawMessage `json:"value"`
	}
	if err := a.call("eval", map[string]any{"script": script}, &res, timeout); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if len(res.Value) == 0 {
		return json.Unmarshal([]byte("null"), out)
	}
	return json.Unmarshal(res.Value, out)
}

func (a *automation) close() { _ = a.conn.Close() }
