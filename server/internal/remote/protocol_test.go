package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// Every RPC body round-trips through JSON; each sample sets every field
// (the adapter contract's own types are covered by machine's seam test).
func TestRPCBodiesRoundTripJSON(t *testing.T) {
	at := time.Date(2026, 11, 22, 10, 0, 5, 0, time.UTC)
	ref := execution.SessionRef{ConnectionID: 7, ExternalSessionID: "s"}
	intent := execution.Intent{RequestID: "r", ConnectionID: 7, Project: "p", Directory: "/w", Title: "t", Prompt: "do", Config: execution.Config{Model: "m", Options: []execution.ModelOption{}, Permission: "x", Mode: "y"},
		Workspace: execution.Workspace{Kind: "root", Base: "b", Branch: "br", FromRemote: true}, Images: []execution.Image{{Name: "a.png", MimeType: "image/png"}}, Marker: "stage-launch:r"}
	roots := []machine.SharedRoot{{HarnessRoot: "/srv", StageRoot: "/home/u/srv"}}
	str := "s"
	samples := map[string]any{
		"Description": Description{Profile: harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, Connections: []Hosted{{ConnectionID: 7, Traits: []string{execution.TraitLinkResolver}}},
			Connectors: []ConnectorState{{Kind: "t3", Summary: harness.Summary{ConnectionID: 7, Configured: true, Connected: true, LastError: "e"}}}, SetupReady: true},
		"ConnectorRequest": ConnectorRequest{Kind: "opencode", Refresh: true, Settings: json.RawMessage(`{"endpoint":"x"}`), Action: "pair", Input: json.RawMessage(`{"link":"l"}`)},
		"ConnectorStatus": ConnectorStatus{Status: harness.Status{Summary: harness.Summary{ConnectionID: 7, Configured: true, Connected: true, LastError: "e"}, State: "connected", Reason: "r",
			Fields: []harness.FieldState{{ID: "url", Value: "v", Set: true, Source: "saved"}}, Actions: []harness.ActionState{{ID: "pair", Available: true, Reason: "r"}},
			Details: map[string]any{"t3Url": "http://x"}}, Result: harness.Result{Replaced: 3}},
		"T3Settings":        T3Settings{LastError: &str, LastFullSyncAt: &at, T3URL: &str, T3PublicURL: &str, EnvironmentID: &str, ConnectionID: 7},
		"URLRequest":        URLRequest{URL: "http://x"},
		"MessageRequest":    MessageRequest{Message: "m"},
		"OpenCodeSettings":  OpenCodeSettings{Setup: config.OpenCodeSetup{Endpoint: "e", BrowserEndpoint: "b", WebClient: "w", Auth: "paired"}, Verified: "e"},
		"ClaudeSettings":    config.ClaudeSettings{Binary: "/usr/bin/claude", ConfigDir: "/home/u/.claude", Disconnected: true},
		"KindRequest":       KindRequest{Kind: "t3"},
		"IDResult":          IDResult{ID: 7},
		"IDsResult":         IDsResult{IDs: []int64{1, 2}},
		"ConnectionRequest": ConnectionRequest{ID: 7},
		"SwitchRequest":     SwitchRequest{Kind: "t3", Identity: "env", Label: "l", Revive: 3},
		"SwitchResult":      SwitchResult{Old: 1, New: 2, Changed: true, Revived: true},
		"IdentityRequest":   IdentityRequest{ID: 7, Identity: "i", Label: "l"},
		"Evidence": Evidence{Bindings: []db.ProjectBinding{{ID: 1, ProjectID: 2, ConnectionID: 7, Harness: "opencode", ExternalProjectID: "p", Workspace: "/w", Revision: 1, Status: "available", ObservedWorkspace: &str, ConnectionActive: true, ConnectionLabel: "l", Origin: db.BindingPath}},
			Observations: []execution.Observation{{Session: ref, Project: "p", State: "running", CreatedAt: at, UpdatedAt: at}}, Unknown: true},
		"SessionRequest":   SessionRequest{Session: ref},
		"WorkspaceRequest": WorkspaceRequest{Project: "p", Directory: "/w"},
		"RegisterRequest":  RegisterRequest{Title: "t", Directory: "/w"},
		"OpeningRequest":   OpeningRequest{Session: ref, Full: true},
		"LaunchRequest":    LaunchRequest{Intent: intent, Checkpoint: execution.Checkpoint{Version: 3, Data: json.RawMessage(`{"a":1}`)}},
		"ImageRequest":     ImageRequest{Index: 2},
		"LinkRequest":      LinkRequest{Link: "l"},
		"CorrelateRequest": CorrelateRequest{Intent: intent},
		"RespondRequest":   RespondRequest{Session: ref, RequestID: "q", Response: execution.Response{Option: "o", Text: "t"}},
		"SendRequest":      SendRequest{Session: ref, Message: execution.Message{Text: "hi"}},
		"InventoryRequest": InventoryRequest{Workspaces: []execution.WorkspaceInfo{{ID: "p", WorkspaceRoot: "/w", CreatedAt: at, UpdatedAt: at, Repo: "r", RepoPath: "."}}, Roots: roots},
		"CheckoutRequest":  CheckoutRequest{Roots: roots, Directory: "/srv/x"},
	}
	for name, v := range samples {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(name, err)
		}
		back := reflect.New(reflect.TypeOf(v))
		if err := json.Unmarshal(raw, back.Interface()); err != nil {
			t.Fatal(name, err)
		}
		if !reflect.DeepEqual(back.Elem().Interface(), v) {
			t.Errorf("%s did not round-trip:\n%+v\n%+v", name, v, back.Elem().Interface())
		}
	}
}

