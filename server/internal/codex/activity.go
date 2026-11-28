package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var _ execution.ActivityReader = (*Adapter)(nil)

type threadSettings struct {
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	Sandbox        string `json:"sandbox,omitempty"`
}

func (r threadResponse) settings() threadSettings {
	s := threadSettings{Sandbox: r.Sandbox.Type}
	if json.Unmarshal(r.ApprovalPolicy, &s.ApprovalPolicy) != nil {
		var policy struct {
			Granular json.RawMessage `json:"granular"`
		}
		if json.Unmarshal(r.ApprovalPolicy, &policy) == nil && policy.Granular != nil {
			s.ApprovalPolicy = "granular"
		}
	}
	return s
}

type activityEntry struct {
	Item struct {
		item
		Summary  []string     `json:"summary"`
		Status   string       `json:"status"`
		Command  string       `json:"command"`
		ExitCode *int         `json:"exitCode"`
		Changes  []fileChange `json:"changes"`
		Server   string       `json:"server"`
		Tool     string       `json:"tool"`
	} `json:"item"`
	StartedAt   int64 `json:"startedAtMs"`
	CompletedAt int64 `json:"completedAtMs"`
}

type itemsPage struct {
	Data       []activityEntry `json:"data"`
	NextCursor string          `json:"nextCursor"`
}

func (a *Adapter) Activity(ctx context.Context, ref execution.SessionRef, q execution.ActivityQuery) (execution.Activity, error) {
	if !a.valid(ref) {
		return execution.Activity{}, execution.ErrNotFound
	}
	t, err := a.read(ctx, ref.ExternalSessionID)
	if err != nil {
		return execution.Activity{}, err
	}
	q = q.Bounded()
	var page itemsPage
	err = a.client.call(ctx, "thread/items/list", map[string]any{
		"threadId": t.ID, "limit": q.Limit, "sortDirection": "desc",
	}, &page)
	switch {
	case notLoaded(err):
		return execution.Activity{}, execution.ErrNotFound
	case err != nil:
		return execution.Activity{}, a.client.deliveryError(err)
	}
	out := execution.Activity{At: a.now().UTC(), Truncated: page.NextCursor != ""}
	if len(page.Data) > q.Limit {
		page.Data, out.Truncated = page.Data[:q.Limit], true
	}
	if t.Model != nil {
		out.Model = *t.Model
	}
	_, settings, held := a.heldThread(t.ID)
	if !held && (t.Status.Type == statusIdle || t.Status.Type == statusActive) {
		// Resume provides effective permission settings for loaded threads.
		// History remains readable even when a thread cannot be resumed.
		call, cancel := context.WithTimeout(ctx, 2*time.Second)
		if s, err := a.resume(call, t.ID); err == nil {
			settings = s.settings
			s.close(call)
		}
		cancel()
	}
	out.Details, _ = json.Marshal(struct {
		Model           string `json:"model,omitempty"`
		ReasoningEffort string `json:"reasoningEffort,omitempty"`
		threadSettings
	}{out.Model, t.ReasoningEffort, settings})

	files := make(map[string]execution.FileChange)
	for i := len(page.Data) - 1; i >= 0; i-- {
		entry := page.Data[i]
		mapped := entry.activityItem()
		if mapped.Kind == "" || mapped.ID == "" {
			continue
		}
		out.Items = append(out.Items, mapped)
		switch mapped.Kind {
		case "plan":
			out.Plan = planSteps(entry.Item.Text)
		case "file_change":
			if mapped.Status == "done" {
				collectChanges(files, entry.Item.Changes)
			}
		}
	}
	paths := slices.Sorted(maps.Keys(files))
	if len(paths) > execution.MaxActivityLimit {
		paths, out.Truncated = paths[:execution.MaxActivityLimit], true
	}
	for _, path := range paths {
		out.Files = append(out.Files, files[path])
	}
	return out.Bounded(q)
}

