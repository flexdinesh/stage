package install

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The release contract (docs/release.md): every asset directory has the
// manifest and checksums.txt besides its artifacts.
const (
	ManifestName   = "stage-release.json"
	ChecksumsName  = "checksums.txt"
	manifestSchema = 1
	metaMax        = 1 << 20
)

// Manifest is stage-release.json schema 1 (the fields the installer reads).
type Manifest struct {
	Schema        int           `json:"schema"`
	Name          string        `json:"name"`
	Version       string        `json:"version"`
	Commit        string        `json:"commit"`
	Identity      string        `json:"identity"`
	Channel       string        `json:"channel"`
	Compatibility Compatibility `json:"compatibility"`
	Artifacts     []Artifact    `json:"artifacts"`
	Omitted       []Omission    `json:"omitted,omitempty"`
}

// Compatibility is the build's API version and platform requirements.
type Compatibility struct {
	APIVersion int `json:"apiVersion"`
}

// Artifact is one release asset.
type Artifact struct {
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`
	Format string  `json:"format"`
	OS     string  `json:"os"`
	Arch   string  `json:"arch,omitempty"`
	Size   int64   `json:"size"`
	SHA256 string  `json:"sha256"`
	CLI    *Binary `json:"cli,omitempty"`
}

// Binary locates the stage CLI inside an artifact and pins its content.
type Binary struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Omission is a target the build left out.
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

// ValidVersion reports whether v is a release version (X.Y.Z[-pre]).
func ValidVersion(v string) bool { return versionRe.MatchString(v) }

// Prerelease reports whether v has a pre-release part.
func Prerelease(v string) bool { return strings.Contains(v, "-") }

// CompareVersions orders two release versions by semver precedence
// (-1, 0, 1). Invalid versions sort before valid ones.
func CompareVersions(a, b string) int {
	va, vb := ValidVersion(a), ValidVersion(b)
	switch {
	case !va && !vb:
		return strings.Compare(a, b)
	case !va:
		return -1
	case !vb:
		return 1
	}
	coreA, preA, _ := strings.Cut(a, "-")
	coreB, preB, _ := strings.Cut(b, "-")
	if c := compareDotted(coreA, coreB, true); c != 0 {
		return c
	}
	switch {
	case preA == "" && preB == "":
		return 0
	case preA == "":
		return 1
	case preB == "":
		return -1
	}
	return compareDotted(preA, preB, false)
}

func compareDotted(a, b string, numeric bool) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, ea := strconv.ParseUint(pa[i], 10, 64)
		nb, eb := strconv.ParseUint(pb[i], 10, 64)
		switch {
		case ea == nil && eb == nil:
			if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
		case numeric:
			return strings.Compare(pa[i], pb[i])
		case ea == nil:
			return -1
		case eb == nil:
			return 1
		default:
			if c := strings.Compare(pa[i], pb[i]); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(pa) < len(pb):
		return -1
	case len(pa) > len(pb):
		return 1
	}
	return 0
}

// readManifest reads and validates dir's manifest.
func readManifest(dir string) (*Manifest, error) {
	data, err := readCapped(filepath.Join(dir, ManifestName), metaMax)
	if err != nil {
		return nil, err
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestName, err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestName, err)
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	switch {
	case m.Schema != manifestSchema:
		return fmt.Errorf("schema %d, this installer reads %d (install a newer Stage with scripts/install.sh)", m.Schema, manifestSchema)
	case m.Name != "stage":
		return fmt.Errorf("name %q is not stage", m.Name)
	case !ValidVersion(m.Version):
		return fmt.Errorf("invalid version %q", m.Version)
	case !commitRe.MatchString(m.Commit):
		return fmt.Errorf("invalid commit %q", m.Commit)
	case m.Identity == "":
		return fmt.Errorf("no identity")
	case m.Channel != "stable" && m.Channel != "dev":
		return fmt.Errorf("invalid channel %q", m.Channel)
	case len(m.Artifacts) == 0:
		return fmt.Errorf("lists no artifacts")
	}
	seen := map[string]bool{}
	for _, a := range m.Artifacts {
		if a.Name == "" || a.Name != filepath.Base(a.Name) || strings.HasPrefix(a.Name, ".") {
			return fmt.Errorf("invalid artifact name %q", a.Name)
		}
		if seen[a.Name] {
			return fmt.Errorf("artifact %s listed twice", a.Name)
		}
		seen[a.Name] = true
		if !sha256Re.MatchString(a.SHA256) || a.Size <= 0 {
			return fmt.Errorf("artifact %s: invalid size or sha256", a.Name)
		}
		// cli.path is a path inside an archive, or the installed path of a
		// system package (/opt/...); only archive paths must be relative.
		if a.CLI != nil && (!sha256Re.MatchString(a.CLI.SHA256) || (a.Format == "tar.gz" && !safeRel(a.CLI.Path))) {
			return fmt.Errorf("artifact %s: invalid cli", a.Name)
		}
	}
	return nil
}

// readChecksums parses checksums.txt (`shasum -a 256` lines), refusing
// malformed lines and duplicates.
func readChecksums(dir string) (map[string]string, error) {
	data, err := readCapped(filepath.Join(dir, ChecksumsName), metaMax)
	if err != nil {
		return nil, err
	}
	sums := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		sum, name, ok := strings.Cut(line, "  ")
		name = strings.TrimPrefix(name, "*")
		if !ok || !sha256Re.MatchString(sum) || name == "" || name != filepath.Base(name) {
			return nil, fmt.Errorf("%s: malformed line %q", ChecksumsName, line)
		}
		if _, dup := sums[name]; dup {
			return nil, fmt.Errorf("%s: %s listed twice", ChecksumsName, name)
		}
		sums[name] = sum
	}
	return sums, sc.Err()
}

func readCapped(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", filepath.Base(path), max)
	}
	return data, nil
}

func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func bytesSHA256(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// safeRel reports whether p is a clean relative path inside its root.
func safeRel(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.ContainsAny(p, "\x00\n\r") {
		return false
	}
	c := filepath.ToSlash(filepath.Clean(p))
	return c == p && c != "." && c != ".." && !strings.HasPrefix(c, "../")
}
