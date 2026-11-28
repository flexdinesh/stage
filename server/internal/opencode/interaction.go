package opencode

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Answering a session's pending permissions and forms and sending it
// messages from Stage (execution.Interactive), over OpenCode's V2 session
// endpoints. Messages are queued (delivery "queue"): OpenCode delivers them
// when the current turn ends, never mid-turn (ADR 0013). A form whose
// fields Stage's question model can't carry (conditions, hidden or external
// fields) is left out of Requests and answered in OpenCode
// (Descriptor.UnlistedRequests).

var _ execution.Interactive = (*Adapter)(nil)

const respondUnavailable = "This OpenCode version cannot answer permissions and forms from Stage (session.permission.reply with a decision, session.form.reply with an answer)."

const sendUnavailable = "This OpenCode version cannot queue a message for the end of a turn (session.prompt with a delivery)."

// deliveryQueue is session.prompt's delivery that waits for the current
// turn to end (OpenCode's default, steer, joins a running turn).
const deliveryQueue = "queue"

// Permission decisions (Permission.Reply), in OpenCode's order: proceed first.
const (
	decisionOnce   = "once"
	decisionAlways = "always"
	decisionReject = "reject"
)

// Request ID prefixes (Permission.ID, Form.ID).
const (
	permissionPrefix = "per"
	formPrefix       = "frm_"
)

// Busy reasons of Send.
const (
	busyPermission = "OpenCode is waiting on a permission request. Answer it first."
	busyQuestions  = "OpenCode is waiting for answers to its questions. Answer them first."
	busyForm       = "OpenCode is waiting on a form Stage can't show. Answer it in OpenCode, then send."
)

// Form field types (Form.Field).
const (
	fieldString      = "string"
	fieldMultiselect = "multiselect"
	fieldBoolean     = "boolean"
	fieldNumber      = "number"
	fieldInteger     = "integer"
)

// Boolean field answers.
const (
	answerYes = "Yes"
	answerNo  = "No"
)

// inspectReplies reports whether session.permission.reply takes a decision
// and session.form.reply an answer.
func inspectReplies(paths map[string]map[string]operation, components map[string]json.RawMessage) bool {
	return requestTakes(paths["/api/session/{sessionID}/permission/{requestID}/reply"]["post"], "session.permission.reply", "decision", components) &&
		requestTakes(paths["/api/session/{sessionID}/form/{formID}/reply"]["post"], "session.form.reply", "answer", components)
}

// requestTakes reports whether op is operation id and its JSON body has field.
func requestTakes(op operation, id, field string, components map[string]json.RawMessage) bool {
	if op.OperationID != id {
		return false
	}
	shape, err := requestShape(op, components)
	return err == nil && shape.Properties[field] != nil
}

// permissionRequest is a Permission.Request.
type permissionRequest struct {
	ID        string          `json:"id"`
	SessionID string          `json:"sessionID"`
	Action    string          `json:"action"`
	Resources []string        `json:"resources"`
	Message   string          `json:"message"`
	Metadata  json.RawMessage `json:"metadata"`
	// Save: the rules "always" saves; none means always is not offered.
	Save []string `json:"save"`
}

// formInfo is a Form.Info.
type formInfo struct {
	ID        string          `json:"id"`
	SessionID string          `json:"sessionID"`
	Title     string          `json:"title"`
	Metadata  json.RawMessage `json:"metadata"`
	Fields    []formField     `json:"fields"`
}

