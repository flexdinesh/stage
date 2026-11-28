package t3

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

const queueTool = "t3_queue"

// QueuedRun is a native queue entry. T3 returns up to 1,000 characters of
// text and provides neither an enqueue time nor a source in this response.
type QueuedRun struct {
	ID   string `json:"queuedRunId"`
	Text string `json:"text"`
}

func (c *MCPClient) ListQueued(ctx context.Context, threadID string) ([]QueuedRun, error) {
	if threadID == "" {
		return nil, ErrNotFound
	}
	var out []QueuedRun
	cursor := 0
	for range maxPages {
		var page struct {
			Items      []QueuedRun `json:"items"`
			NextCursor *int        `json:"nextCursor"`
		}
		args := map[string]any{"action": "list", "threadId": threadID, "cursor": cursor, "limit": 100}
		if err := c.call(ctx, queueTool, args, &page); err != nil {
			return nil, err
		}
		if page.Items == nil {
			return nil, errors.New("t3 t3_queue: missing items array")
		}
		for _, item := range page.Items {
			if item.ID == "" {
				return nil, errors.New("t3 t3_queue: missing queuedRunId")
			}
		}
		out = append(out, page.Items...)
		if page.NextCursor == nil {
			return out, nil
		}
		if *page.NextCursor <= cursor {
			return nil, fmt.Errorf("t3 t3_queue: cursor did not advance (%d -> %d)", cursor, *page.NextCursor)
		}
		cursor = *page.NextCursor
	}
	return nil, fmt.Errorf("t3 t3_queue: more than %d pages", maxPages)
}

func (c *MCPClient) CancelQueued(ctx context.Context, threadID, queuedRunID string) error {
	return c.changeQueued(ctx, threadID, queuedRunID, "cancel", "")
}

func (c *MCPClient) PromoteQueued(ctx context.Context, threadID, queuedRunID, targetRunID string) error {
	if targetRunID == "" {
		return errors.New("t3 t3_queue: targetRunId required for promotion")
	}
	return c.changeQueued(ctx, threadID, queuedRunID, "promote_to_steer", targetRunID)
}

func (c *MCPClient) changeQueued(ctx context.Context, threadID, queuedRunID, action, targetRunID string) error {
	if threadID == "" || queuedRunID == "" {
		return ErrNotFound
	}
	args := map[string]any{"action": action, "threadId": threadID, "queuedRunId": queuedRunID}
	if targetRunID != "" {
		args["targetRunId"] = targetRunID
	}
	err := c.call(ctx, queueTool, args, nil)
	// T3 uses orchestration_error for a run that has already left the queue.
	var failure *ToolError
	if errors.As(err, &failure) && failure.Code == "orchestration_error" {
		message := "Run " + queuedRunID + " is not queued."
		if failure.Message == message || failure.Message == "Queued run "+queuedRunID+" is not queued." {
			return ErrNotFound
		}
	}
	return err
}

var _ execution.QueueManager = (*Adapter)(nil)

func (a *Adapter) Queued(ctx context.Context, ref execution.SessionRef) ([]execution.QueuedMessage, error) {
	if ref.ConnectionID != a.ConnectionID {
		return nil, execution.ErrUnavailable
	}
	runs, err := a.Client.ListQueued(ctx, ref.ExternalSessionID)
	if errors.Is(err, ErrNotFound) {
		return nil, execution.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	items := make([]execution.QueuedMessage, 0, len(runs))
	for _, run := range runs {
		items = append(items, execution.QueuedMessage{ID: run.ID, Text: run.Text, Delivery: execution.DeliveryQueue})
	}
	return items, nil
}

func (a *Adapter) CancelQueued(ctx context.Context, ref execution.SessionRef, id string) error {
	if ref.ConnectionID != a.ConnectionID {
		return execution.ErrUnavailable
	}
	return sendError(a.Client.CancelQueued(ctx, ref.ExternalSessionID, id))
}

func (a *Adapter) PromoteQueued(ctx context.Context, ref execution.SessionRef, id string) error {
	if ref.ConnectionID != a.ConnectionID {
		return execution.ErrUnavailable
	}
	if id == "" {
		return execution.ErrNotFound
	}
	thread, err := a.Client.ReadThread(ctx, ref.ExternalSessionID)
	if errors.Is(err, ErrNotFound) {
		return execution.ErrNotFound
	}
	if err != nil {
		return err
	}
	if thread.ActiveRunID == "" {
		// The run may have left the queue when the active turn ended.
		items, err := a.Client.ListQueued(ctx, ref.ExternalSessionID)
		if errors.Is(err, ErrNotFound) {
			return execution.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(items, func(item QueuedRun) bool { return item.ID == id }) {
			return execution.ErrNotFound
		}
		return &execution.Busy{Reason: "There is no active turn to steer."}
	}
	err = a.Client.PromoteQueued(ctx, ref.ExternalSessionID, id, thread.ActiveRunID)
	var failure *ToolError
	if errors.As(err, &failure) && failure.Code == "orchestration_error" {
		if reason, ok := strings.CutPrefix(failure.Message, "Target run "+thread.ActiveRunID+" "); ok &&
			(reason == "was not found." || strings.HasSuffix(reason, " and cannot be steered.")) {
			return &execution.Busy{Reason: failure.Message}
		}
	}
	return sendError(err)
}
