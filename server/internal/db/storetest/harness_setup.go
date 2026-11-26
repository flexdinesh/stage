package storetest

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

func init() {
	register("HarnessSetup", caseHarnessSetup)
	register("HarnessSetupConcurrentPatches", caseHarnessSetupConcurrentPatches)
}

func requireJSON(t *testing.T, got json.RawMessage, err error, want string) {
	t.Helper()
	var actual, expected any
	if err != nil || json.Unmarshal(got, &actual) != nil || json.Unmarshal([]byte(want), &expected) != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("got %s (%v), want %s", got, err, want)
	}
}

func caseHarnessSetup(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	const kind = "test"
	const machine = db.LocalMachine
	remote := pairMachine(t, s, ctx, "worker").MachineID
	if id, identity, err := s.ActiveConnectionIdentity(ctx, machine, kind); id != 0 || identity != "" || err != nil {
		t.Fatalf("unconnected kind: %d %q %v", id, identity, err)
	}

	raw, err := s.HarnessSettings(ctx, machine, kind)
	requireJSON(t, raw, err, `{}`)
	if secret, err := s.HarnessSecret(ctx, machine, kind, "key"); err != nil || secret != nil {
		t.Fatalf("missing secret: %s %v", secret, err)
	}
	if status, err := s.HarnessSyncStatus(ctx, machine, kind); err != nil || status != (db.HarnessSyncStatus{}) {
		t.Fatalf("missing status: %+v %v", status, err)
	}
	if err := s.SaveHarnessSettingsAndSecret(ctx, machine, kind, json.RawMessage(`{"url":"http://example","nested":{"one":1},"enabled":true}`), "key", json.RawMessage(`{"secret":"value"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveHarnessSettings(ctx, machine, kind, json.RawMessage(`{"url":null,"nested":{"two":2}}`)); err != nil {
		t.Fatal(err)
	}
	for _, patch := range []string{`null`, `[]`, `{"bad":}`} {
		if err := s.SaveHarnessSettings(ctx, machine, kind, json.RawMessage(patch)); err == nil {
			t.Fatalf("invalid settings patch accepted: %s", patch)
		}
	}
	if err := s.SaveHarnessSettingsAndSecret(ctx, machine, kind, json.RawMessage(`{"enabled":false}`), "key", json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid secret accepted")
	}
	raw, err = s.HarnessSettings(ctx, machine, kind)
	requireJSON(t, raw, err, `{"url":null,"nested":{"two":2},"enabled":true}`)
	raw, err = s.HarnessSecret(ctx, machine, kind, "key")
	requireJSON(t, raw, err, `{"secret":"value"}`)
	raw, err = s.HarnessSettings(ctx, remote, kind)
	requireJSON(t, raw, err, `{}`)
	raw, err = s.HarnessSettings(ctx, machine, "other")
	requireJSON(t, raw, err, `{}`)
	if secret, err := s.HarnessSecret(ctx, remote, kind, "key"); err != nil || secret != nil {
		t.Fatalf("secret leaked to worker: %s %v", secret, err)
	}
	if err := s.SaveHarnessSettingsAndSecret(ctx, machine, kind, json.RawMessage(`{"enabled":false}`), "key", nil); err != nil {
		t.Fatal(err)
	}
	raw, err = s.HarnessSettings(ctx, machine, kind)
	requireJSON(t, raw, err, `{"url":null,"nested":{"two":2},"enabled":false}`)
	if secret, err := s.HarnessSecret(ctx, machine, kind, "key"); err != nil || secret != nil {
		t.Fatalf("deleted secret: %s %v", secret, err)
	}
	if err := s.SetHarnessSecret(ctx, machine, kind, "key", json.RawMessage(`null`)); err != nil {
		t.Fatal(err)
	}
	raw, err = s.HarnessSecret(ctx, machine, kind, "key")
	requireJSON(t, raw, err, `null`)
	if err := s.DeleteHarnessSecret(ctx, machine, kind, "key"); err != nil {
		t.Fatal(err)
	}
	if secret, err := s.HarnessSecret(ctx, machine, kind, "key"); err != nil || secret != nil {
		t.Fatalf("deleted JSON null returned %s %v", secret, err)
	}

	at := time.Date(2026, time.November, 26, 1, 2, 3, 456789000, time.FixedZone("AEST", 10*60*60))
	for _, err := range race(2, func(i int) error {
		if i == 0 {
			return s.SetHarnessLastError(ctx, machine, kind, "read failed")
		}
		return s.SetHarnessLastFullSync(ctx, machine, kind, at)
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	status, err := s.HarnessSyncStatus(ctx, machine, kind)
	if err != nil || status.LastError == nil || *status.LastError != "read failed" ||
		status.LastFullSyncAt == nil || !status.LastFullSyncAt.Equal(at) || status.LastFullSyncAt.Location() != time.UTC {
		t.Fatalf("status %+v %v", status, err)
	}
	if other, err := s.HarnessSyncStatus(ctx, remote, kind); err != nil || other != (db.HarnessSyncStatus{}) {
		t.Fatalf("status leaked to worker: %+v %v", other, err)
	}
	if err := s.SetHarnessLastError(ctx, machine, kind, ""); err != nil {
		t.Fatal(err)
	}
	status, err = s.HarnessSyncStatus(ctx, machine, kind)
	if err != nil || status.LastError != nil || status.LastFullSyncAt == nil || !status.LastFullSyncAt.Equal(at) {
		t.Fatalf("status after clearing error %+v %v", status, err)
	}
}

func caseHarnessSetupConcurrentPatches(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	const kind = "merge"
	const writers = 16
	for _, err := range race(writers, func(i int) error {
		patch := json.RawMessage(fmt.Sprintf(`{"field%d":%d,"kept":null}`, i, i))
		return s.SaveHarnessSettings(ctx, db.LocalMachine, kind, patch)
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err := s.HarnessSettings(ctx, db.LocalMachine, kind)
	var settings map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &settings) != nil || len(settings) != writers+1 || string(settings["kept"]) != "null" {
		t.Fatalf("lost concurrent patch: %s %v", raw, err)
	}
	for i := range writers {
		if got := string(settings[fmt.Sprintf("field%d", i)]); got != fmt.Sprint(i) {
			t.Fatalf("field%d = %s", i, got)
		}
	}
}
