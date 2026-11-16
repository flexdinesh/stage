package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

var (
	cliTargets     = []string{"linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64"}
	desktopTargets = []string{"linux-amd64", "linux-arm64", "darwin-universal"}
	rpmArch        = map[string]string{"amd64": "x86_64", "arm64": "aarch64"}
)

// build is what every artifact of one build shares.
type build struct {
	Version, Commit, Identity, Channel string
}

func (b build) slug() string { return b.Identity + "-desktop" }

// appName is the desktop app's name per identity (mygo.config.ts).
func (b build) appName() string {
	if b.Identity == "stage" {
		return "Stage"
	}
	return "Stage Dev"
}

func (b build) cliName(target string) string {
	return b.Identity + "-" + b.Version + "-" + target
}

// debVersion, rpmVersion and archVersion are MyGo's package versions for
// versions validVersion accepts (cmd/mygo deb.go, rpm.go, pkgbuild.go).
func debVersion(v string) string {
	base, pre, ok := strings.Cut(v, "-")
	if !ok {
		return v
	}
	return base + "~" + pre
}

func rpmVersion(v string) string { return debVersion(v) }

func archVersion(v string) string {
	base, pre, _ := strings.Cut(v, "-")
	if pre != "" && pre[0] >= '0' && pre[0] <= '9' {
		pre = "pre" + pre
	}
	return base + pre
}

type omitFlags []Omission

func (o *omitFlags) String() string { return fmt.Sprint(*o) }

