package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// ManifestName and ChecksumsName are the two files every asset directory
// has besides its artifacts.
const (
	ManifestName  = "stage-release.json"
	ChecksumsName = "checksums.txt"
	// ManifestSchema changes only when a reader of an older schema would
	// misread the manifest.
	ManifestSchema = 1
)

// Manifest describes one coherent build: every artifact shares Version,
// Commit and Identity. The installer task reads it (docs/release.md).
type Manifest struct {
	Schema        int           `json:"schema"`
	Name          string        `json:"name"`
	Version       string        `json:"version"`
	Commit        string        `json:"commit"`
	Date          string        `json:"date"`
	Identity      string        `json:"identity"`
	Channel       string        `json:"channel"`
	Compatibility Compatibility `json:"compatibility"`
	Artifacts     []Artifact    `json:"artifacts"`
	Omitted       []Omission    `json:"omitted,omitempty"`
}

// Compatibility is what a machine needs to run the artifacts and which
// servers the CLI and desktop accept.
type Compatibility struct {
	// APIVersion is the /api contract of every binary in this build; a
	// client refuses a server reporting another one.
	APIVersion int      `json:"apiVersion"`
	MacOS      MacOSReq `json:"macos"`
	Linux      LinuxReq `json:"linux"`
	Note       string   `json:"note"`
}

type MacOSReq struct {
	Minimum string `json:"minimum"`
}

type LinuxReq struct {
	Libc           string   `json:"libc"`
	DesktopDepends []string `json:"desktopDepends"`
}

// Artifact kinds and formats.
const (
	KindCLI       = "cli"
	KindDesktop   = "desktop"
	KindInstaller = "installer"

	FormatTarGz    = "tar.gz"
	FormatDeb      = "deb"
	FormatRPM      = "rpm"
	FormatPKGBUILD = "pkgbuild"
	FormatDMG      = "dmg"
	FormatSh       = "sh"
	// FormatPNG is a Linux desktop icon the PKGBUILD names as a source
	// beside the tarball (MyGo's <slug>-<n>x<n>.png, one per release).
	FormatPNG = "png"
)

// Artifact is one release asset.
type Artifact struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Format string `json:"format"`
	OS     string `json:"os"`
	// Arch is amd64, arm64 or universal (macOS); empty for any.
	Arch   string `json:"arch,omitempty"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// CLI is the stage CLI/server inside the artifact: its path in the
	// archive, or its installed path for a system package.
	CLI *Binary `json:"cli,omitempty"`
}

// Binary locates an executable and pins its content.
type Binary struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Omission records a target this build left out, and why.
type Omission struct {
	Kind   string `json:"kind"`
	OS     string `json:"os"`
	Arch   string `json:"arch,omitempty"`
	Reason string `json:"reason"`
}

var (
	versionRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9a-z]+(\.[0-9a-z]+)*)?$`)
	commitRe  = regexp.MustCompile(`^[0-9a-f]{40}(-dirty)?$`)
	sha256Re  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// validVersion reports whether v is a version this tool builds: semver
// X.Y.Z with an optional pre-release of lowercase letters, digits and
// dots (no build metadata), so package formats need no escaping.
func validVersion(v string) bool { return versionRe.MatchString(v) }

func (m *Manifest) validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if m.Schema != ManifestSchema {
		add("schema %d, want %d", m.Schema, ManifestSchema)
	}
	if m.Name != "stage" {
		add("name %q, want stage", m.Name)
	}
	if !validVersion(m.Version) {
		add("version %q is not X.Y.Z[-pre]", m.Version)
	}
	if !commitRe.MatchString(m.Commit) {
		add("commit %q is not a full revision", m.Commit)
	}
	switch {
	case m.Channel == "stable" && m.Identity != "stage":
		add("stable channel with identity %q", m.Identity)
	case m.Channel == "dev" && m.Identity != "stage-dev":
		add("dev channel with identity %q", m.Identity)
	case m.Channel != "stable" && m.Channel != "dev":
		add("channel %q, want stable or dev", m.Channel)
	}
	if m.Channel == "stable" && strings.HasSuffix(m.Commit, "-dirty") {
		add("stable build from a dirty tree")
	}
	if m.Compatibility.APIVersion < 1 {
		add("apiVersion %d", m.Compatibility.APIVersion)
	}
	if len(m.Artifacts) == 0 {
		add("no artifacts")
	}
	seen := map[string]bool{}
	for _, a := range m.Artifacts {
		if a.Name == "" || a.Name != filepath.Base(a.Name) || a.Name == ManifestName || a.Name == ChecksumsName {
			add("artifact name %q", a.Name)
		}
		if seen[a.Name] {
			add("artifact %s listed twice", a.Name)
		}
		seen[a.Name] = true
		if !sha256Re.MatchString(a.SHA256) || a.Size <= 0 {
			add("artifact %s: size %d sha256 %q", a.Name, a.Size, a.SHA256)
		}
		if a.CLI != nil && !sha256Re.MatchString(a.CLI.SHA256) {
			add("artifact %s: cli sha256 %q", a.Name, a.CLI.SHA256)
		}
	}
	return errors.Join(errs...)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func readManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestName, err)
	}
	return &m, nil
}

// writeChecksums writes checksums.txt in `shasum -a 256` format, sorted by
// name, for every artifact.
func writeChecksums(dir string, arts []Artifact) error {
	lines := make([]string, 0, len(arts))
	for _, a := range arts {
		lines = append(lines, a.SHA256+"  "+a.Name)
	}
	slices.Sort(lines)
	return os.WriteFile(filepath.Join(dir, ChecksumsName), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// readChecksums parses checksums.txt into name -> sha256, refusing
// malformed or duplicate lines.
func readChecksums(dir string) (map[string]string, error) {
	f, err := os.Open(filepath.Join(dir, ChecksumsName))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sums := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		sum, name, ok := strings.Cut(sc.Text(), "  ")
		if !ok || !sha256Re.MatchString(sum) || name == "" || name != filepath.Base(name) {
			return nil, fmt.Errorf("%s line %d is malformed", ChecksumsName, n)
		}
		if _, dup := sums[name]; dup {
			return nil, fmt.Errorf("%s lists %s twice", ChecksumsName, name)
		}
		sums[name] = sum
	}
	return sums, sc.Err()
}

func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func bytesSHA256(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
