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

// The generic caller channels every harness can use besides an explicit
// callerSession argument: CallerEnv for the CLI (one process, one session)
// and CallerHeader on HTTP MCP requests. Both take ParseSession's form.
const (
	CallerEnv    = "STAGE_CALLER_SESSION"
	CallerHeader = "Stage-Caller-Session"
)

// Caller sources, strongest first: the per-call transport claim (harness
// request metadata or CallerHeader), an explicit callerSession argument or
// CLI flags, then CallerEnv. A harness's own environment variable is the
// weakest (agenttools derives it when nothing else names the caller).
const (
	SourceRequest  = "request"
	SourceArgument = "argument"
	SourceEnv      = CallerEnv
)

// ParseSession reads "<connectionId>:<externalSessionId>".
func ParseSession(v string) (db.SessionRef, error) {
	id, session, ok := strings.Cut(strings.TrimSpace(v), ":")
	n, err := strconv.ParseInt(id, 10, 64)
	if !ok || err != nil || n <= 0 || strings.TrimSpace(session) == "" {
		return db.SessionRef{}, fmt.Errorf("want <connectionId>:<externalSessionId>, got %q", v)
	}
	return db.SessionRef{ConnectionID: n, ExternalSessionID: strings.TrimSpace(session)}, nil
}

// Caller is the calling agent's context; empty fields are unknown.
type Caller struct {
	Session *db.SessionRef `json:"session"`
	Cwd     string         `json:"cwd"`
	// Source names where Session came from: SourceRequest, SourceArgument,
	// SourceEnv, or a harness's environment variable (agenttools); "" when
	// unknown.
	Source string `json:"-"`
	// invalid is a malformed CallerEnv, reported by Resolve when nothing
	// else names the caller.
	invalid error
}

// FromEnv reads the CLI process's context: CallerEnv and the working
// directory.
func FromEnv() Caller { return fromEnv(os.Getenv) }

func fromEnv(getenv func(string) string) Caller {
	var c Caller
	if v := getenv(CallerEnv); strings.TrimSpace(v) != "" {
		ref, err := ParseSession(v)
		if err != nil {
			c.invalid = fmt.Errorf("%s: %w", CallerEnv, err)
		} else {
			c.Session = &ref
			c.Source = SourceEnv
		}
	}
	if wd, err := os.Getwd(); err == nil {
		c.Cwd = wd
	}
	return c
}

// Resolve combines an explicit caller with a per-call transport claim
// (harness request metadata or CallerHeader). An explicit caller overrides
// the process environment; it must agree with a transport claim.
func (c Caller) Resolve(explicit *db.SessionRef, cwd string, claim *db.SessionRef) (Caller, error) {
	if explicit != nil && (explicit.ConnectionID <= 0 || strings.TrimSpace(explicit.ExternalSessionID) == "") {
		return Caller{}, errors.New("callerSession requires connectionId and externalSessionId")
	}
	if claim != nil && explicit != nil && *claim != *explicit {
		return Caller{}, errors.New("callerSession conflicts with the request's session metadata; remove the conflicting caller argument")
	}
	switch {
	case claim != nil:
		c.Session, c.Source = claim, SourceRequest
	case explicit != nil:
		c.Session, c.Source = explicit, SourceArgument
	case c.invalid != nil:
		return Caller{}, c.invalid
	}
	c.invalid = nil
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