// Set parses kind/os[/arch]=reason.
func (o *omitFlags) Set(s string) error {
	key, reason, ok := strings.Cut(s, "=")
	parts := strings.Split(key, "/")
	if !ok || reason == "" || len(parts) < 2 || len(parts) > 3 {
		return fmt.Errorf("-omit %q: want kind/os[/arch]=reason", s)
	}
	om := Omission{Kind: parts[0], OS: parts[1], Reason: reason}
	if len(parts) == 3 {
		om.Arch = parts[2]
	}
	*o = append(*o, om)
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

type assembleOpts struct {
	out, cliDir, desktopDir, license, readme string
	b                                        build
	date                                     string
	mtime                                    time.Time
	cli, desktop                             []string
	omit                                     []Omission
}

func cmdAssemble(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("assemble", flag.ContinueOnError)
	var o assembleOpts
	var omit omitFlags
	fs.StringVar(&o.out, "out", "", "asset directory to create (must not exist or be empty)")
	fs.StringVar(&o.cliDir, "cli", "", "directory with <os>-<arch>/stage")
	fs.StringVar(&o.desktopDir, "desktop", "", "mygo build output (desktop/app/build)")
	fs.StringVar(&o.license, "license", "LICENSE", "license file")
	fs.StringVar(&o.readme, "readme", "README.md", "readme file")
	fs.StringVar(&o.b.Version, "version", "", "version")
	fs.StringVar(&o.b.Commit, "commit", "", "full revision")
	fs.StringVar(&o.b.Identity, "identity", "", "stage or stage-dev")
	fs.StringVar(&o.b.Channel, "channel", "", "stable or dev")
	fs.StringVar(&o.date, "date", "", "commit date, RFC 3339")
	cliList := fs.String("cli-targets", "", "comma-separated <os>-<arch> CLI targets")
	desktopList := fs.String("desktop-targets", "", "comma-separated desktop targets")
	fs.Var(&omit, "omit", "kind/os[/arch]=reason for a target left out (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	o.cli, o.desktop, o.omit = splitList(*cliList), splitList(*desktopList), omit
	t, err := time.Parse(time.RFC3339, o.date)
	if err != nil {
		return fmt.Errorf("-date: %w", err)
	}
	o.mtime = t.UTC()
	m, err := assemble(o)
	if err != nil {
		return err
	}
	for _, a := range m.Artifacts {
		fmt.Fprintf(w, "%s  %s\n", a.SHA256[:12], a.Name)
	}
	fmt.Fprintf(w, "assembled %s %s (%s) in %s\n", m.Name, m.Version, m.Identity, o.out)
	return nil
}

// assemble writes the asset directory; on failure it removes what it
// wrote, so a half-built directory is never mistaken for a build.
func assemble(o assembleOpts) (m *Manifest, err error) {
	defer func() {
		if err != nil && o.out != "" {
			_ = os.RemoveAll(o.out)
		}
	}()
	return assembleInto(o)
}

func assembleInto(o assembleOpts) (*Manifest, error) {
	if err := checkTargets(o.cli, cliTargets, "cli"); err != nil {
		return nil, err
	}
	if err := checkTargets(o.desktop, desktopTargets, "desktop"); err != nil {
		return nil, err
	}
	if err := prepareOut(o.out); err != nil {
		return nil, err
	}
	host := runtime.GOOS + "-" + runtime.GOARCH
	if !slices.Contains(o.cli, host) {
		return nil, fmt.Errorf("the CLI for this machine (%s) is not built; it reports the API version", host)
	}
	m := &Manifest{
		Schema: ManifestSchema, Name: "stage", Version: o.b.Version, Commit: o.b.Commit,
		Date: o.mtime.Format(time.RFC3339), Identity: o.b.Identity, Channel: o.b.Channel,
		Compatibility: Compatibility{
			MacOS: MacOSReq{Minimum: "12.0"},
			Linux: LinuxReq{Libc: "glibc", DesktopDepends: []string{"libgtk-3-0", "libwebkit2gtk-4.1-0"}},
			Note:  "The desktop app and its bundled CLI are one build. Clients accept a server with the same apiVersion; another version shows a skew banner.",
		},
		Omitted: o.omit,
	}
	api, err := hostAPIVersion(filepath.Join(o.cliDir, host, "stage"), o.b)
	if err != nil {
		return nil, err
	}
	m.Compatibility.APIVersion = api

	cliBins := map[string][]byte{}
	for _, t := range o.cli {
		bin := filepath.Join(o.cliDir, t, "stage")
		data, err := os.ReadFile(bin)
		if err != nil {
			return nil, err
		}
		if err := checkCLIStamp(data, o.b); err != nil {
			return nil, fmt.Errorf("%s: %w", bin, err)
		}
		cliBins[t] = data
		name := o.b.cliName(t) + ".tar.gz"
		dir := o.b.cliName(t)
		files := []tarFile{{Name: dir + "/stage", Mode: 0o755, Src: bin}, {Name: dir + "/LICENSE", Mode: 0o644, Src: o.license}, {Name: dir + "/README.md", Mode: 0o644, Src: o.readme}}
		if err := writeTarGz(filepath.Join(o.out, name), []string{dir}, files, o.mtime); err != nil {
			return nil, err
		}
		osName, arch, _ := strings.Cut(t, "-")
		m.Artifacts = append(m.Artifacts, Artifact{Name: name, Kind: KindCLI, Format: FormatTarGz, OS: osName, Arch: arch, CLI: &Binary{Path: dir + "/stage", SHA256: bytesSHA256(data)}})
	}

	var installer []byte
	for _, t := range o.desktop {
		src := filepath.Join(o.desktopDir, t)
		arts, inst, err := assembleDesktop(o, t, src, cliBins)
		if err != nil {
			return nil, fmt.Errorf("desktop %s: %w", t, err)
		}
		m.Artifacts = append(m.Artifacts, arts...)
		if inst != nil {
			if installer != nil && !bytes.Equal(installer, inst) {
				return nil, errors.New("MyGo wrote different install.sh files per Linux target")
			}
			installer = inst
		}
	}
	if installer != nil {
		name := o.b.slug() + "-install.sh"
		if err := os.WriteFile(filepath.Join(o.out, name), installer, 0o755); err != nil {
			return nil, err
		}
		m.Artifacts = append(m.Artifacts, Artifact{Name: name, Kind: KindInstaller, Format: FormatSh, OS: "linux"})
	}

	for i := range m.Artifacts {
		sum, size, err := fileSHA256(filepath.Join(o.out, m.Artifacts[i].Name))
		if err != nil {
			return nil, err
		}
		m.Artifacts[i].SHA256, m.Artifacts[i].Size = sum, size
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(o.out, ManifestName), m); err != nil {
		return nil, err
	}
	return m, writeChecksums(o.out, m.Artifacts)
}

func checkTargets(got, allowed []string, what string) error {
	for _, t := range got {
		if !slices.Contains(allowed, t) {
			return fmt.Errorf("unknown %s target %q (allowed: %s)", what, t, strings.Join(allowed, ", "))
		}
	}
	return nil
}

func prepareOut(dir string) error {
	if dir == "" {
		return errors.New("-out is required")
	}
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty", dir)
	}
	return os.MkdirAll(dir, 0o755)
}

// cliVersion is `stage version --json`.
type cliVersion struct {
	Identity   string `json:"identity"`
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	APIVersion int    `json:"apiVersion"`
	Build      string `json:"build"`
}

func runCLIVersion(bin string) (cliVersion, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version", "--json").Output()
	if err != nil {
		return cliVersion{}, fmt.Errorf("%s version --json: %w", bin, err)
	}
	var v cliVersion
	if err := json.Unmarshal(out, &v); err != nil {
		return cliVersion{}, fmt.Errorf("%s version --json: %w", bin, err)
	}
	return v, nil
}

func (v cliVersion) check(b build) error {
	if v.Identity != b.Identity || v.Version != b.Version || v.Commit != b.Commit {
		return fmt.Errorf("reports %s %s %s, want %s %s %s", v.Identity, v.Version, v.Commit, b.Identity, b.Version, b.Commit)
	}
	if v.APIVersion < 1 {
		return fmt.Errorf("reports apiVersion %d", v.APIVersion)
	}
	if v.Build != stampFor(b) {
		return fmt.Errorf("reports build %q, want %q", v.Build, stampFor(b))
	}
	return nil
}

func hostAPIVersion(bin string, b build) (int, error) {
	v, err := runCLIVersion(bin)
	if err != nil {
		return 0, err
	}
	if err := v.check(b); err != nil {
		return 0, fmt.Errorf("%s: %w", bin, err)
	}
	return v.APIVersion, nil
}

// checkCLIStamp checks the build stamp of a stage binary (every slice of
// a universal one).
func checkCLIStamp(data []byte, b build) error { return checkStamp(data, b) }

// checkAppStamp checks the build stamp of the desktop executable.
func checkAppStamp(data []byte, b build) error { return checkStamp(data, b) }

func copyFile(dst, src string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}

func assembleDesktop(o assembleOpts, target, src string, cliBins map[string][]byte) ([]Artifact, []byte, error) {
	b := o.b
	slug := b.slug()
	osName, arch, _ := strings.Cut(target, "-")
	if osName == "darwin" {
		return assembleMac(o, src, cliBins)
	}
	cli, err := os.ReadFile(filepath.Join(src, "bin", "stage"))
	if err != nil {
		return nil, nil, err
	}
	if want, ok := cliBins[target]; !ok || !bytes.Equal(cli, want) {
		return nil, nil, errors.New("the bundled bin/stage is not this build's CLI for " + target)
	}
	exe, err := os.ReadFile(filepath.Join(src, slug))
	if err != nil {
		return nil, nil, err
	}
	if err := checkAppStamp(exe, b); err != nil {
		return nil, nil, err
	}
	cliSum := bytesSHA256(cli)
	tarName := slug + "-" + b.Version + "-" + target + ".tar.gz"
	debName := slug + "_" + debVersion(b.Version) + "_" + arch + ".deb"
	rpmName := slug + "-" + rpmVersion(b.Version) + "-1." + rpmArch[arch] + ".rpm"
	pkgName := slug + "-" + b.Version + "-" + target + ".PKGBUILD"
	installed := "/opt/" + slug + "/bin/stage"
	copies := []struct {
		from, to string
		art      Artifact
	}{
		{tarName, tarName, Artifact{Kind: KindDesktop, Format: FormatTarGz, CLI: &Binary{Path: "bin/stage", SHA256: cliSum}}},
		{debName, debName, Artifact{Kind: KindDesktop, Format: FormatDeb, CLI: &Binary{Path: installed, SHA256: cliSum}}},
		{rpmName, rpmName, Artifact{Kind: KindDesktop, Format: FormatRPM, CLI: &Binary{Path: installed, SHA256: cliSum}}},
		{"PKGBUILD", pkgName, Artifact{Kind: KindDesktop, Format: FormatPKGBUILD}},
	}
	var arts []Artifact
	for _, c := range copies {
		if err := copyFile(filepath.Join(o.out, c.to), filepath.Join(src, c.from), 0o644); err != nil {
			return nil, nil, err
		}
		a := c.art
		a.Name, a.OS, a.Arch = c.to, osName, arch
		arts = append(arts, a)
	}
	inst, err := os.ReadFile(filepath.Join(src, "install.sh"))
	if err != nil {
		return nil, nil, err
	}
	return arts, inst, nil
}

func assembleMac(o assembleOpts, src string, cliBins map[string][]byte) ([]Artifact, []byte, error) {
	b := o.b
	app := b.appName() + ".app"
	appDir := filepath.Join(src, app)
	cliPath := app + "/Contents/Resources/bin/stage"
	cli, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(cliPath)))
	if err != nil {
		return nil, nil, err
	}
	if err := checkUniversalCLI(cli, cliBins); err != nil {
		return nil, nil, err
	}
	exe, err := os.ReadFile(filepath.Join(appDir, "Contents", "MacOS", b.appName()))
	if err != nil {
		return nil, nil, err
	}
	if err := checkAppStamp(exe, b); err != nil {
		return nil, nil, err
	}
	cliSum := bytesSHA256(cli)
	tarName := b.slug() + "-" + b.Version + "-darwin-universal.tar.gz"
	if err := packTree(filepath.Join(o.out, tarName), appDir, app, o.mtime); err != nil {
		return nil, nil, err
	}
	arts := []Artifact{{Name: tarName, Kind: KindDesktop, Format: FormatTarGz, OS: "darwin", Arch: "universal", CLI: &Binary{Path: cliPath, SHA256: cliSum}}}
	dmg := b.slug() + "-" + b.Version + ".dmg"
	if _, err := os.Stat(filepath.Join(src, dmg)); err == nil {
		if err := copyFile(filepath.Join(o.out, dmg), filepath.Join(src, dmg), 0o644); err != nil {
			return nil, nil, err
		}
		arts = append(arts, Artifact{Name: dmg, Kind: KindDesktop, Format: FormatDMG, OS: "darwin", Arch: "universal", CLI: &Binary{Path: "/Applications/" + cliPath, SHA256: cliSum}})
	}
	return arts, nil, nil
}

// checkUniversalCLI requires the bundled universal CLI's slices to be this
// build's darwin-arm64 and darwin-amd64 binaries, byte for byte.
func checkUniversalCLI(cli []byte, cliBins map[string][]byte) error {
	parts, err := machoSlices(cli)
	if err != nil {
		return err
	}
	var want [][]byte
	for _, t := range []string{"darwin-arm64", "darwin-amd64"} {
		if bin, ok := cliBins[t]; ok {
			want = append(want, bin)
		}
	}
	if len(want) != 2 || len(parts) != 2 {
		return errors.New("the bundled CLI is not a universal binary of this build's darwin-arm64 and darwin-amd64 CLIs")
	}
	for _, w := range want {
		if !slices.ContainsFunc(parts, func(p []byte) bool { return bytes.Equal(p, w) }) {
			return errors.New("a slice of the bundled universal CLI differs from this build's darwin CLI")
		}
	}
	return nil
}
