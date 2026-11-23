package opencodesetup

import (
	"context"
	"testing"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/opencode"
)

// storePorts are the local machine's store-backed ports.
func storePorts(store *db.Store, install func(old, next int64, a execution.Adapter)) Ports {
	p := machine.NewStorePorts(store.OnMachine(db.LocalMachine), harness.MustRegistry(opencode.Descriptor))
	return Ports{Settings: p, Identity: p, Install: install}
}

// savedSettings reads the local machine's saved settings joined.
func savedSettings(store *db.Store) (config.OpenCodeSettings, string, error) {
	setup, cred, verified, err := store.OpenCodeSettings(context.Background(), db.LocalMachine)
	return config.JoinOpenCode(setup, cred), verified, err
}

// Fake ports: the setup needs nothing from the store.
type fakeSettings struct {
	setup config.OpenCodeSetup
	cred  config.OpenCodeCredential
	saves int
}

func (f *fakeSettings) OpenCodeSettings(context.Context) (config.OpenCodeSetup, config.OpenCodeCredential, string, error) {
	return f.setup, f.cred, "", nil
}
func (f *fakeSettings) SaveOpenCodeSettings(_ context.Context, s config.OpenCodeSetup, c config.OpenCodeCredential) error {
	f.setup, f.cred = s, c
	f.saves++
	return nil
}

type fakeIdentity struct{ Identity }

func (fakeIdentity) EnsureOpenCode(context.Context) (int64, error) { return 9, nil }

type fakeLocal struct{ reads int }

func (f *fakeLocal) Registration() (opencode.Registration, error) {
	f.reads++
	return opencode.Registration{}, &opencode.RegistrationError{Reason: "missing", Path: "/fake/service.json"}
}
func (f *fakeLocal) Probe(context.Context, opencode.Registration) (string, error) { return "", nil }

func TestSetupRunsOnPortsAlone(t *testing.T) {
	settings := &fakeSettings{setup: config.OpenCodeSetup{Auth: config.OpenCodeAuthBasic}, cred: config.OpenCodeCredential{Username: "u", Password: "p"}}
	local := &fakeLocal{}
	s, err := New(t.Context(), Ports{Settings: settings, Identity: fakeIdentity{}, Install: func(int64, int64, execution.Adapter) {}, Local: local}, nil)
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
	if settings.saves != 1 || settings.setup != (config.OpenCodeSetup{Auth: config.OpenCodeAuthNone}) || settings.cred != (config.OpenCodeCredential{}) {
		t.Fatalf("disconnect saved %+v %+v", settings.setup, settings.cred)
	}
}
