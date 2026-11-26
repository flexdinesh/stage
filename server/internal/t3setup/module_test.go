package t3setup_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
)

func TestModuleInstallsOnFirstPairing(t *testing.T) {
	ctx := t.Context()
	store := db.NewStore(testDB.Fresh(t))
	p := machine.NewStorePorts(store.OnMachine(db.LocalMachine), harness.MustRegistry(t3setup.Module.Descriptor))
	fake := &client{Fake: t3.NewFake()}
	fake.SetProbe(reachable("env-a"))
	var oldID, newID int64
	var installed execution.Adapter
	changes := 0
	built, err := t3setup.Module.Compose(ctx, harness.Ports{
		Settings: p.Settings(), Secrets: p.Secrets(), Identity: p,
		Overrides: map[string]any{t3.Kind: fake},
		Logger:    slog.New(slog.DiscardHandler),
		Install:   func(old, next int64, a execution.Adapter) { oldID, newID, installed = old, next, a },
		OnChanged: func() { changes++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if installed != nil || built.Connector.Summary(ctx).ConnectionID != 0 {
		t.Fatal("composition installed an adapter before the first pairing")
	}
	if _, err := built.Connector.Run(ctx, t3.ActionPair, json.RawMessage(`{"code":"CODE"}`)); err != nil {
		t.Fatal(err)
	}
	id, identity, err := p.ActiveIdentity(ctx, t3.Kind)
	adapter, ok := installed.(*t3.Adapter)
	if err != nil || id == 0 || id != newID || oldID != 0 || identity != "env-a" ||
		!ok || adapter.ConnectionID != id || adapter.Client != fake || changes != 1 {
		t.Fatalf("first pairing: (%d, %q, %v), install %d -> %d with %+v, changes %d", id, identity, err, oldID, newID, installed, changes)
	}
}

func TestModuleRestoresAndClearsSettingsAndSecrets(t *testing.T) {
	ctx := t.Context()
	store := db.NewStore(testDB.Reset(t))
	p := machine.NewStorePorts(store.OnMachine(db.LocalMachine), harness.MustRegistry(t3setup.Module.Descriptor))
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	settings, err := json.Marshal(t3setup.Settings{URL: &upstream.URL, PublicURL: ptr("https://browser.example")})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Settings().Save(ctx, t3.Kind, settings); err != nil {
		t.Fatal(err)
	}
	// This is the existing credential JSON shape, including the timestamp.
	expires := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Microsecond)
	token := json.RawMessage(`{"clientId":"stored-client","token":"stored-oauth","expiresAt":"` + expires.Format(time.RFC3339Nano) + `"}`)
	if err := p.Secrets().Set(ctx, t3.Kind, "token", token); err != nil {
		t.Fatal(err)
	}
	if err := p.Secrets().Set(ctx, t3.Kind, "adminToken", json.RawMessage(`"stored-admin"`)); err != nil {
		t.Fatal(err)
	}
	var installed int64
	built, err := t3setup.Module.Compose(ctx, harness.Ports{
		Settings: p.Settings(), Secrets: p.Secrets(), Identity: p,
		Logger: slog.New(slog.DiscardHandler),
		Getenv: func(key string) string {
			// The removed runtime settings have no bearing on saved settings.
			if key == "T3_URL" || key == "T3_PUBLIC_URL" {
				return "https://ignored.example"
			}
			return ""
		},
		Install: func(_, next int64, _ execution.Adapter) { installed = next },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()
	st, err := built.Connector.Status(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := st.Details.(t3setup.Details)
	if !ok || installed != dbtest.T3Connection || st.ConnectionID != installed || !st.Connected ||
		!d.AdminTokenConfigured || d.T3URL != upstream.URL || d.PublicURL != "https://browser.example" ||
		d.ClientID == nil || *d.ClientID != "stored-client" || d.TokenExpiresAt == nil || !d.TokenExpiresAt.Equal(expires) {
		t.Fatalf("restored status %+v, installed %d", st, installed)
	}
	statusJSON, err := json.Marshal(st)
	if err != nil || strings.Contains(string(statusJSON), "stored-oauth") || strings.Contains(string(statusJSON), "stored-admin") {
		t.Fatalf("status exposed a credential or did not marshal: %s (%v)", statusJSON, err)
	}
	if err := built.Connector.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"token", "adminToken"} {
		if raw, err := p.Secrets().Get(ctx, t3.Kind, key); err != nil || raw != nil {
			t.Fatalf("disconnect left %s set: %v", key, err)
		}
	}
	var saved map[string]json.RawMessage
	raw, err := p.Settings().Get(ctx, t3.Kind)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &saved); err != nil || string(saved["url"]) != "null" || string(saved["publicUrl"]) != "null" {
		t.Fatalf("disconnect did not persist both resets: %s (%v)", raw, err)
	}
	if built.Connector.Summary(ctx).Configured {
		t.Fatal("disconnected connector remains configured")
	}
}

func TestModuleValidatesPublicSettingsAndLegacySecrets(t *testing.T) {
	validate := t3setup.Module.ValidateSettings
	if err := validate(json.RawMessage(`{"url":null,"publicUrl":"https://t3.example"}`)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `{"adminToken":"secret"}`, `{"publicUrl":"https://t3.example/path"}`} {
		if err := validate(json.RawMessage(raw)); err == nil {
			t.Errorf("accepted public settings %s", raw)
		}
	}
	valid := map[string]struct {
		key, sample string
	}{
		"t3":       {"token", `{"clientId":"client","token":"oauth","expiresAt":"2026-11-26T12:00:00Z"}`},
		"t3-admin": {"adminToken", `"admin"`},
	}
	for _, entry := range t3setup.Module.LegacySecrets {
		want, ok := valid[entry.Entry]
		if !ok || entry.Kind != t3.Kind || entry.Key != want.key {
			t.Fatalf("unexpected legacy mapping %s -> %s/%s", entry.Entry, entry.Kind, entry.Key)
		}
		if err := entry.Validate(json.RawMessage(want.sample)); err != nil {
			t.Errorf("legacy entry %s rejected: %v", entry.Entry, err)
		}
		if err := entry.Validate(json.RawMessage(`42`)); err == nil {
			t.Errorf("legacy entry %s accepted a number", entry.Entry)
		}
		delete(valid, entry.Entry)
	}
	if len(valid) != 0 {
		t.Fatalf("missing legacy mappings: %v", valid)
	}
}

func TestModuleReportsAuthError(t *testing.T) {
	ctx := t.Context()
	store := db.NewStore(testDB.Reset(t))
	p := machine.NewStorePorts(store.OnMachine(db.LocalMachine), harness.MustRegistry(t3setup.Module.Descriptor))
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	raw, err := json.Marshal(t3setup.Settings{URL: &upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Settings().Save(ctx, t3.Kind, raw); err != nil {
		t.Fatal(err)
	}
	built, err := t3setup.Module.Compose(ctx, harness.Ports{
		Settings: p.Settings(), Secrets: p.Secrets(), Identity: p, SetLastError: p.SetLastError,
		Logger: slog.New(slog.DiscardHandler), Getenv: func(string) string { return "" },
		Install: func(int64, int64, execution.Adapter) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()
	if _, err := built.Connector.Configure(ctx, json.RawMessage(`{"adminToken":"invalid"}`)); err == nil {
		t.Fatal("rejected admin token did not return an error")
	}
	summary := built.Connector.Summary(ctx)
	saved, err := store.HarnessSyncStatus(ctx, db.LocalMachine, t3.Kind)
	if err != nil || saved.LastError == nil || summary.LastError == "" || *saved.LastError != summary.LastError {
		t.Fatalf("auth error %q, saved %+v, read error %v", summary.LastError, saved, err)
	}
	if err := built.Connector.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	saved, err = store.HarnessSyncStatus(ctx, db.LocalMachine, t3.Kind)
	if err != nil || saved.LastError != nil || built.Connector.Summary(ctx).LastError != "" {
		t.Fatalf("disconnect retained the auth error: %+v (%v)", saved, err)
	}
}
