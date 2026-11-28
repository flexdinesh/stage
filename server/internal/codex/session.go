package codex

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// replyLimit bounds a final reply (characters).
const replyLimit = 4000

func unix(s int64) time.Time { return time.Unix(s, 0).UTC() }

func unixPtr(s *int64) *time.Time {
	if s == nil || *s <= 0 {
		return nil
	}
	t := unix(*s)
	return &t
}

// read is the thread without its turns; ErrNotFound when Codex does not
// know it.
func (a *Adapter) read(ctx context.Context, id string) (thread, error) {
	var res threadResponse
	if err := a.client.call(ctx, "thread/read", map[string]any{"threadId": id}, &res); err != nil {
		if notLoaded(err) {
			return thread{}, execution.ErrNotFound
		}
		return thread{}, err
	}
	if res.Thread.ID != id || res.Thread.CreatedAt <= 0 {
		return thread{}, errors.New("codex returned an incomplete or different thread")
	}
	return res.Thread, nil
}

// lastTurn is the thread's newest turn with its items (nil: none yet).
func (a *Adapter) lastTurn(ctx context.Context, id, view string) (*turn, error) {
	var page turnsPage
	if err := a.client.call(ctx, "thread/turns/list", map[string]any{"threadId": id, "limit": 1, "sortDirection": "desc", "itemsView": view}, &page); err != nil {
		if notLoaded(err) {
			return nil, execution.ErrNotFound
		}
		return nil, err
	}
	if len(page.Data) == 0 {
		return nil, nil
	}
	t := page.Data[0]
	if t.ID == "" {
		return nil, errors.New("codex returned a turn without an id")
	}
	return &t, nil
}

// archived reports whether Codex archived the thread. An archived thread
// is never loaded, so a loaded one needs no lookup.
func (a *Adapter) archived(ctx context.Context, t thread) (bool, error) {
	if t.Status.Type != statusNotLoaded {
		return false, nil
	}
	var page threadsPage
	if err := a.client.call(ctx, "thread/list", map[string]any{"includedThreadIds": []string{t.ID}, "archived": true, "limit": 1}, &page); err != nil {
		return false, err
	}
	return slices.ContainsFunc(page.Data, func(x thread) bool { return x.ID == t.ID }), nil
}

// Observe reads the thread, its newest turn and whether it is archived.
// A snapshot taken while a just-started turn settles (errUnsettled,
// errUnclosed) is read again for up to observeSettle. An interrupted turn
// that stays without completedAt reads interrupted: Codex will not close
// it.
func (a *Adapter) Observe(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	o, err := a.snapshot(ctx, ref)
	for deadline := time.Now().Add(observeSettle); unsettled(err) && time.Now().Before(deadline); {
		select {
		case <-ctx.Done():
			return o, ctx.Err()
		case <-time.After(observeStep):
		}
		o, err = a.snapshot(ctx, ref)
	}
	if errors.Is(err, errUnclosed) {
		return o, o.Valid()
	}
	return o, err
}

// observeSettle bounds how long Observe rereads an unsettled snapshot
// (the live window is about 20 ms); observeStep spaces the reads.
const (
	observeSettle = 500 * time.Millisecond
	observeStep   = 20 * time.Millisecond
)

func unsettled(err error) bool {
	return errors.Is(err, errUnsettled) || errors.Is(err, errUnclosed)
}

// snapshot is one read of the thread, its newest turn and archive state.
func (a *Adapter) snapshot(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	var o execution.Observation
	if !a.valid(ref) {
		return o, execution.ErrNotFound
	}
	t, err := a.read(ctx, ref.ExternalSessionID)
	if err != nil {
		return o, err
	}
	last, err := a.lastTurn(ctx, t.ID, "summary")
	if err != nil {
		return o, err
	}
	archived, err := a.archived(ctx, t)
	if err != nil {
		return o, err
	}
	return a.observation(t, last, archived)
}

// errUnsettled: Codex shows a turn in progress on a thread it reports not
// running. It happens for a moment after a client that started a turn
// unsubscribes (live in #551), so it is no evidence of an outcome.
var errUnsettled = errors.New("codex shows a turn in progress on a thread it is not running; refresh again")

