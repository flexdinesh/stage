package install

import (
	"archive/tar"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/filelock"
)

func linuxOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the desktop fixtures are Linux archives")
	}
}

func TestInstallHeadlessFresh(t *testing.T) {
	e := newEnv(t, "stage")
	from := makeRelease(t, rel{identity: "stage", version: "1.0.0"})
	r := e.install("1.0.0", from, Options{Mode: ModeHeadless})
	mustOK(t, r)
	l := e.layout(ModeHeadless)
	if got := cliVersion(t, l.Link); got != "1.0.0" {
		t.Fatalf("link runs %s", got)
	}
	if target, _ := os.Readlink(l.Link); target != l.CLI {
		t.Fatalf("link -> %s, want %s", target, l.CLI)
	}
	for _, f := range []string{"app/LICENSE", "app/README.md"} {
		if _, err := os.Stat(filepath.Join(l.Root, f)); err != nil {
			t.Fatal(err)
		}
	}
	rec := e.record()
	if rec.Version != "1.0.0" || rec.Mode != ModeHeadless || rec.State != StateActive || rec.Channel != "stable" {
		t.Fatalf("record %+v", rec)
	}
	if st, _ := os.Stat(l.Record); st.Mode().Perm() != 0o600 {
		t.Fatalf("record mode %v", st.Mode())
	}
	if r.Service != "not-installed" || !strings.Contains(strings.Join(r.Notes, "\n"), "service start") {
		t.Fatalf("service %q notes %v", r.Service, r.Notes)
	}
	// Staging is cleaned up.
	if got := tree(t, l.Staging); len(got) != 0 {
		t.Fatalf("staging left %v", got)
	}
}

func TestInstallDesktopRegisters(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "stage")
	from := makeRelease(t, rel{identity: "stage", version: "1.0.0"})
	mustOK(t, e.install("1.0.0", from, Options{}))
	l := e.layout(ModeDesktop)
	data, err := os.ReadFile(l.Entry)
	if err != nil {
		t.Fatal(err)
	}
	entry := string(data)
	for _, want := range []string{"Exec=" + l.Desktop + " %U", "Icon=" + filepath.Join(l.App, "stage-desktop.png"), "MimeType=x-scheme-handler/stage;"} {
		if !strings.Contains(entry, want) {
			t.Fatalf("entry lacks %q:\n%s", want, entry)
		}
	}
	if l.Entry != filepath.Join(e.home, ".local", "share", "applications", "stage-desktop.desktop") {
		t.Fatalf("entry at %s", l.Entry)
	}
	if got := cliVersion(t, l.Link); got != "1.0.0" {
		t.Fatalf("link runs %s", got)
	}
	if l.CLI != filepath.Join(e.home, ".local", "lib", "stage", "app", "bin", "stage") {
		t.Fatalf("cli at %s", l.CLI)
	}
	rec := e.record()
	if _, ok := rec.owned(l.Entry); !ok || rec.Mode != ModeDesktop {
		t.Fatalf("record %+v", rec)
	}
}

func TestReinstallIsIdempotentAndRepairs(t *testing.T) {
	e := newEnv(t, "stage")
	from := makeRelease(t, rel{identity: "stage", version: "1.0.0"})
	mustOK(t, e.install("1.0.0", from, Options{Mode: ModeHeadless}))
	l := e.layout(ModeHeadless)
	if err := os.Remove(l.Link); err != nil {
		t.Fatal(err)
	}
	r := e.install("1.0.0", from, Options{})
	mustOK(t, r)
	if r.Changed || !strings.Contains(r.Message, "already installed") {
		t.Fatalf("result %+v", r)
	}
	if target, _ := os.Readlink(l.Link); target != l.CLI {
		t.Fatal("link not repaired")
	}
	if _, err := os.Stat(l.Prev); err == nil {
		t.Fatal("a no-op reinstall must not swap")
	}
}

