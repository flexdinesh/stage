package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

const maxBody = 1 << 20

// decodeStrict rejects removed and misspelled fields on versioned write contracts.
func decodeStrict(r *http.Request, v any) error {
	body, err := rawBody(r)
	if err != nil {
		return err
	}
	return db.DecodeStrict(body, v)
}

// rawBody reads a request body of at most maxBody bytes.
func rawBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, db.Invalid("read body: %v", err)
	}
	if len(body) > maxBody {
		return nil, db.Invalid("body exceeds %d bytes", maxBody)
	}
	return body, nil
}

// decode reads a JSON body into v (400 on bad JSON).
func decode(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		return db.Invalid("read body: %v", err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	if err := json.Unmarshal(body, v); err != nil {
		return db.Invalid("invalid JSON body: %v", err)
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, db.Invalid("invalid id %q", r.PathValue("id"))
	}
	return id, nil
}

// multi returns a repeatable/comma-separated query param.
func multi(q url.Values, key string) []string {
	var out []string
	for _, v := range q[key] {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func queryInt64(q url.Values, key string) (*int64, error) {
	v := strings.TrimSpace(q.Get(key))
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil, db.Invalid("invalid %s %q", key, v)
	}
	return &n, nil
}

func queryBool(q url.Values, key string) (*bool, error) {
	switch v := strings.TrimSpace(q.Get(key)); v {
	case "":
		return nil, nil
	case "true":
		b := true
		return &b, nil
	case "false":
		b := false
		return &b, nil
	default:
		return nil, db.Invalid("invalid %s %q (want true|false)", key, v)
	}
}

// querySubagents parses subagents=include|exclude (default include);
// true = exclude subagent threads.
func querySubagents(q url.Values) (bool, error) {
	switch v := strings.TrimSpace(q.Get("subagents")); v {
	case "", "include":
		return false, nil
	case "exclude":
		return true, nil
	default:
		return false, db.Invalid("invalid subagents %q (want include|exclude)", v)
	}
}

// queryTime parses an ISO date (midnight in loc) or RFC 3339 datetime
// (datetime without zone = loc).
func queryTime(q url.Values, key string, loc *time.Location) (*time.Time, error) {
	v := strings.TrimSpace(q.Get(key))
	if v == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return &t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return &t, nil
		}
	}
	return nil, db.Invalid("invalid %s %q (want ISO date or datetime)", key, v)
}

func validEnum(name, v string, allowed []string) error {
	if !slices.Contains(allowed, v) {
		return db.Invalid("invalid %s %q (want one of %s)", name, v, strings.Join(allowed, ", "))
	}
	return nil
}

func taskStateStrings() []string {
	out := make([]string, len(db.TaskStates))
	for i, s := range db.TaskStates {
		out[i] = string(s)
	}
	return out
}

// worktreeRule is a destination connection's harness name and worktree
// policy (harness.Descriptor.Worktree).
type worktreeRule func(connection int64) (name string, policy harness.Worktree)

// anyWorktree is the rule without a destination: the most permissive.
func anyWorktree(int64) (string, harness.Worktree) {
	return "", harness.Worktree{BaseRef: true, Predecessor: true}
}

// worktreeRule reads a connection's policy from its harness descriptor; an
// unknown kind takes nothing from Stage.
func (s *server) worktreeRule(connection int64) (string, harness.Worktree) {
	if connection <= 0 {
		return anyWorktree(connection)
	}
	kind := s.Store.Harness(connection)
	d, ok := s.registry().Lookup(kind)
	if !ok {
		return kind, harness.Worktree{}
	}
	return d.Name, d.Worktree
}

func validateWorkspace(w db.Workspace) (db.Workspace, error) {
	return validateWorkspaceFor(w, 0, anyWorktree)
}

