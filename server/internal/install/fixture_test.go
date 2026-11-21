package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/service"
	"github.com/flexdinesh/stage/server/internal/worker"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

// rel describes a fixture release.
type rel struct {
	identity, version, channel string
	schema                     int64
	cliBroken                  bool // the CLI exits 1 on version
	desktopBroken              bool // the desktop selfcheck fails
	omitArch                   bool // no artifacts for this machine
	extraMember                *tar.Header
}

type member struct {
	name string
	body []byte
	mode int64
	typ  byte
	link string
}

func cliScript(r rel) []byte {
	if r.cliBroken {
		return []byte("#!/bin/sh\necho broken >&2\nexit 1\n")
	}
	return []byte(fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = version ]; then\n  printf '%%s\\n' '{\"identity\":\"%s\",\"version\":\"%s\",\"commit\":\"%s\",\"schema\":%d}'\n  exit 0\nfi\nexit 0\n",
		r.identity, r.version, testCommit, r.schema))
}

func desktopScript(r rel) []byte {
	if r.desktopBroken {
		return []byte("#!/bin/sh\necho 'error while loading shared libraries: libwebkit2gtk-4.1.so.0' >&2\nexit 127\n")
	}
	return []byte(fmt.Sprintf("#!/bin/sh\nif [ \"$STAGE_DESKTOP_SELFCHECK\" = 1 ]; then\n  printf '%%s\\n' '{\"identity\":\"%s\",\"version\":\"%s\"}'\n  exit 0\nfi\nsleep 60\n", r.identity, r.version))
}

func writeTarGz(t *testing.T, path string, ms []member) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, m := range ms {
		h := &tar.Header{Name: m.name, Mode: m.mode, Typeflag: m.typ, Linkname: m.link, ModTime: time.Unix(0, 0)}
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(m.body))
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write(m.body); err != nil {
				t.Fatal(err)
			}
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