func TestUpdateRestartsServiceAndDesktop(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{}))
	l := e.layout(ModeDesktop)
	e.svc.state, e.svc.exe, e.svc.version, e.svc.schema = "running", l.CLI, "1.0.0", 4
	e.desk.running = []int{4242}

	r := e.install("1.1.0", makeRelease(t, rel{identity: "stage", version: "1.1.0"}), Options{})
	mustOK(t, r)
	if r.Service != "restarted" || e.svc.version != "1.1.0" || !slices.Equal(e.svc.restarts, []string{"1.1.0"}) {
		t.Fatalf("service %q version %s restarts %v", r.Service, e.svc.version, e.svc.restarts)
	}
	if len(e.desk.quit) != 1 || e.desk.launched != 1 || r.Desktop != "relaunched" {
		t.Fatalf("desktop quit %v launched %d (%s)", e.desk.quit, e.desk.launched, r.Desktop)
	}
	if got := cliVersion(t, l.CLI); got != "1.1.0" {
		t.Fatalf("app is %s", got)
	}
	if got := cliVersion(t, filepath.Join(l.Prev, "bin", "stage")); got != "1.0.0" {
		t.Fatalf("prev is %s", got)
	}
	if rec := e.record(); rec.Version != "1.1.0" || rec.State != StateActive {
		t.Fatalf("record %+v", rec)
	}
	if !strings.Contains(r.Message, "1.0.0 -> 1.1.0") {
		t.Fatalf("message %q", r.Message)
	}
}

func TestUpdateKeepsHeadlessAndLeavesForeignService(t *testing.T) {
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{Mode: ModeHeadless}))
	e.svc.state, e.svc.exe, e.svc.version = "running", "/usr/bin/stage", "0.9.0"
	r := e.install("1.1.0", makeRelease(t, rel{identity: "stage", version: "1.1.0"}), Options{})
	mustOK(t, r)
	if r.Mode != ModeHeadless || e.record().Mode != ModeHeadless {
		t.Fatalf("mode %s", r.Mode)
	}
	if r.Service != "foreign" || len(e.svc.restarts) != 0 {
		t.Fatalf("service %q restarts %v", r.Service, e.svc.restarts)
	}
}

func TestModeSwitchRefused(t *testing.T) {
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{Mode: ModeHeadless}))
	r := e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{Mode: ModeDesktop})
	if r.Code != CodeUnsupported || !strings.Contains(r.Message, "headless mode") {
		t.Fatalf("result %+v", r)
	}
}

func TestVerificationFailuresWriteNothing(t *testing.T) {
	cases := []struct {
		name   string
		code   int
		break_ func(t *testing.T, dir string)
		r      rel
		msg    string
	}{
		{name: "tampered artifact", code: CodeVerify, msg: "sha256", break_: func(t *testing.T, dir string) {
			p := headless(t, dir)
			b, _ := os.ReadFile(p)
			b[len(b)-5] ^= 0xff
			_ = os.WriteFile(p, b, 0o644)
		}},
		{name: "partial download", code: CodeVerify, msg: "bytes", break_: func(t *testing.T, dir string) {
			p := headless(t, dir)
			b, _ := os.ReadFile(p)
			_ = os.WriteFile(p, b[:len(b)/2], 0o644)
		}},
		{name: "missing artifact", code: CodeVerify, msg: "missing", break_: func(t *testing.T, dir string) {
			_ = os.Remove(headless(t, dir))
		}},
		{name: "checksums disagree", code: CodeVerify, msg: "disagree", break_: func(t *testing.T, dir string) {
			b, _ := os.ReadFile(filepath.Join(dir, ChecksumsName))
			s := strings.Replace(string(b), string(b[0]), flip(b[0]), 1)
			_ = os.WriteFile(filepath.Join(dir, ChecksumsName), []byte(s), 0o644)
		}},
		{name: "manifest hash tampered to match", code: CodeVerify, msg: "disagree", break_: func(t *testing.T, dir string) {
			m := readM(t, dir)
			m.Artifacts[0].SHA256 = strings.Repeat("0", 64)
			writeM(t, dir, m)
		}},
		{name: "wrong identity", code: CodeVerify, msg: "identity", r: rel{identity: "stage-dev", version: "1.0.0", channel: "dev"}},
		{name: "dev channel for stable", code: CodeVerify, msg: "channel", r: rel{identity: "stage", version: "1.0.0", channel: "dev"}},
		{name: "wrong platform", code: CodeUnsupported, msg: "no cli artifact", r: rel{identity: "stage", version: "1.0.0", omitArch: true}},
		{name: "unsafe member", code: CodeVerify, msg: "unsafe", r: rel{identity: "stage", version: "1.0.0", extraMember: &tar.Header{Name: "../evil", Typeflag: tar.TypeReg}}},
		{name: "escaping symlink", code: CodeVerify, msg: "unexpected member", r: rel{identity: "stage", version: "1.0.0", extraMember: &tar.Header{Name: "evil", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}}},
		{name: "cli does not run", code: CodeVerify, msg: "does not run", r: rel{identity: "stage", version: "1.0.0", cliBroken: true}},
		{name: "version mismatch", code: CodeVerify, msg: "this stage is", r: rel{identity: "stage", version: "1.0.1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, "stage")
			if c.r.identity == "" {
				c.r = rel{identity: "stage", version: "1.0.0"}
			}
			dir := makeRelease(t, c.r)
			if c.break_ != nil {
				c.break_(t, dir)
			}
			r := e.install("1.0.0", dir, Options{Mode: ModeHeadless})
			if r.OK || r.Code != c.code || !strings.Contains(strings.ToLower(r.Message), c.msg) {
				t.Fatalf("got code %d %q, want %d containing %q", r.Code, r.Message, c.code, c.msg)
			}
			// Nothing outside the lock and the (emptied) staging dir.
			for _, p := range tree(t, e.home) {
				switch p {
				case ".local/", ".local/lib/", ".local/lib/stage/", ".local/lib/stage/install.lock", ".local/lib/stage/staging/":
				default:
					t.Fatalf("wrote %s", p)
				}
			}
		})
	}
}

