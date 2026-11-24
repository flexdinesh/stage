package db

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// CreatedTasks is a session batch: the tasks one T3 session created
// (origin_thread_id), grouped by tree.
type CreatedTasks struct {
	// Trees: batch tasks grouped by their tree, ordered by each group's
	// lowest task id; tasks in a group by id.
	Trees []BatchTree `json:"trees"`
	// Independent: batch tasks outside any tree.
	Independent []Task `json:"independent"`
}

// BatchTree is the batch tasks of one tree.
type BatchTree struct {
	Tree  TreeRef `json:"tree"`
	Tasks []Task  `json:"tasks"`
}

// TreeRef names a tree.
type TreeRef struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	StartMode StartMode `json:"startMode"`
}

// CreatedBy returns the batch created by T3 session threadID.
func (s *Store) CreatedBy(ctx context.Context, threadID string) (CreatedTasks, error) {
	return s.CreatedBySession(ctx, SessionRef{ConnectionID: s.LegacyT3ID(), ExternalSessionID: threadID})
}
func (s *Store) CreatedBySession(ctx context.Context, ref SessionRef) (CreatedTasks, error) {
	tasks, err := read(ctx, s, func(tx store.Tx) ([]Task, error) { return tx.Tasks().CreatedBySession(ctx, ref) })
	if err != nil {
		return CreatedTasks{}, err
	}
	return GroupBatch(tasks), nil
}

// GroupBatch groups tasks (ordered by id) by tree; tasks outside a tree are
// independent.
func GroupBatch(tasks []Task) CreatedTasks {
	out := CreatedTasks{Trees: []BatchTree{}, Independent: []Task{}}
	index := map[int64]int{}
	for _, t := range tasks {
		if t.TreeID == nil {
			out.Independent = append(out.Independent, t)
			continue
		}
		i, ok := index[*t.TreeID]
		if !ok {
			ref := TreeRef{ID: *t.TreeID, StartMode: StartAuto}
			if t.TreeTitle != nil {
				ref.Title = *t.TreeTitle
			}
			if t.TreeStartMode != nil {
				ref.StartMode = *t.TreeStartMode
			}
			i = len(out.Trees)
			index[*t.TreeID] = i
			out.Trees = append(out.Trees, BatchTree{Tree: ref, Tasks: []Task{}})
		}
		out.Trees[i].Tasks = append(out.Trees[i].Tasks, t)
	}
	return out
}
