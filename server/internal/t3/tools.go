package t3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"unicode/utf16"
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

func (c *MCPClient) SearchThreads(ctx context.Context, in SearchThreadsInput) ([]SearchMatch, error) {
	args := map[string]any{"query": strings.TrimSpace(in.Query)}
	if in.ProjectID != "" {
		args["projectId"] = in.ProjectID
	}
	if in.Limit > 0 {
		args["limit"] = min(in.Limit, 50)
	}
	var out wireThreadSearch
	if err := c.call(ctx, "t3_thread_search", args, &out); err != nil {
		return nil, err
	}
	return out.toMatches(), nil
}

func (c *MCPClient) ReadThread(ctx context.Context, threadID string) (ThreadDetail, error) {
	if threadID == "" {
		return ThreadDetail{}, ErrNotFound
	}
	args := map[string]any{"threadId": threadID, "limit": 1, "runLimit": 50, "maxCharsPerItem": readMaxChars}
	var out wireThreadRead
	if err := c.call(ctx, "t3_thread_read", args, &out); err != nil {
		return ThreadDetail{}, err
	}
	d := out.toDetail()
	c.noteEnvironment(environmentFromLink(d.Link))
	return d, nil
}

const (
	// replyWindow is positions scanned per t3_thread_read (its max limit).
	replyWindow = 100
	// replyWindows bounds the backward scan (~2000 timeline positions).
	replyWindows = 20
	// itemCharsMax is t3_thread_read's maxCharsPerItem cap.
	itemCharsMax = MaxItemChars
)

func (c *MCPClient) LatestReply(ctx context.Context, in LatestReplyInput) (Reply, error) {
	if in.ThreadID == "" {
		return Reply{}, ErrNotFound
	}
	budget := in.MaxChars
	if budget <= 0 {
		budget = itemCharsMax
	}
	count := in.ItemCount
	if count <= 0 {
		var out wireThreadRead
		args := map[string]any{"threadId": in.ThreadID, "limit": 1, "runLimit": 1, "maxCharsPerItem": 1}
		if err := c.call(ctx, "t3_thread_read", args, &out); err != nil {
			return Reply{}, err
		}
		count = out.Thread.ItemCount
	}
	if count <= 0 {
		return Reply{}, ErrNoReply
	}
	item, found, err := c.findReply(ctx, in.ThreadID, in.RunID, count)
	if err != nil {
		return Reply{}, err
	}
	if !found {
		return Reply{}, ErrNoReply
	}
	return c.replyText(ctx, in.ThreadID, item, budget)
}

// findReply scans the messages view backwards in replyWindow-position windows
// (start, end] for the newest local assistant_message (of runID if set). The
// newest window pages forward while hasMore: the thread may have grown since
// itemCount was read.
func (c *MCPClient) findReply(ctx context.Context, threadID, runID string, itemCount int) (wireTimelineItem, bool, error) {
	end := itemCount - 1
	for w := range replyWindows {
		start := end - replyWindow
		var best wireTimelineItem
		found := false
		after := start
		for range maxPages {
			args := map[string]any{"threadId": threadID, "view": "messages", "limit": replyWindow, "runLimit": 1, "maxCharsPerItem": 1}
			if after >= 0 { // T3 afterPosition is NonNegativeInt; omitted = -1
				args["afterPosition"] = after
			}
			var page wireThreadRead
			if err := c.call(ctx, "t3_thread_read", args, &page); err != nil {
				return wireTimelineItem{}, false, err
			}
			for _, it := range page.Items {
				if it.Position <= after || (w > 0 && it.Position > end) {
					continue
				}
				if it.Type == "assistant_message" && it.Visibility == "local" && (runID == "" || it.RunID == runID) &&
					(!found || it.Position > best.Position) {
					best, found = it, true
				}
			}
			if w > 0 || !page.HasMore || page.NextPosition == nil {
				break
			}
			if *page.NextPosition <= after {
				return wireTimelineItem{}, false, fmt.Errorf("t3 t3_thread_read: position did not advance (%d -> %d)", after, *page.NextPosition)
			}
			after = *page.NextPosition
		}
		if found {
			return best, true, nil
		}
		if start < 0 {
			break
		}
		end = start
	}
	return wireTimelineItem{}, false, nil
}

// replyText reads up to budget UTF-16 units of item's text, paging by
// itemId + textOffset (T3 caps a page at itemCharsMax).
func (c *MCPClient) replyText(ctx context.Context, threadID string, item wireTimelineItem, budget int) (Reply, error) {
	r := Reply{ItemID: item.ItemID, RunID: item.RunID, MessageID: item.MessageID, Position: item.Position, UpdatedAt: item.UpdatedAt.Time}
	var text strings.Builder
	offset := 0
	for i := 0; budget > 0 && i < maxPages; i++ {
		args := map[string]any{"threadId": threadID, "itemId": item.ItemID, "textOffset": offset, "runLimit": 1, "maxCharsPerItem": min(budget, itemCharsMax)}
		var page wireThreadRead
		if err := c.call(ctx, "t3_thread_read", args, &page); err != nil {
			return Reply{}, err
		}
		idx := slices.IndexFunc(page.Items, func(it wireTimelineItem) bool { return it.ItemID == item.ItemID })
		if idx < 0 {
			if offset == 0 {
				return Reply{}, ErrNoReply // gone since the scan (e.g. rolled back)
			}
			r.Truncated = true
			break
		}
		got := page.Items[idx]
		if offset == 0 {
			r.UpdatedAt = got.UpdatedAt.Time
		}
		text.WriteString(got.Text)
		n := utf16Len(got.Text)
		budget -= n
		r.Truncated = got.TextTruncated
		if !got.TextTruncated {
			break
		}
		next := offset + n
		if got.NextTextOffset != nil {
			next = *got.NextTextOffset
		}
		if next <= offset {
			break
		}
		offset = next
	}
	r.Text = text.String()
	return r, nil
}

