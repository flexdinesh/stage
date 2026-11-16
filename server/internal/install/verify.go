package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Expect is what a release must be to install here.
type Expect struct {
	Identity string // the installing binary's identity
	Version  string // required version ("" any)
	Mode     string
}

// Verified is a verified release and the artifact for this machine.
type Verified struct {
	Dir      string
	Manifest *Manifest
	Artifact Artifact
}

const verifyFix = "the download is corrupt or was tampered with; nothing was installed. Retry, and report it if it persists"

// Verify checks the asset directory dir: the manifest is valid and is this
// identity's channel, checksums.txt lists exactly the manifest's
// artifacts with the same hashes, and this machine's artifact for mode is
// present with the manifest's size and sha256. Other artifacts may be
// absent (scripts download only the one they need).
func Verify(dir string, p Platform, e Expect) (*Verified, error) {
	m, err := readManifest(dir)
	if err != nil {
		return nil, wrapErr(CodeVerify, err, verifyFix, "invalid release in %s", dir)
	}
	if m.Identity != e.Identity {
		return nil, newErr(CodeVerify, identityFix(e.Identity), "release identity %s does not match this installation (%s)", m.Identity, e.Identity)
	}
	if e.Identity == "stage" && (m.Channel != "stable" || strings.HasSuffix(m.Commit, "-dirty")) {
		return nil, newErr(CodeVerify, "stable Stage installs only stable releases", "release %s is channel %s (commit %s), not a stable release", m.Version, m.Channel, m.Commit)
	}
	if e.Version != "" && m.Version != e.Version {
		return nil, newErr(CodeVerify, verifyFix, "release is version %s, expected %s", m.Version, e.Version)
	}
	sums, err := readChecksums(dir)
	if err != nil {
		return nil, wrapErr(CodeVerify, err, verifyFix, "invalid %s", ChecksumsName)
	}
	if len(sums) != len(m.Artifacts) {
		return nil, newErr(CodeVerify, verifyFix, "%s lists %d files, the manifest %d", ChecksumsName, len(sums), len(m.Artifacts))
	}
	for _, a := range m.Artifacts {
		if sums[a.Name] != a.SHA256 {
			return nil, newErr(CodeVerify, verifyFix, "%s and the manifest disagree about %s", ChecksumsName, a.Name)
		}
	}
	a, err := pick(m, p, e.Mode)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, a.Name)
	st, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, newErr(CodeVerify, "download it next to the manifest, or rerun the installer", "%s is missing from %s", a.Name, dir)
		}
		return nil, wrapErr(CodeVerify, err, verifyFix, "%s", a.Name)
	}
	if !st.Mode().IsRegular() {
		return nil, newErr(CodeVerify, verifyFix, "%s is not a regular file", a.Name)
	}
	if st.Size() != a.Size {
		return nil, newErr(CodeVerify, "the download is incomplete or corrupt; nothing was installed. Retry", "%s is %d bytes, the manifest says %d", a.Name, st.Size(), a.Size)
	}
	sum, _, err := fileSHA256(path)
	if err != nil {
		return nil, wrapErr(CodeVerify, err, verifyFix, "%s", a.Name)
	}
	if sum != a.SHA256 {
		return nil, newErr(CodeVerify, verifyFix, "%s sha256 %s does not match the manifest (%s)", a.Name, sum, a.SHA256)
	}
	return &Verified{Dir: dir, Manifest: m, Artifact: a}, nil
}

func identityFix(id string) string {
	if id == "stage-dev" {
		return "Stage Dev installs from source: scripts/install-dev.sh (docs/development.md)"
	}
	return "use the installer of the matching identity"
}

// pick is the tar.gz artifact for this machine in mode.
func pick(m *Manifest, p Platform, mode string) (Artifact, error) {
	target, err := platformTarget(p)
	if err != nil {
		return Artifact{}, err
	}
	kind, arch := "cli", p.GOARCH
	if mode == ModeDesktop {
		kind = "desktop"
		if p.GOOS == "darwin" {
			arch = "universal"
		}
	}
	for _, a := range m.Artifacts {
		if a.Kind == kind && a.Format == "tar.gz" && a.OS == p.GOOS && a.Arch == arch {
			if a.CLI == nil {
				return Artifact{}, newErr(CodeVerify, verifyFix, "artifact %s names no CLI", a.Name)
			}
			return a, nil
		}
	}
	fix := ""
	if mode == ModeDesktop {
		fix = "install the CLI/server only with --headless"
		for _, o := range m.Omitted {
			if o.Kind == "desktop" && o.OS == p.GOOS {
				fix = fmt.Sprintf("this release has no desktop app for %s (%s); install the CLI/server only with --headless", p.GOOS, o.Reason)
			}
		}
	}
	return Artifact{}, newErr(CodeUnsupported, fix, "release %s has no %s artifact for %s", m.Version, kind, target)
}
