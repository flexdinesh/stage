package opencodesetup

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/opencode"
)

func storePorts(store *db.Store, install func(old, next int64, a execution.Adapter)) harness.Ports {
	p := machine.NewStorePorts(store.OnMachine(db.LocalMachine), harness.MustRegistry(opencode.Descriptor))
	return harness.Ports{Settings: p.Settings(), Secrets: p.Secrets(), Identity: p, Install: install, SaveSettingsWithSecret: p.SaveSettingsWithSecret}
}

func savedSettings(store *db.Store) (settings, string, error) {
	p, ctx := storePorts(store, nil), context.Background()
	c, err := (settingsStore{public: p.Settings, secret: p.Secrets}).load(ctx)
	if err != nil {
		return settings{}, "", err
	}
	_, verified, err := p.Identity.ActiveIdentity(ctx, opencode.Kind)
	return c, verified, err
}

type fakeSettings struct {
	setup SetupSettings
	saves int
}

func (f *fakeSettings) Get(context.Context, string) (json.RawMessage, error) {
	return json.Marshal(f.setup)
}
func (f *fakeSettings) Save(_ context.Context, _ string, raw json.RawMessage) error {
	if err := json.Unmarshal(raw, &f.setup); err != nil {
		return err
	}
	f.saves++
	return nil
}

type fakeCredential struct {
	cred    Credential
	failSet string
}

func (f *fakeCredential) Get(context.Context, string, string) (json.RawMessage, error) {
	return json.Marshal(f.cred)
}
func (f *fakeCredential) Set(_ context.Context, _, _ string, raw json.RawMessage) error {
	var cred Credential
	if err := json.Unmarshal(raw, &cred); err != nil {
		return err
	}
	if f.failSet != "" && cred.Password == f.failSet {
		return errors.New("unexpected credential " + cred.Password)
	}
	f.cred = cred
	return nil
}
func (f *fakeCredential) Delete(context.Context, string, string) error {
	f.cred = Credential{}
	return nil
}

type fakeIdentity struct{ harness.Identity }

func (fakeIdentity) ActiveIdentity(context.Context, string) (int64, string, error) {
	return 9, "", nil
}

type fakeLocal struct{ reads int }

func (f *fakeLocal) Registration() (opencode.Registration, error) {
	f.reads++
	return opencode.Registration{}, &opencode.RegistrationError{Reason: "missing", Path: "/fake/service.json"}
}
func (f *fakeLocal) Probe(context.Context, opencode.Registration) (string, error) { return "", nil }

func TestSetupRunsOnPortsAlone(t *testing.T) {
	public := &fakeSettings{setup: SetupSettings{Auth: AuthBasic}}
	secret := &fakeCredential{cred: Credential{Username: "u", Password: "p"}}
	local := &fakeLocal{}
	s, err := newService(t.Context(), harness.Ports{Settings: public, Secrets: secret, Identity: fakeIdentity{}, Install: func(int64, int64, execution.Adapter) {}}, local)
	if err != nil {
		t.Fatal(err)
	}
	st := s.Status(t.Context())
	if st.ConnectionID != 9 || !st.HasCredentials || st.Local.Reason != "missing" || local.reads != 1 {
		t.Fatalf("status %+v reads %d", st, local.reads)
	}
	if err := s.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if public.saves != 1 || public.setup != (SetupSettings{Auth: AuthNone}) || secret.cred != (Credential{}) {
		t.Fatalf("disconnect saved %+v credential present %t", public.setup, secret.cred != (Credential{}))
	}
}

func TestWorkerCredentialWriteFailureRestoresPreviousSetup(t *testing.T) {
	previous := settings{SetupSettings: SetupSettings{Endpoint: "https://previous.example", Auth: AuthBasic}, Credential: Credential{Username: "user", Password: "previous"}}
	next := settings{SetupSettings: SetupSettings{Endpoint: "https://next.example", Auth: AuthPaired}, Credential: Credential{Username: "user", Password: "new-secret"}}
	public := &fakeSettings{setup: previous.SetupSettings}
	secret := &fakeCredential{cred: previous.Credential, failSet: next.Password}
	s := settingsStore{public: public, secret: secret}

	if err := s.save(t.Context(), previous, next); err == nil || strings.Contains(err.Error(), next.Password) {
		t.Fatal("expected a credential write error with the secret redacted")
	}
	if public.setup != previous.SetupSettings || secret.cred != previous.Credential {
		t.Fatalf("previous settings were not restored: %+v", public.setup)
	}
	secret.failSet = ""
	if err := s.save(t.Context(), previous, next); err != nil || public.setup != next.SetupSettings || secret.cred != next.Credential {
		t.Fatalf("retry failed: %v %+v", err, public.setup)
	}
}
