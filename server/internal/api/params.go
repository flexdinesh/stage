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

// removedModelFields are the T3 model inputs removed in API v8.
var removedModelFields = []string{"providerInstanceId", "model", "modelOptions", "runtimeMode", "interactionMode"}

// removedWorkspaceField is the Stage worktree input removed in API v8
// (ADR 0007 decisions 5 and 6).
const removedWorkspaceField = "workspace"

// decodeConfig reads a task or schedule body into v (rejecting unknown
// fields when strict). A removed model or workspace input is a 400 naming
// its replacement.
func decodeConfig(r *http.Request, v any, strict bool) error {
	body, err := rawBody(r)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) == nil {
		for _, f := range removedModelFields {
			if _, ok := fields[f]; ok {
				return db.Invalid("%s was removed in API v8; send executionConfig with connectionId", f)
			}
		}
		if _, ok := fields[removedWorkspaceField]; ok {
			return db.Invalid("workspace was removed in API v8; set the harness's launch options in executionConfig.options (worktree, baseRef, branch where the connection declares them; see its capabilities)")
		}
	}
	if strict {
		return db.DecodeStrict(body, v)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	if err := json.Unmarshal(body, v); err != nil {
		return db.Invalid("invalid JSON body: %v", err)
	}
	return nil
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
// true = exclude subagent sessions.
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

func queryCompletedFrom(q url.Values) (*time.Time, error) {
	if q.Has("settledFrom") {
		return nil, db.Invalid("settledFrom was removed in API v9; use completedFrom")
	}
	return queryTime(q, "completedFrom", time.UTC)
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

func (s *server) connected(ctx context.Context, id int64) bool {
	a, e := s.Engine.Connections.Catalog(id)
	return e == nil && a.Health(ctx).Available
}
