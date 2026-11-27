package machine_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/codexsetup"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/opencodesetup"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
)

// Every type that crosses the machine-runtime seam (adapter contract,
// connector setup, port payloads, machine ops) survives a JSON round trip,
// so a remote machine can carry it over the worker channel. Each sample
// sets every exported field: a new field must be added here, and a field
// hidden from JSON fails.
func TestSeamTypesRoundTripJSON(t *testing.T) {
	at := time.Date(2026, 11, 22, 10, 0, 5, 0, time.UTC)
	later := at.Add(time.Hour)
	str := "s"
	cost := 1.5
	ref := execution.SessionRef{ConnectionID: 7, ExternalSessionID: "session-1"}
	avail := execution.Availability{Available: true, Reason: "why"}
	choice := execution.Choice{ID: "c", Label: "C", Availability: avail}
	action := execution.Action{
		Primary: true,
		ID:      "clear", Label: "Clear", Target: execution.TargetSession,
		Input:   []execution.Field{{ID: "scope", Label: "Scope", Kind: execution.FieldChoice, Help: "Choose what to clear.", Required: true, Choices: []execution.ActionChoice{{ID: "c", Label: "C"}}}},
		Confirm: "Clear this scope?", Destructive: true, Callers: []execution.Caller{execution.CallerUser}, Availability: avail,
	}
	def := execution.BoolValue(true)
	option := execution.Option{ID: "effort", Kind: execution.OptionChoice, Label: "Effort", Values: []execution.OptionValue{execution.StringValue("high"), execution.BoolValue(false)}, Default: &def,
		ValueLabels: map[string]string{"high": "High effort"},
		DependsOn:   &execution.OptionDependency{Option: "worktree", Value: execution.BoolValue(true)}}
	cfg := execution.Config{Model: "brain-7", Options: []execution.ModelOption{{ID: "effort", Value: execution.StringValue("high")}, {ID: "fast", Value: execution.BoolValue(true)}}, Permission: "full-access", Mode: "plan"}
	observation := execution.Observation{
		Parent: &execution.SessionRef{ConnectionID: 7, ExternalSessionID: "parent"}, Config: cfg, Session: ref,
		Project: "p", Title: "t", State: "running", Active: true, Archived: true, Child: true, Branch: "feat", Directory: "/w",
		Links:     execution.Links{Web: "https://web", Native: "t3code://x", Command: "t3 resume x"},
		CreatedAt: at, UpdatedAt: later, StartedAt: &at, EndedAt: &later, ExecutionID: "run-1",
		Reply:  &execution.Reply{ItemID: "i", RunID: "r", Text: "done", Truncated: true, UpdatedAt: at},
		Usage:  &execution.Usage{Turns: 2, InputTokens: 3, CacheReadTokens: 4, CacheWriteTokens: 5, OutputTokens: 6, WorkingMs: 7, CostUSD: &cost},
		Closed: &execution.Closure{Signal: execution.ClosedSettled}, StateReason: execution.ReasonOwnerExited, Origin: "cli",
		Facts:   []execution.Fact{{ID: "review", Label: "Review", Value: "Ready", Link: "https://web/review", Copyable: true, Tone: execution.ToneOK}},
		Actions: []execution.ActionState{{ID: "archive", Availability: execution.Availability{Reason: "Already archived."}}},
	}
	machineID := int64(3)
	binding := db.ProjectBinding{
		ID: 1, ProjectID: 2, ConnectionID: 7, Harness: "kind", ExternalProjectID: "p", Workspace: "/w", Revision: 3,
		Status: "available", ObservedWorkspace: &str, ConnectionActive: true, ConnectionLabel: "box", Primary: true,
		Rule: "canonical_path", MachineID: machineID, Title: "Workspace", Remote: "github.com/o/r", Subpath: ".",
		FileHost: &machineID, EvidenceAt: &at, Facts: observation.Facts,
	}
	evidence := execution.WorkspaceEvidence{
		CanonicalPath: "/w", CheckoutKey: "/w/.git", Subpath: ".", Remote: "github.com/o/r", InodeHint: "64769:1234", Gone: true,
	}
	source := execution.WorkspaceSource{
		ID: "folder", Label: "Folder", Help: "Add a folder.", Confirm: "Trust this folder.", Availability: avail,
		Input: []execution.Field{{ID: "path", Label: "Path", Kind: execution.FieldPath, Required: true}},
	}
	samples := map[string]any{
		// Adapter contract (execution.Adapter and its optional interfaces).
		"SessionRef":   ref,
		"Health":       execution.Health{Availability: avail, Identity: "env-a", CheckedAt: at},
		"Availability": avail,
		"WorkspaceInfo": execution.WorkspaceInfo{
			OriginRemote: "git@github.com:o/r.git", ID: "p", Title: "r", WorkspaceRoot: "/w", ArchivedAt: &at, DeletedAt: &later,
			CreatedAt: at, UpdatedAt: later, Importable: true, ActiveAt: &at, Repo: "github.com/o/r", RepoPath: "packages/web",
			Local: true, ImportIf: execution.ImportInCheckout, BlockedReason: "Not trusted.", Trust: &execution.TrustRule{Records: []string{"/w"}, Reason: "Not trusted."}, Evidence: &evidence,
		},
		"Capabilities": execution.Capabilities{
			Permissions: []execution.Choice{choice}, Options: []execution.Option{option}, ModeLabel: "Agent",
			Operations: map[execution.Op]execution.Availability{execution.OpLaunch: avail}, Models: []execution.Model{{Choice: choice, Group: "g", Options: []execution.Option{option}, Permissions: []execution.Choice{choice}}},
			Modes: []execution.Choice{choice}, Actions: []execution.Action{action}, WorkspaceSources: []execution.WorkspaceSource{source},
		},
		"Action":          action,
		"WorkspaceSource": source,
		"WorkspaceAddRequest": execution.WorkspaceAddRequest{
			Source: "folder", Input: json.RawMessage(`{"path":"/w"}`), RequestID: "req-1", Confirmed: true,
		},
		"WorkspaceAdded": execution.WorkspaceAdded{
			Workspace: execution.WorkspaceInfo{ID: "p", WorkspaceRoot: "/w"}, Existing: true, Message: "Added /w",
			Facts: []execution.Fact{{ID: "root", Label: "Folder", Value: "/w"}},
		},
		"ActionRequest": execution.ActionRequest{
			ID: "clear", Target: execution.TargetSession, Session: &ref, Project: &execution.ActionWorkspace{ExternalProjectID: "p", Directory: "/w"},
			Input: json.RawMessage(`{"scope":"c"}`),
		},
		"ActionWorkspace": execution.ActionWorkspace{ExternalProjectID: "p", Directory: "/w"},
		"Intent": execution.Intent{
			RequestID: "req-1", ConnectionID: 7, Project: "p", Directory: "/w", Title: "t", Prompt: "do it", Config: cfg,
			Images: []execution.Image{{Name: "a.png", MimeType: "image/png", Data: []byte{0, 1, 2}}}, Marker: execution.LaunchMarker("req-1"),
		},
		"Checkpoint":  execution.Checkpoint{Version: 2, Data: json.RawMessage(`{"session":"session-1"}`)},
		"Observation": observation,
		"Links":       observation.Links,
		"Reply":       *observation.Reply,
		"Request": execution.Request{ID: "q", Kind: "question", Title: "Pick", Detail: "one", Preview: "go test", Options: []execution.Choice{choice}, TextOption: "c",
			Questions: []execution.Question{{ID: "0", Header: "H", Prompt: "Which?", Options: []execution.QuestionOption{{Label: "A", Description: "a"}}, Multi: true, Custom: true}}, CreatedAt: at},
		"Response": execution.Response{Option: "c", Text: "because", Answers: map[string][]string{"0": {"A"}}},
		"Message":  execution.Message{Text: "hi"},
		// Connector setup (harness.Connector).
		"Summary": harness.Summary{ConnectionID: 7, Configured: true, Connected: true, LastError: "e"},
		"Status": harness.Status{
			Summary: harness.Summary{ConnectionID: 7, Configured: true, Connected: true, LastError: "e"}, State: "connected", Reason: "r",
			Phase:   harness.PhaseConnected,
			Facts:   []execution.Fact{{ID: "publicUrl", Label: "Browser URL", Value: "http://x", Link: "http://x", Copyable: true, Tone: execution.ToneOK}},
			Fix:     &harness.Fix{Text: "Pair again.", Command: "t3 pair"},
			Fields:  []harness.FieldState{{ID: "url", Value: "http://x", Set: true, Source: "saved"}},
			Actions: []harness.ActionState{{ID: "pair", Available: true, Reason: "r", Code: "operation_unavailable"}},
			Details: map[string]any{"state": "connected", "connected": true},
			Sync:    &harness.SyncStatus{Running: true, StartedAt: &at, LastError: &str, LastFullSyncAt: &later, Projects: 1, Sessions: 2},
		},
		"Result": harness.Result{
			Message: "Paired.", Facts: []execution.Fact{{ID: "instance", Label: "Instance", Value: "new"}},
			Link: "http://x", Replaced: 4, JournalError: "journal unavailable",
		},
		"T3Details":      t3setup.Details{Connected: true, State: t3.StateConnected, T3URL: "http://t3", PublicURL: "http://pub", T3URLSource: t3setup.SourceSettings, EnvironmentID: &str, ClientID: &str, TokenExpiresAt: &at, AdminTokenConfigured: true, Reachable: true, CheckedAt: &at, ProbeError: &str, T3: &t3setup.IdentityRef{EnvironmentID: "env", Label: "box", ServerVersion: "1"}, AutoPair: t3setup.AutoPairRef{Available: true, Reason: &str}, ConnectionID: 7},
		"OpenCodeStatus": opencodesetup.Status{ConnectionID: 8, Endpoint: "http://oc", BrowserEndpoint: "http://ocb", WebClient: "http://ocw", HasCredentials: true, State: "connected", Reason: "r", CheckedAt: &at, Auth: opencodesetup.AuthPaired, ExpiresAt: &later, Local: opencodesetup.LocalStatus{Available: true, URL: "http://oc", Version: "2.3.0", Reason: "r"}},
		// Port payloads (settings, credentials, identity).
		"Switch":            db.Switch{Old: 1, New: 2, Changed: true, Revived: true},
		"WorkspaceEvidence": harness.WorkspaceEvidence{ExternalProjectID: "p", Workspace: "/w"},
		"HarnessConnection": db.HarnessConnection{Configured: true, ID: 1, MachineID: 2, Harness: "kind", AutoComplete: true, Active: true, Label: "box", RetiredAt: &str, ForgottenAt: &str, Tasks: 3, Bindings: 4, NewTaskConfig: &execution.Config{Model: "m", Options: []execution.ModelOption{{ID: "effort", Value: execution.StringValue("high")}}, Permission: "p", Mode: "x"}},
		"ProjectBinding":    binding,
		"OpenCodeSetup":     opencodesetup.SetupSettings{Endpoint: "http://oc", BrowserEndpoint: "http://ocb", WebClient: "http://ocw", Auth: opencodesetup.AuthPaired},
		"CodexSettings":     codexsetup.SetupSettings{Binary: "/opt/codex/bin/codex", Connected: true},
		// Machine ops.
		"FilesystemWorkspaceEvidence": evidence,
	}
	for name, v := range samples {
		t.Run(name, func(t *testing.T) {
			rv := reflect.ValueOf(v)
			for i := range rv.NumField() {
				if f := rv.Type().Field(i); f.IsExported() && f.Tag.Get("json") != "-" && rv.Field(i).IsZero() {
					t.Fatalf("sample leaves %s zero: set every field", f.Name)
				}
			}
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			out := reflect.New(rv.Type())
			if err := json.Unmarshal(b, out.Interface()); err != nil {
				t.Fatal(err)
			}
			if got := out.Elem().Interface(); !reflect.DeepEqual(got, v) {
				t.Fatalf("round trip changed it:\n got %#v\nwant %#v\njson %s", got, v, b)
			}
		})
	}
}
