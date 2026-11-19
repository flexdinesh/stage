// Package opencodetest is a fake OpenCode V2 service for tests: health and
// contract, HTTP Basic auth, pairing codes and session tokens.
package opencodetest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
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
}

// New starts a fake service; password "" requires no auth.
func New(t testing.TB, password string) *Server {
	s := &Server{password: password, codes: map[string]bool{}, tokens: map[string]bool{}, version: "2.3.0"}
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
		_ = json.NewEncoder(w).Encode(spec())
	case r.URL.Path == "/api/project":
		fmt.Fprint(w, `[]`)
	default:
		http.NotFound(w, r)
	}
}

// spec is the contract the adapter's health check requires.
func spec() map[string]any {
	bodies := map[string][]string{
		"/api/worktree":                   {"projectID", "from", "name"},
		"/api/session":                    {"id", "location", "metadata"},
		"/api/session/{sessionID}/prompt": {"id", "text", "metadata"},
	}
	paths := map[string]map[string]any{}
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
		if paths[op.path] == nil {
			paths[op.path] = map[string]any{}
		}
		operation := map[string]any{"operationId": op.id}
		if fields := bodies[op.path]; op.method == "post" && fields != nil {
			props := map[string]any{}
			for _, f := range fields {
				props[f] = map[string]any{}
			}
			operation["requestBody"] = map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"properties": props}}}}
		}
		paths[op.path][op.method] = operation
	}
	return map[string]any{"paths": paths}
}
