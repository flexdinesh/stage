package t3

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var _ execution.SessionSearcher = (*Adapter)(nil)

const searchUnsupported = "This T3 server cannot list threads. Enter a thread link or ID."

// SearchSessions reads the environment's current roster, including settled
// threads. The listing has the project ID but no session checkout path.
func (a *Adapter) SearchSessions(ctx context.Context, q execution.SessionSearchQuery) (execution.SessionSearchPage, error) {
	var out execution.SessionSearchPage
	if err := q.Valid(); err != nil {
		return out, err
	}
	in := ListThreadsInput{AllProjects: true, IncludeSubagents: true, Limit: q.Limit}
	offset := 0
	if q.Cursor != "" {
		n, err := strconv.Atoi(q.Cursor)
		if err != nil || n <= 0 || strconv.Itoa(n) != q.Cursor {
			return out, execution.ErrSessionSearchCursor
		}
		offset = n
		in.Cursor = &offset
	}
	page, err := a.Client.ListThreads(ctx, in)
	if err != nil {
		var rpc *jsonrpc.Error
		if errors.As(err, &rpc) && (rpc.Code == jsonrpc.CodeMethodNotFound ||
			(rpc.Code == jsonrpc.CodeInvalidParams && strings.Contains(strings.ToLower(rpc.Message), "unknown tool"))) {
			return out, &execution.SessionSearchUnsupported{Reason: searchUnsupported}
		}
		return out, err
	}
	if len(page.Threads) > q.Limit {
		return out, errors.New("T3 thread list exceeded the requested limit")
	}
	if page.NextCursor != nil {
		if *page.NextCursor <= offset {
			return out, execution.ErrSessionSearchCursor
		}
		out.NextCursor = strconv.Itoa(*page.NextCursor)
	}
	out.Sessions = make([]execution.ListedSession, 0, len(page.Threads))
	if len(page.Threads) == 0 {
		return out, nil
	}
	projects, err := a.Client.ListProjects(ctx)
	if err != nil {
		return execution.SessionSearchPage{}, err
	}
	byID := make(map[string]Project, len(projects))
	for _, project := range projects {
		byID[project.ID] = project
	}
	match := strings.ToLower(strings.TrimSpace(q.Query))
	for _, th := range page.Threads {
		if strings.TrimSpace(th.ThreadID) == "" {
			return execution.SessionSearchPage{}, errors.New("T3 listed a thread without an ID")
		}
		p := byID[th.ProjectID]
		if match != "" && !strings.Contains(strings.ToLower(th.Title), match) &&
			!strings.Contains(strings.ToLower(th.ThreadID), match) &&
			!strings.Contains(strings.ToLower(th.ProjectID), match) &&
			!strings.Contains(strings.ToLower(p.Title), match) &&
			!strings.Contains(strings.ToLower(p.WorkspaceRoot), match) {
			continue
		}
		workspace := p.Title
		if workspace == "" {
			workspace = th.ProjectID
		}
		item := execution.ListedSession{
			Session: execution.SessionRef{ConnectionID: a.ConnectionID, ExternalSessionID: th.ThreadID},
			Title:   th.Title, Workspace: workspace,
			Eligibility: execution.Availability{Available: true},
		}
		if !th.CreatedAt.IsZero() {
			at := th.CreatedAt.UTC()
			item.StartedAt = &at
		}
		switch {
		case th.Archived:
			item.Eligibility = execution.Availability{Reason: "This T3 thread is archived."}
		case th.RelationshipToParent == "subagent":
			item.Eligibility = execution.Availability{Reason: "Track the parent thread in T3."}
		case th.ProjectID == "":
			item.Eligibility = execution.Availability{Reason: "T3 did not list a project for this thread. Enter its ID to check it directly."}
		}
		out.Sessions = append(out.Sessions, item)
	}
	return out, nil
}
