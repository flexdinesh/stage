package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

const activityUnavailable = "Update OpenCode to see recent activity in Stage."

var _ execution.ActivityReader = (*Adapter)(nil)

type activityDetails struct {
	Agent   string         `json:"agent,omitempty"`
	Model   string         `json:"model,omitempty"`
	Variant string         `json:"variant,omitempty"`
	Cost    *float64       `json:"cost,omitempty"`
	Tokens  *sessionTokens `json:"tokens,omitempty"`
}

type fileDiff struct {
	File, Status         string
	Additions, Deletions int
}

// Activity reads recent message parts and the latest turn's file changes.
// The source message window stays bounded even when few messages have parts.
func (a *Adapter) Activity(ctx context.Context, ref execution.SessionRef, q execution.ActivityQuery) (execution.Activity, error) {
	var out execution.Activity
	if !a.valid(ref) {
		return out, execution.ErrNotFound
	}
	contract, err := a.checkContract(ctx)
	if err != nil {
		return out, err
	}
	if !contract.activity {
		return out, errors.New(activityUnavailable)
	}
	s, err := a.readSession(ctx, ref)
	if err != nil {
		return out, err
	}
	path := "/api/session/" + s.ID
	if err := a.readActivityMessages(ctx, path, q.Bounded().Limit, &out); err != nil {
		return out, err
	}
	var files envelope[[]fileDiff]
	if err := a.get(ctx, path+"/diff?context=0", &files); err != nil {
		return out, sessionReadErr(err)
	}
	if files.Data == nil {
		return out, errors.New("incomplete OpenCode session diff")
	}

	out.At = *millis(s.Time.Updated)
	details := activityDetails{Agent: s.Agent, Cost: s.Cost, Tokens: s.Tokens}
	if s.Model != nil {
		out.Model = s.Model.ProviderID + "/" + s.Model.ID
		details.Model, details.Variant = out.Model, s.Model.Variant
	}
	out.Details, err = json.Marshal(details)
	if err != nil {
		return execution.Activity{}, err
	}
	out.Files = aggregateDiffs(files.Data)
	return out.Bounded(q)
}

func (a *Adapter) readActivityMessages(ctx context.Context, path string, limit int, out *execution.Activity) error {
	query := fmt.Sprintf("%s/message?limit=%d", path, execution.MaxActivityLimit)
	next := query + "&order=desc"
	foundPlan := false
	// Metadata-only messages can fill a page. Read up to three pages when
	// needed to fill the feed, then mark any unread history as truncated.
	for range 3 {
		var page struct {
			Data   []message
			Cursor struct{ Next string }
		}
		if err := a.get(ctx, next, &page); err != nil {
			return sessionReadErr(err)
		}
		if page.Data == nil {
			return errors.New("incomplete OpenCode message page")
		}
		for _, m := range page.Data {
			if m.Type == "user" && strings.TrimSpace(m.Text) != "" && len(out.Items) <= limit {
				out.Items = append(out.Items, execution.ActivityItem{
					ID: m.ID, At: activityTime(m.Time.Created), Kind: "message_user", Title: "User message", Text: m.Text,
				})
			}
			if m.Type != "assistant" {
				continue
			}
			for i, part := range slices.Backward(m.Content) {
				if len(out.Items) <= limit {
					if item, ok := partActivity(m, i, part); ok {
						out.Items = append(out.Items, item)
					}
				}
				if !foundPlan {
					out.Plan, foundPlan = partPlan(part)
				}
			}
		}
		out.Truncated = page.Cursor.Next != ""
		if !out.Truncated || len(out.Items) > limit {
			break
		}
		next = query + "&cursor=" + url.QueryEscape(page.Cursor.Next)
	}
	slices.Reverse(out.Items)
	return nil
}

func activityTime(ms int64) time.Time {
	if at := millis(ms); at != nil {
		return *at
	}
	return time.Time{}
}

func partActivity(m message, i int, p messagePart) (execution.ActivityItem, bool) {
	ms := p.Time.Created
	if ms <= 0 {
		ms = m.Time.Created
	}
	item := execution.ActivityItem{ID: fmt.Sprintf("%s/%d", m.ID, i), At: activityTime(ms)}
	switch p.Type {
	case "text":
		item.Kind, item.Title, item.Text = "message_agent", "Assistant message", p.Text
		return item, strings.TrimSpace(p.Text) != ""
	case "reasoning":
		item.Kind, item.Title = "reasoning", "Reasoning"
	case "tool":
		item.Kind, item.Title = "tool", p.Name
		if p.Name == "shell" || p.Name == "bash" {
			item.Kind, item.Title = "command", "Run shell command"
		}
		var input map[string]json.RawMessage
		_ = json.Unmarshal(p.State.Input, &input)
		for _, key := range []string{"command", "filePath", "path", "url", "pattern", "query", "description"} {
			var preview string
			if json.Unmarshal(input[key], &preview) == nil && strings.TrimSpace(preview) != "" {
				for i := range preview {
					if i >= 2_000 {
						preview = preview[:i]
						break
					}
				}
				item.Preview = preview
				break
			}
		}
		switch p.State.Status {
		case "streaming", "running":
			item.Status = "running"
		case "completed":
			item.Status = "done"
		case "error":
			item.Status = "failed"
		}
	default:
		return execution.ActivityItem{}, false
	}
	return item, true
}

func partPlan(p messagePart) ([]execution.PlanStep, bool) {
	if p.Type != "tool" || (p.Name != "todo" && p.Name != "todowrite") || p.State.Status != "completed" {
		return nil, false
	}
	var input struct {
		Todos []struct{ Content, Status string }
	}
	if json.Unmarshal(p.State.Input, &input) != nil || input.Todos == nil {
		return nil, false
	}
	plan := make([]execution.PlanStep, 0, len(input.Todos))
	for _, todo := range input.Todos {
		status := todo.Status
		switch status {
		case "pending", "in_progress":
		case "completed":
			status = "done"
		default:
			continue
		}
		plan = append(plan, execution.PlanStep{Text: todo.Content, Status: status})
	}
	return plan, true
}

func aggregateDiffs(diffs []fileDiff) []execution.FileChange {
	index := make(map[string]int, len(diffs))
	files := make([]execution.FileChange, 0, len(diffs))
	for _, d := range diffs {
		i, ok := index[d.File]
		if !ok {
			i = len(files)
			index[d.File] = i
			f := execution.FileChange{Path: d.File}
			switch d.Status {
			case "added", "deleted", "modified":
				f.Status = d.Status
			}
			files = append(files, f)
		} else if d.Status != files[i].Status {
			files[i].Status = "modified"
		}
		files[i].Additions += d.Additions
		files[i].Deletions += d.Deletions
	}
	return files
}