func headless(t *testing.T, dir string) string {
	t.Helper()
	for _, a := range readM(t, dir).Artifacts {
		if a.Kind == "cli" {
			return filepath.Join(dir, a.Name)
		}
	}
	t.Fatal("no cli artifact")
	return ""
}

func flip(b byte) string {
	if b == '0' {
		return "1"
	}
	return "0"
}

func readM(t *testing.T, dir string) Manifest {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func writeM(t *testing.T, dir string, m Manifest) {
	t.Helper()
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, ManifestName), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollisionRefusedWithoutWrites(t *testing.T) {
	e := newEnv(t, "stage")
	link := filepath.Join(e.home, ".local", "bin", "stage")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{Mode: ModeHeadless})
	if r.Code != CodeUnsupported || !strings.Contains(r.Message, "did not create") || !strings.Contains(r.Fix, "--cli-name") {
		t.Fatalf("result %+v", r)
	}
	if _, err := os.Stat(e.layout(ModeHeadless).App); err == nil {
		t.Fatal("app installed despite the collision")
	}
	if b, _ := os.ReadFile(link); string(b) != "#!/bin/sh\n" {
		t.Fatal("unrelated file overwritten")
	}
	if got := CLIName(e.home, "stage"); got != "stage" {
		t.Fatalf("CLIName without a record = %q, want the identity", got)
	}
	// Another name works.
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{Mode: ModeHeadless, CLIName: "stage-cli"}))
	if e.record().Link != filepath.Join(e.home, ".local", "bin", "stage-cli") {
		t.Fatal("cli name not used")
	}
	if got := CLIName(e.home, "stage"); got != "stage-cli" {
		t.Fatalf("CLIName = %q, want the recorded stage-cli", got)
	}
}

func TestDesktopEntryCollision(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "stage")
	l := e.layout(ModeDesktop)
	if err := os.MkdirAll(filepath.Dir(l.Entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.Entry, []byte("Exec="+e.home+"/.local/stage-desktop.app/stage-desktop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{})
	if r.Code != CodeUnsupported || !strings.Contains(r.Fix, "--uninstall") {
		t.Fatalf("result %+v", r)
	}
}

func TestBusyLock(t *testing.T) {
	e := newEnv(t, "stage")
	l := e.layout(ModeHeadless)
	if err := os.MkdirAll(l.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	lk, err := filelock.Acquire(l.Lock, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	r := e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{Mode: ModeHeadless})
	if r.Code != CodeBusy {
		t.Fatalf("result %+v", r)
	}
	if _, err := os.Stat(l.App); err == nil {
		t.Fatal("installed while another holder had the lock")
	}
}

func TestConcurrentUpdatesStayCoherent(t *testing.T) {
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{Mode: ModeHeadless}))
	from := makeRelease(t, rel{identity: "stage", version: "1.1.0"})
	var wg sync.WaitGroup
	results := make([]*Result, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = e.install("1.1.0", from, Options{})
		}()
	}
	wg.Wait()
	changed := 0
	for _, r := range results {
		switch {
		case r.OK && r.Changed:
			changed++
		case r.OK, r.Code == CodeBusy:
		default:
			t.Fatalf("unexpected result %+v", r)
		}
	}
	if changed != 1 {
		t.Fatalf("%d installs swapped, want exactly 1", changed)
	}
	l := e.layout(ModeHeadless)
	if cliVersion(t, l.CLI) != "1.1.0" || e.record().Version != "1.1.0" {
		t.Fatal("incoherent result")
	}
}

