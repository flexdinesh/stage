package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

func cmdVerify(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	smoke := fs.Bool("smoke", false, "also run this machine's CLI and desktop app from the artifacts")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: release verify [-smoke] DIR")
	}
	dir := fs.Arg(0)
	m, err := verifyDir(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "verified %d artifacts of %s %s (%s, %s)\n", len(m.Artifacts), m.Name, m.Version, m.Identity, m.Commit)
	for _, om := range m.Omitted {
		fmt.Fprintf(w, "omitted %s %s/%s: %s\n", om.Kind, om.OS, om.Arch, om.Reason)
	}
	if *smoke {
		lines, err := smokeTest(dir, m)
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// verifyDir checks an asset directory: the manifest, checksums.txt, the
// exact file set, every artifact's size and hash, names, stamped versions,
// the bundled CLI against the headless one, and that no archive carries
// secrets or local state.
func verifyDir(dir string) (*Manifest, error) {
	m, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestName, err)
	}
	b := build{Version: m.Version, Commit: m.Commit, Identity: m.Identity, Channel: m.Channel}
	sums, err := readChecksums(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{ManifestName: true, ChecksumsName: true}
	for _, a := range m.Artifacts {
		want[a.Name] = true
	}
	var errs []error
	for _, e := range entries {
		if !want[e.Name()] {
			errs = append(errs, fmt.Errorf("%s is not in the manifest", e.Name()))
		}
		delete(want, e.Name())
	}
	for name := range want {
		errs = append(errs, fmt.Errorf("%s is missing", name))
	}
	if len(sums) != len(m.Artifacts) {
		errs = append(errs, fmt.Errorf("%s has %d lines for %d artifacts", ChecksumsName, len(sums), len(m.Artifacts)))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	cliBins := map[string][]byte{}
	// Headless CLIs first: desktop artifacts are compared with them.
	order := slices.Clone(m.Artifacts)
	slices.SortStableFunc(order, func(x, y Artifact) int {
		return boolInt(x.Kind != KindCLI) - boolInt(y.Kind != KindCLI)
	})
	for _, a := range order {
		if err := verifyArtifact(dir, a, b, sums, m, cliBins); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.Name, err))
		}
	}
	return m, errors.Join(errs...)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// expectedName is the asset name of an artifact; installers rely on it.
func expectedName(a Artifact, b build) string {
	slug := b.slug()
	target := a.OS + "-" + a.Arch
	switch {
	case a.Kind == KindCLI && a.Format == FormatTarGz:
		return b.cliName(target) + ".tar.gz"
	case a.Kind == KindDesktop && a.Format == FormatTarGz:
		return slug + "-" + b.Version + "-" + target + ".tar.gz"
	case a.Kind == KindDesktop && a.Format == FormatDeb:
		return slug + "_" + debVersion(b.Version) + "_" + a.Arch + ".deb"
	case a.Kind == KindDesktop && a.Format == FormatRPM:
		return slug + "-" + rpmVersion(b.Version) + "-1." + rpmArch[a.Arch] + ".rpm"
	case a.Kind == KindDesktop && a.Format == FormatPKGBUILD:
		return slug + "-" + b.Version + "-" + target + ".PKGBUILD"
	case a.Kind == KindDesktop && a.Format == FormatDMG:
		return slug + "-" + b.Version + ".dmg"
	case a.Kind == KindInstaller && a.Format == FormatSh:
		return slug + "-install.sh"
	}
	return ""
}

func verifyArtifact(dir string, a Artifact, b build, sums map[string]string, m *Manifest, cliBins map[string][]byte) error {
	if want := expectedName(a, b); a.Name != want {
		return fmt.Errorf("name for %s %s %s/%s should be %q", a.Kind, a.Format, a.OS, a.Arch, want)
	}
	p := filepath.Join(dir, a.Name)
	sum, size, err := fileSHA256(p)
	if err != nil {
		return err
	}
	if sum != a.SHA256 || size != a.Size {
		return fmt.Errorf("sha256 %s size %d, manifest says %s %d", sum, size, a.SHA256, a.Size)
	}
	if sums[a.Name] != sum {
		return fmt.Errorf("%s says %q", ChecksumsName, sums[a.Name])
	}
	switch {
	case a.Kind == KindCLI:
		return verifyCLITar(p, a, b, cliBins)
	case a.Format == FormatTarGz && a.OS == "linux":
		return verifyLinuxDesktopTar(p, a, b, cliBins)
	case a.Format == FormatTarGz && a.OS == "darwin":
		return verifyMacTar(p, a, b, cliBins)
	case a.Format == FormatDeb:
		return verifyDeb(p, a, b, cliBins)
	case a.Format == FormatPKGBUILD:
		return verifyPKGBUILD(p, a, b, m)
	}
	// rpm (its name carries the version and it packs the same staged files
	// as the tarball and deb), dmg and install.sh: name and hash only.
	return nil
}

