package codex

import (
	"context"
	"errors"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var _ execution.SessionSearcher = (*Adapter)(nil)

const searchUnsupported = "This Codex app-server cannot list threads. Enter a thread ID or a Codex resume command."

// SearchSessions asks the app-server for saved, unarchived threads. A
// thread's preview contains prompt text, so only its name is exposed.
func (a *Adapter) SearchSessions(ctx context.Context, q execution.SessionSearchQuery) (execution.SessionSearchPage, error) {
	var out execution.SessionSearchPage
	if err := q.Valid(); err != nil {
		return out, err
	}
	params := map[string]any{
		"limit": q.Limit, "archived": false, "sortKey": "created_at", "sortDirection": "desc",
		"sourceKinds": []string{"cli", "vscode", "exec", "appServer", "unknown"},
	}
	if q.Cursor != "" {
		params["cursor"] = q.Cursor
	}
	var page threadsPage
	if err := a.client.call(ctx, "thread/list", params, &page); err != nil {
		var rpc *RPCError
		switch {
		case unsupported(err):
			return out, &execution.SessionSearchUnsupported{Reason: searchUnsupported}
		case q.Cursor != "" && errors.As(err, &rpc) &&
			(rpc.Code == codeInvalidRequest || rpc.Code == codeInvalidParams) &&
			strings.Contains(strings.ToLower(rpc.Message), "cursor"):
			return out, execution.ErrSessionSearchCursor
		default:
			return out, err
		}
	}
	if page.Data == nil || len(page.Data) > q.Limit {
		return out, errors.New("invalid Codex thread list page")
	}
	if page.NextCursor != nil && *page.NextCursor != "" {
		if *page.NextCursor == q.Cursor || len(*page.NextCursor) > execution.SessionSearchMaxCursor {
			return out, execution.ErrSessionSearchCursor
		}
		out.NextCursor = *page.NextCursor
	}
	out.Sessions = make([]execution.ListedSession, 0, len(page.Data))
	match := strings.ToLower(strings.TrimSpace(q.Query))
	for _, th := range page.Data {
		if !threadID.MatchString(th.ID) || th.CreatedAt <= 0 {
			return execution.SessionSearchPage{}, errors.New("invalid Codex thread list entry")
		}
		name := ""
		if th.Name != nil {
			name = strings.TrimSpace(*th.Name)
		}
		if match != "" && !strings.Contains(strings.ToLower(name), match) &&
			!strings.Contains(strings.ToLower(th.Cwd), match) && !strings.Contains(th.ID, match) {
			continue
		}
		item := execution.ListedSession{
			Session: a.ref(th.ID), Title: name, Workspace: th.Cwd, StartedAt: unixPtr(&th.CreatedAt),
			Origin: th.Originator, Eligibility: available,
		}
		switch {
		case th.ParentThreadID != nil && *th.ParentThreadID != "":
			item.Eligibility = execution.Availability{Reason: "Track the parent thread in Codex."}
		case th.Ephemeral:
			item.Eligibility = execution.Availability{Reason: "Codex has not saved this thread."}
		case th.Status.Type == statusSystemError:
			item.Eligibility = execution.Availability{Reason: "Codex reports a system error for this thread. Check it in Codex."}
		case th.Cwd == "":
			item.Eligibility = execution.Availability{Reason: "Codex did not list a working directory for this thread. Enter its ID to check it directly."}
		}
		out.Sessions = append(out.Sessions, item)
	}
	return out, nil
}
