// Package opencodetest is a fake OpenCode V2 service for tests: health and
// contract and interaction, HTTP Basic auth, pairing codes and session tokens, session
// update (title and archive, session.update), and project listing and
// rename (project.update).
package opencodetest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type Server struct {
	*httptest.Server
	mu       sync.Mutex
	password string
	codes    map[string]bool
	tokens   map[string]bool
	// Users records the Basic username of each authenticated request.
	Users []string
	// Pairs counts POST /api/pair calls.
	Pairs   int
	version string
	// mcp answers GET /api/mcp; mcpDelay holds its reply, mcpFail (a
	// status code) fails it.
	mcp      []map[string]any
	mcpDelay time.Duration
	mcpFail  int
	// noUpdate drops session.update from the contract (an older service).
	noUpdate bool
	// noInteraction drops the permission and form replies and the prompt's
	// delivery from the contract (an older service).
	noInteraction bool
	// noQueueManagement preserves inbox reads for older services.
	noQueueManagement bool
	noDiff            bool
	sessions          map[string]Session
	interactions      []Interaction
	// archived: known session IDs and whether each is archived.
	archived map[string]bool
	// projects: GET /api/project, in insertion order (AddProject).
	projects []*Project
	// noProjectUpdate drops project.update from the contract.
	noProjectUpdate bool
	// projectFail answers the next PATCH /api/project/{id} with this status
	// and message, without renaming (0: succeed).
	projectFail    int
	projectFailMsg string
}

// Project is an OpenCode project as GET /api/project lists it.
type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Canonical string `json:"canonical"`
	Vcs       string `json:"vcs,omitempty"`
	Time      struct {
		Created  int64 `json:"created"`
		Updated  int64 `json:"updated"`
		Archived int64 `json:"archived,omitempty"`
	} `json:"time"`
}

// New starts a fake service; password "" requires no auth.
func New(t testing.TB, password string) *Server {
	s := &Server{password: password, codes: map[string]bool{}, tokens: map[string]bool{}, version: "2.3.0",
		mcp: []map[string]any{MCP("stage", "connected", "")}, archived: map[string]bool{}, sessions: map[string]Session{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// SetPassword changes the server password, revoking issued tokens.
func (s *Server) SetPassword(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.password, s.tokens = p, map[string]bool{}
}

// SetVersion changes what /api/info reports (default 2.3.0).
func (s *Server) SetVersion(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version = v
}

// MCP is a GET /api/mcp entry; errText is set for failed and needs_auth.
func MCP(name, status, errText string) map[string]any {
	st := map[string]any{"status": status}
	if errText != "" {
		st["error"] = errText
	}
	return map[string]any{"name": name, "status": st}
}

// SetMCP sets the configured MCP servers (MCP entries).
func (s *Server) SetMCP(servers ...map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mcp = servers
}

// DelayMCP holds GET /api/mcp replies for d; FailMCP answers them with
// code (0: succeed).
func (s *Server) DelayMCP(d time.Duration) { s.mu.Lock(); s.mcpDelay = d; s.mu.Unlock() }
func (s *Server) FailMCP(code int)         { s.mu.Lock(); s.mcpFail = code; s.mu.Unlock() }

// WithoutSessionUpdate serves a contract without session.update (an older
// service): no rename, no archive.
func (s *Server) WithoutSessionUpdate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.noUpdate = true
}

// WithoutInteraction serves a contract without session.permission.reply,
// session.form.reply and the prompt's delivery (an older service): no
// respond, no send.
func (s *Server) WithoutInteraction() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.noInteraction = true
}

// WithoutQueueManagement removes the inbox mutation routes.
func (s *Server) WithoutQueueManagement() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.noQueueManagement = true
}

// WithoutDiff removes the optional activity route from the advertised contract.
func (s *Server) WithoutDiff() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.noDiff = true
}

// AddProject lists a git project; archived marks it archived in OpenCode.
func (s *Server) AddProject(id, canonical string, archived bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := &Project{ID: id, Canonical: canonical, Vcs: "git"}
	p.Time.Created, p.Time.Updated = 1_700_000_000_000, 1_700_000_000_000
	if archived {
		p.Time.Archived = 1_700_000_000_001
	}
	s.projects = append(s.projects, p)
}

// ProjectName is the project's name ("" when unnamed or unknown).
func (s *Server) ProjectName(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.projects {
		if p.ID == id {
			return p.Name
		}
	}
	return ""
}

