package t3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	projectPageLimit = 100
	maxPages         = 1000
	// readMaxChars keeps t3_thread_read small: we only need thread + runs.
	readMaxChars = 200
)

func (c *MCPClient) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	cursor := 0
	for range maxPages {
		var page wireProjectList
		args := map[string]any{"cursor": cursor, "limit": projectPageLimit}
		if err := c.call(ctx, "t3_project_list", args, &page); err != nil {
			return nil, err
		}
		for _, p := range page.Projects {
			out = append(out, p.toProject())
		}
		if page.NextCursor == nil {
			return out, nil
		}
		if *page.NextCursor <= cursor {
			return nil, fmt.Errorf("t3 t3_project_list: cursor did not advance (%d -> %d)", cursor, *page.NextCursor)
		}
		cursor = *page.NextCursor
	}
	return nil, fmt.Errorf("t3 t3_project_list: more than %d pages", maxPages)
}

func (c *MCPClient) CreateProject(ctx context.Context, in CreateProjectInput) (Project, error) {
	title, root := strings.TrimSpace(in.Title), strings.TrimSpace(in.WorkspaceRoot)
	if title == "" || root == "" {
		return Project{}, errors.New("t3 t3_project_create: title and workspaceRoot are required")
	}
	var p wireProject
	if err := c.call(ctx, "t3_project_create", map[string]any{"title": title, "workspaceRoot": root}, &p); err != nil {
		return Project{}, err
	}
	if p.CommitError != "" {
		c.log.Warn("t3 project created with commit error", "projectId", p.ID, "commitError", p.CommitError)
	}
	return p.toProject(), nil
}

func (c *MCPClient) ListThreads(ctx context.Context, in ListThreadsInput) (ThreadPage, error) {
	args := map[string]any{"includeSubagents": in.IncludeSubagents}
	if in.AllProjects {
		args["allProjects"] = true
	} else {
		if in.ProjectID == "" {
			return ThreadPage{}, errors.New("t3 t3_thread_list: projectId required unless allProjects")
		}
		args["projectId"] = in.ProjectID
	}
	if len(in.Statuses) > 0 {
		args["statuses"] = in.Statuses
	}
	if s := strings.TrimSpace(in.TitleContains); s != "" {
		args["titleContains"] = s
	}
	if in.Settled != nil {
		args["settled"] = *in.Settled
	}
	if in.Cursor != nil {
		args["cursor"] = *in.Cursor
	}
	if in.Limit > 0 {
		args["limit"] = min(in.Limit, 100)
	}
	var out wireThreadList
	if err := c.call(ctx, "t3_thread_list", args, &out); err != nil {
		return ThreadPage{}, err
	}
	page := out.toPage()
	for _, t := range page.Threads {
		if env := environmentFromLink(t.Link); env != "" {
			c.noteEnvironment(env)
			break
		}
	}
	return page, nil
}

func (c *MCPClient) ReadThread(ctx context.Context, threadID string) (ThreadDetail, error) {
	if threadID == "" {
		return ThreadDetail{}, ErrNotFound
	}
	args := map[string]any{"threadId": threadID, "limit": 1, "runLimit": 1, "maxCharsPerItem": readMaxChars}
	var out wireThreadRead
	if err := c.call(ctx, "t3_thread_read", args, &out); err != nil {
		return ThreadDetail{}, err
	}
	d := out.toDetail()
	c.noteEnvironment(environmentFromLink(d.Link))
	return d, nil
}

func (c *MCPClient) LaunchThread(ctx context.Context, in LaunchInput) (LaunchResult, error) {
	if in.ProjectID == "" || strings.TrimSpace(in.Title) == "" {
		return LaunchResult{}, errors.New("t3 t3_thread_launch: projectId and title are required")
	}
	args := map[string]any{"projectId": in.ProjectID, "title": strings.TrimSpace(in.Title)}
	if in.Message != "" {
		args["message"] = in.Message
	}
	if in.ModelSelection != nil {
		args["modelSelection"] = in.ModelSelection
	}
	if in.RuntimeMode != "" {
		args["runtimeMode"] = in.RuntimeMode
	}
	if in.InteractionMode != "" {
		args["interactionMode"] = in.InteractionMode
	}
	if in.Workspace != nil {
		ws, err := workspaceArg(*in.Workspace)
		if err != nil {
			return LaunchResult{}, err
		}
		args["workspaceStrategy"] = ws
	}
	if in.ClientRequestID != "" {
		args["clientRequestId"] = in.ClientRequestID
	}
	var out wireLaunch
	if err := c.call(ctx, "t3_thread_launch", args, &out); err != nil {
		return LaunchResult{}, err
	}
	r := out.toResult()
	c.noteEnvironment(environmentFromLink(r.Link))
	return r, nil
}

// workspaceArg encodes OrchestrationV2ThreadLaunchWorkspaceStrategy.
func workspaceArg(w WorkspaceStrategy) (map[string]any, error) {
	m := map[string]any{"type": string(w.Type)}
	if b := strings.TrimSpace(w.Branch); b != "" {
		m["branch"] = b
	}
	switch w.Type {
	case WorkspaceRoot:
	case WorkspaceExistingWorktree:
		p := strings.TrimSpace(w.WorktreePath)
		if p == "" {
			return nil, errors.New("t3 workspace existing_worktree: worktreePath required")
		}
		m["worktreePath"] = p
	case WorkspaceWorktree:
		ref := strings.TrimSpace(w.BaseRef)
		if ref == "" {
			return nil, errors.New("t3 workspace worktree: baseRef required")
		}
		m["baseRef"] = ref
		if w.StartFromOrigin != nil {
			m["startFromOrigin"] = *w.StartFromOrigin
		}
	default:
		return nil, fmt.Errorf("t3 workspace: unknown type %q", w.Type)
	}
	return m, nil
}

func (c *MCPClient) SendMessage(ctx context.Context, threadID, message string, mode SendMode) error {
	if threadID == "" {
		return ErrNotFound
	}
	if strings.TrimSpace(message) == "" {
		return errors.New("t3 t3_thread_send: message is required")
	}
	args := map[string]any{"threadId": threadID, "message": message}
	if mode != "" {
		args["mode"] = mode
	}
	return c.call(ctx, "t3_thread_send", args, nil)
}

func (c *MCPClient) Interrupt(ctx context.Context, threadID string) error {
	if threadID == "" {
		return ErrNotFound
	}
	return c.call(ctx, "t3_thread_interrupt", map[string]any{"threadId": threadID, "stop": true}, nil)
}

func (c *MCPClient) Capabilities(ctx context.Context) (Catalog, error) {
	var out wireCapabilities
	if err := c.call(ctx, "orchestrator_capabilities", map[string]any{}, &out); err != nil {
		return Catalog{}, err
	}
	return out.toCatalog(), nil
}

// CallRaw calls any T3 MCP tool and returns its result JSON undecoded
// (debugging and dev tools; not part of Client).
func (c *MCPClient) CallRaw(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	var out json.RawMessage
	if args == nil {
		args = map[string]any{}
	}
	if err := c.call(ctx, tool, args, &out); err != nil {
		return nil, err
	}
	return out, nil
}
