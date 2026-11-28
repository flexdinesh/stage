package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

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
			EventType:   "session_action",
			Text:        fmt.Sprintf("Action %q ran on session %q (connection %d), called by %s.", action, ref.ExternalSessionID, ref.ConnectionID, caller),
			Attribution: WorkAttribution(ctx),
		})
	})
	return err
}

// RecordTaskOperation records a dispatched operation against the same task
// session and re-arms polling. The harness call itself happens before this write.
func (s *Store) RecordTaskOperation(ctx context.Context, id int64, ref SessionRef, event, detail string) error {
	return s.RecordTaskAnswer(ctx, id, ref, event, detail, nil)
}

// RecordTaskAnswer is RecordTaskOperation that also records answered (when
// set) as the task's answered state revision, in the same write (ADR 0013
// first answer wins).
func (s *Store) RecordTaskAnswer(ctx context.Context, id int64, ref SessionRef, event, detail string, answered *int64) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		if err := tx.Tasks().Lock(ctx, id); err != nil {
			return err
		}
		task, err := tx.Tasks().Get(ctx, id)
		if err != nil {
			return err
		}
		if task.Session == nil || *task.Session != ref {
			return Conflict("The task's linked session changed during the action.")
		}
		if _, err := tx.Tasks().Touch(ctx, id); err != nil {
			return err
		}
		if answered != nil {
			if err := tx.Tasks().SetAnswered(ctx, id, *answered); err != nil {
				return err
			}
		}
		return addTaskEventType(ctx, tx, id, &task.State, task.State, detail, nil, event)
	})
}

// Derived writes (docs/storage.md "Journal events"). Every change that
// implies a journal event or another derived row goes through one helper
// here, in the caller's Write, so the derived rows commit and roll back with
// their cause. Former SQLite triggers (migration 0026 dropped them); their
// event creator default remains for compatibility. The actor stamp identifies
// new writes. TestDerivedWritesBoundary keeps raw derived writes here.

// workStates record work on the task that enters them.
var workStates = []TaskState{StateLaunching, StateRunning, StateNeedsInput, StateDone, StateFailed, StateInterrupted}

// addTaskEvent appends a task event and journals it in the task and its
// tree at the event's time, under the task's session and attempt; work
// recorded by the task protects it and its tree.
func addTaskEvent(ctx context.Context, tx store.Tx, taskID int64, from *TaskState, to TaskState, detail string) error {
	return addTaskEventAt(ctx, tx, taskID, from, to, detail, nil)
}

func addTaskEventAt(ctx context.Context, tx store.Tx, taskID int64, from *TaskState, to TaskState, detail string, observedAt *time.Time) error {
	return addTaskEventType(ctx, tx, taskID, from, to, detail, observedAt, "")
}

func addTaskEventType(ctx context.Context, tx store.Tx, taskID int64, from *TaskState, to TaskState, detail string, observedAt *time.Time, eventType string) error {
	attribution := WorkAttribution(ctx)
	at, err := tx.Tasks().AddEvent(ctx, taskID, from, to, detail, observedAt, attribution)
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
		// Every state write journals here, so this counts each real
		// transition once (ADR 0013 first answer wins).
		if err := tx.Tasks().BumpStateRevision(ctx, taskID); err != nil {
			return err
		}
	}
	if eventType != "" {
		kind = eventType
	}
	attempt := st.LaunchAttempt
	if err := tx.Journal().InsertEvent(ctx, store.JournalEvent{TaskID: &taskID, TreeID: st.TreeID, SourceSessionID: st.SessionID,
		Attempt: &attempt, EventType: kind, Text: text, At: &at, Attribution: attribution}); err != nil {
		return err
	}
	if !st.RecordedWork && (st.LaunchAttempt > 0 || st.SessionID != nil || st.Adopted || slices.Contains(workStates, to)) {
		if err := tx.Tasks().MarkRecordedWork(ctx, taskID); err != nil {
			return err
		}
		st.RecordedWork = true
	}
	if st.RecordedWork && st.TreeID != nil {
		if err := tx.Trees().MarkRecordedWork(ctx, *st.TreeID, nil); err != nil {
			return err
		}
	}
	return steerTaskEvent(ctx, tx, st.TreeID, taskID, from, to, detail, eventType)
}

