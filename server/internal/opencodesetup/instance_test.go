package opencodesetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// service is a minimal OpenCode V2 service: one project inventory and the
// sessions it stores (all running).
type service struct {
	projects []map[string]any
	sessions map[string]map[string]any
	// codes are unused pairing codes; redeems counts /auth/connect calls.
	codes   map[string]bool
	redeems int
}

func project(id, canonical string) map[string]any {
	return map[string]any{"id": id, "name": id, "canonical": canonical, "time": map[string]int64{"created": 1, "updated": 2}}
}

func runningSession(id, projectID, dir string, created int64) map[string]any {
	return map[string]any{"id": id, "projectID": projectID, "title": id, "location": map[string]string{"directory": dir},
		"time": map[string]int64{"created": created, "updated": created + 10, "busy": created + 5}}
}

func spec() map[string]any {
	ops := []struct{ method, path, id string }{
		{"get", "/api/project", "project.list"}, {"get", "/api/worktree", "worktree.list"}, {"post", "/api/worktree", "worktree.create"},
		{"get", "/api/location", "location.get"}, {"get", "/api/agent", "agent.list"}, {"get", "/api/model", "model.list"},
		{"post", "/api/session", "session.create"}, {"get", "/api/session/{sessionID}", "session.get"}, {"get", "/api/session/active", "session.active"},
		{"post", "/api/session/{sessionID}/prompt", "session.prompt"}, {"post", "/api/session/{sessionID}/interrupt", "session.interrupt"},
		{"get", "/api/session/{sessionID}/message", "session.message.list"}, {"get", "/api/session/{sessionID}/message/{messageID}", "session.message.get"},
		{"get", "/api/session/{sessionID}/inbox", "session.inbox.list"}, {"get", "/api/session/{sessionID}/permission", "session.permission.list"},
		{"get", "/api/session/{sessionID}/form", "session.form.list"},
	}
	bodies := map[string][]string{"/api/worktree": {"projectID", "from", "name"}, "/api/session": {"id", "location", "metadata"}, "/api/session/{sessionID}/prompt": {"id", "text", "metadata"}}
	paths := map[string]map[string]any{}
	for _, op := range ops {
		if paths[op.path] == nil {
			paths[op.path] = map[string]any{}
		}
		operation := map[string]any{"operationId": op.id}
		if fields, ok := bodies[op.path]; ok && op.method == "post" {
			props := map[string]any{}
			for _, f := range fields {
				props[f] = map[string]string{"type": "string"}
			}
			operation["requestBody"] = map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"properties": props}}}}
		}
		paths[op.path][op.method] = operation
	}
	return map[string]any{"paths": paths}
}