// formField is one Form.Field; fields Stage does not read are ignored.
type formField struct {
	Key         string            `json:"key"`
	Type        string            `json:"type"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Hidden      bool              `json:"hidden"`
	Custom      bool              `json:"custom"`
	When        []json.RawMessage `json:"when"`
	// Options: nil when absent (a string field without options is free text).
	Options *[]formOption `json:"options"`
}

type formOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

func (a *Adapter) permissions(ctx context.Context, id string) ([]permissionRequest, error) {
	var list envelope[[]permissionRequest]
	if err := a.get(ctx, "/api/session/"+url.PathEscape(id)+"/permission", &list); missing(err) {
		return nil, execution.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if list.Data == nil {
		return nil, errors.New("incomplete OpenCode permission list")
	}
	for _, p := range list.Data {
		if !strings.HasPrefix(p.ID, permissionPrefix) || p.SessionID != id {
			return nil, errors.New("OpenCode permission request belongs to another session or has an invalid ID")
		}
	}
	return list.Data, nil
}

func (a *Adapter) forms(ctx context.Context, id string) ([]formInfo, error) {
	var list envelope[[]formInfo]
	if err := a.get(ctx, "/api/session/"+url.PathEscape(id)+"/form", &list); missing(err) {
		return nil, execution.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if list.Data == nil {
		return nil, errors.New("incomplete OpenCode form list")
	}
	for _, f := range list.Data {
		if !strings.HasPrefix(f.ID, formPrefix) || f.SessionID != id {
			return nil, errors.New("OpenCode form belongs to another session or has an invalid ID")
		}
	}
	return list.Data, nil
}

// Requests are the session's pending permissions (approvals), then its
// forms Stage can show (question requests). OpenCode reports no request
// time, so createdAt is zero.
func (a *Adapter) Requests(ctx context.Context, ref execution.SessionRef) ([]execution.Request, error) {
	if !a.valid(ref) {
		return nil, execution.ErrNotFound
	}
	perms, err := a.permissions(ctx, ref.ExternalSessionID)
	if err != nil {
		return nil, err
	}
	forms, err := a.forms(ctx, ref.ExternalSessionID)
	if err != nil {
		return nil, err
	}
	out := make([]execution.Request, 0, len(perms)+len(forms))
	for _, p := range perms {
		out = append(out, permissionToRequest(p))
	}
	for _, f := range forms {
		if r, ok := formToRequest(f); ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// actionTitles are OpenCode's own descriptions of its permission actions.
var actionTitles = map[string]string{
	"read":               "Reading a file",
	"edit":               "Modify files, including edits, writes, and patches",
	"glob":               "Match files using glob patterns",
	"grep":               "Search file contents using regular expressions",
	"list":               "List files within a directory",
	"bash":               "Run shell commands",
	"task":               "Launch sub-agents",
	"skill":              "Load a skill by name",
	"lsp":                "Run language server queries",
	"todowrite":          "Update the todo list",
	"webfetch":           "Fetch content from a URL",
	"websearch":          "Search the web",
	"external_directory": "Access files outside the project directory",
	"doom_loop":          "Detect repeated tool calls with identical input",
}

func permissionToRequest(p permissionRequest) execution.Request {
	title, ok := actionTitles[p.Action]
	if !ok {
		title = "Permission required: " + p.Action
	}
	lines := toolInput(p.Metadata)
	lines = append(lines, p.Resources...)
	options := []execution.Choice{choice(decisionOnce, "Allow once")}
	if p.savesRules() {
		options = append(options, choice(decisionAlways, "Allow always"))
	}
	return execution.Request{
		ID: p.ID, Kind: execution.RequestApproval, Title: title, Detail: p.Message,
		Preview: strings.Join(lines, "\n"), TextOption: decisionReject,
		Options: append(options, choice(decisionReject, "Deny")),
	}
}

// savesRules reports whether "always" would save a rule: OpenCode saves
// nothing for a request without save patterns, so always is not offered.
func (p permissionRequest) savesRules() bool {
	return slices.ContainsFunc(p.Save, func(s string) bool { return strings.TrimSpace(s) != "" })
}

// toolInput is metadata.input as "key: value" lines, keys sorted; none
// when the request carries no input object.
func toolInput(metadata json.RawMessage) []string {
	var m struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if len(metadata) == 0 || json.Unmarshal(metadata, &m) != nil {
		return nil
	}
	keys := make([]string, 0, len(m.Input))
	for k := range m.Input {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		var s string
		if json.Unmarshal(m.Input[k], &s) != nil {
			s = string(m.Input[k])
		}
		lines = append(lines, k+": "+s)
	}
	return lines
}

// formKind is metadata.kind ("question" for the agent's question tool).
func formKind(metadata json.RawMessage) string {
	var m struct {
		Kind json.RawMessage `json:"kind"`
	}
	var kind string
	if len(metadata) == 0 || json.Unmarshal(metadata, &m) != nil || json.Unmarshal(m.Kind, &kind) != nil {
		return ""
	}
	return kind
}

// formToRequest maps a form Stage can show; false when a field has a
// condition, is hidden or external, or offers no answer Stage can give.
func formToRequest(f formInfo) (execution.Request, bool) {
	if len(f.Fields) == 0 {
		return execution.Request{}, false
	}
	req := execution.Request{ID: f.ID, Kind: execution.RequestQuestion, Title: f.Title, Questions: make([]execution.Question, 0, len(f.Fields))}
	if formKind(f.Metadata) == "question" {
		req.Title = "OpenCode asks"
		if len(f.Fields) > 1 {
			req.Title = fmt.Sprintf("OpenCode asks %d questions", len(f.Fields))
		}
	} else if strings.TrimSpace(req.Title) == "" {
		req.Title = "OpenCode form"
	}
	for _, field := range f.Fields {
		q, ok := fieldQuestion(field)
		if !ok {
			return execution.Request{}, false
		}
		req.Questions = append(req.Questions, q)
	}
	return req, true
}

func fieldQuestion(f formField) (execution.Question, bool) {
	if f.Key == "" || f.Hidden || len(f.When) > 0 {
		return execution.Question{}, false
	}
	q := execution.Question{ID: f.Key, Prompt: f.Title, Options: []execution.QuestionOption{}}
	if f.Description != "" {
		q.Header, q.Prompt = f.Title, f.Description
	}
	if strings.TrimSpace(q.Prompt) == "" {
		q.Prompt = f.Key
	}
	switch f.Type {
	case fieldString, fieldMultiselect:
		q.Multi = f.Type == fieldMultiselect
		q.Custom = f.Custom || (f.Type == fieldString && f.Options == nil)
		labels := map[string]bool{}
		for _, o := range options(f) {
			label := strings.TrimSpace(o.Label)
			if label == "" || labels[label] {
				return execution.Question{}, false
			}
			labels[label] = true
			q.Options = append(q.Options, execution.QuestionOption{Label: o.Label, Description: o.Description})
		}
		if len(q.Options) == 0 && !q.Custom {
			return execution.Question{}, false
		}
	case fieldBoolean:
		q.Options = append(q.Options, execution.QuestionOption{Label: answerYes}, execution.QuestionOption{Label: answerNo})
	case fieldNumber, fieldInteger:
		q.Custom = true
	default:
		return execution.Question{}, false
	}
	return q, true
}

func options(f formField) []formOption {
	if f.Options == nil {
		return nil
	}
	return *f.Options
}

// Respond answers a pending permission (an option, text only with Deny) or
// a form Stage shows (answers by field key). A request no longer pending is
// ErrNotFound; an answer that does not fit, or one OpenCode refuses, is
// *execution.Invalid.
func (a *Adapter) Respond(ctx context.Context, ref execution.SessionRef, requestID string, r execution.Response) error {
	if !a.valid(ref) {
		return execution.ErrNotFound
	}
	contract, err := a.checkContract(ctx)
	if err != nil {
		return err
	}
	if !contract.respond {
		return errors.New(respondUnavailable)
	}
	switch {
	case strings.HasPrefix(requestID, formPrefix):
		return a.respondForm(ctx, ref.ExternalSessionID, requestID, r)
	case strings.HasPrefix(requestID, permissionPrefix):
		return a.respondPermission(ctx, ref.ExternalSessionID, requestID, r)
	}
	return execution.ErrNotFound
}

func (a *Adapter) respondPermission(ctx context.Context, session, id string, r execution.Response) error {
	perms, err := a.permissions(ctx, session)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(perms, func(p permissionRequest) bool { return p.ID == id })
	if i < 0 {
		return execution.ErrNotFound
	}
	switch {
	case len(r.Answers) > 0:
		return &execution.Invalid{Reason: "Answer an OpenCode permission request with an option, not answers."}
	case r.Option == decisionAlways && !perms[i].savesRules():
		return &execution.Invalid{Reason: "OpenCode offers no Allow always for this request. Choose Allow once or Deny."}
	case r.Option != decisionOnce && r.Option != decisionAlways && r.Option != decisionReject:
		return &execution.Invalid{Reason: "Choose Allow once, Allow always or Deny."}
	case strings.TrimSpace(r.Text) != "" && r.Option != decisionReject:
		return &execution.Invalid{Reason: "Only Deny takes a message."}
	}
	body := map[string]string{"decision": r.Option}
	if text := strings.TrimSpace(r.Text); text != "" {
		body["message"] = text
	}
	return replyErr(a.request(ctx, "POST", "/api/session/"+url.PathEscape(session)+"/permission/"+url.PathEscape(id)+"/reply", body, nil))
}

func (a *Adapter) respondForm(ctx context.Context, session, id string, r execution.Response) error {
	forms, err := a.forms(ctx, session)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(forms, func(f formInfo) bool { return f.ID == id })
	if i < 0 {
		return execution.ErrNotFound
	}
	if _, ok := formToRequest(forms[i]); !ok {
		return &execution.Invalid{Reason: "Answer this form in OpenCode."}
	}
	if len(r.Answers) == 0 {
		return &execution.Invalid{Reason: "Answer OpenCode's questions with answers (an option or text alone does not fit)."}
	}
	answer, err := formAnswer(forms[i].Fields, r.Answers)
	if err != nil {
		return &execution.Invalid{Reason: err.Error()}
	}
	return replyErr(a.request(ctx, "POST", "/api/session/"+url.PathEscape(session)+"/form/"+url.PathEscape(id)+"/reply", map[string]any{"answer": answer}, nil))
}

// formAnswer maps Stage answers (option labels or text, by field key) to
// OpenCode's Form.Answer: option values, booleans and numbers. Fields
// left out stay unanswered; OpenCode checks required fields.
func formAnswer(fields []formField, in map[string][]string) (map[string]any, error) {
	byKey := make(map[string]formField, len(fields))
	for _, f := range fields {
		byKey[f.Key] = f
	}
	out := make(map[string]any, len(in))
	for key, values := range in {
		f, ok := byKey[key]
		if !ok {
			return nil, fmt.Errorf("unknown question %q", key)
		}
		if f.Type == fieldMultiselect {
			picked := make([]string, 0, len(values))
			for _, v := range values {
				value, ok := optionValue(f, v)
				if !ok {
					return nil, fmt.Errorf("question %q takes its options only", key)
				}
				picked = append(picked, value)
			}
			out[key] = picked
			continue
		}
		if len(values) != 1 {
			return nil, fmt.Errorf("question %q takes a single answer", key)
		}
		v := values[0]
		switch f.Type {
		case fieldString:
			value, ok := optionValue(f, v)
			if !ok {
				return nil, fmt.Errorf("question %q takes one of its options", key)
			}
			out[key] = value
		case fieldBoolean:
			switch strings.TrimSpace(v) {
			case answerYes:
				out[key] = true
			case answerNo:
				out[key] = false
			default:
				return nil, fmt.Errorf("question %q takes %s or %s", key, answerYes, answerNo)
			}
		case fieldNumber, fieldInteger:
			n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
				return nil, fmt.Errorf("question %q takes a number", key)
			}
			if f.Type == fieldInteger && n != math.Trunc(n) {
				return nil, fmt.Errorf("question %q takes a whole number", key)
			}
			out[key] = n
		default:
			return nil, fmt.Errorf("question %q can't be answered in Stage", key)
		}
	}
	return out, nil
}

// optionValue is the value of the option labelled v, else v itself when
// the field takes free text; false when it does neither.
func optionValue(f formField, v string) (string, bool) {
	label := strings.TrimSpace(v)
	for _, o := range options(f) {
		if label != "" && strings.TrimSpace(o.Label) == label {
			return o.Value, true
		}
	}
	return v, f.Custom || (f.Type == fieldString && f.Options == nil)
}

// replyErr maps a reply's refusal: a request already settled or gone is
// ErrNotFound, an answer OpenCode rejects is *execution.Invalid.
func replyErr(err error) error {
	var status responseError
	if !errors.As(err, &status) {
		return err
	}
	switch status.code {
	case 404, 409:
		return execution.ErrNotFound
	case 400:
		reason := strings.TrimSpace(status.message)
		if reason == "" {
			reason = "OpenCode refused the answer."
		}
		return &execution.Invalid{Reason: reason}
	}
	return err
}

// Send queues a message in the session (delivery "queue"): an idle session
// starts a run, a running one takes it when its turn ends. A session
// waiting on a permission or form takes no message (*execution.Busy), as
// OpenCode's own composer does. The message ID derives from m.RequestID
// when set, so a retry is admitted once.
func (a *Adapter) Send(ctx context.Context, ref execution.SessionRef, m execution.Message) error {
	if !a.valid(ref) {
		return execution.ErrNotFound
	}
	contract, err := a.checkContract(ctx)
	if err != nil {
		return err
	}
	if !contract.send {
		return errors.New(sendUnavailable)
	}
	id := ref.ExternalSessionID
	perms, err := a.permissions(ctx, id)
	if err != nil {
		return err
	}
	if len(perms) > 0 {
		return &execution.Busy{Reason: busyPermission}
	}
	forms, err := a.forms(ctx, id)
	if err != nil {
		return err
	}
	if len(forms) > 0 {
		for _, f := range forms {
			if _, ok := formToRequest(f); !ok {
				return &execution.Busy{Reason: busyForm}
			}
		}
		return &execution.Busy{Reason: busyQuestions}
	}
	msg, err := messageID(id, m.RequestID)
	if err != nil {
		return err
	}
	marker := m.RequestID
	if marker == "" {
		marker = msg
	}
	body := map[string]any{"id": msg, "text": m.Text, "metadata": map[string]string{"stageMessage": marker}, "delivery": deliveryQueue}
	var result envelope[admitted]
	if err := a.request(ctx, "POST", "/api/session/"+url.PathEscape(id)+"/prompt", body, &result); missing(err) {
		return execution.ErrNotFound
	} else if err != nil {
		return err
	}
	// A plugin may rewrite the text, so only the admission's identity is checked.
	if v := result.Data; v.Type != "user" || v.ID != msg || v.SessionID != id {
		return errors.New("OpenCode returned a mismatched message admission")
	}
	return nil
}

// messageID is the prompt's message ID: derived from the session and
// requestID when set (a retry reuses it and OpenCode admits it once),
// else random.
func messageID(session, requestID string) (string, error) {
	if requestID != "" {
		sum := sha256.Sum256([]byte(session + "\n" + requestID))
		return "msg_" + hex.EncodeToString(sum[:16]), nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "msg_" + hex.EncodeToString(b), nil
}
