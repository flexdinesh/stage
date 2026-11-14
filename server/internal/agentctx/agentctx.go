// Package agentctx resolves the calling agent's context: its T3 session
// (T3CODE_THREAD_ID, set by T3 in every provider process) and working
// directory, and from those the t3-stage project it works in.
package agentctx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/flexdinesh/t3-stage/server/internal/client"
	"github.com/flexdinesh/t3-stage/server/internal/db"
)

// ThreadEnv is the env var T3 sets to the provider session's thread id.
const ThreadEnv = "T3CODE_THREAD_ID"

// Caller is the calling agent's context; empty fields are unknown.
type Caller struct {
	ThreadID string `json:"threadId"`
	Cwd      string `json:"cwd"`
}

// FromEnv reads the caller from the process environment (CLI, stdio MCP).
func FromEnv() Caller {
	c := Caller{ThreadID: strings.TrimSpace(os.Getenv(ThreadEnv))}
	if wd, err := os.Getwd(); err == nil {
		c.Cwd = wd
	}
	return c
}

// Merge returns c with non-empty override fields applied (tool arguments win
// over the environment).
func (c Caller) Merge(threadID, cwd string) Caller {
	if s := strings.TrimSpace(threadID); s != "" {
		c.ThreadID = s
	}
	if s := strings.TrimSpace(cwd); s != "" {
		c.Cwd = s
	}
	return c
}

// ThreadPtr is ThreadID or nil when unknown.
func (c Caller) ThreadPtr() *string {
	if c.ThreadID == "" {
		return nil
	}
	id := c.ThreadID
	return &id
}

// ErrNoProject: no project could be resolved.
var ErrNoProject = errors.New("no project")

// ResolveProject picks the project: explicit (id or exact title,
// case-insensitive) > the caller session's project > the project whose
// workspaceRoot is the longest prefix of the caller's cwd. Source names how
// it was resolved: explicit | session | cwd.
func ResolveProject(ctx context.Context, c *client.Client, caller Caller, explicit string) (db.Project, string, error) {
	projects, err := c.Projects(ctx)
	if err != nil {
		return db.Project{}, "", err
	}
	if s := strings.TrimSpace(explicit); s != "" {
		if p, ok := matchProject(projects, s); ok {
			return p, "explicit", nil
		}
		return db.Project{}, "", fmt.Errorf("%w %q; projects: %s", ErrNoProject, s, projectNames(projects))
	}
	if caller.ThreadID != "" {
		d, err := c.Thread(ctx, caller.ThreadID)
		switch {
		case err == nil && d.Thread.ProjectID != nil:
			for _, p := range projects {
				if p.ID == *d.Thread.ProjectID {
					return p, "session", nil
				}
			}
		case err != nil && !client.IsStatus(err, 404) && !client.IsStatus(err, 503) && !client.IsStatus(err, 502):
			return db.Project{}, "", err
		}
	}
	if p, ok := ProjectForPath(projects, caller.Cwd); ok {
		return p, "cwd", nil
	}
	return db.Project{}, "", fmt.Errorf("%w for this session or directory; pass a project (id or title): %s", ErrNoProject, projectNames(projects))
}

func matchProject(projects []db.Project, s string) (db.Project, bool) {
	if id, err := strconv.ParseInt(s, 10, 64); err == nil {
		for _, p := range projects {
			if p.ID == id {
				return p, true
			}
		}
	}
	for _, p := range projects {
		if strings.EqualFold(p.Title, s) {
			return p, true
		}
	}
	return db.Project{}, false
}

// ProjectForPath returns the project whose workspaceRoot is the longest
// path prefix of dir.
func ProjectForPath(projects []db.Project, dir string) (db.Project, bool) {
	if dir == "" {
		return db.Project{}, false
	}
	dir = filepath.Clean(dir)
	var best db.Project
	found := false
	for _, p := range projects {
		root := filepath.Clean(p.WorkspaceRoot)
		if !filepath.IsAbs(root) {
			continue
		}
		if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
			if !found || len(root) > len(filepath.Clean(best.WorkspaceRoot)) {
				best, found = p, true
			}
		}
	}
	return best, found
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
