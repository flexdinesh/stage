package t3

import (
	"context"
	"slices"
)

func (f *Fake) ListQueued(_ context.Context, threadID string) ([]QueuedRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.threads[threadID]; !ok {
		return nil, ErrNotFound
	}
	return slices.Clone(f.queued[threadID]), nil
}

func (f *Fake) CancelQueued(_ context.Context, threadID, queuedRunID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	queued := f.queued[threadID]
	i := queuedIndex(queued, queuedRunID)
	if i < 0 {
		return ErrNotFound
	}
	f.queued[threadID] = slices.Delete(queued, i, i+1)
	return nil
}

func (f *Fake) PromoteQueued(_ context.Context, threadID, queuedRunID, targetRunID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	queued := f.queued[threadID]
	i := queuedIndex(queued, queuedRunID)
	if i < 0 {
		return ErrNotFound
	}
	thread := f.threads[threadID]
	if thread.Archived {
		return &ToolError{Tool: queueTool, Code: "orchestration_error", Message: "Thread " + threadID + " is not active."}
	}
	if targetRunID == "" || thread.ActiveRunID != targetRunID || thread.Status != StatusRunning {
		return &ToolError{Tool: queueTool, Code: "orchestration_error", Message: "Target run " + targetRunID + " was not found."}
	}
	f.Sent = append(f.Sent, FakeSend{ThreadID: threadID, Message: queued[i].Text, Mode: SendSteer})
	f.queued[threadID] = slices.Delete(queued, i, i+1)
	return nil
}

func queuedIndex(queued []QueuedRun, id string) int {
	return slices.IndexFunc(queued, func(run QueuedRun) bool { return run.ID == id })
}