// makeRelease writes a fixture asset directory and returns it.
func makeRelease(t *testing.T, r rel) string {
	t.Helper()
	if r.channel == "" {
		r.channel = "stable"
	}
	if r.schema == 0 {
		r.schema = 4
	}
	dir := t.TempDir()
	goos, arch := runtime.GOOS, runtime.GOARCH
	if r.omitArch {
		arch = "mips"
	}
	cli := cliScript(r)
	slug := r.identity + "-desktop"
	m := Manifest{Schema: 1, Name: "stage", Version: r.version, Commit: testCommit, Identity: r.identity, Channel: r.channel}
	add := func(name, kind, a, cliPath string) {
		sum, size, err := fileSHA256(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		m.Artifacts = append(m.Artifacts, Artifact{Name: name, Kind: kind, Format: "tar.gz", OS: goos, Arch: a, Size: size, SHA256: sum,
			CLI: &Binary{Path: cliPath, SHA256: bytesSHA256(cli)}})
	}
	base := fmt.Sprintf("%s-%s-%s-%s", r.identity, r.version, goos, arch)
	hm := []member{{name: base + "/", typ: tar.TypeDir, mode: 0o755}, {name: base + "/stage", body: cli, mode: 0o755},
		{name: base + "/LICENSE", body: []byte("MIT")}, {name: base + "/README.md", body: []byte("readme")}}
	if r.extraMember != nil {
		hm = append(hm, member{name: r.extraMember.Name, typ: r.extraMember.Typeflag, link: r.extraMember.Linkname, body: []byte("x")})
	}
	writeTarGz(t, filepath.Join(dir, base+".tar.gz"), hm)
	add(base+".tar.gz", "cli", arch, base+"/stage")
	dname := fmt.Sprintf("%s-%s-%s-%s.tar.gz", slug, r.version, goos, arch)
	entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Stage\nExec=%s %%U\nIcon=%s\nTerminal=false\nMimeType=x-scheme-handler/%s;\n", slug, slug, r.identity)
	dm := []member{{name: slug, body: desktopScript(r), mode: 0o755}, {name: "bin/", typ: tar.TypeDir, mode: 0o755},
		{name: "bin/stage", body: cli, mode: 0o755}, {name: slug + ".desktop", body: []byte(entry)},
		{name: slug + ".png", body: []byte("png")}, {name: "LICENSE", body: []byte("MIT")}}
	writeTarGz(t, filepath.Join(dir, dname), dm)
	add(dname, "desktop", arch, "bin/stage")
	// System packages name their installed CLI path (absolute), as real
	// releases do; they are listed but not downloaded.
	m.Artifacts = append(m.Artifacts, Artifact{Name: fmt.Sprintf("%s_%s_%s.deb", slug, r.version, arch), Kind: "desktop", Format: "deb", OS: "linux", Arch: arch,
		Size: 1, SHA256: strings.Repeat("ab", 32), CLI: &Binary{Path: "/opt/" + slug + "/bin/stage", SHA256: bytesSHA256(cli)}})
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	writeSums(t, dir, m)
	return dir
}

func writeSums(t *testing.T, dir string, m Manifest) {
	t.Helper()
	var b strings.Builder
	for _, a := range m.Artifacts {
		fmt.Fprintf(&b, "%s  %s\n", a.SHA256, a.Name)
	}
	if err := os.WriteFile(filepath.Join(dir, ChecksumsName), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeService is a per-user service that runs whatever exe it was
// restarted onto (by asking it for its version).
type fakeService struct {
	mu          sync.Mutex
	state       string
	exe         string
	version     string
	schema      int64
	dataDir     string
	failVersion string // Restart onto this version fails
	restarts    []string
	uninstalled bool
	// worker: this is the worker service (status carries Worker, not Stage).
	worker bool
}

func (f *fakeService) status() service.Status {
	st := service.Status{State: f.state, DataDir: f.dataDir}
	if f.state != "not-installed" {
		st.Definition = service.DefinitionStatus{Present: true, Exe: f.exe}
	}
	if f.state == "running" && f.worker {
		st.Worker = &service.WorkerStatus{Joined: true, Holder: &worker.Holder{PID: 1, Version: f.version, Managed: true}, OwnedByService: true}
	} else if f.state == "running" {
		st.Stage = service.StageStatus{Reachable: true, Version: f.version, URL: "unix:///fake.sock"}
	}
	return st
}

func (f *fakeService) Status(context.Context) service.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status()
}

func (f *fakeService) Restart(ctx context.Context, exe string) service.Report {
	f.mu.Lock()
	defer f.mu.Unlock()
	out, err := exec.CommandContext(ctx, exe, "version", "--json").Output()
	var pi probeInfo
	if err == nil {
		err = json.Unmarshal(out, &pi)
	}
	f.restarts = append(f.restarts, pi.Version)
	f.exe = exe
	if err != nil || pi.Version == f.failVersion {
		f.state = "failed"
		st := f.status()
		return service.Report{Code: 1, Message: "the server exited", LogTail: []string{"boom"}, Status: &st}
	}
	f.state, f.version = "running", pi.Version
	if pi.Schema > f.schema {
		f.schema = pi.Schema
	}
	st := f.status()
	return service.Report{Code: 0, OK: true, Status: &st}
}

func (f *fakeService) Uninstall(context.Context) service.Report {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uninstalled, f.state = true, "not-installed"
	return service.Report{Code: 0, OK: true}
}

func (f *fakeService) ServerSchema(_ context.Context, st service.Status) (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.schema, st.Stage.Reachable && f.schema > 0
}

type fakeDesktop struct {
	running  []int
	quit     [][]int
	launched int
	quitErr  error
}

func (f *fakeDesktop) Running(Layout) []int { return f.running }
func (f *fakeDesktop) Quit(_ context.Context, pids []int) error {
	if f.quitErr != nil {
		return f.quitErr
	}
	f.quit = append(f.quit, pids)
	f.running = nil
	return nil
}
func (f *fakeDesktop) Launch(Layout) error { f.launched++; return nil }

type env struct {
	t    *testing.T
	home string
	d    Deps
	svc  *fakeService
	wsvc *fakeService // the worker service
	desk *fakeDesktop
}

func newEnv(t *testing.T, identity string) *env {
	t.Helper()
	home := t.TempDir()
	vars := map[string]string{"PATH": filepath.Join(home, ".local", "bin"), "SHELL": "/bin/bash"}
	svc := &fakeService{state: "not-installed", dataDir: filepath.Join(home, "data")}
	desk := &fakeDesktop{}
	wsvc := &fakeService{state: "not-installed", dataDir: filepath.Join(home, "data"), worker: true}
	d := Deps{
		Platform: Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Home: home, Getenv: func(k string) string { return vars[k] }},
		Identity: identity, Service: svc, WorkerService: wsvc, Desktop: desk, Run: runCmd,
		// No host helpers (xdg-mime, update-desktop-database, gh, ldconfig).
		LookPath:  func(string) (string, error) { return "", exec.ErrNotFound },
		FreeSpace: func(string) (uint64, error) { return 1 << 40, nil },
		Now:       func() time.Time { return time.Unix(1700000000, 0) },
	}
	return &env{t: t, home: home, d: d, svc: svc, wsvc: wsvc, desk: desk}
}

// install runs Install as the CLI of version.
func (e *env) install(version, from string, o Options) *Result {
	e.t.Helper()
	d := e.d
	d.Version = version
	o.From = from
	return Install(context.Background(), d, o)
}

func (e *env) layout(mode string) Layout {
	e.t.Helper()
	l, err := LayoutFor(e.d.Platform, e.d.Identity, mode, "")
	if err != nil {
		e.t.Fatal(err)
	}
	return l
}

func (e *env) record() *Record {
	e.t.Helper()
	r, err := readRecord(filepath.Join(RootFor(e.d.Platform, e.d.Identity), "install.json"))
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

// tree lists every path under home (dirs with a trailing slash).
func tree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			rel += "/"
		}
		out = append(out, rel)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func mustOK(t *testing.T, r *Result) {
	t.Helper()
	if !r.OK {
		t.Fatalf("install failed: code %d: %s (fix: %s)", r.Code, r.Message, r.Fix)
	}
}

func cliVersion(t *testing.T, cli string) string {
	t.Helper()
	out, err := exec.Command(cli, "version", "--json").Output()
	if err != nil {
		t.Fatalf("%s: %v", cli, err)
	}
	var pi probeInfo
	if err := json.Unmarshal(out, &pi); err != nil {
		t.Fatal(err)
	}
	return pi.Version
}
