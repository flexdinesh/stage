package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var _ execution.ActivityReader = (*Adapter)(nil)

// Activity reads a session transcript on this connection's machine. The
// project lookup also works for past sessions absent from the roster.
func (a *Adapter) Activity(ctx context.Context, ref execution.SessionRef, q execution.ActivityQuery) (execution.Activity, error) {
	id := ref.ExternalSessionID
	if ref.ConnectionID != a.conn || !validUUID(id) {
		return execution.Activity{}, execution.ErrNotFound
	}
	root := a.projects(ctx)
	if err := ctx.Err(); err != nil {
		return execution.Activity{}, err
	}
	if root == "" {
		return execution.Activity{}, fmt.Errorf("%w: Claude Code did not report a transcript directory", execution.ErrUnavailable)
	}
	entries, rosterErr := a.roster.list(ctx, false)
	entry, listed := findSession(entries, id)
	if err := ctx.Err(); err != nil {
		return execution.Activity{}, err
	}
	at := a.now().UTC()
	path, ok := transcriptPath(root, entry.Cwd, id)
	if !ok {
		switch {
		case rosterErr != nil:
			return execution.Activity{}, rosterErr
		case !listed:
			return execution.Activity{}, execution.ErrNotFound
		default:
			return (execution.Activity{At: at}).Bounded(q)
		}
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return execution.Activity{}, execution.ErrNotFound
	}
	if err != nil {
		return execution.Activity{}, fmt.Errorf("open Claude Code transcript: %w", err)
	}
	defer f.Close()
	return transcriptActivity(ctx, f, q, at)
}

