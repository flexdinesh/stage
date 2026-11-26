package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

type ActionTarget string

const (
	TargetConnection ActionTarget = "connection"
	TargetSession    ActionTarget = "session"
	TargetProject    ActionTarget = "project"
)

type Caller string

const (
	CallerUser  Caller = "user"
	CallerAgent Caller = "agent"
)

type FieldKind string

const (
	FieldText   FieldKind = "text"
	FieldURL    FieldKind = "url"
	FieldSecret FieldKind = "secret"
	FieldBool   FieldKind = "bool"
	FieldChoice FieldKind = "choice"
)

// Field describes one settings field or action input.
type Field struct {
	ID       string         `json:"id"`
	Label    string         `json:"label"`
	Kind     FieldKind      `json:"kind"`
	Help     string         `json:"help,omitempty"`
	Required bool           `json:"required,omitempty"`
	Choices  []ActionChoice `json:"choices,omitempty"`
}

type ActionChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Action describes a harness-owned operation Stage can relay.
type Action struct {
	ID           string       `json:"id"`
	Label        string       `json:"label"`
	Target       ActionTarget `json:"target"`
	Input        []Field      `json:"input,omitempty"`
	Confirm      string       `json:"confirm,omitempty"`
	Destructive  bool         `json:"destructive"`
	Callers      []Caller     `json:"callers"`
	Availability Availability `json:"availability"`
}

func (a Action) Allows(c Caller) bool { return slices.Contains(a.Callers, c) }

type Tone string

const (
	ToneNeutral Tone = "neutral"
	ToneOK      Tone = "ok"
	ToneWarn    Tone = "warn"
	ToneBad     Tone = "bad"
)

// Fact carries display text and, optionally, a link and a copy affordance.
type Fact struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Value    string `json:"value"`
	Tone     Tone   `json:"tone,omitempty"`
	Link     string `json:"link,omitempty"`
	Copyable bool   `json:"copyable,omitempty"`
}

// ActionWorkspace identifies the bound workspace in the adapter's namespace.
type ActionWorkspace struct {
	ExternalProjectID string `json:"externalProjectId"`
	Directory         string `json:"directory"`
}

// ActionRequest is scoped by Stage. The owner interprets and validates Input.
type ActionRequest struct {
	ID      string           `json:"id"`
	Target  ActionTarget     `json:"target"`
	Session *SessionRef      `json:"session,omitempty"`
	Project *ActionWorkspace `json:"project,omitempty"`
	Input   json.RawMessage  `json:"input"`
}

type ActionResult struct {
	Message  string `json:"message"`
	Facts    []Fact `json:"facts,omitempty"`
	Link     string `json:"link,omitempty"`
	Replaced int64  `json:"replaced,omitempty"`
	// JournalError is set by Stage when dispatch succeeded but recording failed.
	JournalError string `json:"journalError,omitempty"`
}

// Actor runs a declared session or project action on this adapter.
type Actor interface {
	Act(context.Context, ActionRequest) (ActionResult, error)
}

// ActionUncertain means the owner may have executed the action. Callers
// must check the harness before retrying.
type ActionUncertain struct{ Cause error }

func (e *ActionUncertain) Error() string {
	return "Action outcome is unknown. Check the harness before retrying."
}

func (e *ActionUncertain) Unwrap() error { return e.Cause }

var extensionID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// ValidateActions validates a declaration set. A blank target permits both
// session and project actions, with IDs scoped to their target.
func ValidateActions(actions []Action, target ActionTarget) error {
	seen := make(map[ActionTarget]map[string]bool)
	for _, a := range actions {
		if !extensionID.MatchString(a.ID) || strings.TrimSpace(a.Label) == "" {
			return fmt.Errorf("action %q needs a stable id and a label", a.ID)
		}
		if target != "" && a.Target != target || target == "" && a.Target != TargetSession && a.Target != TargetProject {
			return fmt.Errorf("action %q has invalid target %q", a.ID, a.Target)
		}
		if seen[a.Target] == nil {
			seen[a.Target] = make(map[string]bool)
		}
		if seen[a.Target][a.ID] {
			return fmt.Errorf("action %q declared twice for %s", a.ID, a.Target)
		}
		seen[a.Target][a.ID] = true
		if !a.Allows(CallerUser) || len(a.Callers) != 1 && !(len(a.Callers) == 2 && a.Allows(CallerAgent)) {
			return fmt.Errorf("action %q must allow users, optionally with agents", a.ID)
		}
		if !a.Availability.Available && strings.TrimSpace(a.Availability.Reason) == "" {
			return fmt.Errorf("action %q must explain why it is unavailable", a.ID)
		}
		if err := ValidateFields(a.Input); err != nil {
			return fmt.Errorf("action %q: %w", a.ID, err)
		}
	}
	return nil
}

