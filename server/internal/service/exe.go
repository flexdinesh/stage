package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/db"
)

// BuildInfo is `stage version --json`.
type BuildInfo struct {
	Identity     string `json:"identity"`
	Version      string `json:"version"`
	Commit       string `json:"commit"`
	APIVersion   int    `json:"apiVersion"`
	ServiceLabel string `json:"serviceLabel"`
	ServiceUnit  string `json:"serviceUnit"`
	// Schema is the highest database schema this binary knows.
	Schema int64 `json:"schema"`
	// Build is the release build stamp (buildinfo.Stamp), absent when
	// unstamped.
	Build string `json:"build,omitempty"`
}

// ThisBuild describes this binary.
func ThisBuild() BuildInfo {
	return BuildInfo{
		Identity: buildinfo.Identity, Version: buildinfo.Version, Commit: buildinfo.Revision(),
		APIVersion: buildinfo.APIVersion, ServiceLabel: buildinfo.Label(), ServiceUnit: UnitName(),
		Schema: db.MaxVersion(), Build: buildinfo.Stamp,
	}
}

// SelfPath is this binary's path to record in a definition: the absolute
// os.Args[0] (looked up in PATH when bare) when it is the same file as
// os.Executable(), so a stable symlink such as /usr/bin/stage or a
// Homebrew link is kept (Linux's os.Executable is already resolved);
// otherwise os.Executable().
func SelfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	cand := os.Args[0]
	if !strings.Contains(cand, "/") {
		lp, err := exec.LookPath(cand)
		if err != nil {
			return exe
		}
		cand = lp
	}
	abs, err := filepath.Abs(cand)
	if err != nil {
		return exe
	}
	a, err1 := os.Stat(abs)
	b, err2 := os.Stat(exe)
	if err1 == nil && err2 == nil && os.SameFile(a, b) {
		return abs
	}
	return exe
}

// DefaultTempDirs are refused as the location of a service executable.
func DefaultTempDirs() []string {
	return []string{os.TempDir(), "/tmp", "/var/tmp", "/private/tmp", "/private/var/folders", "/var/folders"}
}

// ValidateExe refuses executables a service must not point at: relative
// paths, temporary or build-cache locations, macOS App Translocation and
// the Trash, and anything that is not an executable regular file.
func ValidateExe(p string, tempDirs []string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("executable %q must be an absolute path", p)
	}
	if strings.ContainsAny(p, "\n\r\x00") {
		return fmt.Errorf("executable %q contains control characters", p)
	}
	clean := filepath.Clean(p)
	for _, d := range tempDirs {
		d = filepath.Clean(d)
		if d != "." && d != "/" && (clean == d || strings.HasPrefix(clean, d+"/")) {
			return fmt.Errorf("executable %s is in a temporary directory (%s); install stage at a stable path or pass --exe", clean, d)
		}
	}
	for _, seg := range []string{"/go-build", "/AppTranslocation/", "/.Trash/"} {
		if strings.Contains(clean, seg) {
			return fmt.Errorf("executable %s is in a transient location (%s); install stage at a stable path or pass --exe", clean, strings.Trim(seg, "/"))
		}
	}
	st, err := os.Stat(clean)
	if err != nil {
		return fmt.Errorf("executable %s: %w", clean, err)
	}
	if !st.Mode().IsRegular() || st.Mode()&0o111 == 0 {
		return fmt.Errorf("executable %s is not an executable file", clean)
	}
	return nil
}

// ProbeExe runs `<exe> version --json`.
func ProbeExe(ctx context.Context, exe string) (BuildInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "version", "--json").Output()
	if err != nil {
		return BuildInfo{}, fmt.Errorf("%s version --json: %w (is it a stage binary from this release?)", exe, err)
	}
	var bi BuildInfo
	if err := json.Unmarshal(out, &bi); err != nil || bi.Identity == "" {
		return BuildInfo{}, fmt.Errorf("%s version --json: not a stage binary", exe)
	}
	return bi, nil
}
