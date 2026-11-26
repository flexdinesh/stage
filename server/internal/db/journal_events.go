package db

import (
	"context"
	"fmt"
	"slices"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// RecordSessionAction records a successful dispatch. The adapter call is
// outside this write, including when the database retries a transaction.
func (s *Store) RecordSessionAction(ctx context.Context, taskID int64, ref SessionRef, action string, caller execution.Caller, source *SessionRef) error {
	_, err := write(ctx, s, func(tx store.Tx) (struct{}, error) {
		t, err := tx.Tasks().Get(ctx, taskID)
		if err != nil {
			return struct{}{}, err
		}
		var sourceID *int64
		if source != nil {
			exists, err := tx.Connections().Exists(ctx, source.ConnectionID)
			if err != nil {
				return struct{}{}, err
			}
			if exists {
				id, err := ensureSession(ctx, tx, *source)
				if err != nil {
					return struct{}{}, err
				}
				sourceID = &id
			}
		}
		attempt := t.LaunchAttempt
		if t.Session == nil || *t.Session != ref {
			return struct{}{}, Conflict("The task's linked session changed during the action.")
		}
		return struct{}{}, tx.Journal().InsertEvent(ctx, store.JournalEvent{
			TaskID: &taskID, TreeID: t.TreeID, SourceSessionID: sourceID, Attempt: &attempt,
			EventType: "session_action",
			Text:      fmt.Sprintf("Action %q ran on session %q (connection %d), called by %s.", action, ref.ExternalSessionID, ref.ConnectionID, caller),
		})
	})
	return err
}

// Derived writes (docs/storage.md "Journal events"). Every change that
// implies a journal event or another derived row goes through one helper
// here, in the caller's Write, so the derived rows commit and roll back with
// their cause. Former SQLite triggers (migration 0026 dropped them); their
// events keep the trigger's attribution: the default user (column default)
// and no source machine. TestDerivedWritesBoundary keeps the raw repository
// writes in this file.

// workStates record work on the task that enters them.
var workStates = []TaskState{StateLaunching, StateRunning, StateNeedsInput, StateDone, StateFailed, StateInterrupted}

// addTaskEvent appends a task event and journals it in the task and its
// tree at the event's time, under the task's session and attempt; work
// recorded by the task protects it and its tree.
func addTaskEvent(ctx context.Context, tx store.Tx, taskID int64, from *TaskState, to TaskState, detail string) error {
	at, err := tx.Tasks().AddEvent(ctx, taskID, from, to, detail)
	if err != nil {
		return err
	}
	st, err := tx.Tasks().JournalState(ctx, taskID)
	if err != nil {
		return err
	}
	kind, text := "task_event", detail
	switch {
	case from == nil:
		kind = "task_created"
	case *from != to:
		kind = "task_state_changed"
		text += fmt.Sprintf(" (%s -> %s)", *from, to)
	}
	attempt := st.LaunchAttempt
	if err := tx.Journal().InsertEvent(ctx, store.JournalEvent{TaskID: &taskID, TreeID: st.TreeID, SourceSessionID: st.SessionID,
		Attempt: &attempt, EventType: kind, Text: text, At: &at}); err != nil {
		return err
	}
	if !st.RecordedWork && (st.LaunchAttempt > 0 || st.SessionID != nil || st.Adopted || slices.Contains(workStates, to)) {
		if err := tx.Tasks().MarkRecordedWork(ctx, taskID); err != nil {
			return err
		}
		st.RecordedWork = true
	}
	if st.RecordedWork && st.TreeID != nil {
		return tx.Trees().MarkRecordedWork(ctx, *st.TreeID, nil)
	}
	return nil
}

// insertNote writes a note; it protects its task and tree, and stamps the
// tree's updated_at with the note's time.
func insertNote(ctx context.Context, tx store.Tx, o JournalOwner, in AppendNoteInput, sourceID *int64) (JournalEntry, error) {
	e, err := tx.Journal().InsertNote(ctx, o, in, sourceID, ActingUser(ctx), ActingMachine(ctx))
	if err != nil {
		return JournalEntry{}, err
	}
	tree := e.TreeID
	if e.TaskID != nil {
		if err := tx.Tasks().MarkRecordedWork(ctx, *e.TaskID); err != nil {
			return JournalEntry{}, err
		}
		if tree, err = tx.Tasks().TreeID(ctx, *e.TaskID); err != nil {
			return JournalEntry{}, err
		}
	}
	if tree != nil {
		at := e.CreatedAt
		if err := tx.Trees().MarkRecordedWork(ctx, *tree, &at); err != nil {
			return JournalEntry{}, err
		}
	}
	return e, nil
}

// treeEvent journals event on tree.
func treeEvent(ctx context.Context, tx store.Tx, tree int64, event, text string) error {
	return tx.Journal().InsertEvent(ctx, store.JournalEvent{TreeID: &tree, EventType: event, Text: text})
}

// insertTree creates a tree and journals it.
func insertTree(ctx context.Context, tx store.Tx, title string, mode StartMode) (int64, error) {
	id, err := tx.Trees().Insert(ctx, title, mode, ActingUser(ctx), ActingMachine(ctx))
	if err != nil {
		return 0, err
	}
	return id, treeEvent(ctx, tx, id, "tree_created", title)
}

// updateTree sets the tree's title and start mode when set, journaling a
// change of either.
func updateTree(ctx context.Context, tx store.Tx, id int64, title Optional[string], mode Optional[StartMode]) error {
	oldTitle, oldMode, err := tx.Trees().Config(ctx, id)
	if err != nil {
		return err
	}
	if err := tx.Trees().Update(ctx, id, title, mode); err != nil {
		return err
	}
	newTitle, newMode := oldTitle, oldMode
	if title.Set {
		newTitle = title.V
	}
	if mode.Set {
		newMode = mode.V
	}
	if newTitle == oldTitle && newMode == oldMode {
		return nil
	}
	return treeEvent(ctx, tx, id, "tree_config_changed", fmt.Sprintf("Title: %s -> %s; start mode: %s -> %s", oldTitle, newTitle, oldMode, newMode))
}

// setTreeStatus sets the tree's status, journaling a change.
func setTreeStatus(ctx context.Context, tx store.Tx, id int64, status TreeStatus) error {
	return changeTreeStatus(ctx, tx, id, status, func() (bool, error) {
		return true, tx.Trees().SetStatus(ctx, id, status)
	})
}

// archiveDissolved archives a dissolved tree, journaling a change.
func archiveDissolved(ctx context.Context, tx store.Tx, id int64) error {
	return changeTreeStatus(ctx, tx, id, TreeArchived, func() (bool, error) {
		return true, tx.Trees().ArchiveDissolved(ctx, id)
	})
}

// changeTreeStatus runs set (applied: it wrote status) and journals a
// status change from the tree's previous status.
func changeTreeStatus(ctx context.Context, tx store.Tx, id int64, status TreeStatus, set func() (bool, error)) error {
	old, err := tx.Trees().Status(ctx, id)
	if err != nil {
		return err
	}
	applied, err := set()
	if err != nil || !applied || old == status {
		return err
	}
	return treeEvent(ctx, tx, id, "tree_status_changed", fmt.Sprintf("%s -> %s", old, status))
}

// setMembership puts the task in tree (nil: none). A change is journaled in
// both trees, counted in their membership revisions (carrying the task's
// recorded work), and archives the tree it emptied.
func setMembership(ctx context.Context, tx store.Tx, taskID int64, tree *int64) error {
	st, err := tx.Tasks().JournalState(ctx, taskID)
	if err != nil {
		return err
	}
	if err := tx.Trees().SetMembership(ctx, taskID, tree); err != nil {
		return err
	}
	old := st.TreeID
	if equalPtr(old, tree) {
		return nil
	}
	var touched []int64
	for _, owner := range []*int64{old, tree} {
		if owner == nil {
			continue
		}
		touched = append(touched, *owner)
		if err := tx.Journal().InsertEvent(ctx, store.JournalEvent{TreeID: owner, SourceTaskID: &taskID, EventType: "task_moved",
			Text: st.Title, FromTreeID: old, ToTreeID: tree}); err != nil {
			return err
		}
	}
	if err := tx.Trees().BumpMembership(ctx, touched, st.RecordedWork); err != nil {
		return err
	}
	if old == nil {
		return nil
	}
	return changeTreeStatus(ctx, tx, *old, TreeArchived, func() (bool, error) {
		return tx.Trees().ArchiveIfEmpty(ctx, *old)
	})
}

// needsChanged journals each edge in its dependent's tree ("Added" or
// "Removed need #N"); a standalone dependent journals nothing.
func needsChanged(ctx context.Context, tx store.Tx, verb string, edges []store.TreeEdge) error {
	for _, e := range edges {
		tree, err := tx.Tasks().TreeID(ctx, e.TaskID)
		if err != nil {
			return err
		}
		if tree == nil {
			continue
		}
		if err := tx.Journal().InsertEvent(ctx, store.JournalEvent{TreeID: tree, SourceTaskID: &e.TaskID, EventType: "needs_changed",
			Text: fmt.Sprintf("%s need #%d", verb, e.NeedID)}); err != nil {
			return err
		}
	}
	return nil
}

func addNeed(ctx context.Context, tx store.Tx, taskID, need int64) error {
	if err := tx.Trees().AddNeed(ctx, taskID, need); err != nil {
		return err
	}
	return needsChanged(ctx, tx, "Added", []store.TreeEdge{{TaskID: taskID, NeedID: need}})
}

// keepNeeds drops the task's needs not in keep.
func keepNeeds(ctx context.Context, tx store.Tx, taskID int64, keep []int64) error {
	gone, err := tx.Trees().KeepNeeds(ctx, taskID, keep)
	if err != nil {
		return err
	}
	return needsChanged(ctx, tx, "Removed", gone)
}

// inheritNeeds gives the task's dependents its needs.
func inheritNeeds(ctx context.Context, tx store.Tx, taskID int64) error {
	added, err := tx.Trees().InheritNeeds(ctx, taskID)
	if err != nil {
		return err
	}
	return needsChanged(ctx, tx, "Added", added)
}

// dropNeeds removes every need edge of the task, both ways.
func dropNeeds(ctx context.Context, tx store.Tx, taskID int64) error {
	gone, err := tx.Trees().DropNeeds(ctx, taskID)
	if err != nil {
		return err
	}
	return needsChanged(ctx, tx, "Removed", gone)
}

// detachMembers drops the members' needs and takes them out of the tree,
// by id (the last one archives it).
func detachMembers(ctx context.Context, tx store.Tx, id int64) error {
	gone, err := tx.Trees().DropMemberNeeds(ctx, id)
	if err != nil {
		return err
	}
	if err := needsChanged(ctx, tx, "Removed", gone); err != nil {
		return err
	}
	members, err := tx.Trees().MemberIDs(ctx, id)
	if err != nil {
		return err
	}
	for _, m := range members {
		if err := setMembership(ctx, tx, m, nil); err != nil {
			return err
		}
	}
	return nil
}

// insertAcceptance records an acceptance with the task's session as its
// source.
func insertAcceptance(ctx context.Context, tx store.Tx, taskID int64, provenance string, evidence AcceptedEvidence, note string) (int64, error) {
	st, err := tx.Tasks().JournalState(ctx, taskID)
	if err != nil {
		return 0, err
	}
	return tx.Acceptances().Insert(ctx, taskID, provenance, evidence, note, st.SessionID)
}

// recordHandoff stores the handoff with its source session: the evidence's
// session, else none.
func recordHandoff(ctx context.Context, tx store.Tx, taskID int64, h Handoff) error {
	var sourceID *int64
	if ref := h.Session; ref != nil {
		id, err := ensureSession(ctx, tx, *ref)
		if err != nil {
			return err
		}
		sourceID = &id
	}
	return tx.Attempts().RecordHandoff(ctx, taskID, h, sourceID)
}