// denied names are never shipped: environment files, databases, keys and
// local or VCS state.
var deniedRe = regexp.MustCompile(`(?i)^(\.env(\..*)?|.*\.(db|db-wal|db-shm|sqlite|sqlite3|key|pem|p12|pfx)|id_(rsa|ed25519|ecdsa)(\.pub)?|service\.env|\.netrc|\.npmrc)$`)

var deniedDirs = []string{".scratch", ".git", "node_modules", ".mygo"}

func deniedPath(name string) bool {
	parts := strings.Split(name, "/")
	for _, p := range parts {
		if slices.Contains(deniedDirs, p) {
			return true
		}
	}
	return deniedRe.MatchString(parts[len(parts)-1])
}

// archive is the regular files of a tar.gz, by cleaned name.
type archive struct {
	files map[string][]byte
	types map[string]byte
}

// readTarGz reads an archive, refusing denied paths, absolute or escaping
// names and anything but directories, files and (inside an .app) symlinks.
// keep selects which file contents to keep in memory.
func readTarGz(r io.Reader, keep func(string) bool) (*archive, error) {
	ar := &archive{files: map[string][]byte{}, types: map[string]byte{}}
	err := walkTarGz(r, func(name string, h *tar.Header, body io.Reader) error {
		if name == "" || strings.HasPrefix(h.Name, "/") || strings.Contains(h.Name, "..") {
			return fmt.Errorf("unsafe member %q", h.Name)
		}
		if deniedPath(name) {
			return fmt.Errorf("ships %q, which must never be packaged", name)
		}
		switch h.Typeflag {
		case tar.TypeDir, tar.TypeReg, tar.TypeSymlink:
		default:
			return fmt.Errorf("member %q has type %q", name, h.Typeflag)
		}
		ar.types[name] = h.Typeflag
		if h.Typeflag == tar.TypeReg && keep(name) {
			data, err := io.ReadAll(body)
			if err != nil {
				return err
			}
			ar.files[name] = data
		}
		return nil
	})
	return ar, err
}

func openTar(p string, keep func(string) bool) (*archive, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readTarGz(f, keep)
}

func (ar *archive) need(name string) ([]byte, error) {
	data, ok := ar.files[name]
	if !ok {
		return nil, fmt.Errorf("has no %s", name)
	}
	return data, nil
}

func (ar *archive) has(name string) bool { return ar.types[name] == tar.TypeReg }

func checkBundled(a Artifact, cli []byte) error {
	if a.CLI == nil {
		return errors.New("the manifest names no CLI")
	}
	if bytesSHA256(cli) != a.CLI.SHA256 {
		return fmt.Errorf("%s does not match the manifest's CLI sha256", a.CLI.Path)
	}
	return nil
}

func verifyCLITar(p string, a Artifact, b build, cliBins map[string][]byte) error {
	root := b.cliName(a.OS + "-" + a.Arch)
	ar, err := openTar(p, func(string) bool { return true })
	if err != nil {
		return err
	}
	wantNames := []string{root, root + "/stage", root + "/LICENSE", root + "/README.md"}
	got := make([]string, 0, len(ar.types))
	for n := range ar.types {
		got = append(got, n)
	}
	slices.Sort(got)
	slices.Sort(wantNames)
	if !slices.Equal(got, wantNames) {
		return fmt.Errorf("contains %v, want exactly %v", got, wantNames)
	}
	if a.CLI == nil || a.CLI.Path != root+"/stage" {
		return fmt.Errorf("manifest CLI path should be %s/stage", root)
	}
	cli, err := ar.need(root + "/stage")
	if err != nil {
		return err
	}
	if err := checkBundled(a, cli); err != nil {
		return err
	}
	if err := checkCLIStamp(cli, b); err != nil {
		return err
	}
	cliBins[a.OS+"-"+a.Arch] = cli
	return nil
}

func sameAsHeadless(cli []byte, target string, cliBins map[string][]byte) error {
	want, ok := cliBins[target]
	if !ok {
		return fmt.Errorf("no headless CLI for %s to compare the bundled CLI with", target)
	}
	if !bytes.Equal(cli, want) {
		return fmt.Errorf("the bundled CLI differs from the headless %s CLI", target)
	}
	return nil
}

func verifyLinuxDesktopTar(p string, a Artifact, b build, cliBins map[string][]byte) error {
	slug := b.slug()
	ar, err := openTar(p, func(n string) bool { return n == "bin/stage" || n == slug })
	if err != nil {
		return err
	}
	if a.CLI == nil || a.CLI.Path != "bin/stage" {
		return errors.New("manifest CLI path should be bin/stage")
	}
	cli, err := ar.need("bin/stage")
	if err != nil {
		return err
	}
	if err := checkBundled(a, cli); err != nil {
		return err
	}
	if err := sameAsHeadless(cli, a.OS+"-"+a.Arch, cliBins); err != nil {
		return err
	}
	exe, err := ar.need(slug)
	if err != nil {
		return err
	}
	if err := checkAppStamp(exe, b); err != nil {
		return err
	}
	if !ar.has("LICENSE") {
		return errors.New("has no LICENSE")
	}
	return nil
}

