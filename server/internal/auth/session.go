package auth

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Identity is the current account and the matched session. Role and status
// are loaded on each authentication rather than embedded in the credential.
type Identity struct {
	Account store.Account
	Session store.AuthSession
}

type SignInResult struct {
	Identity
	Token Secret
}

func insertSession(ctx context.Context, r store.Access, account store.Account, audience store.SessionAudience, now time.Time) (SignInResult, error) {
	token, digest := newSecret()
	session := store.AuthSession{UserID: account.ID, Audience: audience, CreatedAt: now, ExpiresAt: now.Add(sessionLifetime)}
	if audience == store.SessionBrowser {
		idle := now.Add(browserIdle)
		session.IdleExpiresAt = &idle
	}
	id, err := r.InsertSession(ctx, digest, session)
	if err != nil {
		return SignInResult{}, err
	}
	session.ID = id
	return SignInResult{Identity{account, session}, token}, nil
}

type loginRecord struct {
	account  store.Account
	verifier string
}

func (s *Service) SignIn(ctx context.Context, email, password string, peer netip.Addr, audience store.SessionAudience) (SignInResult, error) {
	if (audience != store.SessionBrowser && audience != store.SessionCLI) || !s.limiter.allow(email, peer, s.now()) {
		return SignInResult{}, ErrSignIn
	}
	var record loginRecord
	if address, err := validEmail(email); err == nil {
		record, err = withAccess(s.db.ReadAccess, ctx, func(r store.Access) (loginRecord, error) {
			account, err := r.AccountByEmail(ctx, address)
			if err != nil || account.Status != store.AccountActive {
				return loginRecord{}, err
			}
			verifier, err := r.LocalVerifier(ctx, account.ID)
			return loginRecord{account, verifier}, err
		})
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return SignInResult{}, err
		}
	}
	match, err := s.verifyPassword(ctx, record.verifier, password)
	if err != nil && !errors.Is(err, ErrBusy) {
		return SignInResult{}, err
	}
	if !match {
		return SignInResult{}, ErrSignIn
	}
	return withAccess(s.db.WriteAccess, ctx, func(r store.Access) (SignInResult, error) {
		if err := lockedMode(ctx, r, store.AccessClaimed, ErrSignIn); err != nil {
			return SignInResult{}, err
		}
		account, err := activeAccount(ctx, r, record.account.ID, ErrSignIn)
		if err != nil {
			return SignInResult{}, err
		}
		if err := verifierStillCurrent(ctx, r, account.ID, record.verifier, ErrSignIn); err != nil {
			return SignInResult{}, err
		}
		return insertSession(ctx, r, account, audience, s.now())
	})
}

func verifierStillCurrent(ctx context.Context, r store.Access, userID int64, verifier string, mismatch error) error {
	current, err := r.LocalVerifier(ctx, userID)
	if errors.Is(err, store.ErrNotFound) || err == nil && current != verifier {
		return mismatch
	}
	return err
}

// Authenticate can extend browser idle time on a real user request. Passive
// stream checks use touch=false so they cannot keep a credential alive.
func (s *Service) Authenticate(ctx context.Context, token string, audience store.SessionAudience, touch bool) (Identity, error) {
	digest, ok := tokenDigest(token)
	if !ok {
		return Identity{}, ErrSession
	}
	now := s.now()
	transact := s.db.ReadAccess
	if touch && audience == store.SessionBrowser {
		transact = s.db.WriteAccess
	}
	return withAccess(transact, ctx, func(r store.Access) (Identity, error) {
		session, err := r.ActiveSession(ctx, digest, audience, now)
		if errors.Is(err, store.ErrNotFound) {
			return Identity{}, ErrSession
		}
		if err != nil {
			return Identity{}, err
		}
		if touch && session.IdleExpiresAt != nil && audience == store.SessionBrowser {
			deadline := now.Add(browserIdle)
			if deadline.After(session.ExpiresAt) {
				deadline = session.ExpiresAt
			}
			if deadline.After(*session.IdleExpiresAt) {
				if err := r.ExtendSessionIdle(ctx, session.ID, now, deadline); err != nil {
					return Identity{}, err
				}
				session, err = r.ActiveSession(ctx, digest, audience, now)
				if errors.Is(err, store.ErrNotFound) {
					return Identity{}, ErrSession
				}
				if err != nil {
					return Identity{}, err
				}
			}
		}
		account, err := activeAccount(ctx, r, session.UserID, ErrSession)
		return Identity{account, session}, err
	})
}

func (s *Service) SignOut(ctx context.Context, token string, audience store.SessionAudience) error {
	digest, ok := tokenDigest(token)
	if !ok {
		return nil
	}
	return s.db.WriteAccess(ctx, func(r store.Access) error {
		now := s.now()
		session, err := r.ActiveSession(ctx, digest, audience, now)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return r.RevokeSession(ctx, session.ID, session.UserID, now)
	})
}

// RotateAgent replaces this account's agent credential without affecting
// human sessions. The active-account check and rotation share a transaction.
func (s *Service) RotateAgent(ctx context.Context, issuer string, audience store.SessionAudience) (SignInResult, error) {
	return withAccess(s.db.WriteAccess, ctx, func(r store.Access) (SignInResult, error) {
		account, err := s.lockAgentAccount(ctx, r, issuer, audience)
		if err != nil {
			return SignInResult{}, err
		}
		userID := account.ID
		now := s.now()
		if err := r.RevokeUserAudience(ctx, userID, store.SessionAgent, now); err != nil {
			return SignInResult{}, err
		}
		result, err := insertSession(ctx, r, account, store.SessionAgent, now)
		if err != nil {
			return SignInResult{}, err
		}
		return result, audit(ctx, r, now, "agent_credential_rotated", &userID, &userID, nil)
	})
}

func (s *Service) RevokeAgent(ctx context.Context, issuer string, audience store.SessionAudience) error {
	return s.db.WriteAccess(ctx, func(r store.Access) error {
		account, err := s.lockAgentAccount(ctx, r, issuer, audience)
		if err != nil {
			return err
		}
		userID := account.ID
		now := s.now()
		if err := r.RevokeUserAudience(ctx, userID, store.SessionAgent, now); err != nil {
			return err
		}
		return audit(ctx, r, now, "agent_credential_revoked", &userID, &userID, nil)
	})
}

func (s *Service) lockAgentAccount(ctx context.Context, r store.Access, issuer string, audience store.SessionAudience) (store.Account, error) {
	digest, ok := tokenDigest(issuer)
	if !ok || audience != store.SessionBrowser && audience != store.SessionCLI {
		return store.Account{}, ErrSession
	}
	if err := lockedMode(ctx, r, store.AccessClaimed, ErrForbidden); err != nil {
		return store.Account{}, err
	}
	session, err := r.ActiveSession(ctx, digest, audience, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return store.Account{}, ErrSession
	}
	if err != nil {
		return store.Account{}, err
	}
	return activeAccount(ctx, r, session.UserID, ErrSession)
}
