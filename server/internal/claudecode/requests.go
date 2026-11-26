package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Claude Code's pending permission prompts and questions, answered through
// the PermissionRequest hook (hooks.go). Option ids of an approval.
const (
	optionAllow  = "allow"
	optionAlways = "always:" // + the suggestion's index in the hook input
	optionDeny   = "deny"
)

// toolAskUserQuestion is Claude's question tool; its answers go back as the
// tool's updated input.
const toolAskUserQuestion = "AskUserQuestion"

const (
	defaultDenial = "Denied in Stage."
	denialPrefix  = "Denied in Stage: "
	maxRuleLabel  = 80
)

var _ execution.Interactive = (*Adapter)(nil)

// waiting reports whether a listed session waits for its user.
func waiting(e Entry) bool {
	if e.Background() {
		return e.State == "blocked"
	}
	return e.Status == "waiting"
}

// hooksRunning is whether approvals can be answered in Stage: the adapter
// has the machine's hook receiver (none: answer in Claude Code).
func (a *Adapter) hooksRunning() execution.Availability {
	if a.hooks == nil {
		return execution.Availability{Reason: reasonAnswer}
	}
	return available
}

// Requests are the open hook requests of a session that still waits. Once
// the session list shows it no longer waiting (answered in Claude Code),
// its requests are released. A session list that can't be read keeps them
// (Respond reads it again).
func (a *Adapter) Requests(ctx context.Context, ref execution.SessionRef) ([]execution.Request, error) {
	if ref.ConnectionID != a.conn || a.hooks == nil {
		return []execution.Request{}, nil
	}
	ps := a.hooks.open(ref.ExternalSessionID)
	if len(ps) == 0 {
		return []execution.Request{}, nil
	}
	if es, err := a.roster.list(ctx, false); err == nil {
		if e, ok := findSession(es, ref.ExternalSessionID); !ok || !waiting(e) {
			a.hooks.release(ref.ExternalSessionID)
			return []execution.Request{}, nil
		}
	}
	out := make([]execution.Request, 0, len(ps))
	for _, p := range ps {
		out = append(out, toRequest(p))
	}
	return out, nil
}

// Respond answers an open request after checking, on a fresh session list,
// that the session still waits (an answer in Claude Code may have come
// first; a late hook answer would be ignored).
func (a *Adapter) Respond(ctx context.Context, ref execution.SessionRef, requestID string, r execution.Response) error {
	if ref.ConnectionID != a.conn || a.hooks == nil {
		return execution.ErrNotFound
	}
	p, ok := a.hooks.find(ref.ExternalSessionID, requestID)
	if !ok {
		return execution.ErrNotFound
	}
	body, err := decision(p, r)
	if err != nil {
		return err
	}
	es, err := a.roster.list(ctx, true)
	if err != nil {
		return err
	}
	if e, ok := findSession(es, ref.ExternalSessionID); !ok || !waiting(e) {
		a.hooks.release(ref.ExternalSessionID)
		return execution.ErrNotFound
	}
	return a.hooks.answer(ctx, ref.ExternalSessionID, requestID, body)
}

