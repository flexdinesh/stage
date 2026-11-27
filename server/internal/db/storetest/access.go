package storetest

import (
	"errors"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

func init() {
	register("SharedAccess", caseSharedAccess)
}

func caseSharedAccess(t *testing.T, open Factory) {
	e := open(t)
	s := db.NewStore(e)
	ctx := t.Context()
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	yes, no := true, false
	personal, err := s.ConfigureAccess(ctx, &no)
	if err != nil || personal.Mode != store.AccessPersonal || personal.ID == "" {
		t.Fatalf("personal state = %+v, %v", personal, err)
	}
	if err := s.ReadAccess(ctx, func(r store.Access) error {
		a, err := r.Account(ctx, db.DefaultUser)
		if err != nil || a.Role != store.RoleAdmin || a.Status != store.AccountActive || a.Email != nil {
			t.Fatalf("implicit admin = %+v, %v", a, err)
		}
		_, err = r.LocalVerifier(ctx, db.DefaultUser)
		return err
	}); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("personal verifier lookup = %v", err)
	}

	for _, err := range race(4, func(int) error {
		_, err := s.ConfigureAccess(ctx, &yes)
		return err
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	s = db.NewStore(e)
	pending, err := s.ConfigureAccess(ctx, nil)
	if err != nil || pending.Mode != store.AccessPending || pending.ID != personal.ID {
		t.Fatalf("sticky pending = %+v, %v", pending, err)
	}
	if _, err := s.ConfigureAccess(ctx, &no); !errors.Is(err, db.ErrSharedAccessRequired) {
		t.Fatalf("pending to personal = %v", err)
	}
	setup := store.TokenDigest{1}
	owner := store.AccountInput{DisplayName: " Owner ", Email: "  Name+Ops@Example.test "}
	verifier := "test verifier"
	if _, err := s.ClaimInstallation(ctx, setup, owner, verifier, now); !errors.Is(err, db.ErrSetupUnavailable) {
		t.Fatalf("claim without grant = %v", err)
	}
	if err := s.WriteAccess(ctx, func(r store.Access) error {
		_, err := r.InsertGrant(ctx, setup, store.AuthGrant{Purpose: store.GrantSetup, CreatedAt: now, ExpiresAt: now.Add(time.Minute)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteAccess(ctx, func(r store.Access) error {
		_, err := r.ConsumeGrant(ctx, setup, store.GrantPasswordReset, now)
		return err
	}); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("consume wrong purpose = %v", err)
	}
	if _, err := s.ClaimInstallation(ctx, setup, owner, verifier, now.Add(time.Minute)); !errors.Is(err, db.ErrSetupUnavailable) {
		t.Fatalf("claim expired grant = %v", err)
	}
	if err := s.InvalidateRestoredAccess(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimInstallation(ctx, setup, owner, verifier, now); !errors.Is(err, db.ErrSetupUnavailable) {
		t.Fatalf("copied pending grant survived restore = %v", err)
	}
	setup = store.TokenDigest{5}
	if err := s.WriteAccess(ctx, func(r store.Access) error {
		_, err := r.InsertGrant(ctx, setup, store.AuthGrant{Purpose: store.GrantSetup, CreatedAt: now, ExpiresAt: now.Add(time.Minute)})
		return err
	}); err != nil {
		t.Fatalf("reissue after restoring pending installation: %v", err)
	}
	winners := 0
	for _, err := range race(8, func(int) error {
		_, err := s.ClaimInstallation(ctx, setup, owner, verifier, now)
		return err
	}) {
		switch {
		case err == nil:
			winners++
		case !errors.Is(err, db.ErrSetupUnavailable):
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d simultaneous claims succeeded", winners)
	}
	claimed, err := s.ConfigureAccess(ctx, nil)
	if err != nil || claimed.Mode != store.AccessClaimed || claimed.ID != personal.ID || claimed.ClaimedAt == nil || !claimed.ClaimedAt.Equal(now) {
		t.Fatalf("sticky claimed = %+v, %v", claimed, err)
	}
	if _, err := s.ConfigureAccess(ctx, &no); !errors.Is(err, db.ErrSharedAccessRequired) {
		t.Fatalf("claimed to personal = %v", err)
	}
	if err := s.ReadAccess(ctx, func(r store.Access) error {
		a, err := r.AccountByEmail(ctx, "\tNAME+ops@EXAMPLE.test ")
		if err != nil || a.ID != db.DefaultUser || a.DisplayName != "Owner" || a.Email == nil ||
			*a.Email != "Name+Ops@Example.test" || a.EmailVerifiedAt != nil || a.Role != store.RoleAdmin {
			t.Fatalf("claimed admin = %+v, %v", a, err)
		}
		v, err := r.LocalVerifier(ctx, a.ID)
		if err != nil || v != verifier {
			t.Fatal("claimed verifier missing or changed")
		}
		g, err := r.Grant(ctx, setup, store.GrantSetup)
		if err != nil || g.ConsumedAt == nil || g.RevokedAt != nil || g.IssuerUserID != nil {
			t.Fatalf("consumed setup = %+v, %v", g, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, err := range race(2, func(i int) error {
		return s.WriteAccess(ctx, func(r store.Access) error {
			email := []string{" Straße@Example.test ", "STRASSE@example.test"}[i]
			_, err := r.InsertMember(ctx, store.AccountInput{DisplayName: "Member", Email: email})
			return err
		})
	}) {
		if err == nil {
			winners++
		} else if !e.Errors().IsUniqueViolation(err) {
			t.Fatal(err)
		}
	}
	if winners != 2 {
		t.Fatalf("expected exactly one account for the canonical email, got %d", winners-1)
	}
	if err := s.WriteAccess(ctx, func(r store.Access) error {
		for _, email := range []string{"Name.Ops@Example.test", "Name@Example.test"} {
			if _, err := r.InsertMember(ctx, store.AccountInput{DisplayName: "Member", Email: email}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("dot/plus-distinct addresses: %v", err)
	}

	session := store.TokenDigest{2}
	invite := store.TokenDigest{3}
	revoked := store.TokenDigest{4}
	ownerID := db.DefaultUser
	target := " Invite@Example.test "
	idle := now.Add(time.Minute)
	absolute := now.Add(time.Hour)
	var sessionID int64
	if err := s.WriteAccess(ctx, func(r store.Access) error {
		var err error
		sessionID, err = r.InsertSession(ctx, session, store.AuthSession{UserID: ownerID, Audience: store.SessionBrowser,
			CreatedAt: now, ExpiresAt: absolute, IdleExpiresAt: &idle})
		if err != nil {
			return err
		}
		g := store.AuthGrant{Purpose: store.GrantInvite, IssuerUserID: &ownerID, Email: &target, CreatedAt: now, ExpiresAt: absolute}
		id, err := r.InsertGrant(ctx, revoked, g)
		if err != nil {
			return err
		}
		if err := r.RevokeGrant(ctx, id, now); err != nil {
			return err
		}
		_, err = r.InsertGrant(ctx, invite, g)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteAccess(ctx, func(r store.Access) error {
		if _, err := r.ConsumeGrant(ctx, revoked, store.GrantInvite, now); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("revoked invite consumption = %v", err)
		}
		if _, err := r.ActiveSession(ctx, session, store.SessionCLI, now); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("cross-audience session lookup = %v", err)
		}
		if _, err := r.ActiveSession(ctx, session, store.SessionBrowser, idle); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("idle-expired session lookup = %v", err)
		}
		if err := r.ExtendSessionIdle(ctx, sessionID, now, absolute.Add(time.Hour)); err != nil {
			return err
		}
		current, err := r.ActiveSession(ctx, session, store.SessionBrowser, idle)
		if err != nil || current.IdleExpiresAt == nil || !current.IdleExpiresAt.Equal(absolute) {
			t.Fatalf("extended session = %+v, %v", current, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.InvalidateRestoredAccess(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadAccess(ctx, func(r store.Access) error {
		if _, err := r.ActiveSession(ctx, session, store.SessionBrowser, now); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("restored session accepted = %v", err)
		}
		if _, err := r.Grant(ctx, invite, store.GrantInvite); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("restored grant accepted = %v", err)
		}
		a, err := r.Installation(ctx)
		if err != nil || a.ID != personal.ID || a.Mode != store.AccessClaimed {
			t.Fatalf("restored installation = %+v, %v", a, err)
		}
		v, err := r.LocalVerifier(ctx, ownerID)
		if err != nil || v != verifier {
			t.Fatal("restore removed or changed verifier")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
