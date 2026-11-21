package service

import (
	"bytes"
	"encoding/xml"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata golden files")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs (run with -update):\n%s", name, got)
	}
}

var awkward = Spec{
	Identity: "stage-dev", Name: "stage-dev.service",
	Exe: `/opt/My Apps/50% off/$HOME/st"age`,
	Env: []KV{{"XDG_DATA_HOME", "/home/u/my data"}, {"XDG_CONFIG_HOME", "/home/u/100%"}},
}

func TestRenderUnitGolden(t *testing.T) {
	golden(t, "stage.service", RenderUnit(Spec{Identity: "stage", Name: "stage.service", Exe: "/usr/bin/stage"}))
	w := RenderUnit(Spec{Identity: "stage", Name: "stage-worker.service", Exe: "/usr/bin/stage", Role: RoleWorker})
	golden(t, "stage-worker.service", w)
	if m := ParseDefinition("linux", w); !m.Generated || m.Exe != "/usr/bin/stage" {
		t.Fatalf("worker marker %+v", m)
	}
	golden(t, "awkward.service", RenderUnit(awkward))
}

func TestRenderPlistGolden(t *testing.T) {
	s := Spec{Identity: "stage", Name: "io.github.flexdinesh.stage", Exe: "/Applications/Stage.app/Contents/MacOS/stage", StderrPath: "/Users/u/Library/Logs/stage/launchd.stderr.log"}
	golden(t, "stage.plist", RenderPlist(s))
	ws := s
	ws.Name, ws.Role, ws.StderrPath = "io.github.flexdinesh.stage.worker", RoleWorker, "/Users/u/Library/Logs/stage/worker.launchd.stderr.log"
	golden(t, "stage-worker.plist", RenderPlist(ws))
	a := awkward
	a.Name, a.Exe = "io.github.flexdinesh.stage-dev", "/Users/u/A & B <x>/stage"
	b := RenderPlist(a)
	golden(t, "awkward.plist", b)
	// Well-formed XML.
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = true
	for {
		if _, err := d.Token(); err != nil {
			if err.Error() != "EOF" {
				t.Fatalf("plist not well-formed: %v", err)
			}
			break
		}
	}
}

func TestParseDefinitionRoundTrip(t *testing.T) {
	m := ParseDefinition("linux", RenderUnit(awkward))
	if !m.Generated || m.Schema != Schema || m.Exe != awkward.Exe || !samePins(m.Env, awkward.Env) {
		t.Fatalf("unit marker %+v", m)
	}
	a := awkward
	a.Exe = "/Users/u/A & B <x>/stage"
	m = ParseDefinition("darwin", RenderPlist(a))
	if !m.Generated || m.Schema != Schema || m.Exe != a.Exe || !samePins(m.Env, a.Env) {
		t.Fatalf("plist marker %+v", m)
	}
	if m := ParseDefinition("linux", []byte("[Service]\nExecStart=/usr/bin/stage\n")); m.Generated {
		t.Fatal("foreign unit parsed as generated")
	}
	if m := ParseDefinition("darwin", []byte("<plist><dict><key>Label</key><string>x</string></dict></plist>")); m.Generated {
		t.Fatal("foreign plist parsed as generated")
	}
}

func TestQuoteSystemd(t *testing.T) {
	cases := map[string]string{
		"/usr/bin/stage": "/usr/bin/stage",
		"a b":            `"a b"`,
		"50%":            "50%%",
		"$HOME":          "$$HOME",
		`a"b`:            `"a\"b"`,
	}
	for in, want := range cases {
		if got := quoteExecArg(in); got != want {
			t.Fatalf("quote %q = %q, want %q", in, got, want)
		}
		if back := unquoteSystemd(quoteExecArg(in), true); back != in {
			t.Fatalf("round trip %q -> %q", in, back)
		}
	}
}