// errUnclosed: Codex lists the newest turn interrupted without completedAt
// on a loaded thread it reports idle. It happens for a moment after
// turn/start and unsubscribe, before the thread reads active (live in
// #555, every send); a real interrupt has completedAt. The observation
// carries the interrupted state for a reading that persists.
var errUnclosed = errors.New("codex shows an interrupted turn it has not closed; refresh again")

// observation maps a thread snapshot to Stage's states. A turn in progress
// on a thread the daemon is not running, or a system error, is not
// evidence of an outcome: it is reported as an error, never a state.
func (a *Adapter) observation(t thread, last *turn, archived bool) (execution.Observation, error) {
	ref := a.ref(t.ID)
	o := execution.Observation{
		Session: ref, Project: t.Cwd, Directory: t.Cwd, Title: threadTitle(t), State: "idle",
		Links:     execution.Links{Command: a.resumeIn(t.Cwd, t.ID)},
		CreatedAt: unix(t.CreatedAt), UpdatedAt: unix(max(t.UpdatedAt, t.CreatedAt)),
		Archived: archived,
		Origin:   t.Originator,
	}
	if t.GitInfo != nil && t.GitInfo.Branch != nil {
		o.Branch = *t.GitInfo.Branch
	}
	if t.ParentThreadID != nil && *t.ParentThreadID != "" {
		o.Child = true
		o.Parent = &execution.SessionRef{ConnectionID: a.connection, ExternalSessionID: *t.ParentThreadID}
	}
	if t.Model != nil {
		o.Config.Model = *t.Model
	}
	if archived {
		o.Closed = &execution.Closure{Signal: execution.ClosedArchived}
		o.Actions = []execution.ActionState{{ID: ActionArchive, Availability: execution.Availability{Reason: "Already archived in Codex."}}}
	}
	if o.Project == "" {
		return o, errors.New("codex thread has no working directory")
	}
	if last != nil {
		o.ExecutionID = last.ID
		o.StartedAt = unixPtr(last.StartedAt)
	}
	switch t.Status.Type {
	case statusActive:
		o.Active = true
		o.State = "running"
		if slices.Contains(t.Status.ActiveFlags, "waitingOnApproval") || slices.Contains(t.Status.ActiveFlags, "waitingOnUserInput") {
			o.State = "needs_input"
		}
		return o, o.Valid()
	case statusSystemError:
		return o, errors.New("codex reports a system error for this thread; check it in Codex")
	case statusIdle, statusNotLoaded:
	default:
		return o, fmt.Errorf("unknown codex thread status %q", t.Status.Type)
	}
	if last == nil {
		return o, o.Valid()
	}
	switch last.Status {
	case turnCompleted:
		o.State = "done"
	case turnFailed:
		o.State = "failed"
	case turnInterrupted:
		o.State = "interrupted"
	case turnInProgress:
		return o, errUnsettled
	default:
		return o, fmt.Errorf("unknown codex turn status %q", last.Status)
	}
	o.EndedAt = unixPtr(last.CompletedAt)
	o.Reply = finalReply(*last)
	if last.Status == turnInterrupted && last.CompletedAt == nil && t.Status.Type == statusIdle {
		return o, errUnclosed
	}
	return o, o.Valid()
}

// threadTitle: the thread's name, else its first message, one line.
func threadTitle(t thread) string {
	s := t.Preview
	if t.Name != nil && strings.TrimSpace(*t.Name) != "" {
		s = *t.Name
	}
	s = strings.TrimSpace(firstLine(s))
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120])
	}
	return s
}

// finalReply is the turn's final answer (the last agent message in the
// final_answer phase, else the last agent message), bounded.
func finalReply(t turn) *execution.Reply {
	var pick *item
	for i := range t.Items {
		it := &t.Items[i]
		if it.Type != itemAgentMessage || strings.TrimSpace(it.Text) == "" {
			continue
		}
		if it.Phase != nil && *it.Phase == phaseFinalAnswer {
			pick = it
		} else if pick == nil || pick.Phase == nil || *pick.Phase != phaseFinalAnswer {
			pick = it
		}
	}
	if pick == nil {
		return nil
	}
	return bounded(pick.ID, t.ID, pick.Text, replyLimit, unixPtr(t.CompletedAt))
}

