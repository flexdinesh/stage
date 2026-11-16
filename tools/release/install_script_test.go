package main

// Tests of scripts/install.sh (docs/install.md): it runs under sh with
// stub uname/id/gh on PATH against a fixture release served over HTTP (or
// through the gh stub). The release's "stage" is a stub that records its
// arguments, so these tests cover only the bootstrap: platform and asset
// selection, gh vs anonymous download, checksum and archive checks, and
// the hand-off to `stage install --from`.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type scriptRelease struct {
	version string
	dir     string
}

func tgz(t *testing.T, path string, files map[string]string, extra ...string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := append([]string{}, extra...)
	for n := range files {
		names = append(names, n)
	}
	for _, n := range names {
		body := files[n]
		mode := int64(0o644)
		if strings.HasSuffix(n, "stage") {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixtureRelease writes headless and desktop archives for linux and
// darwin whose stage records "$@" and the asset dir listing to $STUB_LOG.
func fixtureRelease(t *testing.T, version string, unsafe bool) scriptRelease {
	t.Helper()
	dir := t.TempDir()
	stub := "#!/bin/sh\nprintf '%s\\n' \"$*\" >\"$STUB_LOG\"\nls \"$3\" >>\"$STUB_LOG\"\n"
	var sums strings.Builder
	add := func(name string, files map[string]string) {
		var extra []string
		if unsafe {
			extra = []string{"../evil"}
		}
		tgz(t, filepath.Join(dir, name), files, extra...)
		sum, _, err := fileSHA256(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sums, "%s  %s\n", sum, name)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, os := range []string{"linux", "darwin"} {
			base := fmt.Sprintf("stage-%s-%s-%s", version, os, arch)
			add(base+".tar.gz", map[string]string{base + "/stage": stub, base + "/LICENSE": "MIT"})
		}
		add(fmt.Sprintf("stage-desktop-%s-linux-%s.tar.gz", version, arch), map[string]string{"bin/stage": stub, "stage-desktop": "app"})
	}
	if err := os.WriteFile(filepath.Join(dir, "stage-release.json"), []byte(`{"schema":1,"version":"`+version+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return scriptRelease{version: version, dir: dir}
}

func serveReleases(t *testing.T, latest string, rels ...scriptRelease) *httptest.Server {
	t.Helper()
	byVer := map[string]string{}
	for _, r := range rels {
		byVer[r.version] = r.dir
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/o/r/releases/tag/v"+latest, http.StatusFound)
	})
	mux.HandleFunc("GET /o/r/releases/download/{tag}/{name}", func(w http.ResponseWriter, r *http.Request) {
		dir, ok := byVer[strings.TrimPrefix(r.PathValue("tag"), "v")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, r.PathValue("name")))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type scriptRun struct {
	code   int
	out    string
	called string // the stub CLI's log ("" when it never ran)
}

func writeStub(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func runInstallScript(t *testing.T, env map[string]string, stubs map[string]string, args ...string) scriptRun {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for name, body := range stubs {
		writeStub(t, bin, name, body)
	}
	log := filepath.Join(t.TempDir(), "stub.log")
	cmd := exec.Command("sh", append([]string{filepath.Join(root, "scripts", "install.sh")}, args...)...)
	cmd.Env = []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "TMPDIR=" + t.TempDir(), "STUB_LOG=" + log,
		"STAGE_INSTALL_REPO=o/r"}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if xe, ok := err.(*exec.ExitError); ok {
		code = xe.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	called, _ := os.ReadFile(log)
	return scriptRun{code: code, out: string(out), called: string(called)}
}

func uname(s, m string) string {
	return fmt.Sprintf("case \"$1\" in -s) echo %s ;; -m) echo %s ;; *) echo %s ;; esac\n", s, m, s)
}

func TestInstallScriptHeadlessLatest(t *testing.T) {
	srv := serveReleases(t, "1.2.0", fixtureRelease(t, "1.2.0", false))
	r := runInstallScript(t, map[string]string{"STAGE_INSTALL_WEB": srv.URL, "STAGE_INSTALL_NO_GH": "1"},
		map[string]string{"uname": uname("Linux", "x86_64")}, "--headless")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.out)
	}
	lines := strings.Split(strings.TrimSpace(r.called), "\n")
	if !strings.HasPrefix(lines[0], "install --from ") || !strings.HasSuffix(lines[0], " --headless") {
		t.Fatalf("called %q", lines[0])
	}
	got := strings.Join(lines[1:], ",")
	if got != "checksums.txt,stage-1.2.0-linux-amd64.tar.gz,stage-release.json" {
		t.Fatalf("asset dir %s", got)
	}
}

func TestInstallScriptDesktopArm64AndCLIName(t *testing.T) {
	srv := serveReleases(t, "1.2.0", fixtureRelease(t, "1.2.0", false))
	r := runInstallScript(t, map[string]string{"STAGE_INSTALL_WEB": srv.URL, "STAGE_INSTALL_NO_GH": "1"},
		map[string]string{"uname": uname("Linux", "aarch64")}, "--cli-name", "stage2")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.out)
	}
	if !strings.Contains(r.called, "--desktop --cli-name stage2") || !strings.Contains(r.called, "stage-desktop-1.2.0-linux-arm64.tar.gz") {
		t.Fatalf("called %q", r.called)
	}
}

func TestInstallScriptMacDesktopMissing(t *testing.T) {
	srv := serveReleases(t, "1.2.0", fixtureRelease(t, "1.2.0", false))
	r := runInstallScript(t, map[string]string{"STAGE_INSTALL_WEB": srv.URL, "STAGE_INSTALL_NO_GH": "1"},
		map[string]string{"uname": uname("Darwin", "arm64")})
	if r.code != 7 || !strings.Contains(r.out, "--headless") || r.called != "" {
		t.Fatalf("exit %d: %s", r.code, r.out)
	}
	r = runInstallScript(t, map[string]string{"STAGE_INSTALL_WEB": srv.URL, "STAGE_INSTALL_NO_GH": "1"},
		map[string]string{"uname": uname("Darwin", "arm64")}, "--headless")
	if r.code != 0 || !strings.Contains(r.called, "stage-1.2.0-darwin-arm64.tar.gz") {
		t.Fatalf("exit %d: %s %s", r.code, r.out, r.called)
	}
}

func TestInstallScriptRejectsTamperAndUnsafe(t *testing.T) {
	bad := fixtureRelease(t, "1.2.0", false)
	p := filepath.Join(bad.dir, "stage-1.2.0-linux-amd64.tar.gz")
	b, _ := os.ReadFile(p)
	b[len(b)-3] ^= 0xff
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	srv := serveReleases(t, "1.2.0", bad)
	env := map[string]string{"STAGE_INSTALL_WEB": srv.URL, "STAGE_INSTALL_NO_GH": "1"}
	stubs := map[string]string{"uname": uname("Linux", "x86_64")}
	if r := runInstallScript(t, env, stubs, "--headless"); r.code != 11 || r.called != "" || !strings.Contains(r.out, "failed verification") {
		t.Fatalf("tampered: exit %d: %s", r.code, r.out)
	}
	srv = serveReleases(t, "1.2.0", fixtureRelease(t, "1.2.0", true))
	env["STAGE_INSTALL_WEB"] = srv.URL
	if r := runInstallScript(t, env, stubs, "--headless"); r.code != 11 || r.called != "" || !strings.Contains(r.out, "unsafe") {
		t.Fatalf("unsafe: exit %d: %s", r.code, r.out)
	}
}

func TestInstallScriptErrors(t *testing.T) {
	srv := serveReleases(t, "2.0.0-rc.1", fixtureRelease(t, "2.0.0-rc.1", false))
	env := map[string]string{"STAGE_INSTALL_WEB": srv.URL, "STAGE_INSTALL_NO_GH": "1"}
	stubs := map[string]string{"uname": uname("Linux", "x86_64")}
	if r := runInstallScript(t, env, stubs); r.code != 10 || !strings.Contains(r.out, "pre-release") {
		t.Fatalf("pre-release latest: exit %d: %s", r.code, r.out)
	}
	// A pin may name a pre-release.
	if r := runInstallScript(t, env, stubs, "--version", "v2.0.0-rc.1", "--headless"); r.code != 0 {
		t.Fatalf("pinned: exit %d: %s", r.code, r.out)
	}
	if r := runInstallScript(t, env, stubs, "--version", "9.9.9"); r.code != 10 || !strings.Contains(r.out, "gh auth login") {
		t.Fatalf("missing version: exit %d: %s", r.code, r.out)
	}
	stubs["id"] = "echo 0\n"
	if r := runInstallScript(t, env, stubs); r.code != 7 || !strings.Contains(r.out, "root") {
		t.Fatalf("root: exit %d: %s", r.code, r.out)
	}
	delete(stubs, "id")
	stubs["uname"] = uname("Linux", "riscv64")
	if r := runInstallScript(t, env, stubs); r.code != 7 || !strings.Contains(r.out, "unsupported CPU") {
		t.Fatalf("cpu: exit %d: %s", r.code, r.out)
	}
	if r := runInstallScript(t, env, stubs, "--bogus"); r.code != 2 {
		t.Fatalf("usage: exit %d: %s", r.code, r.out)
	}
}

func TestInstallScriptUsesGh(t *testing.T) {
	rel := fixtureRelease(t, "1.3.0", false)
	gh := fmt.Sprintf(`case "$1 $2" in
"auth status") exit 0 ;;
"release view") echo v1.3.0 ;;
"release download")
  while [ $# -gt 0 ]; do
    case "$1" in --dir) d=$2; shift ;; --pattern) cp %q/"$2" "$d"/ || exit 1; shift ;; esac
    shift
  done ;;
*) exit 1 ;;
esac
`, rel.dir)
	r := runInstallScript(t, map[string]string{"STAGE_INSTALL_WEB": "http://127.0.0.1:9"},
		map[string]string{"uname": uname("Linux", "x86_64"), "gh": gh}, "--headless")
	if r.code != 0 || !strings.Contains(r.called, "stage-1.3.0-linux-amd64.tar.gz") {
		t.Fatalf("exit %d: %s %s", r.code, r.out, r.called)
	}
}