func TestResolvePaths(t *testing.T) {
	env := map[string]string{"HOME": "/h", "XDG_RUNTIME_DIR": "/run/user/1000"}
	get := func(k string) string { return env[k] }
	p, err := ResolvePaths("linux", get, nil, "/h/.config/systemd/user")
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		GOOS: "linux", Identity: "stage", Label: "io.github.flexdinesh.stage", Unit: "stage.service", Name: "stage.service",
		DataDir: "/h/.local/share/stage", DataLock: "/h/.local/share/stage/stage.lock",
		ConfigDir: "/h/.config/stage", ServiceEnv: "/h/.config/stage/service.env", ServiceLock: "/h/.config/stage/service.lock",
		Socket: "/run/user/1000/stage/stage.sock", Definition: "/h/.config/systemd/user/stage.service",
		OtherDefinition: "/h/.config/systemd/user/stage-worker.service",
	}
	if !equalPaths(p, want) {
		t.Fatalf("linux:\n got %+v\nwant %+v", p, want)
	}
	pw, err := ResolvePathsFor(RoleWorker, "linux", get, nil, "/h/.config/systemd/user")
	if err != nil {
		t.Fatal(err)
	}
	want.Role, want.Unit, want.Name, want.Label = RoleWorker, "stage-worker.service", "stage-worker.service", "io.github.flexdinesh.stage.worker"
	want.ServiceEnv = "/h/.config/stage/worker.env"
	want.Definition, want.OtherDefinition = "/h/.config/systemd/user/stage-worker.service", "/h/.config/systemd/user/stage.service"
	if !equalPaths(pw, want) {
		t.Fatalf("linux worker:\n got %+v\nwant %+v", pw, want)
	}
	pins := []KV{{"XDG_DATA_HOME", "/d"}, {"XDG_CONFIG_HOME", "/c"}}
	p, _ = ResolvePaths("linux", get, pins, "/h/.config/systemd/user")
	if p.DataDir != "/d/stage" || p.ServiceEnv != "/c/stage/service.env" || p.Socket != "/run/user/1000/stage/stage.sock" {
		t.Fatalf("pinned: %+v", p)
	}
	p, _ = ResolvePaths("darwin", get, nil, "")
	lib := "/h/Library/Application Support/stage"
	want = Paths{
		GOOS: "darwin", Identity: "stage", Label: "io.github.flexdinesh.stage", Unit: "stage.service", Name: "io.github.flexdinesh.stage",
		DataDir: lib, DataLock: lib + "/stage.lock", ConfigDir: lib, ServiceEnv: lib + "/service.env", ServiceLock: lib + "/service.lock",
		Socket: lib + "/run/stage.sock", Definition: lib + "/io.github.flexdinesh.stage.plist",
		LoginCopy:       "/h/Library/LaunchAgents/io.github.flexdinesh.stage.plist",
		OtherDefinition: lib + "/io.github.flexdinesh.stage.worker.plist",
		LogDir:          "/h/Library/Logs/stage", LogFile: "/h/Library/Logs/stage/stage.log", StderrFile: "/h/Library/Logs/stage/launchd.stderr.log",
	}
	if !equalPaths(p, want) {
		t.Fatalf("darwin:\n got %+v\nwant %+v", p, want)
	}
	pw, _ = ResolvePathsFor(RoleWorker, "darwin", get, nil, "")
	want.Role, want.Label, want.Name, want.Unit = RoleWorker, "io.github.flexdinesh.stage.worker", "io.github.flexdinesh.stage.worker", "stage-worker.service"
	want.ServiceEnv = lib + "/worker.env"
	want.Definition, want.OtherDefinition = lib+"/io.github.flexdinesh.stage.worker.plist", lib+"/io.github.flexdinesh.stage.plist"
	want.LoginCopy = "/h/Library/LaunchAgents/io.github.flexdinesh.stage.worker.plist"
	want.LogFile, want.StderrFile = "/h/Library/Logs/stage/worker.log", "/h/Library/Logs/stage/worker.launchd.stderr.log"
	if !equalPaths(pw, want) {
		t.Fatalf("darwin worker:\n got %+v\nwant %+v", pw, want)
	}
}