// utf16Len is len(s) in UTF-16 code units (JS string length).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += max(utf16.RuneLen(r), 1)
	}
	return n
}

func (c *MCPClient) ReadItems(ctx context.Context, in ReadItemsInput) (ItemPage, error) {
	if in.ThreadID == "" {
		return ItemPage{}, ErrNotFound
	}
	args := map[string]any{"threadId": in.ThreadID, "view": "messages", "runLimit": 1}
	if in.AfterPosition != nil && *in.AfterPosition >= 0 {
		args["afterPosition"] = *in.AfterPosition
	}
	if in.Limit > 0 {
		args["limit"] = min(in.Limit, MaxItemsPerRead)
	}
	if in.MaxChars > 0 {
		args["maxCharsPerItem"] = min(in.MaxChars, MaxItemChars)
	}
	if in.ItemID != "" {
		args["itemId"] = in.ItemID
		if in.TextOffset > 0 {
			args["textOffset"] = in.TextOffset
		}
	}
	var out wireThreadRead
	if err := c.call(ctx, "t3_thread_read", args, &out); err != nil {
		return ItemPage{}, err
	}
	page := out.toItemPage()
	c.noteEnvironment(environmentFromLink(page.Thread.Link))
	return page, nil
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
	if len(in.Attachments) > 0 {
		args["attachments"] = attachmentsArg(in.Attachments)
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

func (c *MCPClient) QueueContinuation(ctx context.Context, threadID, message, requestID string) (SendResult, error) {
	var out SendResult
	err := c.call(ctx, "t3_thread_send", map[string]any{
		"threadId": threadID, "message": message, "mode": SendQueue, "clientRequestId": requestID,
	}, &out)
	if err == nil && (out.ThreadID != threadID || (out.Delivery != "started" && out.Delivery != "queued")) {
		err = errors.New("unexpected continuation delivery result; review in T3")
	}
	return out, err
}

func attachmentsArg(atts []Attachment) []map[string]any {
	out := make([]map[string]any, 0, len(atts))
	for _, a := range atts {
		out = append(out, map[string]any{"type": "image", "id": a.ID, "name": a.Name, "mimeType": a.MimeType, "sizeBytes": a.SizeBytes})
	}
	return out
}

func (c *MCPClient) UploadAttachment(ctx context.Context, in UploadInput) (Attachment, error) {
	if len(in.Data) == 0 || in.Name == "" || in.MimeType == "" {
		return Attachment{}, errors.New("t3 upload: name, mimeType and data are required")
	}
	var prep struct {
		AttachmentID string `json:"attachmentId"`
		RelativeURL  string `json:"relativeUrl"`
	}
	args := map[string]any{"upload": map[string]any{"type": "image", "name": in.Name, "mimeType": in.MimeType, "sizeBytes": len(in.Data)}}
	if err := c.call(ctx, "t3_attachment_prepare_upload", args, &prep); err != nil {
		return Attachment{}, err
	}
	if prep.AttachmentID == "" || !strings.HasPrefix(prep.RelativeURL, "/") {
		return Attachment{}, fmt.Errorf("t3 upload: unexpected prepare_upload result %+v", prep)
	}
	// The relativeUrl is signed; T3 needs no auth header for it.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.opts.BaseURL+prep.RelativeURL, bytes.NewReader(in.Data))
	if err != nil {
		return Attachment{}, err
	}
	req.Header.Set("Content-Type", in.MimeType)
	resp, err := c.upHC.Do(req)
	if err != nil {
		return Attachment{}, fmt.Errorf("t3 upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Attachment{}, fmt.Errorf("t3 upload: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return Attachment{ID: prep.AttachmentID, Name: in.Name, MimeType: in.MimeType, SizeBytes: len(in.Data)}, nil
}

func (c *MCPClient) DiscardAttachment(ctx context.Context, attachmentID string) error {
	return c.call(ctx, "t3_attachment_discard", map[string]any{"attachmentId": attachmentID}, nil)
}

func (c *MCPClient) SendAttachments(ctx context.Context, threadID, message string, atts []Attachment) error {
	if threadID == "" {
		return ErrNotFound
	}
	if len(atts) == 0 || len(atts) > MaxAttachments {
		return fmt.Errorf("t3 t3_thread_send_attachments: 1..%d attachments required", MaxAttachments)
	}
	args := map[string]any{"threadId": threadID, "attachments": attachmentsArg(atts)}
	if strings.TrimSpace(message) != "" {
		args["message"] = message
	}
	return c.call(ctx, "t3_thread_send_attachments", args, nil)
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