// Errors keep the adapter contract's meaning across the channel: offline,
// not hosted and an older worker are unavailable; lost replies and worker
// timeouts stay uncertain; only the worker adapter's own rejection is
// Rejected.
func TestErrorsCrossTheChannel(t *testing.T) {
	roundTrip := func(err error) error {
		e := workerchan.ErrorOf(encodeError(err))
		return decodeError(&workerchan.RemoteError{Code: e.Code, Message: e.Message})
	}
	var rejected *execution.Rejected
	if err := roundTrip(&execution.Rejected{Reason: "model unavailable"}); !errors.As(err, &rejected) || rejected.Reason != "model unavailable" {
		t.Fatal("rejection lost", err)
	}
	var refused *execution.Refused
	if err := roundTrip(&execution.Refused{Reason: "Track it through T3."}); !errors.As(err, &refused) || refused.Reason != "Track it through T3." {
		t.Fatal("refusal lost", err)
	}
	for _, sentinel := range []error{execution.ErrUnavailable, execution.ErrNotFound, execution.ErrNoReply} {
		if err := roundTrip(fmt.Errorf("observe: %w", sentinel)); !errors.Is(err, sentinel) {
			t.Fatal(sentinel, err)
		}
	}
	unavailable := []error{
		workerchan.ErrOffline,
		&workerchan.RemoteError{Code: CodeNotHosted, Message: "no adapter"},
		&workerchan.RemoteError{Code: CodeUnsupported, Message: "no interaction"},
		&workerchan.RemoteError{Code: workerchan.CodeUnknownKind, Message: "adapter.launch"},
	}
	for _, in := range unavailable {
		if err := decodeError(in); !errors.Is(err, execution.ErrUnavailable) || errors.As(err, &rejected) {
			t.Fatal(in, err)
		}
	}
	uncertain := []error{
		workerchan.ErrLostReply,
		&workerchan.RemoteError{Code: workerchan.CodeTimeout, Message: "deadline"},
		&workerchan.RemoteError{Code: workerchan.CodeInternal, Message: "malformed"},
		&workerchan.RemoteError{Code: workerchan.CodeCallEnded, Message: "late"},
	}
	for _, in := range uncertain {
		err := decodeError(in)
		if err == nil || errors.As(err, &rejected) || errors.Is(err, execution.ErrUnavailable) {
			t.Fatal("uncertain outcome reclassified", in, err)
		}
	}
}

// Setup errors keep the store's typed errors (and so their HTTP status and
// conflict code) across the channel in both directions.
func TestSetupErrorsCrossTheChannel(t *testing.T) {
	roundTrip := func(err error) error {
		e := workerchan.ErrorOf(EncodeSetupError(err))
		return DecodeSetupError(&workerchan.RemoteError{Code: e.Code, Message: e.Message})
	}
	var invalid *db.InvalidError
	if err := roundTrip(db.Invalid("code is required")); !errors.As(err, &invalid) || invalid.Msg != "code is required" {
		t.Fatal(err)
	}
	var conflict *db.ConflictError
	if err := roundTrip(&db.ConflictError{Code: "opencode_auth_required", Msg: "sign in"}); !errors.As(err, &conflict) || conflict.Code != "opencode_auth_required" || conflict.Msg != "sign in" {
		t.Fatal(err)
	}
	if err := roundTrip(db.Conflict("locked")); !errors.As(err, &conflict) || conflict.Code != "" || conflict.Msg != "locked" {
		t.Fatal(err)
	}
	var harnErr *db.HarnessError
	if err := roundTrip(&db.HarnessError{Err: errors.New("pairing failed: bad code")}); !errors.As(err, &harnErr) || harnErr.Error() != "pairing failed: bad code" {
		t.Fatal(err)
	}
	if err := roundTrip(fmt.Errorf("%w: connection 1", db.ErrNotFound)); !errors.Is(err, db.ErrNotFound) {
		t.Fatal(err)
	}
	if err := roundTrip(setupNotReady()); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatal(err)
	}
	// Connector calls: offline and a worker still starting are coded
	// unavailability; the typed errors pass through.
	var u *UnavailableError
	if err := connectorError(workerchan.ErrOffline); !errors.As(err, &u) || u.Code != CodeMachineOffline || !errors.Is(err, execution.ErrUnavailable) {
		t.Fatal(err)
	}
	if err := connectorError(setupNotReady()); !errors.As(err, &u) || u.Code != CodeMachineStarting {
		t.Fatal(err)
	}
	if err := connectorError(EncodeSetupError(db.Invalid("bad"))); !errors.As(err, &invalid) {
		t.Fatal(err)
	}
}
