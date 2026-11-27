// Package auth owns shared installation accounts and credentials. Transport
// callers provide authenticated user IDs and enforce request policy.
package auth

import (
	"context"
	"errors"
	"net/mail"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

const (
	shortGrant      = 30 * time.Minute
	inviteLifetime  = 7 * 24 * time.Hour
	browserIdle     = 7 * 24 * time.Hour
	sessionLifetime = 30 * 24 * time.Hour
	emailMax        = 254
	nameMax         = 128
)

type userError string

func (e userError) Error() string { return string(e) }

const (
	ErrPublicOrigin       = userError("Set STAGE_PUBLIC_URL to the browser origin. Use HTTPS, or HTTP on loopback for local testing.")
	ErrEmail              = userError("Enter one email address.")
	ErrName               = userError("Enter a display name of up to 128 characters.")
	ErrPasswordLength     = userError("Use a passphrase of 15 to 128 characters.")
	ErrCommonPassword     = userError("Choose a less common passphrase.")
	ErrSignIn             = userError("Sign-in failed. Check your email and password, or try again shortly.")
	ErrCurrentPassword    = userError("Your current password is incorrect. Try again or sign out and use a reset link.")
	ErrBusy               = userError("Stage is busy verifying passwords. Try again shortly.")
	ErrSession            = userError("Sign in to continue.")
	ErrForbidden          = userError("You do not have access to do this.")
	ErrPending            = userError("Setup is unavailable. Ask the installation owner for a current setup link.")
	ErrInvite             = userError("This invitation is unavailable. Ask a Stage admin for a new link.")
	ErrReset              = userError("This reset link is unavailable. Ask whoever issued it for a new link.")
	ErrAccountExists      = userError("That email address already has a Stage account.")
	ErrInviteExists       = userError("That email address already has an invitation. Replace or revoke the existing invitation.")
	ErrLastAdmin          = userError("Stage needs one active admin. Promote another person first.")
	ErrAccountUnavailable = userError("That account is unavailable.")
	ErrRole               = userError("Choose Admin or Member.")
)

// PublicMessage returns account copy that can be used in a response or on
// the command line without publishing storage errors or request inputs.
func PublicMessage(err error) string {
	var public userError
	if errors.As(err, &public) {
		return public.Error()
	}
	return "Stage could not complete the account operation. Try again."
}

type Service struct {
	db        *db.Store
	origin    string
	clock     func() time.Time
	hashSlots chan struct{}
	limiter   loginLimiter
}

// New takes the canonical origin from ParsePublicOrigin. Personal startup
// may pass an empty origin. Start refuses shared mode without a valid origin.
func New(database *db.Store, origin string, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	canonical, _ := ParsePublicOrigin(origin)
	return &Service{db: database, origin: canonical, clock: clock, hashSlots: make(chan struct{}, argonSlots),
		limiter: loginLimiter{accounts: make(map[[32]byte]attempts), addresses: make(map[netip.Addr]attempts)}}
}

func (s *Service) now() time.Time { return s.clock().UTC().Truncate(time.Microsecond) }

type accessFunc[T any] func(store.Access) (T, error)

func withAccess[T any](transact func(context.Context, func(store.Access) error) error, ctx context.Context, fn accessFunc[T]) (T, error) {
	var result T
	err := transact(ctx, func(r store.Access) error {
		var err error
		result, err = fn(r)
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return result, nil
}

func lockedMode(ctx context.Context, r store.Access, mode store.AccessMode, denied error) error {
	state, err := r.InstallationForUpdate(ctx)
	if err == nil && state.Mode != mode {
		return denied
	}
	return err
}

func checkAdmin(ctx context.Context, r store.Access, actor int64) error {
	if err := lockedMode(ctx, r, store.AccessClaimed, ErrForbidden); err != nil {
		return err
	}
	admin, err := activeAccount(ctx, r, actor, ErrForbidden)
	if err == nil && admin.Role != store.RoleAdmin {
		return ErrForbidden
	}
	return err
}

func activeAccount(ctx context.Context, r store.Access, id int64, denied error) (store.Account, error) {
	a, err := r.Account(ctx, id)
	if errors.Is(err, store.ErrNotFound) || err == nil && (a.Status != store.AccountActive || a.Email == nil) {
		return store.Account{}, denied
	}
	return a, err
}

func audit(ctx context.Context, r store.Access, now time.Time, action string, actor, target, grant *int64) error {
	return r.InsertAccessAudit(ctx, store.AccessAudit{Action: action, ActorUserID: actor, TargetUserID: target, GrantID: grant, CreatedAt: now})
}

func validEmail(input string) (string, error) {
	email := strings.TrimSpace(input)
	if len(email) > emailMax || !utf8.ValidString(email) || strings.ContainsAny(email, "<>\x00") {
		return "", ErrEmail
	}
	a, err := mail.ParseAddress(email)
	if err != nil || a.Name != "" || a.Address != email {
		return "", ErrEmail
	}
	return email, nil
}

func validName(input string) (string, error) {
	name := strings.TrimSpace(input)
	if name == "" || len(name) > nameMax*utf8.UTFMax || !utf8.ValidString(name) ||
		utf8.RuneCountInString(name) > nameMax || strings.ContainsFunc(name, unicode.IsControl) {
		return "", ErrName
	}
	return name, nil
}