func (e activityEntry) activityItem() execution.ActivityItem {
	it := e.Item
	out := execution.ActivityItem{ID: it.ID, Status: itemStatus(it.Status)}
	if stamp := max(e.StartedAt, e.CompletedAt); stamp > 0 {
		out.At = time.UnixMilli(stamp).UTC()
	}
	switch it.Type {
	case itemUserMessage:
		out.Kind, out.Title, out.Text = "message_user", "User message", userText(it.item)
	case itemAgentMessage:
		out.Kind, out.Title, out.Text = "message_agent", "Codex message", it.Text
	case "reasoning":
		out.Kind, out.Title = "reasoning", "Reasoning"
		for _, summary := range it.Summary {
			if summary = strings.TrimSpace(summary); summary != "" {
				out.Title = firstLine(summary)
				break
			}
		}
	case "commandExecution":
		out.Kind, out.Title, out.Preview = "command", "Command", activitySnippet(it.Command)
		if out.Status != "" {
			out.Title += " · " + out.Status
		}
		if it.ExitCode != nil {
			out.Title += fmt.Sprintf(" · exit %d", *it.ExitCode)
		}
	case "fileChange":
		out.Kind, out.Title = "file_change", "File change"
		out.Preview = activitySnippet(filePreview(it.Changes))
	case "mcpToolCall":
		out.Kind, out.Title = "tool", "MCP tool"
		out.Preview = activitySnippet(strings.Trim(it.Server+"/"+it.Tool, "/"))
	case "plan":
		out.Kind, out.Title = "plan", "Plan"
	}
	return out
}

func itemStatus(status string) string {
	switch status {
	case "inProgress":
		return "running"
	case "completed":
		return "done"
	case "failed", "declined":
		return "failed"
	default:
		return ""
	}
}

func collectChanges(files map[string]execution.FileChange, changes []fileChange) {
	for _, change := range changes {
		path, status := change.Path, ""
		switch change.Kind.Type {
		case "add":
			status = "added"
		case "delete":
			status = "deleted"
		case "update":
			status = "modified"
			if change.Kind.MovePath != "" {
				path = change.Kind.MovePath
			}
		}
		if path == "" {
			continue
		}
		f := files[path]
		if path != change.Path {
			previous := files[change.Path]
			f.Additions += previous.Additions
			f.Deletions += previous.Deletions
			f.CountsUnknown = f.CountsUnknown || previous.CountsUnknown
			delete(files, change.Path)
		}
		f.Path, f.Status = path, status
		f.CountsUnknown = f.CountsUnknown || change.Diff == nil
		added, removed := change.lineCounts()
		f.Additions += added
		f.Deletions += removed
		files[path] = f
	}
}

// Codex persists a plan as Markdown text. Preserve checklist states when
// present and show the proposed plan text when there is no checklist.
func planSteps(markdown string) []execution.PlanStep {
	var steps []execution.PlanStep
	for line := range strings.SplitSeq(markdown, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 7 || !strings.ContainsRune("-*+", rune(line[0])) || line[1:3] != " [" || line[4:6] != "] " {
			continue
		}
		status := "pending"
		switch line[3] {
		case 'x', 'X':
			status = "done"
		case ' ':
		default:
			continue
		}
		if text := strings.TrimSpace(line[6:]); text != "" {
			steps = append(steps, execution.PlanStep{Text: activitySnippet(text), Status: status})
		}
		if len(steps) == execution.MaxActivityLimit {
			break
		}
	}
	if len(steps) == 0 && strings.TrimSpace(markdown) != "" {
		steps = append(steps, execution.PlanStep{Text: activitySnippet(strings.TrimSpace(markdown)), Status: "pending"})
	}
	return steps
}

func activitySnippet(s string) string {
	const limit = 2000
	if len(s) <= limit {
		return s
	}
	for i := range s {
		if i >= limit {
			return s[:i] + "…"
		}
	}
	return s
}