func TestRecoversInterruptedSwap(t *testing.T) {
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{Mode: ModeHeadless}))
	l := e.layout(ModeHeadless)
	// Crash after moving the app aside, before moving the new one in.
	if err := os.Rename(l.App, l.Prev); err != nil {
		t.Fatal(err)
	}
	rec := e.record()
	rec.State, rec.From, rec.To = StateActivating, "1.0.0", "1.1.0"
	if err := writeRecord(l.Record, rec); err != nil {
		t.Fatal(err)
	}
	r := e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{})
	mustOK(t, r)
	if cliVersion(t, l.CLI) != "1.0.0" || e.record().State != StateActive || r.Changed {
		t.Fatalf("not recovered: %+v", r)
	}
}

func TestFailedRestartRollsBackWhenSchemaUnchanged(t *testing.T) {
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0", schema: 4}), Options{Mode: ModeHeadless}))
	l := e.layout(ModeHeadless)
	e.svc.state, e.svc.exe, e.svc.version, e.svc.schema = "running", l.CLI, "1.0.0", 4
	e.svc.failVersion = "1.1.0"
	r := e.install("1.1.0", makeRelease(t, rel{identity: "stage", version: "1.1.0", schema: 4}), Options{})
	if r.Code != CodeRolledBack || r.Service != "rolled-back" {
		t.Fatalf("result %+v", r)
	}
	if cliVersion(t, l.CLI) != "1.0.0" || e.record().Version != "1.0.0" || e.svc.version != "1.0.0" || e.svc.state != "running" {
		t.Fatalf("not rolled back: app %s record %s server %s %s", cliVersion(t, l.CLI), e.record().Version, e.svc.version, e.svc.state)
	}
	if !slices.Equal(e.svc.restarts, []string{"1.1.0", "1.0.0"}) {
		t.Fatalf("restarts %v", e.svc.restarts)
	}
}

func TestFailedRestartKeepsNewWhenSchemaChanged(t *testing.T) {
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0", schema: 4}), Options{Mode: ModeHeadless}))
	l := e.layout(ModeHeadless)
	e.svc.state, e.svc.exe, e.svc.version, e.svc.schema = "running", l.CLI, "1.0.0", 4
	e.svc.failVersion = "1.1.0"
	backup := filepath.Join(e.svc.dataDir, "backups", "stage-schema4-20261116T000000Z.db")
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.install("1.1.0", makeRelease(t, rel{identity: "stage", version: "1.1.0", schema: 5}), Options{})
	if r.Code != CodeNotRolled || r.Backup != backup || !strings.Contains(r.Fix, "docs/install.md#recovery") || len(r.LogTail) == 0 {
		t.Fatalf("result %+v", r)
	}
	if cliVersion(t, l.CLI) != "1.1.0" || e.record().Version != "1.1.0" || e.record().State != StateActive {
		t.Fatal("new version not kept")
	}
}

func TestRefusesVersionOlderThanDatabase(t *testing.T) {
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.1.0", makeRelease(t, rel{identity: "stage", version: "1.1.0", schema: 5}), Options{Mode: ModeHeadless}))
	l := e.layout(ModeHeadless)
	e.svc.state, e.svc.exe, e.svc.version, e.svc.schema = "running", l.CLI, "1.1.0", 5
	r := e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0", schema: 4}), Options{})
	if r.Code != CodeUnsupported || !strings.Contains(r.Message, "schema 5") {
		t.Fatalf("result %+v", r)
	}
	if cliVersion(t, l.CLI) != "1.1.0" || len(e.svc.restarts) != 0 {
		t.Fatal("changed despite the refusal")
	}
}