// WithoutProjectUpdate serves a contract without project.update.
func (s *Server) WithoutProjectUpdate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.noProjectUpdate = true
}

// FailProjectUpdate answers the next project.update with code and an
// error body carrying message.
func (s *Server) FailProjectUpdate(code int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projectFail, s.projectFailMsg = code, message
}

// AddSession registers a session PATCH /api/session/{id} can archive.
func (s *Server) AddSession(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.archived[id] = false
}

// Archived reports whether PATCH archived the session.
func (s *Server) Archived(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.archived[id]
}

// Revoke rejects every issued token.
func (s *Server) Revoke() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = map[string]bool{}
}

// Accepts reports whether a token is currently valid.
func (s *Server) Accepts(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[token]
}

func random() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Link issues a single-use code, like `opencode pair`.
func (s *Server) Link() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	code := random()
	s.codes[code] = true
	return s.URL + "/auth/connect/" + code
}

func (s *Server) authorized(r *http.Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.password == "" {
		return true
	}
	user, pass, ok := r.BasicAuth()
	if ok && (pass == s.password || s.tokens[pass]) {
		s.Users = append(s.Users, user)
		return true
	}
	return false
}

func (s *Server) issue() string {
	token := "tok_" + random()
	s.tokens[token] = true
	return token
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if code, ok := strings.CutPrefix(r.URL.Path, "/auth/connect/"); ok && r.Method == http.MethodGet {
		s.mu.Lock()
		valid := s.codes[code] && !strings.Contains(r.Header.Get("Accept"), "text/html")
		delete(s.codes, code)
		var token string
		if valid {
			token = s.issue()
		}
		s.mu.Unlock()
		if !valid {
			http.Error(w, `{"message":"Pairing link expired or already used"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
		return
	}
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/pair":
		s.mu.Lock()
		s.Pairs++
		code := random()
		s.codes[code] = true
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "expires_in": 300})
	case r.URL.Path == "/api/info":
		s.mu.Lock()
		version := s.version
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"version": version, "hostname": "fake", "pid": 42})
	case r.URL.Path == "/openapi.json":
		s.mu.Lock()
		contract := spec(!s.noUpdate, !s.noProjectUpdate, !s.noInteraction, !s.noDiff, !s.noQueueManagement)
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(contract)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/session/") && strings.Count(r.URL.Path, "/") == 3:
		s.patchSession(w, r, strings.TrimPrefix(r.URL.Path, "/api/session/"))
	case strings.HasPrefix(r.URL.Path, "/api/session/"):
		s.serveSession(w, r)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/project/"):
		s.patchProject(w, r, strings.TrimPrefix(r.URL.Path, "/api/project/"))
	case r.URL.Path == "/api/project":
		s.mu.Lock()
		projects := []Project{}
		for _, p := range s.projects {
			projects = append(projects, *p)
		}
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(projects)
	case r.Method == http.MethodGet && r.URL.Path == "/api/mcp":
		s.mu.Lock()
		servers, delay, fail := s.mcp, s.mcpDelay, s.mcpFail
		s.mu.Unlock()
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		if fail != 0 {
			http.Error(w, `{"_tag":"InternalError"}`, fail)
			return
		}
		if servers == nil {
			servers = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"location": map[string]any{"directory": "/home/u"}, "data": servers})
	default:
		http.NotFound(w, r)
	}
}

// patchSession is session.update for title and archived: repeating the
// current state is a no-op; any other field is rejected.
func (s *Server) patchSession(w http.ResponseWriter, r *http.Request, id string) {
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"_tag":"InvalidRequestError"}`, http.StatusBadRequest)
		return
	}
	var archived *bool
	for k, v := range body {
		if k == "title" {
			continue
		}
		if k != "archived" || json.Unmarshal(v, &archived) != nil {
			http.Error(w, `{"_tag":"InvalidRequestError"}`, http.StatusBadRequest)
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.noUpdate {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.archived[id]; !ok {
		http.Error(w, `{"_tag":"SessionNotFoundError"}`, http.StatusNotFound)
		return
	}
	if archived != nil {
		s.archived[id] = *archived
	}
	w.WriteHeader(http.StatusNoContent)
}

// patchProject is project.update for a name (additionalProperties: false
// rejects other unknown fields).
func (s *Server) patchProject(w http.ResponseWriter, r *http.Request, id string) {
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"_tag":"InvalidRequestError"}`, http.StatusBadRequest)
		return
	}
	var name *string
	for k, v := range body {
		if k != "name" || json.Unmarshal(v, &name) != nil || name == nil {
			http.Error(w, `{"_tag":"InvalidRequestError"}`, http.StatusBadRequest)
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.noProjectUpdate {
		http.NotFound(w, r)
		return
	}
	if s.projectFail != 0 {
		code, msg := s.projectFail, s.projectFailMsg
		s.projectFail, s.projectFailMsg = 0, ""
		b, _ := json.Marshal(map[string]string{"_tag": "InvalidRequestError", "message": msg})
		http.Error(w, string(b), code)
		return
	}
	for _, p := range s.projects {
		if p.ID == id {
			if name != nil {
				p.Name = *name
			}
			p.Time.Updated++
			_ = json.NewEncoder(w).Encode(p)
			return
		}
	}
	http.Error(w, `{"_tag":"ProjectNotFoundError"}`, http.StatusNotFound)
}

// spec is the contract the adapter's health check requires, with
// session.update unless update is false, project.update unless
// projectUpdate is false, and the permission and form replies and the
// prompt's delivery unless interaction is false. Queue management adds the
// native inbox update and cancel operations.
func spec(update, projectUpdate, interaction, diff, queueManagement bool) map[string]any {
	bodies := map[string][]string{
		"/api/worktree":                   {"projectID", "from", "name"},
		"/api/session":                    {"id", "location", "metadata"},
		"/api/session/{sessionID}/prompt": {"id", "text", "metadata"},
	}
	if interaction {
		bodies["/api/session/{sessionID}/prompt"] = append(bodies["/api/session/{sessionID}/prompt"], "delivery")
	}
	paths := map[string]map[string]any{}
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
		if paths[op.path] == nil {
			paths[op.path] = map[string]any{}
		}
		operation := map[string]any{"operationId": op.id}
		if fields := bodies[op.path]; op.method == "post" && fields != nil {
			props := map[string]any{}
			for _, f := range fields {
				props[f] = map[string]any{}
			}
			if _, ok := props["delivery"]; ok {
				props["delivery"] = map[string]any{"anyOf": []any{
					map[string]any{"$ref": "#/components/schemas/Session.Inbox.Delivery"}, map[string]any{"type": "null"},
				}}
			}
			operation["requestBody"] = map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"properties": props}}}}
		}
		paths[op.path][op.method] = operation
	}
	if queueManagement {
		paths["/api/session/{sessionID}/inbox/{inboxID}"] = map[string]any{
			"delete": map[string]any{"operationId": "session.inbox.cancel"},
			"patch": map[string]any{
				"operationId": "session.inbox.update",
				"requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
					"properties": map[string]any{"delivery": map[string]any{"$ref": "#/components/schemas/Session.Inbox.Delivery"}},
				}}}},
			},
		}
	}
	if diff {
		paths["/api/session/{sessionID}/diff"] = map[string]any{"get": map[string]any{"operationId": "session.diff"}}
	}
	if interaction {
		for path, op := range map[string]struct {
			id     string
			fields []string
		}{
			"/api/session/{sessionID}/permission/{requestID}/reply": {"session.permission.reply", []string{"decision", "message"}},
			"/api/session/{sessionID}/form/{formID}/reply":          {"session.form.reply", []string{"answer"}},
		} {
			props := map[string]any{}
			for _, f := range op.fields {
				props[f] = map[string]any{}
			}
			paths[path] = map[string]any{"post": map[string]any{"operationId": op.id,
				"requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"properties": props}}}}}}
		}
	}
	if update {
		paths["/api/session/{sessionID}"]["patch"] = map[string]any{
			"operationId": "session.update",
			"requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"properties": map[string]any{
				"title": map[string]any{}, "metadata": map[string]any{}, "permissions": map[string]any{}, "archived": map[string]any{},
			}}}}},
		}
	}
	if projectUpdate {
		paths["/api/project/{projectID}"] = map[string]any{"patch": map[string]any{
			"operationId": "project.update",
			"requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"properties": map[string]any{
				"canonical": map[string]any{}, "name": map[string]any{}, "icon": map[string]any{}, "commands": map[string]any{},
			}}}}},
		}}
	}
	return map[string]any{"paths": paths, "components": map[string]any{"schemas": map[string]any{
		"Session.Inbox.Delivery": map[string]any{"type": "string", "enum": []string{"steer", "queue"}},
	}}}
}
