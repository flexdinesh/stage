package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// WorkspaceAddTimeout bounds one add-workspace request, from the API through
// a worker to the harness (ADR 0012). A harness clone can take minutes.
const WorkspaceAddTimeout = 5 * time.Minute

// WorkspaceAddMaxRequestID bounds the caller's reconciliation key.
const WorkspaceAddMaxRequestID = 256

// WorkspaceSource is one way a harness adds a workspace (an existing
// folder, a clone, a trusted folder), in the harness's own words. Stage
// relays the request; the harness validates, creates and owns the result
// (docs/adr/0012-harness-workspace-add.md).
type WorkspaceSource struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Help  string  `json:"help,omitempty"`
	Input []Field `json:"input,omitempty"`
	// Confirm is the harness's explicit confirmation text; a caller must
	// send confirmed:true to run the source.
	Confirm      string       `json:"confirm,omitempty"`
	Availability Availability `json:"availability"`
}

// ValidateInput checks submitted input against the source's fields without
// exposing submitted values.
func (s WorkspaceSource) ValidateInput(input json.RawMessage) error {
	return validateInput(s.Input, "Workspace source", input)
}

// ValidateWorkspaceSources checks the add-workspace declaration of c: stable
// unique source IDs, labels, valid fields, a reason for each unavailable
// source, and at least one available source while the operation is.
func (f Capabilities) ValidateWorkspaceSources() error {
	seen := make(map[string]bool)
	available := false
	for _, s := range f.WorkspaceSources {
		if !extensionID.MatchString(s.ID) || strings.TrimSpace(s.Label) == "" || seen[s.ID] {
			return fmt.Errorf("workspace source %q needs a unique stable id and a label", s.ID)
		}
		seen[s.ID] = true
		if !s.Availability.Available && strings.TrimSpace(s.Availability.Reason) == "" {
			return fmt.Errorf("workspace source %q must explain why it is unavailable", s.ID)
		}
		if err := ValidateFields(s.Input); err != nil {
			return fmt.Errorf("workspace source %q: %w", s.ID, err)
		}
		available = available || s.Availability.Available
	}
	if f.Operations[OpWorkspaceAdd].Available && !available {
		return fmt.Errorf("operation %s is available without an available workspace source", OpWorkspaceAdd)
	}
	return nil
}

// WorkspaceAddRequest is an explicit request from a person or agent. Input
// is validated by Stage against the source's fields and interpreted by the
// adapter. RequestID lets the adapter and the caller reconcile a retry.
type WorkspaceAddRequest struct {
	Source    string          `json:"source"`
	Input     json.RawMessage `json:"input"`
	RequestID string          `json:"requestId"`
	Confirmed bool            `json:"confirmed,omitempty"`
}

// WorkspaceAdded is the harness's result. Existing: the harness already
// had the target and changed nothing. Workspace names the row the harness
// will list (native ID and root).
type WorkspaceAdded struct {
	Workspace WorkspaceInfo `json:"workspace"`
	Existing  bool          `json:"existing"`
	Message   string        `json:"message,omitempty"`
	Facts     []Fact        `json:"facts,omitempty"`
}

// WorkspaceAdder adds a workspace with the harness's own API. It is
// idempotent per target: an existing target returns Existing:true.
// *Invalid: the harness refused the input (nothing changed).
// *OutcomeUncertain: the harness may have added it.
type WorkspaceAdder interface {
	AddWorkspace(context.Context, WorkspaceAddRequest) (WorkspaceAdded, error)
}

// OutcomeUncertain means the harness may have performed the request. A
// retry with the same request ID is safe: the operation is idempotent per
// target. The API answers 504 outcome_uncertain.
type OutcomeUncertain struct{ Cause error }

func (e *OutcomeUncertain) Error() string {
	return "The harness did not confirm the outcome. Retry with the same requestId."
}

func (e *OutcomeUncertain) Unwrap() error { return e.Cause }

// WorkspaceAddNotSupported is the declaration of a harness whose adapter
// cannot add workspaces yet; name is the harness's display name.
func WorkspaceAddNotSupported(name string) Availability {
	return Availability{Reason: "Adding workspaces from Stage is not supported for " + name + " yet."}
}
