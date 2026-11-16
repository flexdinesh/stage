// Package install is the single authority that installs, updates and
// uninstalls a Stage installation (docs/install.md): the desktop app with
// its bundled CLI/server, or the headless CLI/server. scripts/install.sh,
// scripts/install-dev.sh and `stage update` only fetch and verify a
// release, then run the new release's `stage install --from DIR`, which
// does everything else here: locking, staging, the swap, links, desktop
// registration, the service restart, readiness and rollback.
package install

import (
	"errors"
	"fmt"
)

// Exit codes of install, update and uninstall (docs/install.md).
const (
	CodeOK          = 0
	CodeFailed      = 1
	CodeUsage       = 2
	CodeBusy        = 3  // another install/update holds the lock
	CodeUnsupported = 7  // not an installer install, collision, platform, requirements
	CodeNetwork     = 10 // offline, rate limited, release not found
	CodeVerify      = 11 // assets failed verification; nothing outside staging written
	CodeRolledBack  = 12 // activation failed, previous version restored
	CodeNotRolled   = 13 // activation failed, new version kept (schema changed)
)

// Error is a failure with an exit code and a fix for the user.
type Error struct {
	Code int
	Msg  string
	Fix  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

func newErr(code int, fix string, format string, a ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...), Fix: fix}
}

func wrapErr(code int, err error, fix string, format string, a ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...), Fix: fix, Err: err}
}

// CodeOf is err's exit code: its Error code, else CodeFailed.
func CodeOf(err error) int {
	if err == nil {
		return CodeOK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeFailed
}

// FixOf is err's fix, if any.
func FixOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Fix
	}
	return ""
}
