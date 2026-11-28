package t3

import (
	"cmp"
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var _ execution.ActivityReader = (*Adapter)(nil)

type activityDetails struct {
	ProviderInstance string          `json:"providerInstance"`
	Model            string          `json:"model"`
	RuntimeMode      RuntimeMode     `json:"runtimeMode"`
	InteractionMode  InteractionMode `json:"interactionMode"`
	Branch           string          `json:"branch"`
	Worktree         string          `json:"worktree"`
}

func (a *Adapter) Activity(ctx context.Context, ref execution.SessionRef, q execution.ActivityQuery) (execution.Activity, error) {
	if ref.ConnectionID != a.ConnectionID {
		return execution.Activity{}, execution.ErrUnavailable
	}
	page, err := a.Client.ReadItems(ctx, ReadItemsInput{
		ThreadID: ref.ExternalSessionID, Activity: true, Limit: q.Bounded().Limit, MaxChars: 2000,
	})
	if err != nil {
		return execution.Activity{}, err
	}
	d := page.Thread
	details, _ := json.Marshal(activityDetails{
		ProviderInstance: d.ProviderInstanceID, Model: d.Model, RuntimeMode: d.RuntimeMode,
		InteractionMode: d.InteractionMode, Branch: d.Branch, Worktree: d.WorktreePath,
	})
	out := execution.Activity{Model: d.Model, At: d.UpdatedAt, Details: details, Truncated: page.HasMore}
	slices.SortStableFunc(page.Items, func(a, b TimelineItem) int { return cmp.Compare(a.Position, b.Position) })
	// A newest-first T3 query can have an earlier position but no forward page.
	if len(page.Items) > 0 && page.Items[0].Position > 0 {
		out.Truncated = true
	}
	files := make(map[string]int)
	for _, item := range page.Items {
		if item.Visibility != "local" {
			continue
		}
		event := execution.ActivityItem{
			ID: item.ItemID, At: item.UpdatedAt, Kind: "tool", Title: item.Title,
			Text: item.Text, Status: activityStatus(item.Status),
		}
		switch item.Type {
		case "user_message":
			event.Kind = "message_user"
		case "assistant_message":
			event.Kind = "message_agent"
		case "reasoning":
			event.Kind = "reasoning"
		case "command_execution":
			event.Kind = "command"
			event.Preview, _, _ = strings.Cut(strings.TrimPrefix(item.Text, "$ "), "\n")
			event.Text = ""
		case "file_change":
			event.Kind = "file_change"
			file := activityFile(item.Text)
			event.Preview = file.Path
			event.Text = ""
			if file.Path != "" {
				if index, ok := files[file.Path]; ok {
					aggregate := &out.Files[index]
					if aggregate.CountsUnknown || file.CountsUnknown {
						aggregate.CountsUnknown = true
						aggregate.Additions, aggregate.Deletions = 0, 0
					} else {
						aggregate.Additions += file.Additions
						aggregate.Deletions += file.Deletions
					}
				} else {
					files[file.Path] = len(out.Files)
					out.Files = append(out.Files, file)
				}
			}
		case "proposed_plan":
			event.Kind = "plan"
		case "todo_list":
			event.Kind = "plan"
			out.Plan = activityPlan(item.Text)
		default:
			if event.Title == "" {
				event.Title = strings.ReplaceAll(item.Type, "_", " ")
			}
		}
		out.Items = append(out.Items, event)
	}
	return out.Bounded(q)
}

func activityStatus(status string) string {
	switch status {
	case "pending", "running", "waiting":
		return "running"
	case "completed":
		return "done"
	case "failed", "cancelled", "interrupted":
		return "failed"
	default:
		return ""
	}
}

var fileCounts = regexp.MustCompile(`^\+([0-9]+) -([0-9]+)$`)

// T3 flattens a file change to a path, an optional "+N -N" line and a diff.
func activityFile(text string) execution.FileChange {
	path, rest, _ := strings.Cut(text, "\n")
	counts, _, _ := strings.Cut(rest, "\n")
	file := execution.FileChange{Path: path, CountsUnknown: true}
	if match := fileCounts.FindStringSubmatch(counts); match != nil {
		additions, addErr := strconv.Atoi(match[1])
		deletions, delErr := strconv.Atoi(match[2])
		if addErr == nil && delErr == nil {
			file.Additions, file.Deletions, file.CountsUnknown = additions, deletions, false
		}
	}
	return file
}

// T3 projects todo steps as "[status] text" lines after its explanation.
func activityPlan(text string) []execution.PlanStep {
	var plan []execution.PlanStep
	for line := range strings.SplitSeq(text, "\n") {
		if !strings.HasPrefix(line, "[") {
			continue
		}
		status, step, ok := strings.Cut(line[1:], "] ")
		if !ok || step == "" {
			continue
		}
		switch status {
		case "pending", "in_progress":
		case "completed", "done":
			status = "done"
		default:
			continue
		}
		plan = append(plan, execution.PlanStep{Text: step, Status: status})
	}
	return plan
}