func equalPaths(a, b Paths) bool {
	if !samePins(a.Pinned, b.Pinned) {
		return false
	}
	a.Pinned, b.Pinned = nil, nil
	return reflect.DeepEqual(a, b)
}

func TestPinsFor(t *testing.T) {
	cli := map[string]string{"XDG_DATA_HOME": "/d/", "XDG_CONFIG_HOME": "/same", "HOME": "/h"}
	mgr := map[string]string{"XDG_CONFIG_HOME": "/same"}
	got := PinsFor(func(k string) string { return cli[k] }, func(k string) string { return mgr[k] })
	if !samePins(got, []KV{{"XDG_DATA_HOME", "/d"}}) {
		t.Fatalf("pins %v", got)
	}
	cli["XDG_DATA_HOME"] = "relative"
	if got := PinsFor(func(k string) string { return cli[k] }, func(k string) string { return mgr[k] }); len(got) != 0 {
		t.Fatalf("relative pinned: %v", got)
	}
}

func TestValidateExe(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "stage")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ValidateExe(exe, nil); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if err := ValidateExe(exe, []string{dir}); err == nil || !strings.Contains(err.Error(), "temporary") {
		t.Fatalf("temp dir: %v", err)
	}
	noexec := filepath.Join(dir, "plain")
	_ = os.WriteFile(noexec, nil, 0o644)
	for _, bad := range []string{"stage", noexec, dir, filepath.Join(dir, "missing"), "/x/go-build123/b001/exe/stage", "/private/var/AppTranslocation/X/d/Stage.app/stage", "/Users/u/.Trash/stage"} {
		if err := ValidateExe(bad, nil); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestParsePrint(t *testing.T) {
	out := "gui/501/io.github.flexdinesh.stage = {\n\tactive count = 1\n\tpath = /Users/u/Library/Application Support/stage/io.github.flexdinesh.stage.plist\n\ttype = LaunchAgent\n\tstate = running\n\n\tprogram = /usr/local/bin/stage\n\targuments = {\n\t\tstate = nested\n\t}\n\truns = 3\n\tpid = 4242\n\tlast exit code = 1: Operation not permitted\n}\n"
	rs := parsePrint(out)
	if !rs.Loaded || rs.Phase != PhaseRunning || rs.PID != 4242 || rs.Restarts != 2 || rs.LastExit == nil || *rs.LastExit != 1 || !strings.HasSuffix(rs.LoadedFrom, ".plist") {
		t.Fatalf("running: %+v", rs)
	}
	rs = parsePrint("x = {\n\tstate = not running\n\truns = 1\n\tlast exit code = (never exited)\n}\n")
	if rs.Phase != PhaseStopped || rs.LastExit != nil {
		t.Fatalf("not running: %+v", rs)
	}
	rs = parsePrint("x = {\n\tstate = not running\n\truns = 2\n\tlast exit code = 75\n}\n")
	if rs.Phase != PhaseFailed || *rs.LastExit != 75 {
		t.Fatalf("crashed: %+v", rs)
	}
	rs = parsePrint("x = {\n\tstate = spawn scheduled\n}\n")
	if rs.Phase != PhaseStarting {
		t.Fatalf("spawn: %+v", rs)
	}
}

func TestUnitDir(t *testing.T) {
	up := "/h/.config/systemd/user.control /run/user/1000/systemd/user.control /run/user/1000/systemd/transient /h/.cfg/systemd/user /h/.cfg/systemd/user.attached /etc/systemd/user /run/user/1000/systemd/user /h/.local/share/systemd/user /usr/lib/systemd/user"
	if got := unitDir(up, "/h", "/h/.config", "/run/user/1000"); got != "/h/.cfg/systemd/user" {
		t.Fatalf("from UnitPath: %s", got)
	}
	if got := unitDir("", "/h", "/x", ""); got != "/x/systemd/user" {
		t.Fatalf("fallback: %s", got)
	}
	if got := unitDir("", "/h", "", ""); got != "/h/.config/systemd/user" {
		t.Fatalf("default: %s", got)
	}
}
