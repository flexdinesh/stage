package workersetup_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/worker"
	"github.com/flexdinesh/stage/server/internal/workersetup"
)

const cacheIdentity = "test-join-fingerprint"

func newCachedRig(t *testing.T) *rig {
	return newRigWith(t, nil, rigOptions{profile: harness.Profile{Kind: harness.ProfileServer}, cacheIdentity: cacheIdentity})
}

func (r *rig) cachePath() string {
	return filepath.Join(worker.Dir(r.cfgPath), "settings-cache.json")
}

func TestSetupCacheRestoresTheAdapterBeforeChannelAndReconcilesOnAttach(t *testing.T) {
	r := newCachedRig(t)
	ctx := t.Context()
	fake := opencodetest.New(t, "requires-auth")
	link, err := json.Marshal(map[string]string{"link": fake.Link()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.connector(opencode.Kind).Run(ctx, opencode.ActionPair, link); err != nil {
		t.Fatal(err)
	}
	id := r.store.ActiveID(r.id, opencode.Kind)
	if id == 0 {
		t.Fatal("OpenCode pairing did not create a connection")
	}
	cred := r.openCodeCred()
	if !fake.Accepts(cred.Password) {
		t.Fatal("pairing did not save a usable local credential")
	}
	c := r.connector(t3.Kind)
	if _, err := c.Configure(ctx, json.RawMessage(`{"url":"http://cached.t3:3773","adminToken":"harness-admin-secret"}`)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(r.cachePath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("settings cache mode: %o", info.Mode().Perm())
	}
	cache, err := os.ReadFile(r.cachePath())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := worker.LoadConfig(r.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{cred.Password, cfg.Token, "harness-admin-secret"} {
		if strings.Contains(string(cache), secret) {
			t.Fatal("settings cache contains a credential")
		}
	}

	r.stopWorker()
	if err := r.store.RetireConnection(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.store.SaveHarnessSettings(ctx, r.id, t3.Kind, json.RawMessage(`{"url":"http://current.t3:3773"}`)); err != nil {
		t.Fatal(err)
	}

	type restored struct {
		host    *remote.Host
		err     error
		ready   bool
		session string
		found   bool
		healthy bool
		health  string
		url     any
	}
	offline := make(chan restored, 1)
	r.startWorkerWith(func(ctx context.Context, host *remote.Host, setup *workersetup.Setup) {
		out := restored{host: host, err: setup.Restore(ctx), ready: host.Ready(), session: r.hub.SessionID(r.id)}
		if a, ok := host.Local().Adapter(id); ok {
			out.found = true
			h := a.Health(ctx)
			out.healthy, out.health = h.Available, h.Reason
		}
		if c, ok := host.Local().Connector(t3.Kind); ok {
			st, err := c.Status(ctx, false)
			out.err = errors.Join(out.err, err)
			if len(st.Fields) != 0 {
				out.url = st.Fields[0].Value
			}
		}
		offline <- out
	})
	got := <-offline
	if got.err != nil || got.ready || got.session != "" || !got.found || !got.healthy || got.url != "http://cached.t3:3773" {
		t.Fatalf("offline restoration: %+v", got)
	}
	if _, ok := got.host.Local().Adapter(id); ok {
		t.Fatal("retired adapter survived authoritative reconciliation")
	}
	st, err := c.Status(ctx, false)
	if err != nil || len(st.Fields) == 0 || st.Fields[0].Value != "http://current.t3:3773" {
		t.Fatalf("T3 after authoritative reconciliation: %+v %v", st.Fields, err)
	}
	if got := r.store.ActiveID(r.id, opencode.Kind); got != 0 {
		t.Fatalf("offline restore recreated OpenCode connection %d", got)
	}

	r.stopWorker()
	offlineAgain := make(chan error, 1)
	r.startWorkerWith(func(ctx context.Context, host *remote.Host, setup *workersetup.Setup) {
		err := setup.Restore(ctx)
		if _, ok := host.Local().Adapter(id); ok {
			err = errors.Join(err, errors.New("retired OpenCode adapter was cached again"))
		}
		offlineAgain <- err
	})
	if err := <-offlineAgain; err != nil {
		t.Fatal(err)
	}
}

func TestSetupCacheReportsInvalidDataAndRecoversOnAttach(t *testing.T) {
	r := newCachedRig(t)
	tests := []struct {
		name string
		file string
		err  string
	}{
		{name: "first boot"},
		{name: "corrupt JSON", file: `{"schema":`, err: "malformed harness setup cache"},
		{name: "different join", file: `{"schema":1,"join":"old","modules":{}}`, err: "another join"},
		{name: "secret in public settings", file: `{"schema":1,"join":"test-join-fingerprint","modules":{"t3":{"settings":{"adminToken":"invalid"},"connection":{"id":0}}}}`, err: "invalid public settings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r.stopWorker()
			path := r.cachePath()
			if tt.file == "" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte(tt.file), 0o600); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			r.startWorkerWith(func(ctx context.Context, _ *remote.Host, setup *workersetup.Setup) {
				result <- setup.Restore(ctx)
			})
			err := <-result
			if tt.err == "" {
				if !errors.Is(err, workersetup.ErrNoCache) {
					t.Fatalf("first boot restore: %v", err)
				}
			} else if err == nil || errors.Is(err, workersetup.ErrNoCache) || !strings.Contains(err.Error(), tt.err) {
				t.Fatalf("invalid cache restore: %v", err)
			}
			r.stopWorker()
			r.startWorkerWith(func(ctx context.Context, _ *remote.Host, setup *workersetup.Setup) {
				result <- setup.Restore(ctx)
			})
			if err := <-result; err != nil {
				t.Fatalf("authoritative snapshot did not recover the cache: %v", err)
			}
		})
	}
}
