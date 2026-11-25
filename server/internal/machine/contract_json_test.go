package machine_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/opencode"
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
	ref := execution.SessionRef{ConnectionID: 7, ExternalSessionID: "thread-1"}
	avail := execution.Availability{Available: true, Reason: "why"}
	choice := execution.Choice{ID: "c", Label: "C", Availability: avail}
	def := execution.BoolValue(true)
	option := execution.Option{ID: "effort", Values: []execution.OptionValue{execution.StringValue("high"), execution.BoolValue(false)}, Default: &def}
	cfg := execution.Config{Model: "brain-7", Options: []execution.ModelOption{{ID: "effort", Value: execution.StringValue("high")}, {ID: "fast", Value: execution.BoolValue(true)}}, Permission: "full-access", Mode: "plan"}
	workspace := execution.Workspace{Kind: "worktree", Base: "origin/main", Branch: "feat", FromRemote: true}
	observation := execution.Observation{
		ModelLabel: "Brain 7", Parent: &execution.SessionRef{ConnectionID: 7, ExternalSessionID: "parent"}, Config: cfg, Session: ref,
		Project: "p", Title: "t", State: "running", Active: true, Archived: true, Child: true, Branch: "feat", Directory: "/w",
		Links:     execution.Links{Service: "svc", Web: "https://web", App: "https://app", Native: "t3code://x", Command: "t3 resume x"},
		CreatedAt: at, UpdatedAt: later, StartedAt: &at, EndedAt: &later, ExecutionID: "run-1",
		Reply:     &execution.Reply{ItemID: "i", RunID: "r", MessageID: "m", Text: "done", Truncated: true, Position: 2, UpdatedAt: at},
		Usage:     &execution.Usage{Turns: 2, InputTokens: 3, CacheReadTokens: 4, CacheWriteTokens: 5, OutputTokens: 6, WorkingMs: 7, CostUSD: &cost},
		Artifacts: []execution.Artifact{{Host: "github.com", Repository: "o/r", Number: 3, URL: "https://github.com/o/r/pull/3", Source: "reply", State: "open"}},
		Base:      avail, Closed: &execution.Closure{Signal: execution.ClosedSettled}, StateReason: execution.ReasonOwnerExited,
	}
	binding := db.ProjectBinding{ID: 1, ProjectID: 2, ConnectionID: 7, Harness: "kind", ExternalProjectID: "p", Workspace: "/w", Revision: 3, Status: "available", ObservedWorkspace: &str, ConnectionActive: true, ConnectionLabel: "box", Origin: db.BindingPath}
	samples := map[string]any{
		// Adapter contract (execution.Adapter and its optional interfaces).
		"SessionRef":   ref,
		"Health":       execution.Health{Availability: avail, Identity: "env-a", CheckedAt: at},
		"Availability": avail,
		"WorkspaceInfo": execution.WorkspaceInfo{
			OriginRemote: "git@github.com:o/r.git", ID: "p", Title: "r", WorkspaceRoot: "/w", ArchivedAt: &at, DeletedAt: &later,
			CreatedAt: at, UpdatedAt: later, Importable: true, ActiveAt: &at, Repo: "github.com/o/r", RepoPath: "packages/web",
		},
		"Capabilities": execution.Capabilities{
			Permissions: []execution.Choice{choice}, Options: []execution.Option{option}, ModeLabel: "Agent",
			Operations: map[string]execution.Availability{"launch": avail}, Models: []execution.Model{{Choice: choice, Group: "g", Options: []execution.Option{option}, Permissions: []execution.Choice{choice}}},
			Modes: []execution.Choice{choice}, Workspaces: []execution.Choice{choice},
		},
		"Intent": execution.Intent{
			RequestID: "req-1", ConnectionID: 7, Project: "p", Directory: "/w", Title: "t", Prompt: "do it", Config: cfg, Workspace: workspace,
			Images: []execution.Image{{Name: "a.png", MimeType: "image/png", Data: []byte{0, 1, 2}}}, Marker: execution.LaunchMarker("req-1"),
		},
		"Checkpoint":  execution.Checkpoint{Version: 2, Data: json.RawMessage(`{"thread":"t-1"}`)},
		"Observation": observation,
		"Credential":  execution.Credential{ClientID: "client", Token: "tok", ExpiresAt: at},
		"Request": execution.Request{ID: "q", Kind: "question", Title: "Pick", Detail: "one", Preview: "go test", Options: []execution.Choice{choice}, TextOption: "c",
			Questions: []execution.Question{{ID: "0", Header: "H", Prompt: "Which?", Options: []execution.QuestionOption{{Label: "A", Description: "a"}}, Multi: true, Custom: true}}, CreatedAt: at},
		"Response": execution.Response{Option: "c", Text: "because", Answers: map[string][]string{"0": {"A"}}},
		"Message":  execution.Message{Text: "hi"},
		// Connector setup (harness.Connector).
		"Summary": harness.Summary{ConnectionID: 7, Configured: true, Connected: true, LastError: "e"},
		"Status": harness.Status{
			Summary: harness.Summary{ConnectionID: 7, Configured: true, Connected: true, LastError: "e"}, State: "connected", Reason: "r",
			Fields:  []harness.FieldState{{ID: "url", Value: "http://x", Set: true, Source: "saved"}},
			Actions: []harness.ActionState{{ID: "pair", Available: true, Reason: "r"}},
			Details: map[string]any{"state": "connected", "connected": true},
		},
		"Result":         harness.Result{Replaced: 4},
		"T3Details":      t3setup.Details{Connected: true, State: t3.StateConnected, T3URL: "http://t3", PublicURL: "http://pub", T3URLSource: t3setup.SourceSettings, EnvironmentID: &str, ClientID: &str, TokenExpiresAt: &at, AdminTokenConfigured: true, LastError: &str, LastFullSyncAt: &at, Reachable: true, CheckedAt: &at, ProbeError: &str, T3: &t3setup.IdentityRef{EnvironmentID: "env", Label: "box", ServerVersion: "1"}, AutoPair: t3setup.AutoPairRef{Available: true, Reason: &str}, Sync: t3setup.SyncProgress{Running: true, StartedAt: &at, Projects: 1, Threads: 2}, ConnectionID: 7},
		"OpenCodeStatus": config.OpenCodeStatus{ConnectionID: 8, Endpoint: "http://oc", BrowserEndpoint: "http://ocb", WebClient: "http://ocw", HasCredentials: true, State: "connected", Reason: "r", CheckedAt: &at, Auth: config.OpenCodeAuthPaired, ExpiresAt: &later, Local: config.OpenCodeLocal{Available: true, URL: "http://oc", Version: "2.3.0", Reason: "r"}},
		// Port payloads (settings, credentials, identity).
		"Switch":             db.Switch{Old: 1, New: 2, Changed: true, Revived: true},
		"Connection":         db.Connection{LastError: &str, LastFullSyncAt: &at, T3URL: &str, T3PublicURL: &str, EnvironmentID: &str, ConnectionID: 1},
		"HarnessConnection":  db.HarnessConnection{Configured: true, ID: 1, MachineID: 2, Harness: "kind", AutoComplete: true, Active: true, Label: "box", RetiredAt: &str, ForgottenAt: &str, Tasks: 3, Bindings: 4, NewTaskConfig: &execution.Config{Model: "m", Options: []execution.ModelOption{{ID: "effort", Value: execution.StringValue("high")}}, Permission: "p", Mode: "x"}},
		"ProjectBinding":     binding,
		"OpenCodeSetup":      config.OpenCodeSetup{Endpoint: "http://oc", BrowserEndpoint: "http://ocb", WebClient: "http://ocw", Auth: config.OpenCodeAuthPaired},
		"OpenCodeCredential": config.OpenCodeCredential{Username: "opencode", Password: "tok", TokenIssuedAt: &at},
		"Registration":       opencode.Registration{ID: "svc", Version: "2.3.0", URL: "http://127.0.0.1:4096", Password: "pw", PID: 42},
		// Machine ops.
		"SharedRoot":       machine.SharedRoot{HarnessRoot: "/srv", StageRoot: "/home/u/src"},
		"CheckoutEvidence": machine.CheckoutEvidence{Common: "64769:1234", Subproject: "app", Remote: "github.com/o/r", RepoPath: "app"},
		"SharedPath":       db.SharedPath{ConnectionID: 7, HarnessRoot: "/srv", StageRoot: "/home/u/src"},
	}
	for name, v := range samples {
		t.Run(name, func(t *testing.T) {
			rv := reflect.ValueOf(v)
			for i := range rv.NumField() {
				if f := rv.Type().Field(i); f.IsExported() && rv.Field(i).IsZero() {
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