func bounded(itemID, runID, text string, limit int, at *time.Time) *execution.Reply {
	r := []rune(text)
	truncated := len(r) > limit
	if truncated {
		text = string(r[:limit])
	}
	rep := &execution.Reply{ItemID: itemID, RunID: runID, Text: text, Truncated: truncated}
	if at != nil {
		rep.UpdatedAt = *at
	}
	return rep
}

// Opening is the thread's first user message.
func (a *Adapter) Opening(ctx context.Context, ref execution.SessionRef, full bool) (*execution.Reply, error) {
	if !a.valid(ref) {
		return nil, execution.ErrNotFound
	}
	var page turnsPage
	if err := a.client.call(ctx, "thread/turns/list", map[string]any{"threadId": ref.ExternalSessionID, "limit": 1, "sortDirection": "asc", "itemsView": "full"}, &page); err != nil {
		if notLoaded(err) {
			return nil, execution.ErrNotFound
		}
		return nil, err
	}
	limit := 2000
	if full {
		limit = 1_000_000
	}
	for _, t := range page.Data {
		for _, it := range t.Items {
			if it.Type != itemUserMessage {
				continue
			}
			if text := userText(it); strings.TrimSpace(text) != "" {
				return bounded(it.ID, t.ID, text, limit, unixPtr(t.StartedAt)), nil
			}
		}
	}
	return nil, execution.ErrNoReply
}

// Act runs a declared session action: archive archives the thread in Codex.
func (a *Adapter) Act(ctx context.Context, r execution.ActionRequest) (execution.ActionResult, error) {
	if r.Target != execution.TargetSession || r.Session == nil {
		return execution.ActionResult{}, &execution.Invalid{Reason: "Codex actions target a session."}
	}
	if r.ID != ActionArchive {
		return execution.ActionResult{}, &execution.Invalid{Reason: "Unknown Codex action."}
	}
	if !a.valid(*r.Session) {
		return execution.ActionResult{}, execution.ErrNotFound
	}
	err := a.client.call(ctx, "thread/archive", map[string]any{"threadId": r.Session.ExternalSessionID}, nil)
	var re *RPCError
	switch {
	case err == nil:
		return execution.ActionResult{Message: "Archived in Codex."}, nil
	case notLoaded(err):
		return execution.ActionResult{}, execution.ErrNotFound
	case errors.As(err, &re):
		return execution.ActionResult{}, &execution.Busy{Reason: "Codex: " + re.Message}
	}
	return execution.ActionResult{}, err
}

// Stop interrupts the thread's running turn. Nothing running: done.
func (a *Adapter) Stop(ctx context.Context, ref execution.SessionRef) error {
	if !a.valid(ref) {
		return execution.ErrNotFound
	}
	t, err := a.read(ctx, ref.ExternalSessionID)
	if err != nil {
		return err
	}
	if t.Status.Type != statusActive {
		return nil
	}
	last, err := a.lastTurn(ctx, t.ID, "notLoaded")
	if err != nil {
		return err
	}
	if last == nil || last.Status != turnInProgress {
		return nil
	}
	err = a.client.call(ctx, "turn/interrupt", map[string]any{"threadId": t.ID, "turnId": last.ID}, nil)
	var re *RPCError
	if errors.As(err, &re) && strings.Contains(re.Message, "no active turn") {
		return nil
	}
	return err
}

// Attention lists the daemon's running threads that wait on the user.
func (a *Adapter) Attention(ctx context.Context) ([]execution.SessionRef, error) {
	var ids []string
	var cursor *string
	for range 20 {
		var page idsPage
		params := map[string]any{"limit": 100}
		if cursor != nil {
			params["cursor"] = *cursor
		}
		if err := a.client.call(ctx, "thread/loaded/list", params, &page); err != nil {
			return nil, err
		}
		ids = append(ids, page.Data...)
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	var out []execution.SessionRef
	for _, id := range ids {
		if !threadID.MatchString(id) {
			continue
		}
		t, err := a.read(ctx, id)
		if errors.Is(err, execution.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if t.Status.Type == statusActive && (slices.Contains(t.Status.ActiveFlags, "waitingOnApproval") || slices.Contains(t.Status.ActiveFlags, "waitingOnUserInput")) {
			out = append(out, a.ref(id))
		}
	}
	return out, nil
}