type activityRecord struct {
	record
	PermissionMode string `json:"permissionMode"`
	Effort         string `json:"effort"`
	PerTurnEffort  string `json:"perTurnEffort"`
	Worktree       *struct {
		Path string `json:"worktreePath"`
	} `json:"worktreeSession"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type activityBlock struct {
	block
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

type activityDetails struct {
	PermissionMode string `json:"permissionMode,omitempty"`
	Effort         string `json:"effort,omitempty"`
	Worktree       string `json:"worktree,omitempty"`
}

type activityFeed struct {
	out           execution.Activity
	details       activityDetails
	worktreeState bool
	items         []execution.ActivityItem
	next          int
	files         map[string]execution.FileChange
	summaryTools  map[string]string
}

func transcriptActivity(ctx context.Context, r io.Reader, q execution.ActivityQuery, at time.Time) (execution.Activity, error) {
	limit := q.Bounded().Limit
	f := activityFeed{
		out:          execution.Activity{At: at},
		items:        make([]execution.ActivityItem, 0, limit),
		files:        make(map[string]execution.FileChange),
		summaryTools: make(map[string]string),
	}
	var line int
	err := eachRecord(r, func(rec activityRecord) bool {
		if ctx.Err() != nil {
			return false
		}
		line++
		f.record(rec, line)
		return true
	})
	if err != nil {
		return execution.Activity{}, fmt.Errorf("read Claude Code transcript: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return execution.Activity{}, err
	}
	// next points to the oldest item once the ring fills.
	f.out.Items = append(f.out.Items, f.items[f.next:]...)
	f.out.Items = append(f.out.Items, f.items[:f.next]...)
	for _, file := range f.files {
		f.out.Files = append(f.out.Files, file)
	}
	slices.SortFunc(f.out.Files, func(a, b execution.FileChange) int { return strings.Compare(a.Path, b.Path) })
	if f.details != (activityDetails{}) {
		f.out.Details, err = json.Marshal(f.details)
		if err != nil {
			return execution.Activity{}, err
		}
	}
	return f.out.Bounded(q)
}

func (f *activityFeed) add(item execution.ActivityItem) {
	item.Text, _ = truncate(item.Text, 2000)
	item.Preview, _ = truncate(item.Preview, 4096)
	item.Title, _ = truncate(item.Title, 200)
	if len(f.items) < cap(f.items) {
		f.items = append(f.items, item)
		return
	}
	f.out.Truncated = true
	f.items[f.next] = item
	f.next = (f.next + 1) % len(f.items)
}

func (f *activityFeed) record(rec activityRecord, line int) {
	if rec.IsSidechain {
		return
	}
	if rec.Type == "permission-mode" && rec.PermissionMode != "" {
		f.details.PermissionMode = rec.PermissionMode
	}
	if rec.Type == "worktree-state" {
		f.worktreeState = true
		f.details.Worktree = ""
		if rec.Worktree != nil {
			f.details.Worktree = rec.Worktree.Path
		}
	}
	if rec.IsMeta || rec.Message == nil {
		return
	}

	id := rec.UUID
	if id == "" {
		id = "line:" + strconv.Itoa(line)
	}
	at := parseTime(rec.Timestamp)
	var blocks []activityBlock
	switch rec.Type {
	case "user":
		if rec.PermissionMode != "" {
			f.details.PermissionMode = rec.PermissionMode
		}
		f.updateWorktree(rec.Cwd)
		if text, ok := promptText(rec.Message.Content); ok {
			if strings.TrimSpace(text) != "" && (rec.Origin == nil || rec.Origin.Kind == originHuman) {
				f.add(execution.ActivityItem{ID: id, At: at, Kind: "message_user", Text: text})
			}
		} else if json.Unmarshal(rec.Message.Content, &blocks) == nil {
			f.toolResults(blocks, rec.ToolUseResult)
		}
	case "assistant":
		model := rec.Message.Model
		if strings.HasPrefix(model, "<") {
			return
		}
		f.updateWorktree(rec.Cwd)
		if model != "" {
			f.out.Model, _ = truncate(model, 200)
		}
		switch {
		case rec.PerTurnEffort != "":
			f.details.Effort = rec.PerTurnEffort
		case rec.Effort != "":
			f.details.Effort = rec.Effort
		}
		if json.Unmarshal(rec.Message.Content, &blocks) == nil {
			for i, b := range blocks {
				itemID := id + ":" + strconv.Itoa(i)
				f.assistantBlock(b, execution.ActivityItem{ID: itemID, At: at})
			}
		}
	}
}

func (f *activityFeed) updateWorktree(cwd string) {
	if f.worktreeState || cwd == "" {
		return
	}
	f.details.Worktree = ""
	if root, rest, ok := strings.Cut(filepath.ToSlash(cwd), worktreeMarker); ok && rest != "" {
		name, _, _ := strings.Cut(rest, "/")
		f.details.Worktree = filepath.FromSlash(root + worktreeMarker + name)
	}
}

func (f *activityFeed) assistantBlock(b activityBlock, item execution.ActivityItem) {
	switch b.Type {
	case "text":
		if strings.TrimSpace(b.Text) == "" {
			return
		}
		item.Kind, item.Text = "message_agent", b.Text
	case "thinking", "redacted_thinking":
		item.Kind, item.Title = "reasoning", "Thinking"
	case "tool_use":
		if b.ID != "" {
			item.ID = b.ID
			switch b.Name {
			case "Edit", "Write", "TodoWrite":
				f.summaryTools[b.ID] = b.Name
			}
		}
		item.Kind, item.Title, item.Status = "tool", b.Name, "running"
		switch b.Name {
		case "Bash":
			var input struct {
				Command string `json:"command"`
			}
			_ = json.Unmarshal(b.Input, &input)
			item.Kind, item.Preview = "command", input.Command
		case "Edit", "Write", "MultiEdit", "NotebookEdit":
			var input struct {
				FilePath     string `json:"file_path"`
				NotebookPath string `json:"notebook_path"`
			}
			_ = json.Unmarshal(b.Input, &input)
			item.Kind, item.Preview = "file_change", input.FilePath
			if b.Name == "NotebookEdit" {
				item.Preview = input.NotebookPath
			}
		case "TodoWrite":
			item.Kind = "plan"
		default:
			var compact bytes.Buffer
			if json.Compact(&compact, b.Input) == nil {
				item.Preview = compact.String()
			}
		}
	default:
		return
	}
	f.add(item)
}

type activityResult struct {
	Interrupted bool   `json:"interrupted"`
	Type        string `json:"type"`
	FilePath    string `json:"filePath"`
	Content     string `json:"content"`
	Patch       []struct {
		Lines []string `json:"lines"`
	} `json:"structuredPatch"`
	Todos []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	} `json:"newTodos"`
}

func (f *activityFeed) toolResults(blocks []activityBlock, raw json.RawMessage) {
	// Claude's structured result is on the user record, with one tool_result
	// block. If the message has several, the blocks still carry their status.
	var result activityResult
	if len(blocks) == 1 && blocks[0].Type == "tool_result" && len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &result)
	}
	for _, b := range blocks {
		if b.Type != "tool_result" || b.ToolUseID == "" {
			continue
		}
		status := "done"
		if b.IsError || result.Interrupted {
			status = "failed"
		}
		for i := range f.items {
			if f.items[i].ID == b.ToolUseID {
				f.items[i].Status = status
			}
		}
		tool := f.summaryTools[b.ToolUseID]
		delete(f.summaryTools, b.ToolUseID)
		if status == "done" && tool != "" {
			f.summarize(tool, result)
		}
	}
}

// Edit and Write results identify the file themselves, and TodoWrite returns
// the updated list. Both remain available after their tool items leave the ring.
func (f *activityFeed) summarize(tool string, r activityResult) {
	if tool == "TodoWrite" && r.Todos != nil {
		f.out.Plan = make([]execution.PlanStep, 0, len(r.Todos))
		for _, todo := range r.Todos {
			status := todo.Status
			switch status {
			case "pending", "in_progress":
			case "completed":
				status = "done"
			default:
				continue
			}
			text, _ := truncate(todo.Content, 2000)
			if strings.TrimSpace(text) != "" {
				f.out.Plan = append(f.out.Plan, execution.PlanStep{Text: text, Status: status})
			}
		}
	}
	if tool == "TodoWrite" || r.FilePath == "" || (r.Type != "create" && r.Type != "update" && r.Patch == nil) {
		return
	}
	path, _ := truncate(r.FilePath, 4096)
	file := f.files[path]
	file.Path = path
	if r.Type == "create" {
		file.Status = "added"
		// Create results carry the new content and an empty structuredPatch.
		if len(r.Patch) == 0 && r.Content != "" {
			file.Additions += strings.Count(r.Content, "\n")
			if !strings.HasSuffix(r.Content, "\n") {
				file.Additions++
			}
		}
	} else if file.Status == "" {
		file.Status = "modified"
	}
	for _, hunk := range r.Patch {
		for _, line := range hunk.Lines {
			switch {
			case strings.HasPrefix(line, "+"):
				file.Additions++
			case strings.HasPrefix(line, "-"):
				file.Deletions++
			}
		}
	}
	f.files[path] = file
}
