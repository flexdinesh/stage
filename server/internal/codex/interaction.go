package codex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Interaction (execution.Interactive). Each call opens a connection of its
// own and resumes the thread (thread/resume), which replays the thread's
// pending server requests to it, acts, then unsubscribes and disconnects;
// the daemon keeps running the turn (live gates g8, g9, g10 in testdata).
// Approvals of commands and file changes and requestUserInput questions
// are listed; permission requests, MCP elicitations and questions with a
// secret answer are answered in Codex (Descriptor.UnlistedRequests).

var _ execution.Interactive = (*Adapter)(nil)

// Server request methods Stage lists, and the notification that clears one.
const (
	methodCommandApproval = "item/commandExecution/requestApproval"
	methodFileApproval    = "item/fileChange/requestApproval"
	methodUserInput       = "item/tool/requestUserInput"
	notifyResolved        = "serverRequest/resolved"
)

// Replay timing: the daemon replays pending requests right after the
// resume reply (about 2 ms in the gates). Stage waits for the first one,
// then for a quiet spell, at most replayWait in all.
var (
	replayQuiet = 250 * time.Millisecond
	replayWait  = 3 * time.Second
)

// pending holds one thread's server requests on a connection by request
// id, in arrival order; serverRequest/resolved removes one. Requests for
// other threads are ignored.
type pending struct {
	thread  string
	changed chan struct{}

	mu    sync.Mutex
	order []string
	reqs  map[string]serverRequest
}

func newPending(thread string) *pending {
	return &pending{thread: thread, changed: make(chan struct{}, 1), reqs: map[string]serverRequest{}}
}

func (p *pending) threadOf(params json.RawMessage) string {
	var v struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(params, &v)
	return v.ThreadID
}

func (p *pending) signal() {
	select {
	case p.changed <- struct{}{}:
	default:
	}
}

// add records a server request (conn's request handler).
func (p *pending) add(r serverRequest) {
	if p.threadOf(r.Params) != p.thread {
		return
	}
	k := idKey(r.ID)
	p.mu.Lock()
	if _, ok := p.reqs[k]; !ok {
		p.order = append(p.order, k)
	}
	p.reqs[k] = r
	p.mu.Unlock()
	p.signal()
}

// notify clears a resolved request (conn's notification handler).
func (p *pending) notify(n notification) {
	if n.Method != notifyResolved || p.threadOf(n.Params) != p.thread {
		return
	}
	var v struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(n.Params, &v) != nil || len(v.RequestID) == 0 {
		return
	}
	k := idKey(v.RequestID)
	p.mu.Lock()
	delete(p.reqs, k)
	p.order = slices.DeleteFunc(p.order, func(x string) bool { return x == k })
	p.mu.Unlock()
	p.signal()
}

// list is the pending requests in arrival order.
func (p *pending) list() []serverRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]serverRequest, 0, len(p.order))
	for _, k := range p.order {
		out = append(out, p.reqs[k])
	}
	return out
}

func (p *pending) size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.order)
}