// askInput is AskUserQuestion's input.
type askInput struct {
	Questions []struct {
		Question string `json:"question"`
		Header   string `json:"header"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
		MultiSelect bool `json:"multiSelect"`
	} `json:"questions"`
}

// suggestion is one permission_suggestions entry (Claude's "always" choices).
type suggestion struct {
	Type        string `json:"type"`
	Destination string `json:"destination"`
	Behavior    string `json:"behavior"`
	Mode        string `json:"mode"`
	Rules       []struct {
		ToolName    string `json:"toolName"`
		RuleContent string `json:"ruleContent"`
	} `json:"rules"`
}

// toRequest maps an open hook request.
func toRequest(p *pendingRequest) execution.Request {
	if p.tool == toolAskUserQuestion {
		if q, ok := questionRequest(p); ok {
			return q
		}
	}
	title, preview := describeTool(p.tool, p.input)
	req := execution.Request{
		ID: p.id, Kind: execution.RequestApproval, Title: title, Preview: preview, TextOption: optionDeny, CreatedAt: p.created,
		Detail: inputString(p.input, "description"),
	}
	req.Options = append(req.Options, execution.Choice{ID: optionAllow, Label: "Allow", Availability: available})
	for i, raw := range p.suggestions {
		if label, ok := alwaysLabel(raw); ok {
			req.Options = append(req.Options, execution.Choice{ID: optionAlways + strconv.Itoa(i), Label: label, Availability: available})
		}
	}
	req.Options = append(req.Options, execution.Choice{ID: optionDeny, Label: "Deny", Availability: available})
	return req
}

func questionRequest(p *pendingRequest) (execution.Request, bool) {
	var in askInput
	if json.Unmarshal(p.input, &in) != nil || len(in.Questions) == 0 {
		return execution.Request{}, false
	}
	req := execution.Request{ID: p.id, Kind: execution.RequestQuestion, Title: "Claude Code asks", CreatedAt: p.created}
	if len(in.Questions) > 1 {
		req.Title = fmt.Sprintf("Claude Code asks %d questions", len(in.Questions))
	}
	for i, q := range in.Questions {
		if strings.TrimSpace(q.Question) == "" {
			return execution.Request{}, false
		}
		out := execution.Question{ID: strconv.Itoa(i), Header: q.Header, Prompt: q.Question, Multi: q.MultiSelect, Custom: true, Options: []execution.QuestionOption{}}
		for _, o := range q.Options {
			out.Options = append(out.Options, execution.QuestionOption{Label: o.Label, Description: o.Description})
		}
		req.Questions = append(req.Questions, out)
	}
	return req, true
}

// describeTool is an approval's title and preview.
func describeTool(tool string, input json.RawMessage) (string, string) {
	switch tool {
	case "Bash":
		return "Run a command", inputString(input, "command")
	case "Edit", "MultiEdit", "Write":
		return "Edit a file", inputString(input, "file_path")
	case "NotebookEdit":
		return "Edit a notebook", inputString(input, "notebook_path")
	case "WebFetch":
		return "Fetch a URL", inputString(input, "url")
	case "WebSearch":
		return "Search the web", inputString(input, "query")
	}
	return "Use " + tool, compactJSON(input)
}

func inputString(input json.RawMessage, key string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(m[key], &s) != nil {
		return ""
	}
	return s
}

// compactJSON is input on one line, bounded.
func compactJSON(input json.RawMessage) string {
	var v any
	if json.Unmarshal(input, &v) != nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	s, cut := truncate(string(b), 2000)
	if cut {
		s += "…"
	}
	return s
}

// alwaysLabel describes a suggestion Stage offers: allow rules, directory
// access and accept-edits; others are left to Claude Code.
func alwaysLabel(raw json.RawMessage) (string, bool) {
	var s suggestion
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	where := destinationLabel(s.Destination)
	if where == "" {
		return "", false
	}
	switch s.Type {
	case "addRules":
		if s.Behavior != "allow" || len(s.Rules) == 0 {
			return "", false
		}
		rules := make([]string, 0, len(s.Rules))
		for _, r := range s.Rules {
			if r.ToolName == "" {
				return "", false
			}
			rule := r.ToolName
			if r.RuleContent != "" {
				rule += "(" + r.RuleContent + ")"
			}
			rules = append(rules, rule)
		}
		text, cut := truncate(strings.Join(rules, ", "), maxRuleLabel)
		if cut {
			text += "…"
		}
		return "Always allow " + text + " " + where, true
	case "addDirectories":
		var d struct {
			Directories []string `json:"directories"`
		}
		if json.Unmarshal(raw, &d) != nil || len(d.Directories) == 0 {
			return "", false
		}
		return "Always allow access to " + strings.Join(d.Directories, ", ") + " " + where, true
	case "setMode":
		if s.Mode != "acceptEdits" {
			return "", false
		}
		return "Allow and accept edits " + where, true
	}
	return "", false
}

func destinationLabel(d string) string {
	switch d {
	case "session":
		return "for this session"
	case "localSettings":
		return "in this project (.claude/settings.local.json)"
	case "projectSettings":
		return "in this project (.claude/settings.json)"
	case "userSettings":
		return "in every project (~/.claude/settings.json)"
	}
	return ""
}

// decision is the hook response body answering p with r.
func decision(p *pendingRequest, r execution.Response) ([]byte, error) {
	if p.tool == toolAskUserQuestion {
		if _, ok := questionRequest(p); ok {
			return questionDecision(p, r)
		}
	}
	if len(r.Answers) > 0 {
		return nil, &execution.Invalid{Reason: "This request takes an option, not answers."}
	}
	text := strings.TrimSpace(r.Text)
	var d map[string]any
	switch {
	case r.Option == optionAllow:
		d = map[string]any{"behavior": "allow"}
	case r.Option == optionDeny:
		// Claude reads the message as the tool's result: say it is a denial.
		message := defaultDenial
		if text != "" {
			message = denialPrefix + text
		}
		d = map[string]any{"behavior": "deny", "message": message}
	case strings.HasPrefix(r.Option, optionAlways):
		i, err := strconv.Atoi(strings.TrimPrefix(r.Option, optionAlways))
		if err != nil || i < 0 || i >= len(p.suggestions) {
			return nil, &execution.Invalid{Reason: fmt.Sprintf("Unknown option %q.", r.Option)}
		}
		if _, ok := alwaysLabel(p.suggestions[i]); !ok {
			return nil, &execution.Invalid{Reason: fmt.Sprintf("Unknown option %q.", r.Option)}
		}
		d = map[string]any{"behavior": "allow", "updatedPermissions": []json.RawMessage{p.suggestions[i]}}
	default:
		return nil, &execution.Invalid{Reason: fmt.Sprintf("Unknown option %q: pick allow, an always option or deny.", r.Option)}
	}
	if text != "" && r.Option != optionDeny {
		return nil, &execution.Invalid{Reason: "Only a denial takes text."}
	}
	return hookDecision(d)
}

// questionDecision allows AskUserQuestion with the answers added to its
// input ({question text: answer}; several picks join with ", ").
func questionDecision(p *pendingRequest, r execution.Response) ([]byte, error) {
	if r.Option != "" || strings.TrimSpace(r.Text) != "" {
		return nil, &execution.Invalid{Reason: "Answer each question."}
	}
	var in askInput
	if err := json.Unmarshal(p.input, &in); err != nil {
		return nil, err
	}
	answers := map[string]string{}
	for i, q := range in.Questions {
		var picks []string
		for _, v := range r.Answers[strconv.Itoa(i)] {
			if v = strings.TrimSpace(v); v != "" {
				picks = append(picks, v)
			}
		}
		switch {
		case len(picks) == 0:
			return nil, &execution.Invalid{Reason: fmt.Sprintf("Answer %q.", q.Question)}
		case len(picks) > 1 && !q.MultiSelect:
			return nil, &execution.Invalid{Reason: fmt.Sprintf("Pick one answer for %q.", q.Question)}
		}
		answers[q.Question] = strings.Join(picks, ", ")
	}
	for id := range r.Answers {
		if i, err := strconv.Atoi(id); err != nil || i < 0 || i >= len(in.Questions) {
			return nil, &execution.Invalid{Reason: fmt.Sprintf("Unknown question %q.", id)}
		}
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(p.input, &input); err != nil {
		return nil, err
	}
	b, err := json.Marshal(answers)
	if err != nil {
		return nil, err
	}
	input["answers"] = b
	return hookDecision(map[string]any{"behavior": "allow", "updatedInput": input})
}

func hookDecision(d map[string]any) ([]byte, error) {
	return json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": eventPermissionRequest, "decision": d}})
}