func ValidateFields(fields []Field) error {
	seen := make(map[string]bool)
	for _, f := range fields {
		if !extensionID.MatchString(f.ID) || strings.TrimSpace(f.Label) == "" || seen[f.ID] {
			return fmt.Errorf("field %q needs a unique id and a label", f.ID)
		}
		seen[f.ID] = true
		switch f.Kind {
		case FieldText, FieldURL, FieldSecret, FieldBool:
			if len(f.Choices) != 0 {
				return fmt.Errorf("field %q does not accept choices", f.ID)
			}
		case FieldChoice:
			if len(f.Choices) == 0 {
				return fmt.Errorf("field %q needs choices", f.ID)
			}
			choices := make(map[string]bool)
			for _, c := range f.Choices {
				if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Label) == "" || choices[c.ID] {
					return fmt.Errorf("field %q needs unique choice ids and labels", f.ID)
				}
				choices[c.ID] = true
			}
		default:
			return fmt.Errorf("field %q has unknown kind %q", f.ID, f.Kind)
		}
	}
	return nil
}

// ValidateInput checks the public schema without exposing submitted values.
func (a Action) ValidateInput(input json.RawMessage) error {
	d := json.NewDecoder(bytes.NewReader(input))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return &Invalid{Reason: "Action input must be a JSON object."}
	}
	seen := make(map[string]bool)
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return &Invalid{Reason: "Malformed action input."}
		}
		id, ok := key.(string)
		if !ok {
			return &Invalid{Reason: "Malformed action input field."}
		}
		i := slices.IndexFunc(a.Input, func(f Field) bool { return f.ID == id })
		if i < 0 {
			return &Invalid{Reason: "Action input contains an unknown field."}
		}
		if seen[id] {
			return &Invalid{Reason: fmt.Sprintf("Action field %q was supplied twice.", id)}
		}
		seen[id] = true
		var value json.RawMessage
		if err := d.Decode(&value); err != nil || !a.Input[i].validValue(value) {
			return &Invalid{Reason: fmt.Sprintf("Invalid value for action field %q.", id)}
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return &Invalid{Reason: "Malformed action input."}
	}
	if _, err := d.Token(); err != io.EOF {
		return &Invalid{Reason: "Action input must be one JSON object."}
	}
	for _, f := range a.Input {
		if f.Required && !seen[f.ID] {
			return &Invalid{Reason: fmt.Sprintf("Action field %q is required.", f.ID)}
		}
	}
	return nil
}

func (f Field) validValue(raw json.RawMessage) bool {
	if bytes.Equal(raw, []byte("null")) {
		return false
	}
	if f.Kind == FieldBool {
		var b bool
		return json.Unmarshal(raw, &b) == nil
	}
	var s string
	if json.Unmarshal(raw, &s) != nil || f.Required && strings.TrimSpace(s) == "" {
		return false
	}
	switch f.Kind {
	case FieldChoice:
		return slices.ContainsFunc(f.Choices, func(c ActionChoice) bool { return c.ID == s })
	case FieldURL:
		u, err := url.Parse(s)
		return err == nil && u.IsAbs() && (u.Host != "" || u.Opaque != "" || u.Path != "")
	default:
		return true
	}
}

// ValidateFacts checks display metadata. Link policy belongs to the descriptor.
func ValidateFacts(facts []Fact) error {
	seen := make(map[string]bool)
	for _, f := range facts {
		if !extensionID.MatchString(f.ID) || strings.TrimSpace(f.Label) == "" || seen[f.ID] {
			return fmt.Errorf("fact %q needs a unique id and a label", f.ID)
		}
		seen[f.ID] = true
		switch f.Tone {
		case "", ToneNeutral, ToneOK, ToneWarn, ToneBad:
		default:
			return fmt.Errorf("fact %q has an unknown tone", f.ID)
		}
	}
	return nil
}