func verifyMacTar(p string, a Artifact, b build, cliBins map[string][]byte) error {
	app := b.appName() + ".app"
	cliPath := app + "/Contents/Resources/bin/stage"
	exePath := app + "/Contents/MacOS/" + b.appName()
	plist := app + "/Contents/Info.plist"
	ar, err := openTar(p, func(n string) bool { return n == cliPath || n == exePath || n == plist })
	if err != nil {
		return err
	}
	if a.CLI == nil || a.CLI.Path != cliPath {
		return fmt.Errorf("manifest CLI path should be %s", cliPath)
	}
	cli, err := ar.need(cliPath)
	if err != nil {
		return err
	}
	if err := checkBundled(a, cli); err != nil {
		return err
	}
	if err := checkCLIStamp(cli, b); err != nil {
		return err
	}
	if err := checkUniversalCLI(cli, cliBins); err != nil {
		return err
	}
	exe, err := ar.need(exePath)
	if err != nil {
		return err
	}
	if err := checkAppStamp(exe, b); err != nil {
		return err
	}
	info, err := ar.need(plist)
	if err != nil {
		return err
	}
	for _, key := range []string{"CFBundleShortVersionString", "CFBundleVersion"} {
		if !regexp.MustCompile(`<key>` + key + `</key>\s*<string>` + regexp.QuoteMeta(b.Version) + `</string>`).Match(info) {
			return fmt.Errorf("Info.plist %s is not %s", key, b.Version)
		}
	}
	if !ar.has(app + "/Contents/Resources/LICENSE") {
		return errors.New("has no Contents/Resources/LICENSE")
	}
	return nil
}

func verifyDeb(p string, a Artifact, b build, cliBins map[string][]byte) error {
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	members, err := arMembers(data)
	if err != nil {
		return err
	}
	control, ok := members["control.tar.gz"]
	if !ok {
		return errors.New("has no control.tar.gz")
	}
	ctl, err := readTarGz(bytes.NewReader(control), func(n string) bool { return n == "control" })
	if err != nil {
		return fmt.Errorf("control.tar.gz: %w", err)
	}
	c, err := ctl.need("control")
	if err != nil {
		return fmt.Errorf("control.tar.gz %w", err)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(c), "\n") {
		if k, v, ok := strings.Cut(line, ": "); ok && !strings.HasPrefix(line, " ") {
			fields[k] = v
		}
	}
	if fields["Package"] != b.slug() || fields["Version"] != debVersion(b.Version) || fields["Architecture"] != a.Arch {
		return fmt.Errorf("control says %s %s %s, want %s %s %s", fields["Package"], fields["Version"], fields["Architecture"], b.slug(), debVersion(b.Version), a.Arch)
	}
	payload, ok := members["data.tar.gz"]
	if !ok {
		return errors.New("has no data.tar.gz")
	}
	if a.CLI == nil || a.CLI.Path != "/opt/"+b.slug()+"/bin/stage" {
		return errors.New("manifest CLI path should be /opt/<slug>/bin/stage")
	}
	cliName := strings.TrimPrefix(a.CLI.Path, "/")
	dat, err := readTarGz(bytes.NewReader(payload), func(n string) bool { return n == cliName })
	if err != nil {
		return fmt.Errorf("data.tar.gz: %w", err)
	}
	cli, err := dat.need(cliName)
	if err != nil {
		return err
	}
	if err := checkBundled(a, cli); err != nil {
		return err
	}
	if err := sameAsHeadless(cli, a.OS+"-"+a.Arch, cliBins); err != nil {
		return err
	}
	if !dat.has(path.Join("opt", b.slug(), "LICENSE")) {
		return errors.New("has no LICENSE")
	}
	return nil
}

func verifyPKGBUILD(p string, a Artifact, b build, m *Manifest) error {
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	tar := Artifact{Kind: KindDesktop, Format: FormatTarGz, OS: a.OS, Arch: a.Arch}
	tarName := expectedName(tar, b)
	var tarSum string
	for _, x := range m.Artifacts {
		if x.Name == tarName {
			tarSum = x.SHA256
		}
	}
	if tarSum == "" {
		return fmt.Errorf("its source %s is not in the release", tarName)
	}
	s := string(data)
	for _, want := range []string{"pkgver='" + archVersion(b.Version) + "'", "source=('" + tarName + "')", "sha256sums=('" + tarSum + "')"} {
		if !strings.Contains(s, want) {
			return fmt.Errorf("does not contain %s", want)
		}
	}
	return nil
}
