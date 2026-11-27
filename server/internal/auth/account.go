package auth

import (
	"context"
	"errors"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

func readAdmin(ctx context.Context, r store.Access, actor int64) error {
	state, err := r.Installation(ctx)
	if err != nil {
		return err
	}
	if state.Mode != store.AccessClaimed {
		return ErrForbidden
	}
	a, err := activeAccount(ctx, r, actor, ErrForbidden)
	if err == nil && a.Role != store.RoleAdmin {
		return ErrForbidden
	}
	return err
}

func pageLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	return min(limit, 100)
}

func (s *Service) ListAccounts(ctx context.Context, actor, beforeID int64, limit int) ([]store.Account, error) {
	return withAccess(s.db.ReadAccess, ctx, func(r store.Access) ([]store.Account, error) {
		if err := readAdmin(ctx, r, actor); err != nil {
			return nil, err
		}
		return r.ListAccounts(ctx, beforeID, pageLimit(limit))
	})
}

func (s *Service) SetDisplayName(ctx context.Context, userID int64, name string) (store.Account, error) {
	name, err := validName(name)
	if err != nil {
		return store.Account{}, err
	}
	return withAccess(s.db.WriteAccess, ctx, func(r store.Access) (store.Account, error) {
		if err := lockedMode(ctx, r, store.AccessClaimed, ErrSession); err != nil {
			return store.Account{}, err
		}
		account, err := activeAccount(ctx, r, userID, ErrSession)
		if err != nil || account.DisplayName == name {
			return account, err
		}
		if err := r.UpdateDisplayName(ctx, userID, name); err != nil {
			return store.Account{}, err
		}
		account.DisplayName = name
		return account, nil
	})
}

func ensureOtherAdmin(ctx context.Context, r store.Access, account store.Account) error {
	if account.Role != store.RoleAdmin {
		return nil
	}
	count, err := r.CountActiveAdmins(ctx)
	if err == nil && count <= 1 {
		return ErrLastAdmin
	}
	return err
}

func (s *Service) SetRole(ctx context.Context, actor, target int64, role store.AccountRole) error {
	if role != store.RoleAdmin && role != store.RoleMember {
		return ErrRole
	}
	return s.db.WriteAccess(ctx, func(r store.Access) error {
		if err := checkAdmin(ctx, r, actor); err != nil {
			return err
		}
		account, err := activeAccount(ctx, r, target, ErrAccountUnavailable)
		if err != nil || account.Role == role {
			return err
		}
		if err := ensureOtherAdmin(ctx, r, account); err != nil {
			return err
		}
		if err := r.SetAccountRole(ctx, target, role); err != nil {
			return err
		}
		now := s.now()
		if err := revokeOpen(ctx, r, store.GrantPasswordReset, target, now); err != nil {
			return err
		}
		action := "role_promoted"
		if role == store.RoleMember {
			action = "role_demoted"
		}
		return audit(ctx, r, now, action, &actor, &target, nil)
	})
}

func (s *Service) Disable(ctx context.Context, actor, target int64) error {
	return s.db.WriteAccess(ctx, func(r store.Access) error {
		if err := checkAdmin(ctx, r, actor); err != nil {
			return err
		}
		account, err := activeAccount(ctx, r, target, ErrAccountUnavailable)
		if err != nil {
			return err
		}
		if err := ensureOtherAdmin(ctx, r, account); err != nil {
			return err
		}
		if err := r.DisableAccount(ctx, target); err != nil {
			return err
		}
		now := s.now()
		if err := r.RevokeUserSessions(ctx, target, now); err != nil {
			return err
		}
		if err := revokeOpen(ctx, r, store.GrantPasswordReset, target, now); err != nil {
			return err
		}
		return audit(ctx, r, now, "account_disabled", &actor, &target, nil)
	})
}

