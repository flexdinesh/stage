package serve_test

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/auth"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/ownercontrol"
	"github.com/flexdinesh/stage/server/internal/serve"
)

const ownerReady = "Only installation owner commands are available"

func startSharedOwner(t *testing.T, opts serve.Options) (*inst, *syncBuffer) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	i := &inst{cancel: cancel, done: make(chan error, 1)}
	logger, logs := bufLogger()
	opts.Logger = logger
	opts.Ready = func(serve.Ready) { t.Error("served product traffic before shared transport integration") }
	go func() { i.done <- serve.Run(ctx, opts) }()
	t.Cleanup(func() { i.stop(t) })
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for !strings.Contains(logs.String(), ownerReady) {
		select {
		case err := <-i.done:
			close(i.done)
			t.Fatalf("shared owner startup: %v", err)
		case <-deadline.C:
			t.Fatal("owner control did not start")
		case <-poll.C:
		}
	}
	return i, logs
}

func TestSharedDataNeverStartsPersonalRuntime(t *testing.T) {
	short := isolate(t)
	c := cfg(t, filepath.Join(short, "data"), "unix:"+filepath.Join(short, "shared.sock"))
	sharedDataNeverStartsPersonalRuntime(t, c)
}

func sharedDataNeverStartsPersonalRuntime(t *testing.T, c config.Config) {
	t.Helper()
	c.PublicURL = "https://stage.example"
	on, off := true, false
	output := &syncBuffer{}
	opts := serve.Options{Config: c, SharedAccess: &on, BootstrapOutput: output}
	first, logs := startSharedOwner(t, opts)
	link := setupLink(t, output.String(), c.PublicURL, "/auth/setup")
	assertPrivateOwner(t, c, logs.String(), link)
	if err := first.stop(t); err != nil {
		t.Fatal(err)
	}

	output = &syncBuffer{}
	opts.SharedAccess = nil
	opts.BootstrapOutput = output
	restarted, logs := startSharedOwner(t, opts)
	if output.String() != "" || !strings.Contains(logs.String(), "owner reissue-setup") {
		t.Fatal("pending restart should offer the reissue command without showing a link")
	}
	newLink, err := ownercontrol.ReissueSetup(t.Context(), c.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	setupLink(t, newLink, c.PublicURL, "/auth/setup")
	if newLink == link {
		t.Fatal("reissue returned the previous link")
	}
	assertPrivateOwner(t, c, logs.String(), newLink)
	if err := restarted.stop(t); err != nil {
		t.Fatal(err)
	}

	opts.SharedAccess = &off
	opts.Logger = quiet()
	if err := serve.Run(t.Context(), opts); !errors.Is(err, db.ErrSharedAccessRequired) {
		t.Fatalf("explicit false: %v", err)
	}
}

func TestPrivateBootstrapAndAdminRecoveryAcrossProfiles(t *testing.T) {
	for _, profile := range []string{"managed", "container", "redirected foreground"} {
		t.Run(profile, func(t *testing.T) {
			short := isolate(t)
			c := cfg(t, filepath.Join(short, "data"), "unix:"+filepath.Join(short, "product.sock"))
			c.PublicURL = "https://stage.example"
			on := true
			output := &syncBuffer{}
			opts := serve.Options{Config: c, SharedAccess: &on, BootstrapOutput: output}
			switch profile {
			case "managed":
				opts.Service = &serve.Service{Manager: "systemd", Name: "stage-test"}
			case "container":
				opts.Config = cfg(t, c.DataDir, freePort(t))
				opts.Config.PublicURL = c.PublicURL
				opts.Profile.Kind = harness.ProfileContainer
			case "redirected foreground":
				opts.BootstrapOutput = nil
			}
			i, logs := startSharedOwner(t, opts)
			bootstrap := ownercontrol.BootstrapPath(c.DataDir)
			st, err := os.Stat(bootstrap)
			if err != nil || st.Mode().Perm() != 0o600 || output.String() != "" {
				t.Fatal("bootstrap file must be private and separate from captured stdout")
			}
			body, err := os.ReadFile(bootstrap)
			if err != nil {
				t.Fatal(err)
			}
			link := setupLink(t, string(body), c.PublicURL, "/auth/setup")
			assertPrivateOwner(t, opts.Config, logs.String(), link)
			rotated, err := ownercontrol.ReissueSetup(t.Context(), c.DataDir)
			if err != nil {
				t.Fatal(err)
			}
			gone(t, bootstrap)
			if err := i.stop(t); err != nil {
				t.Fatal(err)
			}

			const email = "owner@example.test"
			eng, err := sqlite.Open(t.Context(), filepath.Join(c.DataDir, sqlite.DBFile))
			if err != nil {
				t.Fatal(err)
			}
			svc := auth.New(db.NewStore(eng), c.PublicURL, time.Now)
			if _, err := svc.InspectGrant(t.Context(), linkToken(t, link), store.GrantSetup); !errors.Is(err, auth.ErrPending) {
				t.Fatalf("rotated link remained valid: %v", err)
			}
			claimed, err := svc.ClaimSetup(t.Context(), linkToken(t, rotated), email, "Owner", "spindrift persimmon telescope")
			_ = eng.Close()
			if err != nil || claimed.Account.ID != 1 || claimed.Account.Role != store.RoleAdmin {
				t.Fatalf("claim original user 1 after restart: %v", err)
			}

			opts.SharedAccess = nil
			_, logs = startSharedOwner(t, opts)
			gone(t, bootstrap)
			if _, err := ownercontrol.ReissueSetup(t.Context(), c.DataDir); err == nil {
				t.Fatal("setup reissue remained open after claim")
			}
			reset, err := ownercontrol.RecoverAdmin(t.Context(), c.DataDir, email)
			if err != nil {
				t.Fatal(err)
			}
			setupLink(t, reset, c.PublicURL, "/auth/reset")
			assertPrivateOwner(t, opts.Config, logs.String(), reset)
		})
	}
}

func TestSharedOriginNeedsBrowserReachability(t *testing.T) {
	short := isolate(t)
	data := filepath.Join(short, "data")
	c := cfg(t, data, "unix:"+filepath.Join(short, "product.sock"))
	on := true
	for _, publicURL := range []string{"", "http://127.0.0.1:7380", "http://stage.example", "https://secret@example.test"} {
		c.PublicURL = publicURL
		err := serve.Run(t.Context(), serve.Options{Config: c, SharedAccess: &on, Logger: quiet()})
		var invalid *config.InvalidError
		if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "STAGE_PUBLIC_URL") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("expected actionable and secret-free origin error, got %v", err)
		}
	}
	// Invalid shared configuration must leave the private product socket
	// available for the unchanged personal installation.
	c.PublicURL = ""
	personal := start(t, c, newT3())
	if err := personal.stop(t); err != nil {
		t.Fatal(err)
	}
	address := freePort(t)
	c = cfg(t, data, address)
	c.PublicURL = "http://" + address
	output := &syncBuffer{}
	_, logs := startSharedOwner(t, serve.Options{Config: c, SharedAccess: &on, BootstrapOutput: output})
	link := setupLink(t, output.String(), c.PublicURL, "/auth/setup")
	assertPrivateOwner(t, c, logs.String(), link)
}

