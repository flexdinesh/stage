package t3

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Answering a thread's questions and sending it messages from Stage
// (execution.Interactive). T3's MCP lists and answers user-input questions
// only: a permission request is answered in T3 (Descriptor.UnlistedRequests),
// and a send waits until no request is pending.

var _ execution.Interactive = (*Adapter)(nil)

// Busy reasons of Send.
const (
	busyQuestions = "T3 is waiting for answers to its questions. Answer them first."
	busyApproval  = "T3 is waiting on a permission request. Approve it in T3, then send."
	busyRequest   = "T3 is waiting on a request. Answer it first."
)

const turnEnded = "The turn ended; send instead."

// Requests are the thread's pending user-input questions, one question
// request each. A request answered while they are read is left out.
func (a *Adapter) Requests(ctx context.Context, ref execution.SessionRef) ([]execution.Request, error) {
	if ref.ConnectionID != a.ConnectionID {
		return nil, execution.ErrUnavailable
	}
	ids, err := a.Client.ListPendingQuestions(ctx, ref.ExternalSessionID)
	if errors.Is(err, ErrNotFound) {
		return nil, execution.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	seen := a.asked.seen(ref.ExternalSessionID, ids, time.Now())
	out := make([]execution.Request, 0, len(ids))
	for _, id := range ids {
		pq, err := a.Client.ReadPendingQuestion(ctx, ref.ExternalSessionID, id)
		if gone(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		req := questionRequest(pq)
		req.CreatedAt = seen[id]
		out = append(out, req)
	}
	return out, nil
}

// askedRequests remembers when this process first listed each pending
// request (T3 reports no request time), per thread.
type askedRequests struct {
	mu sync.Mutex
	at map[string]map[string]time.Time // thread id -> request id -> first seen
}

// seen records ids as the thread's pending requests at now, forgetting the
// thread's requests no longer listed, and returns when each was first seen.
func (r *askedRequests) seen(thread string, ids []string, now time.Time) map[string]time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev := r.at[thread]
	next := make(map[string]time.Time, len(ids))
	for _, id := range ids {
		if t, ok := prev[id]; ok {
			next[id] = t
		} else {
			next[id] = now
		}
	}
	if r.at == nil {
		r.at = map[string]map[string]time.Time{}
	}
	if len(next) == 0 {
		delete(r.at, thread)
	} else {
		r.at[thread] = next
	}
	return maps.Clone(next)
}

// gone: the request (or its thread) is no longer pending.
func gone(err error) bool {
	return errors.Is(err, ErrRequestAnswered) || errors.Is(err, ErrRequestStale) || errors.Is(err, ErrNotFound)
}

func questionRequest(pq PendingQuestion) execution.Request {
	req := execution.Request{ID: pq.RequestID, Kind: execution.RequestQuestion, Title: "T3 asks", Questions: []execution.Question{}}
	if len(pq.Questions) > 1 {
		req.Title = fmt.Sprintf("T3 asks %d questions", len(pq.Questions))
	}
	for _, q := range pq.Questions {
		out := execution.Question{ID: q.ID, Header: q.Header, Prompt: q.Question, Multi: q.MultiSelect, Custom: q.CustomAllowed(), Options: []execution.QuestionOption{}}
		for _, o := range q.Options {
			out.Options = append(out.Options, execution.QuestionOption{Label: o.Label, Description: o.Description, Preview: o.Preview})
		}
		req.Questions = append(req.Questions, out)
	}
	return req
}

// Respond answers a pending question request: each question by option
// labels (sent as T3's option values) or one custom text where allowed.
func (a *Adapter) Respond(ctx context.Context, ref execution.SessionRef, requestID string, r execution.Response) error {
	if ref.ConnectionID != a.ConnectionID {
		return execution.ErrUnavailable
	}
	pq, err := a.Client.ReadPendingQuestion(ctx, ref.ExternalSessionID, requestID)
	if gone(err) {
		return execution.ErrNotFound
	}
	if err != nil {
		return err
	}
	if len(r.Answers) == 0 {
		return &execution.Invalid{Reason: "Answer T3's questions with answers (an option or text alone does not fit)."}
	}
	answers, err := toAnswers(pq.Questions, r.Answers)
	if err != nil {
		return &execution.Invalid{Reason: err.Error()}
	}
	err = a.Client.RespondPendingQuestion(ctx, ref.ExternalSessionID, requestID, answers)
	var te *ToolError
	switch {
	case gone(err):
		return execution.ErrNotFound
	case errors.As(err, &te) && te.Code == "invalid_request":
		return &execution.Invalid{Reason: te.Message}
	}
	return err
}

// toAnswers maps Stage answers (option labels or custom text, by question
// id) to T3's, checked by T3's own rules.
func toAnswers(questions []Question, in map[string][]string) (map[string]Answer, error) {
	byID := make(map[string]Question, len(questions))
	for _, q := range questions {
		byID[q.ID] = q
	}
	out := make(map[string]Answer, len(in))
	for id, values := range in {
		q, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("unknown question %q", id)
		}
		resolved := make([]string, 0, len(values))
		options := true
		for _, v := range values {
			value, ok := labelValue(q, v)
			options = options && ok
			resolved = append(resolved, value)
		}
		switch {
		case q.MultiSelect && options && len(values) > 0:
			out[id] = ValuesAnswer(resolved)
		case len(values) == 1:
			out[id] = TextAnswer(resolved[0])
		case q.MultiSelect:
			return nil, fmt.Errorf("question %q takes its options or one custom text", id)
		default:
			return nil, fmt.Errorf("question %q takes a single answer", id)
		}
	}
	if err := ValidateAnswers(questions, out); err != nil {
		return nil, err
	}
	return NormalizeAnswers(questions, out), nil
}

