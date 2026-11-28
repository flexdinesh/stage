package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

const (
	DefaultActivityLimit = 30
	MaxActivityLimit     = 100
	maxActivityDetails   = 16 << 10
)

type ActivityQuery struct {
	Limit int `json:"limit"`
}

// Bounded applies the default and maximum feed size.
func (q ActivityQuery) Bounded() ActivityQuery {
	if q.Limit <= 0 {
		q.Limit = DefaultActivityLimit
	}
	q.Limit = min(q.Limit, MaxActivityLimit)
	return q
}

// Activity is a recent session view read from a harness. Items are the last
// requested items, oldest first. Plan, Files and Model describe the session
// now. Truncated means older items exist or Details exceeded the size limit.
// Details is harness-specific JSON for its own UI slot.
type Activity struct {
	Items     []ActivityItem  `json:"items"`
	Plan      []PlanStep      `json:"plan"`
	Files     []FileChange    `json:"files"`
	Model     string          `json:"model"`
	Truncated bool            `json:"truncated"`
	Details   json.RawMessage `json:"details,omitempty"`
	At        time.Time       `json:"at"`
}

type ActivityItem struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"` // message_user, message_agent, reasoning, tool, command, file_change, plan or event
	Title   string    `json:"title"`
	Text    string    `json:"text"`
	Status  string    `json:"status"`  // running, done, failed or empty
	Preview string    `json:"preview"` // the command, path or URL
}

type PlanStep struct {
	Text   string `json:"text"`
	Status string `json:"status"` // pending, in_progress or done
}

type FileChange struct {
	Path          string `json:"path"`
	Additions     int    `json:"additions"`
	Deletions     int    `json:"deletions"`
	Status        string `json:"status"` // added, modified, deleted or empty
	CountsUnknown bool   `json:"countsUnknown,omitempty"`
}

// ActivityReader reads the session's bounded recent activity (OpActivity).
type ActivityReader interface {
	Activity(context.Context, SessionRef, ActivityQuery) (Activity, error)
}

// Bounded validates adapter output and applies the Stage bounds without
// modifying the adapter's items. Workers use it before encoding the feed.
func (a Activity) Bounded(q ActivityQuery) (Activity, error) {
	for _, item := range a.Items {
		switch item.Kind {
		case "message_user", "message_agent", "reasoning", "tool", "command", "file_change", "plan", "event":
		default:
			return Activity{}, fmt.Errorf("invalid activity item kind %q", item.Kind)
		}
		switch item.Status {
		case "", "running", "done", "failed":
		default:
			return Activity{}, fmt.Errorf("invalid activity item status %q", item.Status)
		}
	}
	for _, step := range a.Plan {
		switch step.Status {
		case "pending", "in_progress", "done":
		default:
			return Activity{}, fmt.Errorf("invalid activity plan status %q", step.Status)
		}
	}
	for _, file := range a.Files {
		switch file.Status {
		case "", "added", "modified", "deleted":
		default:
			return Activity{}, fmt.Errorf("invalid activity file status %q", file.Status)
		}
	}
	if limit := q.Bounded().Limit; len(a.Items) > limit {
		a.Items, a.Truncated = a.Items[len(a.Items)-limit:], true
	}
	a.Items = slices.Clone(a.Items)
	for i := range a.Items {
		a.Items[i].Text = activityPrefix(a.Items[i].Text, 2000)
		a.Items[i].Title = activityPrefix(a.Items[i].Title, 200)
	}
	switch {
	case len(a.Details) > maxActivityDetails:
		a.Details, a.Truncated = nil, true
	case len(a.Details) > 0 && !json.Valid(a.Details):
		return Activity{}, fmt.Errorf("invalid activity details JSON")
	}
	if a.Items == nil {
		a.Items = []ActivityItem{}
	}
	if a.Plan == nil {
		a.Plan = []PlanStep{}
	}
	if a.Files == nil {
		a.Files = []FileChange{}
	}
	return a, nil
}

func activityPrefix(s string, limit int) string {
	for i := range s {
		if limit == 0 {
			return s[:i]
		}
		limit--
	}
	return s
}
