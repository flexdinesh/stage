package t3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Pending user questions (t3_pending_request; t3code apps/server/src/mcp/
// toolkits/thread/tools.ts + handlers.ts). Approvals are never listed and
// cannot be answered here. Answer rules mirror t3code packages/client-runtime/
// src/state/pendingUserInput.ts.

var (
	// ErrRequestAnswered: the user-input request was already answered.
	ErrRequestAnswered = errors.New("t3: request already answered")
	// ErrRequestStale: the request is gone or no longer pending.
	ErrRequestStale = errors.New("t3: request no longer pending")
)

// Question is one T3 user-input question.
type Question struct {
	ID                string           `json:"id"`
	Header            string           `json:"header"`
	Question          string           `json:"question"`
	Options           []QuestionOption `json:"options"`
	MultiSelect       bool             `json:"multiSelect"`
	AllowCustomAnswer *bool            `json:"allowCustomAnswer"` // nil = allowed
	InitialAnswer     *string          `json:"initialAnswer"`
	Required          *bool            `json:"required"`
}

// QuestionOption: Value nil = answered by Label.
type QuestionOption struct {
	Label       string  `json:"label"`
	Description string  `json:"description"`
	Value       *string `json:"value"`
}

// PendingQuestion is a t3_pending_request read result.
type PendingQuestion struct {
	RequestID string     `json:"requestId"`
	Questions []Question `json:"questions"`
}

// Answer is one question's answer: a string (option value/label or custom
// text) or, for multi-select, an array of option values/labels.
type Answer struct {
	Text   string
	Values []string
	Multi  bool
}

func TextAnswer(s string) Answer     { return Answer{Text: s} }
func ValuesAnswer(v []string) Answer { return Answer{Values: v, Multi: true} }

// CustomAllowed: T3 accepts free text unless allowCustomAnswer is false.
func (q Question) CustomAllowed() bool { return q.AllowCustomAnswer == nil || *q.AllowCustomAnswer }

func (a Answer) MarshalJSON() ([]byte, error) {
	if a.Multi {
		if a.Values == nil {
			return []byte("[]"), nil
		}
		return json.Marshal(a.Values)
	}
	return json.Marshal(a.Text)
}

func (a *Answer) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*a = TextAnswer(s)
		return nil
	}
	var v []string
	if err := json.Unmarshal(data, &v); err != nil {
		return errors.New("answer must be a string or an array of strings")
	}
	*a = ValuesAnswer(v)
	return nil
}

// optionValue resolves a submitted value to the option answer T3 expects:
// exact option value, else trimmed label of an option without a value.
func optionValue(q Question, v string) (string, bool) {
	for _, o := range q.Options {
		if o.Value != nil && *o.Value == v {
			return v, true
		}
	}
	label := strings.TrimSpace(v)
	if label == "" {
		return "", false
	}
	for _, o := range q.Options {
		if o.Value == nil && strings.TrimSpace(o.Label) == label {
			return label, true
		}
	}
	return "", false
}

// ValidateAnswers checks answers against questions by T3's client rules:
// every question answered, no unknown ids, arrays only for multi-select
// (known options, no duplicates), custom text only when allowed.
func ValidateAnswers(questions []Question, answers map[string]Answer) error {
	ids := map[string]bool{}
	for _, q := range questions {
		ids[q.ID] = true
		a, ok := answers[q.ID]
		if !ok {
			return fmt.Errorf("question %q is unanswered", q.ID)
		}
		if a.Multi {
			if !q.MultiSelect {
				return fmt.Errorf("question %q takes a single answer", q.ID)
			}
			if len(a.Values) == 0 {
				return fmt.Errorf("question %q needs at least one option", q.ID)
			}
			var seen []string
			for _, v := range a.Values {
				r, ok := optionValue(q, v)
				if !ok {
					return fmt.Errorf("question %q has no option %q", q.ID, v)
				}
				if slices.Contains(seen, r) {
					return fmt.Errorf("question %q repeats option %q", q.ID, v)
				}
				seen = append(seen, r)
			}
			continue
		}
		if _, ok := optionValue(q, a.Text); ok {
			continue
		}
		if !q.CustomAllowed() {
			return fmt.Errorf("question %q needs one of its options", q.ID)
		}
		if strings.TrimSpace(a.Text) == "" && q.InitialAnswer == nil {
			return fmt.Errorf("question %q is unanswered", q.ID)
		}
	}
	for id := range answers {
		if !ids[id] {
			return fmt.Errorf("unknown question %q", id)
		}
	}
	return nil
}

// NormalizeAnswers maps option labels/values to the exact strings T3 expects
// (trimmed label or exact value). Call after ValidateAnswers.
func NormalizeAnswers(questions []Question, answers map[string]Answer) map[string]Answer {
	out := make(map[string]Answer, len(answers))
	for _, q := range questions {
		a := answers[q.ID]
		if a.Multi {
			vals := make([]string, 0, len(a.Values))
			for _, v := range a.Values {
				r, _ := optionValue(q, v)
				vals = append(vals, r)
			}
			out[q.ID] = ValuesAnswer(vals)
			continue
		}
		if r, ok := optionValue(q, a.Text); ok {
			out[q.ID] = TextAnswer(r)
			continue
		}
		out[q.ID] = a
	}
	return out
}

// requestError maps T3 invalid_request texts (handlers.ts readQuestion).
func requestError(e *ToolError, target error) bool {
	if e.Code != "invalid_request" {
		return false
	}
	msg := strings.ToLower(e.Message)
	switch target {
	case ErrRequestAnswered:
		return strings.Contains(msg, "already answered")
	case ErrRequestStale:
		return strings.Contains(msg, "user-input request was not found") || strings.Contains(msg, "no longer pending")
	}
	return false
}

func (c *MCPClient) ListPendingQuestions(ctx context.Context, threadID string) ([]string, error) {
	if threadID == "" {
		return nil, ErrNotFound
	}
	var out struct {
		RequestIDs []string `json:"requestIds"`
	}
	if err := c.call(ctx, "t3_pending_request", map[string]any{"action": "list", "threadId": threadID}, &out); err != nil {
		return nil, err
	}
	return out.RequestIDs, nil
}

func (c *MCPClient) ReadPendingQuestion(ctx context.Context, threadID, requestID string) (PendingQuestion, error) {
	if threadID == "" {
		return PendingQuestion{}, ErrNotFound
	}
	var out PendingQuestion
	err := c.call(ctx, "t3_pending_request", map[string]any{"action": "read", "threadId": threadID, "requestId": requestID}, &out)
	if err == nil && out.RequestID != requestID {
		err = fmt.Errorf("t3 t3_pending_request: read returned request %q, want %q", out.RequestID, requestID)
	}
	return out, err
}

func (c *MCPClient) RespondPendingQuestion(ctx context.Context, threadID, requestID string, answers map[string]Answer) error {
	if threadID == "" {
		return ErrNotFound
	}
	return c.call(ctx, "t3_pending_request", map[string]any{
		"action": "respond", "threadId": threadID, "requestId": requestID, "answers": answers,
	}, nil)
}
