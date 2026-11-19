// Package agentctx resolves per-call session identity and Stage project ownership.
package agentctx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
)

// ThreadEnv is the env var T3 sets to the provider session's thread id.
const ThreadEnv = "T3CODE_THREAD_ID"

// Caller is the calling agent's context; empty fields are unknown.
type Caller struct {
	Session  *db.SessionRef `json:"session"`
	ThreadID string         `json:"threadId"`
	Cwd      string         `json:"cwd"`
}

// FromEnv reads process-local context for the CLI or explicitly dedicated T3 stdio.
func FromEnv() Caller {
	c := Caller{ThreadID: strings.TrimSpace(os.Getenv(ThreadEnv))}
	if wd, err := os.Getwd(); err == nil {
		c.Cwd = wd
	}
	return c
}

// Resolve combines an explicit caller with a per-call transport claim.
// t3 resolves the active T3 connection for a legacy T3 thread id.
func (c Caller) Resolve(explicit *db.SessionRef, legacy, cwd string, meta *db.SessionRef, t3 func() (int64, error)) (Caller, error) {
	legacy = strings.TrimSpace(legacy)
	if legacy != "" && explicit != nil {
		return Caller{}, errors.New("pass callerSession or callerThreadId")
	}
	if legacy != "" {
		id, err := t3()
		if err != nil {
			return Caller{}, err
		}
		explicit = &db.SessionRef{ConnectionID: id, ExternalSessionID: legacy}
	}
	if explicit != nil && (explicit.ConnectionID <= 0 || strings.TrimSpace(explicit.ExternalSessionID) == "") {
		return Caller{}, errors.New("callerSession requires connectionId and externalSessionId")
	}
	if meta != nil && explicit != nil && *meta != *explicit {
		return Caller{}, errors.New("callerSession conflicts with OpenCode request metadata; remove the conflicting caller argument")
	}
	switch {
	case meta != nil:
		c.Session = meta
		c.ThreadID = ""
	case explicit != nil:
		c.Session = explicit
		c.ThreadID = legacy
	case c.Session == nil && c.ThreadID != "":
		id, err := t3()
		if err != nil {
			return Caller{}, err
		}
		c.Session = &db.SessionRef{ConnectionID: id, ExternalSessionID: c.ThreadID}
	}
	if s := strings.TrimSpace(cwd); s != "" {
		c.Cwd = s
	}
	return c, nil
}

// ErrNoProject: no project could be resolved.
var ErrNoProject = errors.New("no project")

// ResolveProject picks the project: explicit (id or exact title,
// case-insensitive, unambiguous) > the caller session's project. Paths are
// not evidence of shared filesystem identity. Source: explicit | session.
func ResolveProject(ctx context.Context, c *client.Client, caller Caller, explicit string) (db.Project, string, error) {
	if s := strings.TrimSpace(explicit); s != "" {
		projects, err := c.Projects(ctx)
		if err != nil {
			return db.Project{}, "", err
		}
		if p, ok := matchProject(projects, s); ok {
			return p, "explicit", nil
		}
		return db.Project{}, "", fmt.Errorf("%w %q; projects: %s", ErrNoProject, s, projectNames(projects))
	}
	if caller.Session != nil {
		d, err := c.SessionContext(ctx, *caller.Session)
		if err != nil {
			return db.Project{}, "", fmt.Errorf("resolve caller project or pass project explicitly: %w", err)
		}
		if d.Project != nil {
			return *d.Project, "session", nil
		}
	}
	return db.Project{}, "", fmt.Errorf("%w for this caller; pass project (id or unambiguous title)", ErrNoProject)
}

func matchProject(projects []db.Project, s string) (db.Project, bool) {
	if id, err := strconv.ParseInt(s, 10, 64); err == nil {
		for _, p := range projects {
			if p.ID == id {
				return p, true
			}
		}
	}
	var match db.Project
	found := false
	for _, p := range projects {
		if strings.EqualFold(p.Title, s) {
			if found {
				return db.Project{}, false
			}
			match = p
			found = true
		}
	}
	return match, found
}

func projectNames(projects []db.Project) string {
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		names = append(names, fmt.Sprintf("%d %s", p.ID, p.Title))
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}