// settle waits for the replay: the first request, then replayQuiet without
// another, at most replayWait in all.
func (p *pending) settle(ctx context.Context) error {
	deadline := time.NewTimer(replayWait)
	defer deadline.Stop()
	for p.size() == 0 {
		select {
		case <-p.changed:
		case <-deadline.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for {
		quiet := time.NewTimer(replayQuiet)
		select {
		case <-p.changed:
			quiet.Stop()
		case <-quiet.C:
			return nil
		case <-deadline.C:
			quiet.Stop()
			return nil
		case <-ctx.Done():
			quiet.Stop()
			return ctx.Err()
		}
	}
}

// threadSession is a connection subscribed to one thread.
type threadSession struct {
	c      *conn
	thread thread
	reqs   *pending
}

// sessionErr marks a dropped session connection as unavailable.
func sessionErr(err error) error {
	if errors.Is(err, errClosed) {
		return fmt.Errorf("%w: %w", execution.ErrUnavailable, err)
	}
	return err
}

// resume opens a connection and resumes the thread on it. ErrNotFound:
// Codex does not know the thread.
func (a *Adapter) resume(ctx context.Context, id string) (*threadSession, error) {
	reqs := newPending(id)
	c, err := a.client.session(ctx, reqs.notify, reqs.add)
	if err != nil {
		return nil, err
	}
	var res threadResponse
	if err := c.call(ctx, "thread/resume", map[string]any{"threadId": id, "excludeTurns": true}, &res); err != nil {
		c.close()
		if notLoaded(err) {
			return nil, execution.ErrNotFound
		}
		return nil, sessionErr(err)
	}
	if res.Thread.ID != id {
		c.close()
		return nil, errors.New("codex resumed a different thread")
	}
	return &threadSession{c: c, thread: res.Thread, reqs: reqs}, nil
}

// close unsubscribes (the turn keeps running) and disconnects.
func (s *threadSession) close(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = s.c.call(ctx, "thread/unsubscribe", map[string]any{"threadId": s.thread.ID}, nil)
	s.c.close()
}

// waiting reports a thread waiting on an approval or user input.
func waiting(t thread) bool {
	return t.Status.Type == statusActive &&
		(slices.Contains(t.Status.ActiveFlags, "waitingOnApproval") || slices.Contains(t.Status.ActiveFlags, "waitingOnUserInput"))
}

// replayed resumes a waiting thread and collects its replayed requests;
// nil session (and no requests) when the thread waits on nothing.
func (a *Adapter) replayed(ctx context.Context, ref execution.SessionRef) (*threadSession, error) {
	if !a.valid(ref) {
		return nil, execution.ErrNotFound
	}
	t, err := a.read(ctx, ref.ExternalSessionID)
	if err != nil {
		return nil, err
	}
	if !waiting(t) {
		return nil, nil
	}
	s, err := a.resume(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	if !waiting(s.thread) {
		return s, nil
	}
	if err := s.reqs.settle(ctx); err != nil {
		s.close(ctx)
		return nil, err
	}
	return s, nil
}

// Requests lists the thread's pending approvals and questions Stage can
// answer. A thread waiting on nothing is not resumed.
func (a *Adapter) Requests(ctx context.Context, ref execution.SessionRef) ([]execution.Request, error) {
	s, err := a.replayed(ctx, ref)
	if err != nil || s == nil {
		return nil, err
	}
	defer s.close(ctx)
	out := []execution.Request{}
	for _, r := range s.reqs.list() {
		if req, ok := listed(r); ok {
			out = append(out, req)
		}
	}
	return out, nil
}

// Respond answers a listed request: an approval with one of its options,
// a question request with an answer for every question. ErrNotFound: no
// longer pending (or a kind Stage does not list).
func (a *Adapter) Respond(ctx context.Context, ref execution.SessionRef, requestID string, r execution.Response) error {
	s, err := a.replayed(ctx, ref)
	if err != nil {
		return err
	}
	if s == nil {
		return execution.ErrNotFound
	}
	defer s.close(ctx)
	for _, sr := range s.reqs.list() {
		req, ok := listed(sr)
		if !ok || req.ID != requestID {
			continue
		}
		result, err := answer(sr, req, r)
		if err != nil {
			return err
		}
		return sessionErr(s.c.respond(ctx, sr.ID, result))
	}
	return execution.ErrNotFound
}

// Wire shapes of the listed server requests (codex-cli 0.172.0, gates g9
// and g10).
type approvalParams struct {
	ItemID             string            `json:"itemId"`
	ApprovalID         *string           `json:"approvalId"`
	Reason             *string           `json:"reason"`
	Command            *string           `json:"command"`
	GrantRoot          *string           `json:"grantRoot"`
	StartedAtMs        *int64            `json:"startedAtMs"`
	AvailableDecisions []json.RawMessage `json:"availableDecisions"`
}

type userInputParams struct {
	ItemID    string `json:"itemId"`
	Questions []struct {
		ID          string `json:"id"`
		Header      string `json:"header"`
		Question    string `json:"question"`
		IsOther     bool   `json:"isOther"`
		IsSecret    bool   `json:"isSecret"`
		MultiSelect bool   `json:"multiSelect"`
		Options     []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

// Approval decisions Stage offers, in Codex's order, labelled by outcome in
// Codex's words. Decisions that carry an amendment (an exec policy or
// network rule) are left to Codex.
const (
	decisionAccept           = "accept"
	decisionAcceptForSession = "acceptForSession"
	decisionDecline          = "decline"
	decisionCancel           = "cancel"
)

var decisions = []string{decisionAccept, decisionAcceptForSession, decisionDecline, decisionCancel}

var decisionLabels = map[string]map[string]string{
	methodCommandApproval: {
		decisionAccept:           "Run command",
		decisionAcceptForSession: "Run, and don't ask again for this command",
		decisionDecline:          "Don't run",
		decisionCancel:           "Stop the turn",
	},
	methodFileApproval: {
		decisionAccept:           "Apply edits",
		decisionAcceptForSession: "Apply, and don't ask again for these files",
		decisionDecline:          "Don't apply",
		decisionCancel:           "Stop the turn",
	},
}

const fileChangeHidden = "Stage can't show the edits: Codex shares them only with the session that made them. To read the diff first, open the thread with the resume command."

// listed maps a server request Stage can answer; false for other kinds.
func listed(r serverRequest) (execution.Request, bool) {
	switch r.Method {
	case methodCommandApproval, methodFileApproval:
		var p approvalParams
		if json.Unmarshal(r.Params, &p) != nil {
			return execution.Request{}, false
		}
		id := p.ItemID
		if p.ApprovalID != nil && *p.ApprovalID != "" {
			id = *p.ApprovalID
		}
		if id == "" {
			return execution.Request{}, false
		}
		req := execution.Request{ID: id, Kind: execution.RequestApproval, Options: approvalOptions(r.Method, p)}
		if p.StartedAtMs != nil && *p.StartedAtMs > 0 {
			req.CreatedAt = time.UnixMilli(*p.StartedAtMs).UTC()
		}
		reason := ""
		if p.Reason != nil {
			reason = strings.TrimSpace(*p.Reason)
		}
		if r.Method == methodCommandApproval {
			req.Title, req.Detail = "Run a command", reason
			if p.Command != nil {
				req.Preview = *p.Command
			}
		} else {
			req.Title = "Edit files"
			lines := []string{}
			if reason != "" {
				lines = append(lines, reason)
			}
			if p.GrantRoot != nil && *p.GrantRoot != "" {
				lines = append(lines, "Also lets Codex write anywhere under "+*p.GrantRoot+" for the rest of this session.")
			}
			req.Detail = strings.Join(append(lines, fileChangeHidden), " ")
		}
		return req, len(req.Options) > 0
	case methodUserInput:
		var p userInputParams
		if json.Unmarshal(r.Params, &p) != nil || p.ItemID == "" || len(p.Questions) == 0 {
			return execution.Request{}, false
		}
		req := execution.Request{ID: p.ItemID, Kind: execution.RequestQuestion, Title: "Codex asks", Questions: make([]execution.Question, 0, len(p.Questions))}
		if len(p.Questions) > 1 {
			req.Title = fmt.Sprintf("Codex asks %d questions", len(p.Questions))
		}
		for _, q := range p.Questions {
			if q.IsSecret || q.ID == "" {
				return execution.Request{}, false
			}
			eq := execution.Question{ID: q.ID, Header: q.Header, Prompt: q.Question, Multi: q.MultiSelect, Options: []execution.QuestionOption{}}
			if strings.TrimSpace(eq.Prompt) == "" {
				eq.Prompt = q.ID
			}
			for _, o := range q.Options {
				eq.Options = append(eq.Options, execution.QuestionOption{Label: o.Label, Description: o.Description})
			}
			eq.Custom = q.IsOther || len(eq.Options) == 0
			req.Questions = append(req.Questions, eq)
		}
		return req, true
	}
	return execution.Request{}, false
}

// approvalOptions are the plain decisions the request offers, in Codex's
// order; all four when it names none (file changes never do).
func approvalOptions(method string, p approvalParams) []execution.Choice {
	offered := []string{}
	for _, raw := range p.AvailableDecisions {
		var d string
		if json.Unmarshal(raw, &d) == nil && slices.Contains(decisions, d) && !slices.Contains(offered, d) {
			offered = append(offered, d)
		}
	}
	if len(p.AvailableDecisions) == 0 {
		offered = decisions
	}
	out := make([]execution.Choice, 0, len(offered))
	for _, d := range offered {
		out = append(out, execution.Choice{ID: d, Label: decisionLabels[method][d], Availability: available})
	}
	return out
}

// answer is the server request's result for r; *execution.Invalid when r
// does not fit the request.
func answer(sr serverRequest, req execution.Request, r execution.Response) (any, error) {
	if req.Kind == execution.RequestApproval {
		if len(r.Answers) > 0 || strings.TrimSpace(r.Text) != "" {
			return nil, &execution.Invalid{Reason: "Answer a Codex approval with one of its options; it takes no text."}
		}
		if !slices.ContainsFunc(req.Options, func(c execution.Choice) bool { return c.ID == r.Option }) {
			return nil, &execution.Invalid{Reason: fmt.Sprintf("Codex does not offer %q for this request.", r.Option)}
		}
		return map[string]any{"decision": r.Option}, nil
	}
	if r.Option != "" || strings.TrimSpace(r.Text) != "" {
		return nil, &execution.Invalid{Reason: "Answer each of Codex's questions by its id."}
	}
	for qid := range r.Answers {
		if !slices.ContainsFunc(req.Questions, func(q execution.Question) bool { return q.ID == qid }) {
			return nil, &execution.Invalid{Reason: fmt.Sprintf("Codex asked no question %q.", qid)}
		}
	}
	out := map[string]any{}
	for _, q := range req.Questions {
		as := r.Answers[q.ID]
		if len(as) == 0 {
			return nil, &execution.Invalid{Reason: fmt.Sprintf("Answer Codex's question %q.", q.Prompt)}
		}
		if !q.Multi && len(as) > 1 {
			return nil, &execution.Invalid{Reason: fmt.Sprintf("Codex's question %q takes one answer.", q.Prompt)}
		}
		custom := 0
		for _, a := range as {
			if strings.TrimSpace(a) == "" {
				return nil, &execution.Invalid{Reason: "An answer to Codex is blank."}
			}
			if !slices.ContainsFunc(q.Options, func(o execution.QuestionOption) bool { return o.Label == a }) {
				custom++
			}
		}
		if custom > 1 || (custom == 1 && !q.Custom) {
			return nil, &execution.Invalid{Reason: fmt.Sprintf("Pick one of the options Codex offers for %q.", q.Prompt)}
		}
		out[q.ID] = map[string]any{"answers": as}
	}
	return map[string]any{"answers": out}, nil
}

// Busy reasons for a send.
const (
	busyWorking = "Codex is working. Send once it finishes."
	busyWaiting = "Codex is waiting on an approval or question. Answer that first."
)

func busyFor(t thread) error {
	if t.Status.Type != statusActive {
		return nil
	}
	if waiting(t) {
		return &execution.Busy{Reason: busyWaiting}
	}
	return &execution.Busy{Reason: busyWorking}
}

// Send starts a turn with the message on an idle thread: thread/resume,
// turn/start with the message's request id as its client message id, then
// unsubscribe. A thread with a turn in progress is busy (Stage never
// steers a running turn). A message whose request id the newest turn
// already carries was delivered: nothing is sent again. An archived thread
// is *execution.Refused.
func (a *Adapter) Send(ctx context.Context, ref execution.SessionRef, m execution.Message) error {
	if err := m.Delivery.RequireSend(); err != nil {
		return err
	}
	if !a.valid(ref) {
		return execution.ErrNotFound
	}
	t, err := a.read(ctx, ref.ExternalSessionID)
	if err != nil {
		return err
	}
	if err := busyFor(t); err != nil {
		return err
	}
	s, err := a.resume(ctx, t.ID)
	var re *RPCError
	if errors.As(err, &re) && strings.Contains(re.Message, "is archived") {
		// Codex takes no message until the user unarchives it there.
		return &execution.Refused{Reason: "Codex: " + re.Message}
	}
	if err != nil {
		return err
	}
	defer s.close(ctx)
	if err := busyFor(s.thread); err != nil {
		return err
	}
	clientID := m.RequestID
	if clientID == "" {
		clientID = randomID()
	} else if delivered, err := s.delivered(ctx, clientID); err != nil {
		return err
	} else if delivered {
		return nil
	}
	input := []map[string]any{{"type": "text", "text": m.Text, "text_elements": []any{}}}
	err = s.c.call(ctx, "turn/start", map[string]any{"threadId": t.ID, "input": input, "clientUserMessageId": clientID}, nil)
	if errors.As(err, &re) && re.Code == codeInvalidRequest {
		// Refused (a turn started meanwhile, or the thread can't run one):
		// nothing was sent.
		return &execution.Busy{Reason: "Codex: " + re.Message}
	}
	return sessionErr(err)
}

// delivered reports whether the newest turn's user message carries
// clientID.
func (s *threadSession) delivered(ctx context.Context, clientID string) (bool, error) {
	var page turnsPage
	if err := s.c.call(ctx, "thread/turns/list", map[string]any{"threadId": s.thread.ID, "limit": 1, "sortDirection": "desc", "itemsView": "full"}, &page); err != nil {
		return false, sessionErr(err)
	}
	for _, tu := range page.Data {
		for _, it := range tu.Items {
			if it.Type == itemUserMessage && it.ClientID != nil && *it.ClientID == clientID {
				return true, nil
			}
		}
	}
	return false, nil
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "stage-" + hex.EncodeToString(b)
}