type failedBootstrap struct{ syncBuffer }

func (f *failedBootstrap) Write(p []byte) (int, error) {
	_, _ = f.syncBuffer.Write(p)
	return 0, errors.New(string(p))
}

func TestPrivateBootstrapWriteFailure(t *testing.T) {
	short := isolate(t)
	c := cfg(t, filepath.Join(short, "data"), "unix:"+filepath.Join(short, "product.sock"))
	c.PublicURL = "https://stage.example"
	on := true
	output := &failedBootstrap{}
	_, logs := startSharedOwner(t, serve.Options{Config: c, SharedAccess: &on, BootstrapOutput: output})
	first := setupLink(t, output.String(), c.PublicURL, "/auth/setup")
	if !strings.Contains(logs.String(), "owner reissue-setup") {
		t.Fatal("operator output failure should identify the recovery command")
	}
	assertPrivateOwner(t, c, logs.String(), first)
	if _, err := ownercontrol.ReissueSetup(t.Context(), c.DataDir); err != nil {
		t.Fatal(err)
	}
}

func setupLink(t *testing.T, out, origin, path string) string {
	t.Helper()
	var found string
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(line, "http") {
			continue
		}
		if found != "" {
			t.Fatal("setup output contained multiple URLs")
		}
		found = line
	}
	u, err := url.Parse(found)
	if err != nil || u.Scheme+"://"+u.Host != origin || u.Path != path || u.RawQuery != "" {
		t.Fatal("link did not use the configured origin, expected path and fragment")
	}
	linkToken(t, found)
	return found
}

func linkToken(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal("invalid grant URL")
	}
	token, ok := strings.CutPrefix(u.Fragment, "token=")
	if !ok || len(token) != 43 {
		t.Fatal("grant URL has no opaque 256-bit token in its fragment")
	}
	return token
}

func assertPrivateOwner(t *testing.T, c config.Config, logs, link string) {
	t.Helper()
	private := filepath.Dir(ownercontrol.BootstrapPath(c.DataDir))
	if st, err := os.Stat(private); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatal("owner control directory must be accessible only to the installation owner")
	}
	if st, err := os.Stat(filepath.Join(private, "control.sock")); err != nil || st.Mode()&os.ModeSocket == 0 || st.Mode().Perm() != 0o600 {
		t.Fatal("owner control must use a separate private Unix socket")
	}
	_, meta, err := instance.ReadHolder(c.DataDir)
	if err != nil || meta == nil || len(meta.Listeners) != 0 {
		t.Fatal("service status must not advertise private owner control")
	}
	holder, err := os.ReadFile(filepath.Join(c.DataDir, instance.LockFile))
	token := linkToken(t, link)
	if err != nil || strings.Contains(logs, token) || strings.Contains(string(holder), token) {
		t.Fatal("the setup or recovery grant reached diagnostics")
	}
	for _, l := range c.Listen {
		conn, err := net.DialTimeout(l.Network, l.Address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			t.Fatal("a product listener offered traffic before shared transport integration")
		}
	}
}
