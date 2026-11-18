// Package opencode connects Stage to the OpenCode V2 HTTP API.
package opencode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

const supportedVersion = "2.3.0"

type Adapter struct {
	connection                        int64
	endpoint, web, username, password string
	http                              *http.Client
	contractMu                        sync.Mutex
	verified                          serverInfo
}

var _ execution.Adapter = (*Adapter)(nil)
var _ execution.WorkspaceFeatures = (*Adapter)(nil)
var _ execution.LinkResolver = (*Adapter)(nil)

func serviceURL(s string) (string, error) {
	u, e := url.Parse(s)
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("configure an HTTP(S) URL without credentials, query or fragment")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func New(connection int64, endpoint, web, username, password string) (*Adapter, error) {
	if connection <= 0 {
		return nil, errors.New("configure a scoped OpenCode connection")
	}
	ep, e := serviceURL(endpoint)
	if e != nil {
		return nil, e
	}
	if web == "" {
		web = ep
	}
	site, e := serviceURL(web)
	if e != nil {
		return nil, e
	}
	if (username == "") != (password == "") {
		return nil, errors.New("provide both OpenCode Basic credentials or neither")
	}
	return &Adapter{connection: connection, endpoint: ep, web: site, username: username, password: password, http: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type responseError struct{ code int }

func (e responseError) Error() string {
	switch e.code {
	case 401, 403:
		return "OpenCode refused access. Check the configured credentials."
	case 404:
		return "OpenCode resource is unavailable."
	default:
		return fmt.Sprintf("OpenCode returned HTTP %d.", e.code)
	}
}
func missing(e error) bool {
	var status responseError
	return errors.As(e, &status) && status.code == 404
}

func (a *Adapter) request(ctx context.Context, method, path string, input, out interface{}) error {
	var body io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, a.endpoint+path, body)
	if e != nil {
		return e
	}
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.password != "" {
		req.SetBasicAuth(a.username, a.password)
	}
	resp, e := a.http.Do(req)
	if e != nil {
		return fmt.Errorf("OpenCode request: %w", e)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError{resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	const limit = 16 << 20
	b, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return e
	}
	if len(b) > limit {
		return errors.New("OpenCode response exceeded the supported size")
	}
	if e = json.Unmarshal(b, out); e != nil {
		return fmt.Errorf("invalid OpenCode JSON: %w", e)
	}
	return nil
}
func (a *Adapter) get(ctx context.Context, path string, out interface{}) error {
	return a.request(ctx, http.MethodGet, path, nil, out)
}

type envelope[T any] struct {
	Data     T
	Location struct{ Directory string }
}
type serverInfo struct {
	Version, Hostname string
	PID               int
}

type schemaShape struct {
	Ref        string `json:"$ref"`
	Properties map[string]json.RawMessage
}
type operation struct {
	OperationID string
	RequestBody struct {
		Content map[string]struct{ Schema schemaShape }
	}
}

func (a *Adapter) check(ctx context.Context) (serverInfo, error) {
	var info serverInfo
	if e := a.get(ctx, "/api/info", &info); e != nil {
		return info, e
	}
	if info.Version != supportedVersion {
		return info, fmt.Errorf("OpenCode %q is unverified. Connect a tested V2 %s service.", info.Version, supportedVersion)
	}
	a.contractMu.Lock()
	defer a.contractMu.Unlock()
	if info.PID > 0 && info.Hostname != "" && a.verified == info {
		return info, nil
	}
	var spec struct {
		Paths      map[string]map[string]operation
		Components struct {
			Schemas map[string]schemaShape
		}
	}
	if e := a.get(ctx, "/openapi.json", &spec); e != nil {
		return info, e
	}
	for _, op := range []struct{ method, path, id string }{
		{"get", "/api/project", "project.list"},
		{"get", "/api/worktree", "worktree.list"},
		{"post", "/api/worktree", "worktree.create"},
		{"get", "/api/location", "location.get"},
		{"get", "/api/agent", "agent.list"},
		{"get", "/api/model", "model.list"},
		{"post", "/api/session", "session.create"},
		{"get", "/api/session/{sessionID}", "session.get"},
		{"get", "/api/session/active", "session.active"},
		{"post", "/api/session/{sessionID}/prompt", "session.prompt"},
		{"post", "/api/session/{sessionID}/interrupt", "session.interrupt"},
		{"get", "/api/session/{sessionID}/message", "session.message.list"},
		{"get", "/api/session/{sessionID}/message/{messageID}", "session.message.get"},
		{"get", "/api/session/{sessionID}/inbox", "session.inbox.list"},
		{"get", "/api/session/{sessionID}/permission", "session.permission.list"},
		{"get", "/api/session/{sessionID}/form", "session.form.list"},
	} {
		if spec.Paths[op.path][op.method].OperationID != op.id {
			return info, fmt.Errorf("OpenCode contract is missing %s. Check the server build.", op.id)
		}
	}
	for path, fields := range map[string][]string{
		"/api/worktree":                   {"projectID", "from", "name"},
		"/api/session":                    {"id", "location", "metadata"},
		"/api/session/{sessionID}/prompt": {"id", "text", "metadata"},
	} {
		shape := spec.Paths[path]["post"].RequestBody.Content["application/json"].Schema
		if shape.Ref != "" {
			shape = spec.Components.Schemas[strings.TrimPrefix(shape.Ref, "#/components/schemas/")]
		}
		for _, field := range fields {
			if shape.Properties[field] == nil {
				return info, fmt.Errorf("OpenCode %s request lacks %s", path, field)
			}
		}
	}
	a.verified = info
	return info, nil
}
func (a *Adapter) Health(ctx context.Context) execution.Health {
	h := execution.Health{CheckedAt: time.Now().UTC(), Identity: a.endpoint}
	if info, e := a.check(ctx); e != nil {
		h.Reason = e.Error()
	} else {
		h.Available = true
		h.Identity = fmt.Sprintf("%s|%s|%d", a.endpoint, info.Hostname, info.PID)
	}
	return h
}

var sessionID = regexp.MustCompile(`^ses_[A-Za-z0-9_-]+$`)

func (a *Adapter) valid(ref execution.SessionRef) bool {
	return ref.ConnectionID == a.connection && sessionID.MatchString(ref.ExternalSessionID)
}
func (a *Adapter) sessionPrefix() string {
	return a.web + "/server/" + base64.RawURLEncoding.EncodeToString([]byte(a.endpoint)) + "/session/"
}
func (a *Adapter) Destinations(_ context.Context, ref execution.SessionRef) execution.Links {
	if !a.valid(ref) {
		return execution.Links{}
	}
	return execution.Links{Web: a.sessionPrefix() + ref.ExternalSessionID}
}
func (a *Adapter) ResolveLink(_ context.Context, s string) (execution.SessionRef, error) {
	ref := execution.SessionRef{ConnectionID: a.connection, ExternalSessionID: strings.TrimPrefix(strings.TrimSpace(s), a.sessionPrefix())}
	if !a.valid(ref) {
		return execution.SessionRef{}, errors.New("paste a session ID or a session link for this OpenCode connection")
	}
	return ref, nil
}
