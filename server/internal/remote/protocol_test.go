package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

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
	samples := map[string]any{
		"Description":      Description{Profile: harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, Connections: []Hosted{{ConnectionID: 7, Traits: []string{execution.TraitLinkResolver}}}},
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
