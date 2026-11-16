package install

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UpdateOptions are `stage update` flags.
type UpdateOptions struct {
	Check   bool
	Version string // pin (may be a pre-release or older)
	From    string // local asset directory instead of GitHub
}

// Check is `stage update --check`.
type Check struct {
	Installed       string `json:"installed"`
	Latest          string `json:"latest"`
	Server          string `json:"server,omitempty"`
	Mode            string `json:"mode"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

// Prepared is a verified release ready for its own CLI to install:
// run `CLI install --from Dir`, then Cleanup.
type Prepared struct {
	CLI, Dir string
	Mode     string
	Cleanup  func()
}

// Lookup is the installation whose CLI is exe, or nil.
func Lookup(d Deps, exe string) *Record {
	rec, err := readRecord(filepath.Join(RootFor(d.Platform, d.Identity), "install.json"))
	if err != nil || rec == nil || !sameFile(exe, rec.CLI) {
		return nil
	}
	return rec
}

// Installed finds this binary's installation for `stage update`.
func Installed(d Deps, exe string) (*Record, error) {
	if d.Identity == "stage-dev" {
		return nil, newErr(CodeUnsupported, "update Stage Dev with scripts/install-dev.sh from a checkout (docs/development.md)",
			"Stage Dev does not update from releases")
	}
	rec, err := readRecord(filepath.Join(RootFor(d.Platform, d.Identity), "install.json"))
	if err != nil {
		return nil, wrapErr(CodeFailed, err, "reinstall with scripts/install.sh (docs/install.md)", "unreadable install record")
	}
	if rec == nil || !sameFile(exe, rec.CLI) {
		return nil, newErr(CodeUnsupported, notInstalledFix(exe), "this stage (%s) was not installed by the Stage installer", exe)
	}
	return rec, nil
}

func notInstalledFix(exe string) string {
	switch {
	case strings.HasPrefix(exe, "/opt/") || strings.HasPrefix(exe, "/usr/"):
		return "it belongs to a system package: update it with your package manager"
	case strings.Contains(exe, "/go-build") || strings.Contains(exe, "/.scratch/"):
		return "it is a development build: rebuild it, or install Stage with scripts/install.sh (docs/install.md)"
	}
	return "install Stage with scripts/install.sh (docs/install.md); it then updates with `stage update`"
}

func sameFile(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return false
	}
	sa, err1 := os.Stat(ra)
	sb, err2 := os.Stat(rb)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

// Target is the version to update to and whether it differs from rec.
func Target(ctx context.Context, src Source, rec *Record, o UpdateOptions) (string, bool, error) {
	switch {
	case o.From != "":
		m, err := readManifest(o.From)
		if err != nil {
			return "", false, wrapErr(CodeVerify, err, verifyFix, "invalid release in %s", o.From)
		}
		if o.Version != "" && m.Version != o.Version {
			return "", false, newErr(CodeVerify, "", "%s holds %s, not %s", o.From, m.Version, o.Version)
		}
		return m.Version, m.Version != rec.Version, nil
	case o.Version != "":
		v := strings.TrimPrefix(o.Version, "v")
		if !ValidVersion(v) {
			return "", false, newErr(CodeUsage, "", "invalid version %q (want X.Y.Z or X.Y.Z-pre)", o.Version)
		}
		return v, v != rec.Version, nil
	}
	latest, err := src.Latest(ctx)
	if err != nil {
		return "", false, err
	}
	return latest, CompareVersions(latest, rec.Version) > 0, nil
}

// Prepare downloads (unless o.From) and verifies version for rec's mode,
// and unpacks the release's CLI next to it.
func Prepare(ctx context.Context, d Deps, src Source, rec *Record, version string, o UpdateOptions) (*Prepared, error) {
	l, err := LayoutFor(d.Platform, d.Identity, rec.Mode, rec.CLIName)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(l.Staging, 0o700); err != nil {
		return nil, permErr(err, l.Staging)
	}
	work, err := os.MkdirTemp(l.Staging, "update-")
	if err != nil {
		return nil, permErr(err, l.Staging)
	}
	cleanup := func() { _ = os.RemoveAll(work) }
	fail := func(err error) (*Prepared, error) { cleanup(); return nil, err }
	dir := o.From
	if dir == "" {
		dir = filepath.Join(work, "assets")
		if err := os.Mkdir(dir, 0o700); err != nil {
			return fail(permErr(err, dir))
		}
		d.logf("downloading %s %s", l.AppName, version)
		for _, name := range []string{ManifestName, ChecksumsName} {
			if err := src.Fetch(ctx, version, name, filepath.Join(dir, name), metaMax); err != nil {
				return fail(err)
			}
		}
		m, err := readManifest(dir)
		if err != nil {
			return fail(wrapErr(CodeVerify, err, verifyFix, "invalid release v%s", version))
		}
		a, err := pick(m, d.Platform, rec.Mode)
		if err != nil {
			return fail(err)
		}
		if err := src.Fetch(ctx, version, a.Name, filepath.Join(dir, a.Name), a.Size); err != nil {
			return fail(err)
		}
	}
	v, err := Verify(dir, d.Platform, Expect{Identity: d.Identity, Version: version, Mode: rec.Mode})
	if err != nil {
		return fail(err)
	}
	cli := filepath.Join(work, "stage")
	if err := extractFile(filepath.Join(dir, v.Artifact.Name), v.Artifact.CLI.Path, cli); err != nil {
		return fail(wrapErr(CodeVerify, err, verifyFix, "unsafe or corrupt %s", v.Artifact.Name))
	}
	if sum, _, err := fileSHA256(cli); err != nil || sum != v.Artifact.CLI.SHA256 {
		return fail(newErr(CodeVerify, verifyFix, "the CLI in %s does not match the manifest", v.Artifact.Name))
	}
	if err := os.Chmod(cli, 0o755); err != nil {
		return fail(permErr(err, cli))
	}
	return &Prepared{CLI: cli, Dir: dir, Mode: rec.Mode, Cleanup: cleanup}, nil
}

// UpToDate is the result when no update is needed.
func UpToDate(rec *Record, latest string) *Result {
	msg := fmt.Sprintf("Stage %s is up to date (%s mode)", rec.Version, rec.Mode)
	if CompareVersions(rec.Version, latest) > 0 {
		msg = fmt.Sprintf("Stage %s is newer than the latest stable release (%s)", rec.Version, latest)
	}
	return &Result{OK: true, Message: msg, Version: rec.Version, Mode: rec.Mode, Notes: []string{}, Warnings: []string{}}
}
