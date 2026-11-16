package t3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"
)

// ProbeTTL is how long a probe result (success or failure) is reused.
const ProbeTTL = 10 * time.Second

// ProbeURL reads base/.well-known/t3/environment (unauthenticated
// ExecutionEnvironmentDescriptor). Anything but a 200 with an environment
// id is unreachable, with a short reason in Err. hc bounds the request.
func ProbeURL(ctx context.Context, hc *http.Client, base string) Probe {
	p := Probe{CheckedAt: time.Now().UTC()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/.well-known/t3/environment", nil)
	if err != nil {
		p.Err = "invalid T3 URL"
		return p
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		p.Err = probeError(err)
		return p
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		p.Err = fmt.Sprintf("not a T3 server (HTTP %d)", resp.StatusCode)
		return p
	}
	var d struct {
		EnvironmentID string `json:"environmentId"`
		Label         string `json:"label"`
		ServerVersion string `json:"serverVersion"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d) != nil || d.EnvironmentID == "" {
		p.Err = "not a T3 server (no environment id)"
		return p
	}
	p.Reachable = true
	p.Identity = &Identity{EnvironmentID: d.EnvironmentID, Label: d.Label, ServerVersion: d.ServerVersion}
	return p
}

// probeError is a short human reason for a failed probe request.
func probeError(err error) string {
	var dns *net.DNSError
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, context.DeadlineExceeded), os.IsTimeout(err):
		return "timed out"
	case errors.As(err, &dns):
		return "host not found"
	}
	return err.Error()
}

// probeRun is one in-flight probe; waiters read p after done closes.
type probeRun struct {
	done chan struct{}
	p    Probe
}

// Probe implements Client: the cached probe while fresh, else one shared
// request (bounded by envTimeout, not by ctx, so a caller that gives up
// does not fail the others).
func (c *MCPClient) Probe(ctx context.Context) Probe {
	run, p := c.startProbe()
	if run == nil {
		return p
	}
	select {
	case <-run.done:
		return run.p
	case <-ctx.Done():
		return c.cachedProbe()
	}
}

// startProbe returns the cached probe while fresh (nil run), else the
// running probe, starting one when none runs.
func (c *MCPClient) startProbe() (*probeRun, Probe) {
	c.probeMu.Lock()
	defer c.probeMu.Unlock()
	if !c.probe.CheckedAt.IsZero() && c.now().Sub(c.probe.CheckedAt) < ProbeTTL {
		return nil, c.probe
	}
	if c.probeRun != nil {
		return c.probeRun, Probe{}
	}
	run := &probeRun{done: make(chan struct{})}
	c.probeRun = run
	gen, base := c.probeGen, c.base()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), envTimeout)
		p := ProbeURL(ctx, c.envHC, base)
		cancel()
		p.CheckedAt = c.now().UTC()
		run.p = p
		c.probeMu.Lock()
		if c.probeGen == gen {
			c.probe = p
			if p.Identity != nil {
				c.noteEnvironment(p.Identity.EnvironmentID)
			}
		}
		if c.probeRun == run {
			c.probeRun = nil
		}
		c.probeMu.Unlock()
		close(run.done)
	}()
	return run, Probe{}
}

// cachedProbe is the last probe of the current base URL (zero: none yet).
func (c *MCPClient) cachedProbe() Probe {
	c.probeMu.Lock()
	defer c.probeMu.Unlock()
	return c.probe
}

// FullAccessScopes are the scopes a pairing code needs for stage's
// full-access mcp-client session (`t3 auth pairing create --scope`).
func FullAccessScopes() []string { return slices.Clone(fullAccessScopes) }

// PairingLabel is the label stage gives its pairing codes.
func PairingLabel(clientName string) string {
	if clientName == "" {
		clientName = DefaultClientName
	}
	return clientName + " mcp"
}
