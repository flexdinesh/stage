package auth

import (
	"context"
	"errors"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

func (s *Service) Invite(ctx context.Context, actor int64, email string) (IssuedGrant, error) {
	email, err := validEmail(email)
	if err != nil {
		return IssuedGrant{}, err
	}
	return withAccess(s.db.WriteAccess, ctx, func(r store.Access) (IssuedGrant, error) {
		if err := checkAdmin(ctx, r, actor); err != nil {
			return IssuedGrant{}, err
		}
		if err := emailAvailable(ctx, r, email); err != nil {
			return IssuedGrant{}, err
		}
		_, err := r.OpenGrant(ctx, store.GrantInvite, email, 0)
		if err == nil {
			return IssuedGrant{}, ErrInviteExists
		}
		if !errors.Is(err, store.ErrNotFound) {
			return IssuedGrant{}, err
		}
		return s.issueInvite(ctx, r, actor, email, s.now(), "invite_issued")
	})
}

func (s *Service) issueInvite(ctx context.Context, r store.Access, actor int64, email string, now time.Time, action string) (IssuedGrant, error) {
	return s.issue(ctx, r, store.AuthGrant{Purpose: store.GrantInvite, IssuerUserID: &actor, Email: &email,
		CreatedAt: now, ExpiresAt: now.Add(inviteLifetime)}, action)
}

func emailAvailable(ctx context.Context, r store.Access, email string) error {
	_, err := r.AccountByEmail(ctx, email)
	if err == nil {
		return ErrAccountExists
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}

func (s *Service) ReissueInvite(ctx context.Context, actor, grantID int64) (IssuedGrant, error) {
	return withAccess(s.db.WriteAccess, ctx, func(r store.Access) (IssuedGrant, error) {
		if err := checkAdmin(ctx, r, actor); err != nil {
			return IssuedGrant{}, err
		}
		old, err := openInvite(ctx, r, grantID)
		if err != nil {
			return IssuedGrant{}, err
		}
		if err := emailAvailable(ctx, r, *old.Email); err != nil {
			return IssuedGrant{}, err
		}
		now := s.now()
		if err := r.RevokeGrant(ctx, old.ID, now); err != nil {
			return IssuedGrant{}, err
		}
		return s.issueInvite(ctx, r, actor, *old.Email, now, "invite_reissued")
	})
}

func (s *Service) RevokeInvite(ctx context.Context, actor, grantID int64) error {
	return s.db.WriteAccess(ctx, func(r store.Access) error {
		if err := checkAdmin(ctx, r, actor); err != nil {
			return err
		}
		if _, err := openInvite(ctx, r, grantID); err != nil {
			return err
		}
		now := s.now()
		if err := r.RevokeGrant(ctx, grantID, now); err != nil {
			return err
		}
		return audit(ctx, r, now, "invite_revoked", &actor, nil, &grantID)
	})
}

func openInvite(ctx context.Context, r store.Access, grantID int64) (store.AuthGrant, error) {
	grant, err := r.GrantByID(ctx, grantID)
	if errors.Is(err, store.ErrNotFound) || err == nil &&
		(grant.Purpose != store.GrantInvite || grant.Email == nil || grant.ConsumedAt != nil || grant.RevokedAt != nil) {
		return store.AuthGrant{}, ErrInvite
	}
	return grant, err
}

func (s *Service) RedeemInvite(ctx context.Context, token, name, password string) (SignInResult, error) {
	digest, _, err := s.inspect(ctx, token, store.GrantInvite)
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
		if err := lockedMode(ctx, r, store.AccessClaimed, ErrInvite); err != nil {
			return SignInResult{}, err
		}
		now := s.now()
		grant, err := consume(ctx, r, digest, store.GrantInvite, now)
		if err != nil {
			return SignInResult{}, err
		}
		if err := emailAvailable(ctx, r, *grant.Email); err != nil {
			if errors.Is(err, ErrAccountExists) {
				err = ErrInvite
			}
			return SignInResult{}, err
		}
		id, err := r.InsertMember(ctx, store.AccountInput{DisplayName: name, Email: *grant.Email})
		if err != nil {
			return SignInResult{}, err
		}
		if err := r.InsertLocalCredential(ctx, id, verifier); err != nil {
			return SignInResult{}, err
		}
		if err := audit(ctx, r, now, "invite_redeemed", &id, &id, &grant.ID); err != nil {
			return SignInResult{}, err
		}
		account, err := r.Account(ctx, id)
		if err != nil {
			return SignInResult{}, err
		}
		return insertSession(ctx, r, account, store.SessionBrowser, now)
	})
}

type Invitation struct {
	store.AuthGrant
	Status string
}

func (s *Service) ListInvites(ctx context.Context, actor, beforeID int64, limit int) ([]Invitation, error) {
	return withAccess(s.db.ReadAccess, ctx, func(r store.Access) ([]Invitation, error) {
		if err := readAdmin(ctx, r, actor); err != nil {
			return nil, err
		}
		grants, err := r.ListInvites(ctx, beforeID, pageLimit(limit))
		if err != nil {
			return nil, err
		}
		now := s.now()
		invitations := make([]Invitation, 0, len(grants))
		for _, g := range grants {
			state := "pending"
			switch {
			case g.ConsumedAt != nil:
				state = "used"
			case g.RevokedAt != nil:
				state = "revoked"
			case !now.Before(g.ExpiresAt):
				state = "expired"
			}
			invitations = append(invitations, Invitation{g, state})
		}
		return invitations, nil
	})
}