// insertNote writes a note; it protects its task and tree, and stamps the
// tree's updated_at with the note's time.
func insertNote(ctx context.Context, tx store.Tx, o JournalOwner, in AppendNoteInput, sourceID *int64) (JournalEntry, error) {
	e, err := tx.Journal().InsertNote(ctx, o, in, sourceID, ActingUser(ctx), ActingMachine(ctx), WorkAttribution(ctx))
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
	return e, steerNote(ctx, tx, tree, e, sourceID)
}

// treeEvent journals event on tree.
func treeEvent(ctx context.Context, tx store.Tx, tree int64, event, text string) error {
	return tx.Journal().InsertEvent(ctx, store.JournalEvent{TreeID: &tree, EventType: event, Text: text, Attribution: WorkAttribution(ctx)})
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
	if err := treeEvent(ctx, tx, id, "tree_status_changed", fmt.Sprintf("%s -> %s", old, status)); err != nil {
		return err
	}
	return enqueueSteering(ctx, tx, id, SteerTreeStatus, nil, func() (map[string]any, error) {
		return map[string]any{"from": string(old), "to": string(status)}, nil
	})
}

// setMembership puts the task in tree (nil: none). A change is journaled in
// both trees, counted in their membership revisions (carrying the task's
// recorded work), and archives the tree it emptied.
func setMembership(ctx context.Context, tx store.Tx, taskID int64, tree *int64) error {
	if err := steererNotMember(ctx, tx, taskID, tree); err != nil {
		return err
	}
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
			Text: st.Title, FromTreeID: old, ToTreeID: tree, Attribution: WorkAttribution(ctx)}); err != nil {
			return err
		}
	}
	if err := tx.Trees().BumpMembership(ctx, touched, st.RecordedWork); err != nil {
		return err
	}
	if err := steerMembership(ctx, tx, taskID, st.Title, old, tree); err != nil {
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
			Text: fmt.Sprintf("%s need #%d", verb, e.NeedID), Attribution: WorkAttribution(ctx)}); err != nil {
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
	return tx.Acceptances().Insert(ctx, taskID, provenance, evidence, note, st.SessionID, WorkAttribution(ctx))
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

// ---- Tree steering (ADR 0013) ----
//
// Steering events are enqueued here, in the Write of the change that
// caused them, and only while the tree has an active steerer binding.

// Steering event kinds.
const (
	SteerMemberNeedsInput  = "member_needs_input"
	SteerMemberDone        = "member_done"
	SteerMemberFailed      = "member_failed"
	SteerMemberInterrupted = "member_interrupted"
	SteerMemberCancelled   = "member_cancelled"
	SteerMemberReady       = "member_ready"
	SteerMemberAdded       = "member_added"
	SteerMemberRemoved     = "member_removed"
	SteerMemberAccepted    = "member_accepted"
	SteerMemberReopened    = "member_reopened"
	SteerMemberAnswered    = "member_answered"
	SteerNote              = "note"
	SteerTreeStatus        = "tree_status"
	SteerStarted           = "steering_started"
	SteerEnded             = "steering_ended"
)

// steerReplyRunes caps member_done's reply excerpt.
const steerReplyRunes = 600

// ReadyDetail is the task event detail of a readiness episode (MarkReady).
const ReadyDetail = "ready: needs completed"

type steeringCauseKey struct{}

// withSteeringCause makes c the cause of the steering events ctx enqueues,
// for derived chains with no caller session of their own (a dependent made
// ready by an acceptance inherits the acceptance's caller).
func withSteeringCause(ctx context.Context, c CallerSession) context.Context {
	return context.WithValue(ctx, steeringCauseKey{}, c)
}

// steeringCausedBy is the caller session and its task that caused the
// events ctx enqueues (nil, nil: none).
func steeringCausedBy(ctx context.Context) (*SessionRef, *int64) {
	c, ok := ctx.Value(steeringCauseKey{}).(CallerSession)
	if !ok {
		if c, ok = CallerSessionFrom(ctx); !ok {
			return nil, nil
		}
	}
	var ref *SessionRef
	if c.Ref != (SessionRef{}) {
		r := c.Ref
		ref = &r
	}
	var task *int64
	if c.TaskID != 0 {
		id := c.TaskID
		task = &id
	}
	return ref, task
}

// enqueueSteering appends a steering event on tree when it has an active
// steerer; payload builds the event's payload only then. Event ids are
// allocated under the tree's event lock, after which the binding is checked
// again: an event never follows its binding's end.
func enqueueSteering(ctx context.Context, tx store.Tx, tree int64, kind string, task *int64, payload func() (map[string]any, error)) error {
	st := tx.Steering()
	if _, err := st.Active(ctx, tree); errors.Is(err, ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	return insertSteeringEvent(ctx, tx, tree, kind, task, payload)
}

// insertSteeringEvent locks tree's events and appends one to its active
// binding (none: nothing).
func insertSteeringEvent(ctx context.Context, tx store.Tx, tree int64, kind string, task *int64, payload func() (map[string]any, error)) error {
	st := tx.Steering()
	if err := st.LockEvents(ctx, tree); err != nil {
		return err
	}
	s, err := st.Active(ctx, tree)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var p map[string]any
	if payload != nil {
		if p, err = payload(); err != nil {
			return err
		}
	}
	ref, by := steeringCausedBy(ctx)
	_, err = st.InsertEvent(ctx, store.NewSteeringEvent{TreeID: tree, SteererID: s.ID, Kind: kind, TaskID: task, Payload: p, CausedBy: ref, CausedByTask: by})
	return err
}

// steerTaskEvent enqueues the steering event a task event implies, in the
// task's tree: a state it entered, an answer to it, or a readiness episode.
func steerTaskEvent(ctx context.Context, tx store.Tx, tree *int64, taskID int64, from *TaskState, to TaskState, detail, eventType string) error {
	if tree == nil || from == nil {
		return nil
	}
	kind := ""
	extra := map[string]any{}
	cause := WorkAttribution(ctx).Cause
	switch {
	case *from != to:
		kind = map[TaskState]string{
			StateNeedsInput: SteerMemberNeedsInput, StateDone: SteerMemberDone, StateFailed: SteerMemberFailed,
			StateInterrupted: SteerMemberInterrupted, StateCancelled: SteerMemberCancelled,
		}[to]
	case eventType == "session_response":
		kind, extra["via"] = SteerMemberAnswered, "respond"
		if cause != nil && cause.RequestID != "" {
			extra["requestId"] = cause.RequestID
		}
	case cause != nil && cause.Kind == "session_message":
		kind, extra["via"] = SteerMemberAnswered, "send"
	case to == StateBacklog && detail == ReadyDetail:
		kind = SteerMemberReady
	}
	if kind == "" {
		return nil
	}
	return enqueueSteering(ctx, tx, *tree, kind, &taskID, func() (map[string]any, error) {
		t, err := tx.Tasks().Get(ctx, taskID)
		if err != nil {
			return nil, err
		}
		extra["title"] = t.Title
		switch kind {
		case SteerMemberNeedsInput:
			extra["stateRevision"] = t.StateRevision
		case SteerMemberDone:
			if t.SessionIdentityID == nil {
				break
			}
			o, err := tx.Observations().Get(ctx, *t.SessionIdentityID)
			if errors.Is(err, ErrNotFound) {
				break
			}
			if err != nil {
				return nil, err
			}
			if o.Reply != nil {
				reply := []rune(o.Reply.Text)
				extra["replyTruncated"] = o.Reply.Truncated || len(reply) > steerReplyRunes
				if len(reply) > steerReplyRunes {
					reply = reply[:steerReplyRunes]
				}
				extra["reply"] = string(reply)
			}
		case SteerMemberFailed, SteerMemberInterrupted:
			if t.LastError != nil {
				extra["error"] = *t.LastError
			}
		}
		return extra, nil
	})
}

// steerNote enqueues a blocker or decision note on a member task or on the
// tree itself.
func steerNote(ctx context.Context, tx store.Tx, tree *int64, e JournalEntry, sourceID *int64) error {
	if tree == nil || (e.Category != "blocker" && e.Category != "decision") {
		return nil
	}
	if _, named := CallerSessionFrom(ctx); !named && e.SourceSession != nil {
		c := CallerSession{Ref: *e.SourceSession}
		if sourceID != nil {
			owner, err := tx.Tasks().BySessionID(ctx, *sourceID)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			c.TaskID = owner
		}
		ctx = withSteeringCause(ctx, c)
	}
	return enqueueSteering(ctx, tx, *tree, SteerNote, e.TaskID, func() (map[string]any, error) {
		text := []rune(e.Text)
		p := map[string]any{"noteId": e.ID, "category": e.Category, "truncated": len(text) > steerReplyRunes}
		if len(text) > steerReplyRunes {
			text = text[:steerReplyRunes]
		}
		p["text"] = string(text)
		return p, nil
	})
}

// steerMembership enqueues member_removed in the tree the task left and
// member_added in the tree it joined.
func steerMembership(ctx context.Context, tx store.Tx, taskID int64, title string, old, tree *int64) error {
	payload := func() (map[string]any, error) { return map[string]any{"title": title}, nil }
	if old != nil {
		if err := enqueueSteering(ctx, tx, *old, SteerMemberRemoved, &taskID, payload); err != nil {
			return err
		}
	}
	if tree != nil {
		return enqueueSteering(ctx, tx, *tree, SteerMemberAdded, &taskID, payload)
	}
	return nil
}

// steerAcceptance enqueues member_accepted (event task_completed) or
// member_reopened for an acceptance change of member id at revision.
func steerAcceptance(ctx context.Context, tx store.Tx, id, revision int64, acceptanceID *int64) error {
	tree, err := tx.Tasks().TreeID(ctx, id)
	if err != nil || tree == nil {
		return err
	}
	kind := SteerMemberReopened
	if acceptanceID != nil {
		kind = SteerMemberAccepted
	}
	return enqueueSteering(ctx, tx, *tree, kind, &id, func() (map[string]any, error) {
		t, err := tx.Tasks().Get(ctx, id)
		if err != nil {
			return nil, err
		}
		p := map[string]any{"title": t.Title, "revision": revision}
		if acceptanceID != nil {
			p["acceptanceId"] = *acceptanceID
		}
		return p, nil
	})
}

// steererNotMember refuses task joining tree while it steers tree.
func steererNotMember(ctx context.Context, tx store.Tx, taskID int64, tree *int64) error {
	if tree == nil {
		return nil
	}
	s, err := tx.Steering().ActiveByTask(ctx, taskID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.TreeID == *tree {
		return SteererNotMember(taskID, *tree)
	}
	return nil
}

// SteererNotMember: a tree's steerer cannot be its member (400
// steerer_not_member, ADR 0013).
func SteererNotMember(taskID, tree int64) error {
	return &InvalidError{Msg: fmt.Sprintf("Task %d steers tree %d, so it cannot be a member of it.", taskID, tree), Code: "steerer_not_member"}
}

// bindSteerer binds task as tree's active steerer, enqueues steering_started
// and journals steering_started (or steering_replaced, when replaced names
// the steerer task it replaced).
func bindSteerer(ctx context.Context, tx store.Tx, tree, task int64, requestID, digest string, replaced *int64) (int64, error) {
	member, err := tx.Tasks().TreeID(ctx, task)
	if err != nil {
		return 0, err
	}
	if member != nil && *member == tree {
		return 0, SteererNotMember(task, tree)
	}
	id, err := tx.Steering().Bind(ctx, tree, task, requestID, digest, WorkAttribution(ctx))
	if err != nil {
		return 0, err
	}
	if err := insertSteeringEvent(ctx, tx, tree, SteerStarted, &task, nil); err != nil {
		return 0, err
	}
	if replaced != nil {
		return id, treeEvent(ctx, tx, tree, "steering_replaced", fmt.Sprintf("Steerer #%d replaced by #%d", *replaced, task))
	}
	return id, treeEvent(ctx, tx, tree, "steering_started", fmt.Sprintf("Steering by #%d started", task))
}

// endSteerer ends binding s with reason, enqueuing steering_ended before
// the end; a replace (reason replaced) journals with the new binding.
func endSteerer(ctx context.Context, tx store.Tx, s store.Steerer, reason store.SteeringEndReason) error {
	task := s.TaskID
	if err := insertSteeringEvent(ctx, tx, s.TreeID, SteerEnded, &task, func() (map[string]any, error) {
		return map[string]any{"reason": string(reason)}, nil
	}); err != nil {
		return err
	}
	ended, err := tx.Steering().End(ctx, s.ID, reason)
	if err != nil {
		return err
	}
	if !ended {
		return Conflict("Steering changed. Refresh the tree.")
	}
	if reason == store.SteeringReplaced {
		return nil
	}
	return treeEvent(ctx, tx, s.TreeID, "steering_ended", fmt.Sprintf("Steering by #%d ended (%s)", task, reason))
}
