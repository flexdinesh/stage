package store

import (
	"context"
	"strings"
	"time"

	"golang.org/x/text/cases"
)

type AccessMode string

const (
	AccessPersonal AccessMode = "personal"
	AccessPending  AccessMode = "setup_pending"
	AccessClaimed  AccessMode = "claimed"
)

type InstallationAccess struct {
	ID        string
	Mode      AccessMode
	ClaimedAt *time.Time
}

type AccountRole string

const (
	RoleAdmin  AccountRole = "admin"
	RoleMember AccountRole = "member"
)

type AccountStatus string

const (
	AccountActive   AccountStatus = "active"
	AccountDisabled AccountStatus = "disabled"
)

// Account is the sign-in record. Operational user lookups use User, which
// has no login address.
type Account struct {
	User
	Email           *string
	EmailVerifiedAt *time.Time
	Role            AccountRole
	Status          AccountStatus
}

type AccountInput struct {
	DisplayName string
	Email       string
}

// EmailKey is the unique login key on either engine. Store the trimmed
// submitted address separately for presentation.
func EmailKey(email string) string {
	return cases.Fold().String(strings.TrimSpace(email))
}

type SessionAudience string

const (
	SessionBrowser SessionAudience = "browser"
	SessionCLI     SessionAudience = "cli"
	SessionAgent   SessionAudience = "agent"
)

// TokenDigest is the SHA-256 digest of an opaque credential. The repository
// never receives the raw credential.
type TokenDigest [32]byte

type AuthSession struct {
	ID            int64
	UserID        int64
	Audience      SessionAudience
	CreatedAt     time.Time
	ExpiresAt     time.Time
	IdleExpiresAt *time.Time
}

type GrantPurpose string

const (
	GrantSetup         GrantPurpose = "setup"
	GrantInvite        GrantPurpose = "invite"
	GrantPasswordReset GrantPurpose = "password_reset"
)

// A nil issuer denotes the local installation owner. For an invitation,
// Email is the fixed recipient. A reset instead binds TargetUserID.
type AuthGrant struct {
	ID           int64
	Purpose      GrantPurpose
	IssuerUserID *int64
	Email        *string
	TargetUserID *int64
	CreatedAt    time.Time
	ExpiresAt    time.Time
	ConsumedAt   *time.Time
	RevokedAt    *time.Time
}

// AccessAudit records a committed access-management action. A nil actor is
// the local installation owner. Grant IDs remain meaningful after a copy
// discards the grants themselves.
type AccessAudit struct {
	Action       string
	ActorUserID  *int64
	TargetUserID *int64
	GrantID      *int64
	CreatedAt    time.Time
}

// Access is the transactional storage contract for installation and sign-in
// state. Take InstallationForUpdate before changing installation mode or
// making account changes that depend on shared invariants.
type Access interface {
	Installation(context.Context) (InstallationAccess, error)
	InstallationForUpdate(context.Context) (InstallationAccess, error)
	InitializeInstallation(context.Context, string, AccessMode) error
	MarkClaimed(context.Context, time.Time) error

	Account(context.Context, int64) (Account, error)
	AccountByEmail(context.Context, string) (Account, error)
	// ListAccounts includes disabled sign-in accounts, newest ID first.
	// Pass beforeID zero for the first page.
	ListAccounts(ctx context.Context, beforeID int64, limit int) ([]Account, error)
	ClaimOwner(context.Context, AccountInput) error
	InsertMember(context.Context, AccountInput) (int64, error)
	InsertLocalCredential(context.Context, int64, string) error
	LocalVerifier(context.Context, int64) (string, error)
	UpdateLocalVerifier(context.Context, int64, string) error
	UpdateDisplayName(context.Context, int64, string) error
	CountActiveAdmins(context.Context) (int, error)
	SetAccountRole(context.Context, int64, AccountRole) error
	DisableAccount(context.Context, int64) error

	InsertSession(context.Context, TokenDigest, AuthSession) (int64, error)
	// ActiveSession matches the exact audience and an active user. Both
	// deadlines must be in the future and the session must be unrevoked.
	ActiveSession(context.Context, TokenDigest, SessionAudience, time.Time) (AuthSession, error)
	// ExtendSessionIdle only extends an active idle deadline, capped at the
	// absolute expiry, and cannot revive an expired or revoked credential.
	ExtendSessionIdle(context.Context, int64, time.Time, time.Time) error
	RevokeSession(context.Context, int64, int64, time.Time) error
	RevokeUserSessions(context.Context, int64, time.Time) error
	RevokeUserAudience(context.Context, int64, SessionAudience, time.Time) error

	InsertGrant(context.Context, TokenDigest, AuthGrant) (int64, error)
	Grant(context.Context, TokenDigest, GrantPurpose) (AuthGrant, error)
	GrantByID(context.Context, int64) (AuthGrant, error)
	// OpenGrant selects the unconsumed, unrevoked row, including expired
	// rows. Use email for invitations and targetUserID for password resets.
	OpenGrant(ctx context.Context, purpose GrantPurpose, email string, targetUserID int64) (AuthGrant, error)
	// ListInvites includes invite history, newest ID first. Pass beforeID
	// zero for the first page.
	ListInvites(ctx context.Context, beforeID int64, limit int) ([]AuthGrant, error)
	// ConsumeGrant succeeds once and returns ErrNotFound for an unusable
	// grant. Call in the same write transaction as the operation it grants.
	ConsumeGrant(context.Context, TokenDigest, GrantPurpose, time.Time) (AuthGrant, error)
	RevokeGrant(context.Context, int64, time.Time) error

	InsertAccessAudit(context.Context, AccessAudit) error
	DiscardTransientAccess(context.Context) error
}
