package opencodesetup

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

// A saved address carrying credentials, a query or a fragment is never
// echoed by Status.
func TestStatusNeverEchoesSavedSecrets(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var temp dbtest.DB
	store := db.NewStore(temp.Reset(t))
	setup := SetupSettings{Endpoint: "https://user:private@example.com", BrowserEndpoint: "https://example.com?secret=private", WebClient: "https://example.com#private", Auth: AuthBasic}
	p := storePorts(store, func(int64, int64, execution.Adapter) {})
	if _, err := p.Identity.SwitchInstance(t.Context(), opencode.Kind, setup.Endpoint, "OpenCode", 0); err != nil {
		t.Fatal(err)
	}
	persist := settingsStore{public: p.Settings, secret: p.Secrets, atomic: p.SaveSettingsWithSecret}
	if err := persist.save(t.Context(), settings{}, settings{SetupSettings: setup, Credential: Credential{Username: "user", Password: "private"}}); err != nil {
		t.Fatal(err)
	}
	s, err := newService(t.Context(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(s.Status(t.Context()))
	if err != nil || strings.Contains(string(body), "private") {
		t.Fatalf("public status contains the address or credentials: %s", body)
	}
}

func TestModuleRestoresSavedAdapter(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx := t.Context()
	var temp dbtest.DB
	store := db.NewStore(temp.Reset(t))
	fake := opencodetest.New(t, "saved-secret")
	var in installs
	p := storePorts(store, in.install)
	persist := settingsStore{public: p.Settings, secret: p.Secrets, atomic: p.SaveSettingsWithSecret}
	saved := settings{
		SetupSettings: SetupSettings{Endpoint: fake.URL, Auth: AuthBasic},
		Credential:    Credential{Username: opencode.PairUser, Password: "saved-secret"},
	}
	if err := persist.save(ctx, settings{}, saved); err != nil {
		t.Fatal(err)
	}

	initial, err := Module.Compose(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if sum := initial.Connector.Summary(ctx); sum.ConnectionID != 0 || sum.Configured || len(in.moves) != 0 {
		t.Fatalf("startup created an OpenCode connection from settings alone: %+v, moves %v", sum, in.moves)
	}
	if _, err := initial.Connector.Configure(ctx, json.RawMessage("{}")); err != nil {
		t.Fatal(err)
	}
	first := initial.Connector.Summary(ctx).ConnectionID
	initial.Close()
	if first == 0 || !store.ConnectionActive(first) {
		t.Fatalf("verification did not activate a connection: %d", first)
	}

	restarted, err := Module.Compose(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	st, err := restarted.Connector.Status(ctx, true)
	if err != nil || !st.Connected || st.ConnectionID != first || st.State != "connected" {
		t.Fatalf("saved OpenCode connection did not activate: %+v %v", st, err)
	}
	if len(in.moves) != 2 || in.moves[1] != [2]int64{first, first} {
		t.Fatalf("saved adapter moves: %v", in.moves)
	}
}

func TestModuleSettingsSchema(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"endpoint":null,"browserEndpoint":"http://localhost:8080","auth":null}`,
		`{"webClient":"http://localhost:8081","auth":"paired"}`,
	} {
		if err := Module.ValidateSettings(json.RawMessage(raw)); err != nil {
			t.Fatalf("rejected settings %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`null`, `{"password":"secret"}`, `{"endpoint":1}`, `{"auth":"unknown"}`,
		`{"endpoint":"http://user:secret@localhost"}`,
		`{"webClient":"http://localhost?token=secret"}`,
	} {
		if err := Module.ValidateSettings(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid settings %s", raw)
		} else if strings.Contains(err.Error(), "secret") {
			t.Fatal("settings validation exposed a credential value")
		}
	}
	legacy := Module.LegacySecrets[0]
	raw := json.RawMessage(`{"username":"opencode","password":"legacy","tokenIssuedAt":"2026-11-26T01:02:03Z"}`)
	cred, err := decodeCredential(raw)
	if legacy.Entry != "opencode" || legacy.Kind != opencode.Kind || legacy.Key != "credential" || legacy.Validate(raw) != nil ||
		err != nil || cred.Username != opencode.PairUser || cred.Password != "legacy" || cred.TokenIssuedAt == nil {
		t.Fatal("the legacy OpenCode credential failed validation")
	}
	got, err := json.Marshal(cred)
	if err != nil || string(got) != string(raw) {
		t.Fatal("the legacy OpenCode credential encoding changed")
	}
}
