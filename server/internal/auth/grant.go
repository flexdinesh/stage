package auth

import (
	"context"
	"errors"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

type IssuedGrant struct {
	Grant store.AuthGrant
	Link  Link
}

func (s *Service) issue(ctx context.Context, r store.Access, grant store.AuthGrant, action string) (IssuedGrant, error) {
	if s.origin == "" {
		return IssuedGrant{}, ErrPublicOrigin
	}
	token, digest := newSecret()
	id, err := r.InsertGrant(ctx, digest, grant)
	if err != nil {
		return IssuedGrant{}, err
	}
	if err := audit(ctx, r, grant.CreatedAt, action, grant.IssuerUserID, grant.TargetUserID, &id); err != nil {
		return IssuedGrant{}, err
	}
	grant.ID = id
	return IssuedGrant{grant, Link{Secret{s.origin + grantPath(grant.Purpose) + "#token=" + token.Reveal()}, grant.ExpiresAt}}, nil
}

func revokeOpen(ctx context.Context, r store.Access, purpose store.GrantPurpose, target int64, now time.Time) error {
	previous, err := r.OpenGrant(ctx, purpose, "", target)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.RevokeGrant(ctx, previous.ID, now)
}

type startup struct {
	state store.InstallationAccess
	link  Link
}

// Start configures sticky installation state and issues a bootstrap link
// only on the committed personal-to-pending transition. A pending restart
// leaves any outstanding grant in place until the owner explicitly reissues.
func (s *Service) Start(ctx context.Context, intent *bool) (store.InstallationAccess, Link, error) {
	result, err := withAccess(s.db.WriteAccess, ctx, func(r store.Access) (startup, error) {
		previous, err := r.InstallationForUpdate(ctx)
		if err != nil {
			return startup{}, err
		}
		state, err := db.ConfigureAccessTx(ctx, r, intent)
		if err != nil {
			return startup{}, err
		}
		out := startup{state: state}
		if state.Mode != store.AccessPersonal && s.origin == "" {
			return startup{}, ErrPublicOrigin
		}
		if previous.Mode == store.AccessPersonal && state.Mode == store.AccessPending {
			now := s.now()
			issued, err := s.issue(ctx, r, store.AuthGrant{Purpose: store.GrantSetup, CreatedAt: now, ExpiresAt: now.Add(shortGrant)}, "setup_issued")
			if err != nil {
				return startup{}, err
			}
			out.link = issued.Link
		}
		return out, nil
	})
	return result.state, result.link, err
}

// ReissueSetup and RecoverAdmin are for the OS-private owner control path.
func (s *Service) ReissueSetup(ctx context.Context) (Link, error) {
	return withAccess(s.db.WriteAccess, ctx, func(r store.Access) (Link, error) {
		if err := lockedMode(ctx, r, store.AccessPending, ErrPending); err != nil {
			return Link{}, err
		}
		now := s.now()
		if err := revokeOpen(ctx, r, store.GrantSetup, 0, now); err != nil {
			return Link{}, err
		}
		issued, err := s.issue(ctx, r, store.AuthGrant{Purpose: store.GrantSetup, CreatedAt: now, ExpiresAt: now.Add(shortGrant)}, "setup_reissued")
		return issued.Link, err
	})
}

type GrantInfo struct {
	Email     *string
	ExpiresAt time.Time
}

// InspectGrant can populate a form after the client removes the fragment and
// supplies the secret in a request body. Inspection never consumes a grant.
func (s *Service) InspectGrant(ctx context.Context, token string, purpose store.GrantPurpose) (GrantInfo, error) {
	_, grant, err := s.inspect(ctx, token, purpose)
	return GrantInfo{grant.Email, grant.ExpiresAt}, err
}

func invalidGrant(purpose store.GrantPurpose) error {
	switch purpose {
	case store.GrantSetup:
		return ErrPending
	case store.GrantInvite:
		return ErrInvite
	case store.GrantPasswordReset:
		return ErrReset
	}
	return ErrForbidden
}

func (s *Service) inspect(ctx context.Context, token string, purpose store.GrantPurpose) (store.TokenDigest, store.AuthGrant, error) {
	digest, ok := tokenDigest(token)
	if !ok || grantPath(purpose) == "" {
		return store.TokenDigest{}, store.AuthGrant{}, invalidGrant(purpose)
	}
	grant, err := withAccess(s.db.ReadAccess, ctx, func(r store.Access) (store.AuthGrant, error) {
		grant, err := r.Grant(ctx, digest, purpose)
		now := s.now()
		if errors.Is(err, store.ErrNotFound) || err == nil && (grant.ConsumedAt != nil || grant.RevokedAt != nil ||
			now.Before(grant.CreatedAt) || !now.Before(grant.ExpiresAt)) {
			return store.AuthGrant{}, invalidGrant(purpose)
		}
		return grant, err
	})
	return digest, grant, err
}

func consume(ctx context.Context, r store.Access, digest store.TokenDigest, purpose store.GrantPurpose, now time.Time) (store.AuthGrant, error) {
	grant, err := r.ConsumeGrant(ctx, digest, purpose, now)
	if errors.Is(err, store.ErrNotFound) {
		return store.AuthGrant{}, invalidGrant(purpose)
	}
	return grant, err
}

func (s *Service) ClaimSetup(ctx context.Context, token, email, name, password string) (SignInResult, error) {
	digest, grant, err := s.inspect(ctx, token, store.GrantSetup)
	if err != nil {
		return SignInResult{}, err
	}
	email, err = validEmail(email)
	if err != nil {
		return SignInResult{}, err
	}
	name, err = validName(name)
	if err != nil {
		return SignInResult{}, err
	}
	verifier, err := s.hashPassword(ctx, password)
	if err != nil {
		return SignInResult{}, err
	}
	return withAccess(s.db.WriteAccess, ctx, func(r store.Access) (SignInResult, error) {
		if err := lockedMode(ctx, r, store.AccessPending, ErrPending); err != nil {
			return SignInResult{}, err
		}
		now := s.now()
		account, err := db.ClaimInstallationTx(ctx, r, digest, store.AccountInput{Email: email, DisplayName: name}, verifier, now)
		if errors.Is(err, db.ErrSetupUnavailable) {
			return SignInResult{}, ErrPending
		}
		if err != nil {
			return SignInResult{}, err
		}
		if err := audit(ctx, r, now, "setup_claimed", &account.ID, &account.ID, &grant.ID); err != nil {
			return SignInResult{}, err
		}
		return insertSession(ctx, r, account, store.SessionBrowser, now)
	})
}
