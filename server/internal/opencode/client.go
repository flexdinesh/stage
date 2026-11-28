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
	"unicode"

	"github.com/flexdinesh/stage/server/internal/execution"
)

type Adapter struct {
	connection                                         int64
	endpoint, browserEndpoint, web, username, password string
	http                                               *http.Client
	contractMu                                         sync.Mutex
	verified                                           serverContract
	onHealth                                           func()
	// local: the service runs on Stage's machine (local auth), so its
	// checkout paths are readable here for repository metadata.
	local bool
	// eventsIdle ends the event stream when nothing, not even a heartbeat,
	// arrives this long (0: defaultEventsIdle).
	eventsIdle time.Duration
}

var _ execution.Adapter = (*Adapter)(nil)
var _ execution.WorkspaceFeatures = (*Adapter)(nil)
var _ execution.LinkResolver = (*Adapter)(nil)

// invisible reports a character a pasted address can carry unseen: control,
// format (zero-width, BOM, direction marks) or any space but a plain one.
func invisible(field, s string) error {
	for _, r := range s {
		if r != ' ' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zs, unicode.Zl, unicode.Zp)) {
			return fmt.Errorf("%s contains an invisible character (U+%04X); retype it", field, r)
		}
	}
	return nil
}

// ValidateURL validates and normalizes the field's address. Errors name the field and
// never echo the value, which may carry credentials.
func ValidateURL(field, s string) (string, error) {
	if e := invisible(field, s); e != nil {
		return "", e
	}
	u, e := url.Parse(s)
	switch {
	case e != nil || (u.Scheme != "https" && u.Scheme != "http"):
		return "", fmt.Errorf("%s must be an http:// or https:// URL", field)
	case u.Host == "":
		return "", fmt.Errorf("%s needs a host, like http://127.0.0.1:49374", field)
	case u.User != nil:
		return "", fmt.Errorf("%s must not include credentials; use the username and password fields", field)
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return "", fmt.Errorf("%s must not include a query or fragment", field)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// Settings configures the HTTP adapter and its browser links.
type Settings struct {
	Endpoint, BrowserEndpoint, WebClient string
	Username, Password                   string
	Local                                bool
}

func New(connection int64, cfg Settings) (*Adapter, error) {
	if connection <= 0 {
		return nil, errors.New("configure a scoped OpenCode connection")
	}
	ep, e := ValidateURL("Service URL", cfg.Endpoint)
	if e != nil {
		return nil, e
	}
	if cfg.BrowserEndpoint == "" {
		cfg.BrowserEndpoint = ep
	}
	public, e := ValidateURL("Browser service URL", cfg.BrowserEndpoint)
	if e != nil {
		return nil, e
	}
	if cfg.WebClient == "" {
		cfg.WebClient = public
	}
	site, e := ValidateURL("Web client URL", cfg.WebClient)
	if e != nil {
		return nil, e
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return nil, errors.New("provide both OpenCode Basic credentials or neither")
	}
	return &Adapter{connection: connection, endpoint: ep, browserEndpoint: public, web: site, username: cfg.Username, password: cfg.Password, local: cfg.Local, http: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// OnHealth registers fn to run at the start of every health check (the
// sync watch checks each installed adapter every few seconds). Set it
// before the adapter is shared; fn must not block.
func (a *Adapter) OnHealth(fn func()) { a.onHealth = fn }

const rejectedReason = "OpenCode refused access. Check the configured credentials."

// Rejected reports a health check that OpenCode answered with 401 or 403.
func Rejected(h execution.Health) bool { return !h.Available && h.Reason == rejectedReason }

// responseError is a non-2xx answer; message is OpenCode's encoded error
// message when the body carries one.
type responseError struct {
	code    int
	message string
}

func (e responseError) Error() string {
	switch e.code {
	case 401, 403:
		return rejectedReason
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

func (e responseError) gone() bool {
	return e.code == http.StatusNotFound || e.code == http.StatusGone
}

func sessionReadErr(err error) error {
	var status responseError
	if errors.As(err, &status) && status.gone() {
		return execution.ErrNotFound
	}
	return err
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
		var body struct {
			Tag     string `json:"_tag"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body)
		if resp.StatusCode == http.StatusBadRequest && method == http.MethodGet &&
			strings.HasPrefix(path, "/api/session?") && req.URL.Query().Has("cursor") && body.Tag == "InvalidCursorError" {
			return execution.ErrSessionSearchCursor
		}
		if resp.StatusCode == http.StatusNotFound && method == http.MethodGet &&
			strings.HasPrefix(path, "/api/location?") && body.Tag == "LocationNotFoundError" {
			return errLocationNotFound
		}
		if resp.StatusCode == http.StatusNotFound && method == http.MethodGet && strings.HasPrefix(path, "/api/fs/list?") {
			switch body.Tag {
			case "FileNotFoundError":
				return errDirectoryNotFound
			case "LocationNotFoundError":
				return errLocationNotFound
			}
		}
		return responseError{code: resp.StatusCode, message: body.Message}
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
	OperationID           string
	Parameters, Responses json.RawMessage
	RequestBody           struct {
		Content map[string]struct{ Schema schemaShape }
	}
}

type serverContract struct {
	info   serverInfo
	search sessionListContract
	// rename: PATCH /api/session/{sessionID} (session.update) takes a title.
	rename bool
	// archive: PATCH /api/session/{sessionID} (session.update) takes
	// "archived". Optional: an older service keeps its other operations.
	archive bool
	// location: GET /api/location (location.get) resolves a directory; the
	// folder workspace source calls it.
	location bool
	// projectRename: PATCH /api/project/{projectID} (project.update) takes
	// a name (the project rename action).
	projectRename bool
	// fsList: GET /api/fs/list (fs.list) lists a folder (DirectoryBrowser).
	fsList bool
	// respond: session.permission.reply takes a decision and
	// session.form.reply an answer (Interactive.Respond).
	respond bool
	// Prompt delivery modes explicitly advertised by session.prompt.
	delivery map[string]bool
	// activity: session.diff augments the required message and session reads.
	activity bool
	// events: the event types Stage subscribes to on GET /api/event
	// (event.subscribe); nil: no stream, polling only (EventSource).
	events []string
}

func (a *Adapter) check(ctx context.Context) (serverInfo, error) {
	contract, err := a.checkContract(ctx)
	return contract.info, err
}

func (a *Adapter) checkContract(ctx context.Context) (serverContract, error) {
	var contract serverContract
	info := &contract.info
	if e := a.get(ctx, "/api/info", info); e != nil {
		return contract, e
	}
	// Any version is accepted while its OpenAPI contract has what Stage
	// calls; version-specific handling can branch on info.Version.
	a.contractMu.Lock()
	defer a.contractMu.Unlock()
	if info.PID > 0 && info.Hostname != "" && a.verified.info == *info {
		return a.verified, nil
	}
	var spec struct {
		Paths      map[string]map[string]operation
		Components struct {
			Schemas map[string]json.RawMessage
		}
	}
	if e := a.get(ctx, "/openapi.json", &spec); e != nil {
		return contract, e
	}
	for _, op := range []struct{ method, path, id string }{
		{"get", "/api/project", "project.list"},
		{"get", "/api/worktree", "worktree.list"},
		{"post", "/api/worktree", "worktree.create"},
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
			return contract, fmt.Errorf("OpenCode %s is missing %s, which Stage needs.", info.Version, op.id)
		}
	}
	for path, fields := range map[string][]string{
		"/api/worktree":                   {"projectID", "from", "name"},
		"/api/session":                    {"id", "location", "metadata"},
		"/api/session/{sessionID}/prompt": {"id", "text", "metadata"},
	} {
		shape, err := requestShape(spec.Paths[path]["post"], spec.Components.Schemas)
		if err != nil {
			return contract, fmt.Errorf("invalid OpenCode %s request schema: %w", path, err)
		}
		for _, field := range fields {
			if shape.Properties[field] == nil {
				return contract, fmt.Errorf("OpenCode %s: the %s request lacks %s, which Stage needs.", info.Version, path, field)
			}
		}
	}
	contract.search = inspectSessionList(spec.Paths["/api/session"]["get"], spec.Components.Schemas)
	update := spec.Paths["/api/session/{sessionID}"]["patch"]
	contract.rename = sessionUpdateTakes(update, spec.Components.Schemas, "title")
	contract.archive = sessionUpdateTakes(update, spec.Components.Schemas, "archived")
	contract.projectRename = operationTakes(spec.Paths["/api/project/{projectID}"]["patch"], spec.Components.Schemas, "project.update", "name")
	contract.location = spec.Paths["/api/location"]["get"].OperationID == "location.get"
	contract.fsList = spec.Paths["/api/fs/list"]["get"].OperationID == "fs.list"
	contract.respond = inspectReplies(spec.Paths, spec.Components.Schemas)
	contract.delivery = inspectDelivery(spec.Paths["/api/session/{sessionID}/prompt"]["post"], spec.Components.Schemas)
	contract.activity = spec.Paths["/api/session/{sessionID}/diff"]["get"].OperationID == "session.diff"
	contract.events = inspectEvents(spec.Paths["/api/event"]["get"])
	a.verified = contract
	return contract, nil
}

func inspectDelivery(op operation, schemas map[string]json.RawMessage) map[string]bool {
	shape, _ := requestShape(op, schemas) // the required prompt shape was already verified
	modes := map[string]bool{}
	seen := map[string]bool{}
	var inspect func(json.RawMessage)
	inspect = func(raw json.RawMessage) {
		var schema struct {
			Ref          string `json:"$ref"`
			Enum         []string
			AnyOf, OneOf []json.RawMessage
		}
		if json.Unmarshal(raw, &schema) != nil {
			return
		}
		for _, mode := range schema.Enum {
			modes[mode] = true
		}
		if schema.Ref != "" && !seen[schema.Ref] {
			seen[schema.Ref] = true
			inspect(schemas[strings.TrimPrefix(schema.Ref, "#/components/schemas/")])
		}
		for _, sub := range append(schema.AnyOf, schema.OneOf...) {
			inspect(sub)
		}
	}
	inspect(shape.Properties["delivery"])
	return modes
}

// requestShape is op's JSON request schema, with a component $ref resolved.
func requestShape(op operation, schemas map[string]json.RawMessage) (schemaShape, error) {
	shape := op.RequestBody.Content["application/json"].Schema
	if shape.Ref == "" {
		return shape, nil
	}
	ref := schemas[strings.TrimPrefix(shape.Ref, "#/components/schemas/")]
	shape = schemaShape{}
	if len(ref) == 0 {
		return shape, nil
	}
	err := json.Unmarshal(ref, &shape)
	return shape, err
}
func (a *Adapter) Health(ctx context.Context) execution.Health {
	if a.onHealth != nil {
		a.onHealth()
	}
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
func (a *Adapter) sessionPath() string {
	return "/server/" + base64.RawURLEncoding.EncodeToString([]byte(a.browserEndpoint)) + "/session/"
}
func (a *Adapter) sessionWebURL(id string) string {
	return a.web + a.sessionPath() + id
}
func (a *Adapter) ResolveLink(_ context.Context, s string) (execution.SessionRef, error) {
	id := strings.TrimSpace(s)
	for _, base := range []string{a.web, a.browserEndpoint} {
		if suffix, ok := strings.CutPrefix(id, base+a.sessionPath()); ok {
			id = suffix
			break
		}
	}
	ref := execution.SessionRef{ConnectionID: a.connection, ExternalSessionID: id}
	if !a.valid(ref) {
		return execution.SessionRef{}, errors.New("paste a session ID or a session link for this OpenCode connection")
	}
	return ref, nil
}
