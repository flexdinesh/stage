// Package buildinfo holds build-time identity and version metadata
// (docs/runtime.md). Release builds set the variables with -ldflags -X.
package buildinfo

import (
	"regexp"
	"runtime/debug"
)

// Identity names an installation: it derives the default data dir, the
// runtime (socket) dir and the T3 client name. "stage" is the stable
// installation; a developer installation is built with another name
// (-X github.com/flexdinesh/stage/server/internal/buildinfo.Identity=...),
// so its binary can never use the stable paths.
var Identity = "stage"

// ServiceLabel overrides the macOS launchd label of the per-user service
// (docs/service.md). Empty: Label() derives it from Identity. Packaging may
// set it with -X .../buildinfo.ServiceLabel=...
var ServiceLabel = ""

// Label is the launchd label of the per-user service: ServiceLabel, or
// "io.github.flexdinesh.<Identity>".
func Label() string {
	if ServiceLabel != "" {
		return ServiceLabel
	}
	return "io.github.flexdinesh." + Identity
}

var identityRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidIdentity reports whether Identity is usable in a unit name, a
// launchd label and paths: lowercase letters, digits, '.', '_' and '-'.
func ValidIdentity() bool { return identityRe.MatchString(Identity) }

// Version is the release version ("dev" when not stamped).
var Version = "dev"

// Commit is the source revision; when not stamped, the VCS revision Go
// embedded at build time, or "".
var Commit = ""

// Stamp is set by release builds (scripts/release.sh) to
// "stage-build/1 identity=<id> version=<v> commit=<sha>;" (docs/release.md).
// Go omits -ldflags from the embedded build info under -trimpath, so
// release verification finds this marker in binaries it cannot run (other
// architectures). `stage version --json` reports it as "build".
var Stamp = ""

// APIVersion is the /api contract version. It changes only on breaking
// API changes; clients refuse a server that reports a different one.
const APIVersion = 9

// APIVersionHeader carries APIVersion on every /api response.
const APIVersionHeader = "Stage-Api-Version"

// Service is the fixed service name /api/health reports, so a client can
// tell a Stage server from anything else on an address.
const Service = "stage"

// Revision is Commit, or the embedded VCS revision when Commit is unset.
func Revision() string {
	if Commit != "" {
		return Commit
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev != "" && dirty {
		rev += "-dirty"
	}
	return rev
}