func TestDesktopNeedsWebKit(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "stage")
	r := e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0", desktopBroken: true}), Options{})
	if r.Code != CodeUnsupported || !strings.Contains(r.Message, "WebKitGTK") || !strings.Contains(r.Fix, "--headless") {
		t.Fatalf("result %+v", r)
	}
	if _, err := os.Stat(e.layout(ModeDesktop).App); err == nil {
		t.Fatal("installed without WebKit")
	}
}

func TestDiskFull(t *testing.T) {
	e := newEnv(t, "stage")
	e.d.FreeSpace = func(string) (uint64, error) { return 1 << 10, nil }
	r := e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{Mode: ModeHeadless})
	if r.Code != CodeFailed || !strings.Contains(r.Message, "disk space") {
		t.Fatalf("result %+v", r)
	}
}

func TestDesktopQuitFailureChangesNothing(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{}))
	e.desk.running = []int{7}
	e.desk.quitErr = os.ErrDeadlineExceeded
	r := e.install("1.1.0", makeRelease(t, rel{identity: "stage", version: "1.1.0"}), Options{})
	if r.OK || !strings.Contains(r.Fix, "nothing was changed") {
		t.Fatalf("result %+v", r)
	}
	if cliVersion(t, e.layout(ModeDesktop).CLI) != "1.0.0" {
		t.Fatal("swapped although the desktop did not quit")
	}
}