// labelValue is the T3 answer for option label v (its value, else its
// label); v itself, false when no option has that label.
func labelValue(q Question, v string) (string, bool) {
	label := strings.TrimSpace(v)
	for _, o := range q.Options {
		if label != "" && strings.TrimSpace(o.Label) == label {
			if o.Value != nil {
				return *o.Value, true
			}
			return label, true
		}
	}
	return v, false
}

// Send uses T3's explicit steer and queue modes. The same native queue
// mode starts a new turn for Send and queues the next turn for Queue.
func (a *Adapter) Send(ctx context.Context, ref execution.SessionRef, m execution.Message) error {
	op, err := m.Delivery.Operation()
	if err != nil {
		return err
	}
	if ref.ConnectionID != a.ConnectionID {
		return execution.ErrUnavailable
	}
	d, err := a.Client.ReadThread(ctx, ref.ExternalSessionID)
	if errors.Is(err, ErrNotFound) {
		return execution.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !d.Archived && d.PendingRequestCount > 0 {
		ids, err := a.Client.ListPendingQuestions(ctx, ref.ExternalSessionID)
		switch {
		case err != nil:
			return &execution.Busy{Reason: busyRequest}
		case len(ids) == 0:
			return &execution.Busy{Reason: busyApproval}
		}
		return &execution.Busy{Reason: busyQuestions}
	}
	mode := SendQueue
	if !d.Archived {
		switch {
		case op == execution.OpSend && d.ActiveRunID != "":
			return &execution.Busy{Reason: "A turn is running; steer or queue instead."}
		case op != execution.OpSend && d.ActiveRunID == "":
			return &execution.Busy{Reason: turnEnded}
		}
	}
	if op == execution.OpSteer {
		mode = SendSteer
	}
	err = a.Client.SendMessage(ctx, ref.ExternalSessionID, m.Text, mode, m.RequestID)
	return sendError(err)
}

func sendError(err error) error {
	var te *ToolError
	switch {
	case errors.Is(err, ErrNotFound):
		return execution.ErrNotFound
	case errors.As(err, &te):
		if te.Code == "thread_not_sendable" && strings.Contains(te.Message, "has no running turn that can be steered") {
			return &execution.Busy{Reason: te.Message}
		}
		return &execution.Refused{Reason: te.Message}
	}
	return err
}