func (s *Service) ChangePassword(ctx context.Context, userID int64, current, next string) error {
	if err := validatePassword(next); err != nil {
		return err
	}
	previous, err := withAccess(s.db.ReadAccess, ctx, func(r store.Access) (string, error) {
		if _, err := activeAccount(ctx, r, userID, ErrSession); err != nil {
			return "", err
		}
		return r.LocalVerifier(ctx, userID)
	})
	if err != nil {
		return err
	}
	match, err := s.verifyPassword(ctx, previous, current)
	if err != nil {
		return err
	}
	if !match {
		return ErrCurrentPassword
	}
	verifier, err := s.hashPassword(ctx, next)
	if err != nil {
		return err
	}
	return s.db.WriteAccess(ctx, func(r store.Access) error {
		if err := lockedMode(ctx, r, store.AccessClaimed, ErrSession); err != nil {
			return err
		}
		if _, err := activeAccount(ctx, r, userID, ErrSession); err != nil {
			return err
		}
		if err := verifierStillCurrent(ctx, r, userID, previous, ErrCurrentPassword); err != nil {
			return err
		}
		return replacePassword(ctx, r, userID, verifier, "password_changed", nil, s.now())
	})
}

func (s *Service) ResetMember(ctx context.Context, actor, target int64) (IssuedGrant, error) {
	return withAccess(s.db.WriteAccess, ctx, func(r store.Access) (IssuedGrant, error) {
		if err := checkAdmin(ctx, r, actor); err != nil {
			return IssuedGrant{}, err
		}
		account, err := activeAccount(ctx, r, target, ErrAccountUnavailable)
		if err != nil {
			return IssuedGrant{}, err
		}
		if account.Role != store.RoleMember {
			return IssuedGrant{}, ErrAccountUnavailable
		}
		return s.resetGrant(ctx, r, &actor, target)
	})
}

func (s *Service) RecoverAdmin(ctx context.Context, email string) (Link, error) {
	email, err := validEmail(email)
	if err != nil {
		return Link{}, err
	}
	return withAccess(s.db.WriteAccess, ctx, func(r store.Access) (Link, error) {
		if err := lockedMode(ctx, r, store.AccessClaimed, ErrAccountUnavailable); err != nil {
			return Link{}, err
		}
		account, err := r.AccountByEmail(ctx, email)
		if errors.Is(err, store.ErrNotFound) || err == nil && (account.Status != store.AccountActive || account.Role != store.RoleAdmin) {
			return Link{}, ErrAccountUnavailable
		}
		if err != nil {
			return Link{}, err
		}
		issued, err := s.resetGrant(ctx, r, nil, account.ID)
		return issued.Link, err
	})
}

func (s *Service) resetGrant(ctx context.Context, r store.Access, actor *int64, target int64) (IssuedGrant, error) {
	now := s.now()
	if err := revokeOpen(ctx, r, store.GrantPasswordReset, target, now); err != nil {
		return IssuedGrant{}, err
	}
	return s.issue(ctx, r, store.AuthGrant{Purpose: store.GrantPasswordReset, IssuerUserID: actor, TargetUserID: &target,
		CreatedAt: now, ExpiresAt: now.Add(shortGrant)}, "password_reset_issued")
}

func (s *Service) RedeemReset(ctx context.Context, token, password string) error {
	digest, _, err := s.inspect(ctx, token, store.GrantPasswordReset)
	if err != nil {
		return err
	}
	verifier, err := s.hashPassword(ctx, password)
	if err != nil {
		return err
	}
	return s.db.WriteAccess(ctx, func(r store.Access) error {
		if err := lockedMode(ctx, r, store.AccessClaimed, ErrReset); err != nil {
			return err
		}
		now := s.now()
		grant, err := consume(ctx, r, digest, store.GrantPasswordReset, now)
		if err != nil {
			return err
		}
		account, err := activeAccount(ctx, r, *grant.TargetUserID, ErrReset)
		if err != nil {
			return err
		}
		if (grant.IssuerUserID == nil && account.Role != store.RoleAdmin) ||
			(grant.IssuerUserID != nil && account.Role != store.RoleMember) {
			return ErrReset
		}
		return replacePassword(ctx, r, account.ID, verifier, "password_reset", &grant.ID, now)
	})
}

func replacePassword(ctx context.Context, r store.Access, target int64, verifier, action string, grant *int64, now time.Time) error {
	if err := r.UpdateLocalVerifier(ctx, target, verifier); err != nil {
		return err
	}
	if err := r.RevokeUserSessions(ctx, target, now); err != nil {
		return err
	}
	if err := revokeOpen(ctx, r, store.GrantPasswordReset, target, now); err != nil {
		return err
	}
	return audit(ctx, r, now, action, &target, &target, grant)
}