func TestUninstallKeepsData(t *testing.T) {
	linuxOnly(t)
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{}))
	l := e.layout(ModeDesktop)
	e.svc.state, e.svc.exe = "stopped", l.CLI
	data := filepath.Join(e.svc.dataDir, "stage.db")
	if err := os.MkdirAll(filepath.Dir(data), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(data, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	// MyGo's runtime URL handler and autostart entries point into the app.
	for _, p := range []string{l.Handler, l.Autostart} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("Exec="+l.Desktop+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d := e.d
	d.Version = "1.0.0"
	r := Uninstall(context.Background(), d)
	mustOK(t, r)
	if !e.svc.uninstalled {
		t.Fatal("service definition kept")
	}
	for _, p := range []string{l.Root, l.App, l.Link, l.Entry, l.Handler, l.Autostart} {
		if _, err := os.Lstat(p); err == nil {
			t.Fatalf("%s kept", p)
		}
	}
	if b, err := os.ReadFile(data); err != nil || string(b) != "db" {
		t.Fatal("data removed")
	}
	if !slices.Contains(r.Retained, e.svc.dataDir) {
		t.Fatalf("retained %v", r.Retained)
	}
	// Not installed any more.
	if r := Uninstall(context.Background(), d); r.Code != CodeUnsupported {
		t.Fatalf("second uninstall %+v", r)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0}, {"1.0.1", "1.0.0", 1}, {"1.10.0", "1.9.0", 1}, {"2.0.0", "10.0.0", -1},
		{"1.0.0-rc.1", "1.0.0", -1}, {"1.0.0-rc.2", "1.0.0-rc.10", -1}, {"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-rc.1", "1.0.0-rc", 1}, {"0.0.0-dev.x", "0.0.1", -1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestLayoutDarwin(t *testing.T) {
	p := Platform{GOOS: "darwin", GOARCH: "arm64", Home: "/Users/u"}
	l, err := LayoutFor(p, "stage", ModeDesktop, "")
	if err != nil {
		t.Fatal(err)
	}
	if l.App != "/Users/u/Applications/Stage.app" || l.CLI != "/Users/u/Applications/Stage.app/Contents/Resources/bin/stage" ||
		l.Desktop != "/Users/u/Applications/Stage.app/Contents/MacOS/Stage" || l.Link != "/Users/u/.local/bin/stage" || l.Entry != "" {
		t.Fatalf("layout %+v", l)
	}
	l, _ = LayoutFor(p, "stage-dev", ModeDesktop, "")
	if l.App != "/Users/u/Applications/Stage Dev.app" || l.Link != "/Users/u/.local/bin/stage-dev" {
		t.Fatalf("dev layout %+v", l)
	}
	tr, err := treeFor(Artifact{Name: "x", CLI: &Binary{Path: "Stage.app/Contents/Resources/bin/stage"}}, p, ModeDesktop)
	if err != nil || tr.strip != "Stage.app/" || tr.cli != "Contents/Resources/bin/stage" {
		t.Fatalf("tree %+v %v", tr, err)
	}
}

func TestDesktopEntryQuoting(t *testing.T) {
	l := Layout{Names: NamesFor("stage"), App: "/home/a b/.local/lib/stage/app", Desktop: "/home/a b/.local/lib/stage/app/stage-desktop"}
	got := string(desktopEntry([]byte("[Desktop Entry]\nExec=stage-desktop %U\n"), l))
	if !strings.Contains(got, `Exec="/home/a b/.local/lib/stage/app/stage-desktop" %U`) {
		t.Fatalf("entry %q", got)
	}
}

func TestUpdateRestartsWorkerService(t *testing.T) {
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0", schema: 4}), Options{Mode: ModeHeadless}))
	l := e.layout(ModeHeadless)
	e.wsvc.state, e.wsvc.exe, e.wsvc.version = "running", l.CLI, "1.0.0"
	r := e.install("1.1.0", makeRelease(t, rel{identity: "stage", version: "1.1.0", schema: 4}), Options{})
	mustOK(t, r)
	if r.WorkerService != "restarted" || r.Service != "not-installed" || e.wsvc.version != "1.1.0" || !slices.Equal(e.wsvc.restarts, []string{"1.1.0"}) || len(e.svc.restarts) != 0 {
		t.Fatalf("worker %q server %q version %s restarts %v / %v", r.WorkerService, r.Service, e.wsvc.version, e.wsvc.restarts, e.svc.restarts)
	}
}

func TestUpdateLeavesStoppedOrForeignWorkerService(t *testing.T) {
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{Mode: ModeHeadless}))
	e.wsvc.state, e.wsvc.exe, e.wsvc.version = "running", "/usr/bin/stage", "0.9.0"
	r := e.install("1.1.0", makeRelease(t, rel{identity: "stage", version: "1.1.0"}), Options{})
	mustOK(t, r)
	if r.WorkerService != "foreign" || len(e.wsvc.restarts) != 0 {
		t.Fatalf("foreign worker service: %q %v", r.WorkerService, e.wsvc.restarts)
	}
	e.wsvc.state, e.wsvc.exe = "stopped", e.layout(ModeHeadless).CLI
	r = e.install("1.2.0", makeRelease(t, rel{identity: "stage", version: "1.2.0"}), Options{})
	mustOK(t, r)
	if r.WorkerService != "stopped" || len(e.wsvc.restarts) != 0 {
		t.Fatalf("stopped worker service: %q %v", r.WorkerService, e.wsvc.restarts)
	}
}

func TestFailedWorkerRestartRollsBack(t *testing.T) {
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0", schema: 4}), Options{Mode: ModeHeadless}))
	l := e.layout(ModeHeadless)
	e.wsvc.state, e.wsvc.exe, e.wsvc.version = "running", l.CLI, "1.0.0"
	e.wsvc.failVersion = "1.1.0"
	// A worker has no database: a schema change does not block the rollback.
	r := e.install("1.1.0", makeRelease(t, rel{identity: "stage", version: "1.1.0", schema: 5}), Options{})
	if r.Code != CodeRolledBack || r.WorkerService != "rolled-back" || !strings.Contains(r.Fix, "worker service logs") {
		t.Fatalf("result %+v", r)
	}
	if cliVersion(t, l.CLI) != "1.0.0" || e.record().Version != "1.0.0" || e.wsvc.version != "1.0.0" || e.wsvc.state != "running" {
		t.Fatalf("not rolled back: app %s record %s worker %s %s", cliVersion(t, l.CLI), e.record().Version, e.wsvc.version, e.wsvc.state)
	}
	if !slices.Equal(e.wsvc.restarts, []string{"1.1.0", "1.0.0"}) {
		t.Fatalf("restarts %v", e.wsvc.restarts)
	}
}

func TestUninstallRemovesWorkerService(t *testing.T) {
	e := newEnv(t, "stage")
	mustOK(t, e.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{Mode: ModeHeadless}))
	e.wsvc.state, e.wsvc.exe = "running", e.layout(ModeHeadless).CLI
	d := e.d
	d.Version = "1.0.0"
	r := Uninstall(context.Background(), d)
	mustOK(t, r)
	if !e.wsvc.uninstalled || r.WorkerService != "removed" || r.Service != "not-installed" {
		t.Fatalf("worker service %q uninstalled %t", r.WorkerService, e.wsvc.uninstalled)
	}
}
