package install

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/service"
)

// identityPaths is every per-user path and name one identity owns on goos:
// install layout, service, socket, data, config, logs, desktop data
// (notification preferences live in it), bundle id and URL scheme.
func identityPaths(t *testing.T, goos, identity string) map[string]string {
	t.Helper()
	defer func(id, label string) { buildinfo.Identity, buildinfo.ServiceLabel = id, label }(buildinfo.Identity, buildinfo.ServiceLabel)
	buildinfo.Identity, buildinfo.ServiceLabel = identity, ""
	home := "/home/u"
	if goos == "darwin" {
		home = "/Users/u"
	}
	env := func(k string) string {
		switch k {
		case "HOME":
			return home
		case "XDG_RUNTIME_DIR":
			if goos == "linux" {
				return "/run/user/1000"
			}
		}
		return ""
	}
	p := Platform{GOOS: goos, GOARCH: "arm64", Home: home, Getenv: env}
	l, err := LayoutFor(p, identity, ModeDesktop, "")
	if err != nil {
		t.Fatal(err)
	}
	sp, err := service.ResolvePaths(goos, env, nil, filepath.Join(home, ".config", "systemd", "user"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{
		"root": l.Root, "app": l.App, "prev": l.Prev, "cli": l.CLI, "desktop": l.Desktop, "link": l.Link,
		"entry": l.Entry, "record": l.Record, "lock": l.Lock, "staging": l.Staging, "handler": l.Handler, "autostart": l.Autostart,
		"data": sp.DataDir, "socket": sp.Socket, "config": sp.ConfigDir, "service.env": sp.ServiceEnv,
		"service.lock": sp.ServiceLock, "definition": sp.Definition, "login copy": sp.LoginCopy, "service logs": sp.LogDir,
		"unit": "unit:" + sp.Unit, "label": "label:" + sp.Label, "bundle id": "id:" + l.Identifier, "scheme": "scheme:" + identity,
	}
	d := Deps{Platform: p, Identity: identity}
	for i, r := range retained(d, l, "", "") {
		out["desktop data "+string(rune('a'+i))] = r
	}
	for k, v := range out {
		if v == "" {
			delete(out, k)
		}
	}
	return out
}

// overlaps reports whether a is b or lies under it (or the other way round).
func overlaps(a, b string) bool {
	if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return a == b
	}
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		if r, err := filepath.Rel(pair[1], pair[0]); err == nil && r != ".." && !strings.HasPrefix(r, "../") {
			return true
		}
	}
	return false
}

// Stage and Stage Dev share no app, CLI, service, socket, data, config,
// log, desktop data (notification preferences), bundle id or scheme.
func TestStableAndDevPathsAreDisjoint(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		stable, dev := identityPaths(t, goos, "stage"), identityPaths(t, goos, "stage-dev")
		if len(stable) < 20 || len(dev) != len(stable) {
			t.Fatalf("%s: %d stable and %d dev paths", goos, len(stable), len(dev))
		}
		for sk, sv := range stable {
			for dk, dv := range dev {
				// The install root holds the headless app, lock and staging
				// of the same identity only; across identities nothing nests.
				if overlaps(sv, dv) {
					t.Errorf("%s: stable %s %s collides with dev %s %s", goos, sk, sv, dk, dv)
				}
			}
		}
		// `mise run desktop:dev` (mygo dev) appends .dev to the bundle id and
		// desktop data, so it never shares the installed Stage Dev's either.
		if !strings.Contains(dev["bundle id"], "stage-dev.desktop") {
			t.Fatalf("%s: dev bundle id %s", goos, dev["bundle id"])
		}
	}
}

// snapshot reads every file under the stable identity's install paths.
func snapshot(t *testing.T, l Layout) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range []string{l.Root, l.Entry} {
		_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				t.Fatal(rerr)
			}
			out[p] = string(b)
			return nil
		})
	}
	target, err := os.Readlink(l.Link)
	if err != nil {
		t.Fatal(err)
	}
	out[l.Link] = "-> " + target
	return out
}

// Installing, updating and uninstalling Stage Dev next to Stage leaves
// every stable file byte for byte as it was.
func TestDevInstallLeavesStableUntouched(t *testing.T) {
	linuxOnly(t)
	st := newEnv(t, "stage")
	mustOK(t, st.install("1.0.0", makeRelease(t, rel{identity: "stage", version: "1.0.0"}), Options{}))
	sl := st.layout(ModeDesktop)
	before := snapshot(t, sl)

	dv := newEnv(t, "stage-dev")
	dv.d.Platform = st.d.Platform
	check := func(step string) {
		t.Helper()
		after := snapshot(t, sl)
		if len(after) != len(before) {
			t.Fatalf("%s: stable files %d, were %d", step, len(after), len(before))
		}
		for p, b := range before {
			if !bytes.Equal([]byte(after[p]), []byte(b)) {
				t.Fatalf("%s: %s changed", step, p)
			}
		}
	}
	mustOK(t, dv.install("0.0.0-dev.g0123456789ab", makeRelease(t, rel{identity: "stage-dev", version: "0.0.0-dev.g0123456789ab", channel: "dev"}), Options{}))
	check("dev install")
	dl := dv.layout(ModeDesktop)
	if dl.Root == sl.Root || dl.Link == sl.Link || dl.Entry == sl.Entry {
		t.Fatalf("dev layout %+v overlaps stable %+v", dl, sl)
	}
	mustOK(t, dv.install("0.0.0-dev.gfedcba987654", makeRelease(t, rel{identity: "stage-dev", version: "0.0.0-dev.gfedcba987654", channel: "dev"}), Options{}))
	check("dev update")
	d := dv.d
	d.Version = "0.0.0-dev.gfedcba987654"
	mustOK(t, Uninstall(context.Background(), d))
	check("dev uninstall")
	if r := st.record(); r == nil || r.Identity != "stage" || r.Version != "1.0.0" {
		t.Fatalf("stable record %+v", r)
	}
}