func validateWorkspaceFor(w db.Workspace, connection int64, rule worktreeRule) (db.Workspace, error) {
	name, policy := rule(connection)
	if !policy.BaseRef {
		return harnessWorktree(w, name, policy)
	}
	if w.Base != "" && w.Base != db.BaseRef && w.Base != db.BasePredecessor {
		return w, db.Invalid("invalid workspace.base %q (want ref|predecessor)", w.Base)
	}
	if w.BaseTaskID != nil && w.Base != db.BasePredecessor {
		return w, db.Invalid("workspace.baseTaskId needs base predecessor")
	}
	switch w.Type {
	case "root":
		if w.Base == db.BasePredecessor {
			return w, db.Invalid("workspace.base predecessor needs type worktree")
		}
		return db.Workspace{Type: "root"}, nil
	case "worktree":
		w.BaseRef = strings.TrimSpace(w.BaseRef)
		if w.BaseRef == "" {
			return w, db.Invalid("workspace.baseRef is required for worktree (the fallback with base predecessor)")
		}
		if w.Base != db.BasePredecessor {
			w.Base = ""
		}
		if w.Branch != nil {
			b := strings.TrimSpace(*w.Branch)
			if b == "" {
				w.Branch = nil
			} else {
				w.Branch = &b
			}
		}
		return w, nil
	}
	return w, db.Invalid("invalid workspace.type %q (want root|worktree)", w.Type)
}

// harnessWorktree validates a workspace for a harness that chooses the
// worktree base and branch: no ref or branch, and a predecessor base only
// when the policy allows it (the harness's default is its fallback).
func harnessWorktree(w db.Workspace, name string, policy harness.Worktree) (db.Workspace, error) {
	if name == "" {
		name = "This harness"
	}
	if w.Base != "" && w.Base != db.BaseRef && w.Base != db.BasePredecessor {
		return w, db.Invalid("invalid workspace.base %q (want ref|predecessor)", w.Base)
	}
	if w.BaseTaskID != nil && w.Base != db.BasePredecessor {
		return w, db.Invalid("workspace.baseTaskId needs base predecessor")
	}
	if !policy.Predecessor && (w.Base == db.BasePredecessor || strings.TrimSpace(w.BaseRef) != "" || w.Branch != nil) {
		return w, db.Invalid("%s manages the worktree base and branch. Choose root or worktree without a ref, branch or predecessor.", name)
	}
	if strings.TrimSpace(w.BaseRef) != "" || w.Branch != nil {
		return w, db.Invalid("%s manages the worktree base and branch. Choose root, worktree or a predecessor's branch, without a ref or branch.", name)
	}
	switch w.Type {
	case "root":
		if w.Base == db.BasePredecessor {
			return w, db.Invalid("workspace.base predecessor needs type worktree")
		}
		return db.Workspace{Type: "root"}, nil
	case "worktree":
		if w.Base != db.BasePredecessor {
			return db.Workspace{Type: "worktree"}, nil
		}
		return db.Workspace{Type: "worktree", Base: db.BasePredecessor, BaseTaskID: w.BaseTaskID}, nil
	}
	return w, db.Invalid("invalid workspace.type %q (want root|worktree)", w.Type)
}

func validateOptions(opts []execution.ModelOption) ([]execution.ModelOption, error) {
	if opts == nil {
		return []execution.ModelOption{}, nil
	}
	for _, o := range opts {
		if strings.TrimSpace(o.ID) == "" {
			return nil, db.Invalid("modelOptions[].id is required")
		}
	}
	return opts, nil
}

// trimmedPtr trims; "" becomes nil.
func trimmedPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return &v
}

// requiredText validates a PATCH text field that must not be null/empty.
func requiredText(name string, o *db.Optional[string]) error {
	if !o.Set {
		return nil
	}
	o.V = strings.TrimSpace(o.V)
	if o.Null || o.V == "" {
		return db.Invalid("%s must be a non-empty string", name)
	}
	return nil
}

// nullableText normalizes a PATCH nullable text field ("" = null).
func nullableText(o *db.Optional[string]) {
	if o.Set && !o.Null {
		o.V = strings.TrimSpace(o.V)
		if o.V == "" {
			o.Null = true
		}
	}
}

func enumField(name string, o *db.Optional[string], allowed []string) error {
	if !o.Set {
		return nil
	}
	if o.Null {
		return db.Invalid("%s must not be null", name)
	}
	return validEnum(name, o.V, allowed)
}

// envID returns the T3 environment id for web links ("" if unknown).
func (s *server) envID(ctx context.Context) string {
	if s.Setup != nil {
		return s.Setup.Status(ctx, false).Conn.EnvironmentID
	}
	return ""
}

func (s *server) connected(ctx context.Context, id int64) bool {
	a, e := s.Sync.Adapter(id)
	return e == nil && a.Health(ctx).Available
}