func (s *service) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		switch p := r.URL.Path; {
		case p == "/api/info":
			write(map[string]any{"version": "2.3.0", "hostname": "box", "pid": 7})
		case p == "/openapi.json":
			write(spec())
		case p == "/api/project":
			write(s.projects)
		case strings.HasPrefix(p, "/auth/connect/"):
			s.redeems++
			code := strings.TrimPrefix(p, "/auth/connect/")
			if !s.codes[code] {
				w.WriteHeader(http.StatusUnauthorized)
				write(map[string]string{"message": "Pairing link expired or already used"})
				return
			}
			delete(s.codes, code)
			write(map[string]string{"token": "tok-" + code})
		case p == "/api/session/active":
			active := map[string]any{}
			for id := range s.sessions {
				active[id] = map[string]string{"type": "running"}
			}
			write(map[string]any{"data": active})
		case strings.HasSuffix(p, "/permission") || strings.HasSuffix(p, "/form") || strings.HasSuffix(p, "/message"):
			write(map[string]any{"data": []any{}})
		case strings.HasPrefix(p, "/api/session/"):
			if sess, ok := s.sessions[strings.TrimPrefix(p, "/api/session/")]; ok {
				write(map[string]any{"data": sess})
				return
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

type installs struct{ moves [][2]int64 }

func (i *installs) install(old, next int64, _ execution.Adapter) {
	i.moves = append(i.moves, [2]int64{old, next})
}

// record stores what the active OpenCode connection holds: a binding and
// the observed session the next service must prove it serves.
func record(t *testing.T, store *db.Store, s *Service, endpoint string) int64 {
	t.Helper()
	ctx := context.Background()
	id := s.ID()
	p, err := store.InsertProject(ctx, db.Project{Title: "repo", Bindings: []db.ProjectBinding{{ConnectionID: id, ExternalProjectID: "proj", Workspace: "/repo"}}})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	a := s.active
	s.mu.Unlock()
	o, err := a.Observe(ctx, execution.SessionRef{ConnectionID: id, ExternalSessionID: "ses_a"})
	if err != nil {
		t.Fatal(endpoint, err)
	}
	if err := store.PutObservation(ctx, o); err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func TestReplacingTheOpenCodeServiceRetiresItsConnection(t *testing.T) {
	ctx := context.Background()
	var temp dbtest.DB
	store := db.NewStore(temp.Reset(t))
	original := &service{projects: []map[string]any{project("proj", "/repo")}, sessions: map[string]map[string]any{"ses_a": runningSession("ses_a", "proj", "/repo", 1000)}}
	// The same service at another address, and a different service holding
	// the same repository at the same path (OpenCode derives the project id
	// from the repository) but none of the stored sessions.
	addrA, addrA2 := original.start(t), original.start(t)
	other := &service{projects: []map[string]any{project("proj", "/repo")}, sessions: map[string]map[string]any{"ses_b": runningSession("ses_b", "proj", "/repo", 5000)}}
	addrB := other.start(t)

	var in installs
	s, err := newService(ctx, storePorts(store, in.install), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID() != 0 || len(in.moves) != 0 {
		t.Fatal("unconfigured OpenCode created a connection at boot")
	}
	if err := s.Save(ctx, Edit{Endpoint: &addrA}); err != nil {
		t.Fatal(err)
	}
	first := s.ID()
	if first == 0 || len(in.moves) != 1 || in.moves[0] != [2]int64{0, first} {
		t.Fatalf("first connection %d, adapter moves %v", first, in.moves)
	}
	projectID := record(t, store, s, addrA)

	// Same service, new address: the connection stays.
	if err := s.Save(ctx, Edit{Endpoint: &addrA2}); err != nil || s.ID() != first {
		t.Fatalf("moved address: %v id %d", err, s.ID())
	}
	if _, verified, _ := savedSettings(store); verified != addrA2 {
		t.Fatalf("verified %q", verified)
	}

	// A different service is refused without confirmation; nothing changes.
	err = s.Save(ctx, Edit{Endpoint: &addrB})
	var conflict *db.ConflictError
	if !errors.As(err, &conflict) || conflict.Code != "instance_changed" || s.ID() != first {
		t.Fatalf("unconfirmed switch: %v id %d", err, s.ID())
	}

	// Confirmed: a new connection; the old one keeps the binding and session.
	if err := s.Save(ctx, Edit{Endpoint: &addrB, NewInstance: true}); err != nil {
		t.Fatal(err)
	}
	second := s.ID()
	if second == first || store.ConnectionActive(first) || !store.ConnectionActive(second) {
		t.Fatalf("connections %d -> %d", first, second)
	}
	if got := in.moves[len(in.moves)-1]; got != [2]int64{first, second} {
		t.Fatalf("adapter move %v", got)
	}
	// Discovery on the new service never makes the old binding launchable
	// there, though the repository and path are the same.
	if _, err := store.RefreshWorkspaces(ctx, second, []execution.WorkspaceInfo{{ID: "proj", Title: "repo", WorkspaceRoot: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BoundProject(ctx, db.ProjectBinding{ConnectionID: second, ExternalProjectID: "proj", Workspace: "/repo"}); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("new service silently bound: %v", err)
	}
	p, err := store.GetProject(ctx, projectID)
	if err != nil || len(p.Bindings) != 1 || p.Bindings[0].ConnectionID != first || p.Bindings[0].ConnectionActive {
		t.Fatalf("binding %+v %v", p.Bindings, err)
	}

	// The original service again: confirmed, its connection is restored.
	if err := s.Save(ctx, Edit{Endpoint: &addrA}); !errors.As(err, &conflict) || conflict.Code != "instance_returned" || s.ID() != second {
		t.Fatalf("unconfirmed revive: %v id %d", err, s.ID())
	}
	if err := s.Save(ctx, Edit{Endpoint: &addrA, NewInstance: true}); err != nil {
		t.Fatal(err)
	}
	if s.ID() != first || !store.ConnectionActive(first) {
		t.Fatalf("not revived: %d", s.ID())
	}
	if p, _ := store.GetProject(ctx, projectID); !p.Bindings[0].ConnectionActive {
		t.Fatal(fmt.Sprintf("revived binding inactive %+v", p.Bindings))
	}
}

func TestOpenCodeAddressChangeWithAnUnboundWorkspace(t *testing.T) {
	ctx := t.Context()
	var temp dbtest.DB
	store := db.NewStore(temp.Reset(t))
	original := &service{projects: []map[string]any{project("proj", "/repo")}}
	other := &service{projects: []map[string]any{project("proj", "/repo")}}
	firstAddress, nextAddress := original.start(t), other.start(t)
	var in installs
	s, err := newService(ctx, storePorts(store, in.install), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, Edit{Endpoint: &firstAddress}); err != nil {
		t.Fatal(err)
	}
	first := s.ID()
	catalog := []execution.WorkspaceInfo{{ID: "proj", Title: "repo", WorkspaceRoot: "/repo"}}
	if _, err := store.RefreshWorkspaces(ctx, first, catalog); err != nil {
		t.Fatal(err)
	}
	assertNeedsConfirmation := func() {
		t.Helper()
		var conflict *db.ConflictError
		if err := s.Save(ctx, Edit{Endpoint: &nextAddress}); !errors.As(err, &conflict) || conflict.Code != "instance_changed" || s.ID() != first {
			t.Fatalf("address change with no continuity proof: %v, id %d", err, s.ID())
		}
	}
	assertNeedsConfirmation()
	p, err := store.InsertProject(ctx, db.Project{Title: "Planning"})
	if err != nil {
		t.Fatal(err)
	}
	oldSelector := db.WorkspaceID(first, "proj", "/repo")
	task, err := store.CreateTask(ctx, db.NewTask{ConnectionID: first, WorkspaceID: oldSelector, ProjectID: p.ID, Title: "Draft", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefreshWorkspaces(ctx, first, nil); err != nil {
		t.Fatal(err)
	}
	// The saved unbound pin still counts after the workspace leaves inventory.
	assertNeedsConfirmation()
	if err := s.Save(ctx, Edit{Endpoint: &nextAddress, NewInstance: true}); err != nil {
		t.Fatal(err)
	}
	second := s.ID()
	if second == first {
		t.Fatal("the new service inherited the old connection")
	}
	if _, err := store.RefreshWorkspaces(ctx, second, catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveDestination(ctx, second, oldSelector); err == nil {
		t.Fatal("the new service admitted the old workspace selector")
	}
	draft, err := store.GetTask(ctx, task.ID)
	if err != nil || draft.ConnectionID != first || draft.WorkspaceID != oldSelector || draft.ConnectionActive {
		t.Fatalf("the draft followed the new service: %+v, %v", draft, err)
	}
}

// A pairing link works once: when its service needs confirming as new, the
// confirming retry reuses the token Stage already redeemed.
func TestPairingAnotherServiceKeepsTheRedeemedTokenForTheConfirm(t *testing.T) {
	ctx := context.Background()
	var temp dbtest.DB
	store := db.NewStore(temp.Reset(t))
	original := &service{projects: []map[string]any{project("proj", "/repo")}, sessions: map[string]map[string]any{"ses_a": runningSession("ses_a", "proj", "/repo", 1000)}}
	addrA := original.start(t)
	other := &service{projects: []map[string]any{project("proj", "/repo")}, sessions: map[string]map[string]any{}, codes: map[string]bool{"once": true}}
	addrB := other.start(t)

	var in installs
	s, err := newService(ctx, storePorts(store, in.install), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID() != 0 || len(in.moves) != 0 {
		t.Fatal("unconfigured OpenCode created a connection at boot")
	}
	if err := s.Save(ctx, Edit{Endpoint: &addrA}); err != nil {
		t.Fatal(err)
	}
	first := s.ID()
	if first == 0 || len(in.moves) != 1 || in.moves[0] != [2]int64{0, first} {
		t.Fatalf("first connection %d, adapter moves %v", first, in.moves)
	}
	record(t, store, s, addrA)

	link := addrB + "/auth/connect/once"
	var conflict *db.ConflictError
	if err := s.Pair(ctx, link, false); !errors.As(err, &conflict) || conflict.Code != "instance_changed" || s.ID() != first {
		t.Fatalf("unconfirmed pair: %v id %d", err, s.ID())
	}
	if err := s.Pair(ctx, link, true); err != nil {
		t.Fatalf("confirmed pair: %v", err)
	}
	if other.redeems != 1 {
		t.Fatalf("link redeemed %d times", other.redeems)
	}
	saved, _, err := savedSettings(store)
	if err != nil || s.ID() == first || saved.Auth != AuthPaired || saved.Password != "tok-once" || saved.Endpoint != addrB {
		t.Fatalf("paired %+v id %d %v", saved, s.ID(), err)
	}
	// Spent: the same link now needs a fresh one.
	if err := s.Pair(ctx, link, true); !errors.As(err, &conflict) || conflict.Code != CodePairingExpired {
		t.Fatalf("reused link: %v", err)
	}
}
