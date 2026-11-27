package auth

import (
	"context"
	"errors"
	"net/netip"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

const (
	testOrigin  = "https://stage.example.test"
	firstPass   = "Cargo trains cross at twelve"
	secondPass  = "The new passphrase has a sail"
	thirdPass   = "Cider season ends on Tuesday"
	ownerEmail  = "Admin+work@Example.test"
	memberEmail = "Member+ops@Example.test"
)

var testPeer = netip.MustParseAddr("192.0.2.44")

type fixture struct {
	*Service
	database *db.Store
	nowAt    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{database: db.NewStore(dbtest.FreshEngine(t)), nowAt: time.Date(2026, 10, 6, 13, 0, 0, 0, time.UTC)}
	f.restart()
	return f
}

func (f *fixture) restart() {
	f.Service = New(f.database, testOrigin, func() time.Time { return f.nowAt })
}

func require[T any](t *testing.T) func(T, error) T {
	return func(value T, err error) T {
		t.Helper()
		checkErr(t, err, nil)
		return value
	}
}

func checkErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func linkToken(t *testing.T, link Link, purpose store.GrantPurpose) string {
	t.Helper()
	u := require[*url.URL](t)(url.Parse(link.URL()))
	if u.Scheme+"://"+u.Host != testOrigin || u.Path != grantPath(purpose) || u.RawQuery != "" {
		t.Fatal("grant URL has incorrect origin, path or query")
	}
	token := require[url.Values](t)(url.ParseQuery(u.Fragment)).Get("token")
	if _, ok := tokenDigest(token); !ok {
		t.Fatal("fragment lacks a 256-bit token")
	}
	return token
}

func startSetup(t *testing.T, f *fixture) Link {
	t.Helper()
	yes := true
	state, link, err := f.Start(t.Context(), &yes)
	checkErr(t, err, nil)
	if state.Mode != store.AccessPending || state.ID == "" || !link.ExpiresAt.Equal(f.nowAt.Add(30*time.Minute)) {
		t.Fatalf("initial setup state = %v, link expiry = %v", state.Mode, link.ExpiresAt)
	}
	return link
}

func claim(t *testing.T, f *fixture) SignInResult {
	t.Helper()
	token := linkToken(t, startSetup(t, f), store.GrantSetup)
	return require[SignInResult](t)(f.ClaimSetup(t.Context(), token, ownerEmail, "Owner", firstPass))
}

func inviteMember(t *testing.T, f *fixture) SignInResult {
	t.Helper()
	invite := require[IssuedGrant](t)(f.Invite(t.Context(), db.DefaultUser, memberEmail))
	return require[SignInResult](t)(f.RedeemInvite(t.Context(), linkToken(t, invite.Link, store.GrantInvite), "Member", firstPass))
}

func raceTwo[T any](fn func(int) (T, error)) ([2]T, [2]error) {
	var results [2]T
	var errs [2]error
	var wg sync.WaitGroup
	gate := make(chan struct{})
	for i := range results {
		wg.Go(func() {
			<-gate
			results[i], errs[i] = fn(i)
		})
	}
	close(gate)
	wg.Wait()
	return results, errs
}

func winner(t *testing.T, errs [2]error, loser error) int {
	t.Helper()
	if errs[0] == nil {
		checkErr(t, errs[1], loser)
		return 0
	}
	checkErr(t, errs[0], loser)
	checkErr(t, errs[1], nil)
	return 1
}

func TestSetupRestartsRotationExpiryAndOneClaim(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	yes := true
	_, _, err := New(f.database, "http://stage.example.test?input=private", nil).Start(ctx, &yes)
	checkErr(t, err, ErrPublicOrigin)
	first := startSetup(t, f)
	oldToken := linkToken(t, first, store.GrantSetup)
	f.restart()
	state, again, err := f.Start(ctx, nil)
	checkErr(t, err, nil)
	if state.Mode != store.AccessPending || again.URL() != "" {
		t.Fatal("pending restart printed another setup link")
	}
	_, err = f.InspectGrant(ctx, oldToken, store.GrantSetup)
	checkErr(t, err, nil)
	next := require[Link](t)(f.ReissueSetup(ctx))
	_, err = f.ClaimSetup(ctx, oldToken, ownerEmail, "Owner", firstPass)
	checkErr(t, err, ErrPending)
	f.nowAt = next.ExpiresAt
	_, err = f.InspectGrant(ctx, linkToken(t, next, store.GrantSetup), store.GrantSetup)
	checkErr(t, err, ErrPending)
	third := require[Link](t)(f.ReissueSetup(ctx))
	token := linkToken(t, third, store.GrantSetup)
	_, err = f.ClaimSetup(ctx, token, ownerEmail, "", firstPass)
	checkErr(t, err, ErrName)
	_, err = f.InspectGrant(ctx, token, store.GrantInvite)
	checkErr(t, err, ErrInvite)
	results, errs := raceTwo(func(int) (SignInResult, error) {
		return f.ClaimSetup(ctx, token, "  "+ownerEmail+"  ", "Build Owner", firstPass)
	})
	result := results[winner(t, errs, ErrPending)]
	if result.Account.ID != db.DefaultUser || result.Account.Role != store.RoleAdmin ||
		result.Account.Email == nil || *result.Account.Email != ownerEmail || result.Account.DisplayName != "Build Owner" {
		t.Fatal("setup did not claim the existing admin identity")
	}
	f.restart()
	state, again, err = f.Start(ctx, nil)
	checkErr(t, err, nil)
	if state.Mode != store.AccessClaimed || again.URL() != "" {
		t.Fatal("claimed restart changed mode or issued another link")
	}
	identity := require[Identity](t)(f.Authenticate(ctx, result.Token.Reveal(), store.SessionBrowser, false))
	if identity.Session.ID != result.Session.ID {
		t.Fatal("setup session did not survive restart")
	}
	_, err = f.ReissueSetup(ctx)
	checkErr(t, err, ErrPending)
	_, err = f.ClaimSetup(ctx, token, ownerEmail, "Owner", firstPass)
	checkErr(t, err, ErrPending)
}

var errAuditWrite = errors.New("test audit write failure")

type auditFaultEngine struct {
	store.Engine
	fail bool
}

func (e *auditFaultEngine) Write(ctx context.Context, fn func(store.Tx) error) error {
	return e.Engine.Write(ctx, func(tx store.Tx) error { return fn(auditFaultTx{tx, e.fail}) })
}

type auditFaultTx struct {
	store.Tx
	fail bool
}

func (tx auditFaultTx) Access() store.Access { return auditFaultAccess{tx.Tx.Access(), tx.fail} }

type auditFaultAccess struct {
	store.Access
	fail bool
}

func (r auditFaultAccess) InsertAccessAudit(ctx context.Context, a store.AccessAudit) error {
	if a.Action == "setup_claimed" {
		if a.ActorUserID == nil || *a.ActorUserID != db.DefaultUser || a.TargetUserID == nil || *a.TargetUserID != db.DefaultUser || a.GrantID == nil {
			return errors.New("setup claim lacks audit attribution")
		}
		if r.fail {
			return errAuditWrite
		}
	}
	return r.Access.InsertAccessAudit(ctx, a)
}

func TestAuditFailureRollsBackSetupAndLeavesGrantUsable(t *testing.T) {
	engine := &auditFaultEngine{Engine: dbtest.FreshEngine(t)}
	f := &fixture{database: db.NewStore(engine), nowAt: time.Date(2026, 10, 6, 13, 0, 0, 0, time.UTC)}
	f.restart()
	ctx := t.Context()
	token := linkToken(t, startSetup(t, f), store.GrantSetup)
	engine.fail = true
	_, err := f.ClaimSetup(ctx, token, ownerEmail, "Owner", firstPass)
	checkErr(t, err, errAuditWrite)
	state, _, err := f.Start(ctx, nil)
	checkErr(t, err, nil)
	if state.Mode != store.AccessPending {
		t.Fatal("setup committed after audit failure")
	}
	engine.fail = false
	_ = require[SignInResult](t)(f.ClaimSetup(ctx, token, ownerEmail, "Owner", firstPass))
}

func TestInvitationFixedAddressRotationAndOneRedemption(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	admin := claim(t, f).Account.ID
	_, err := f.Invite(ctx, admin, "one@example.test, two@example.test")
	checkErr(t, err, ErrEmail)
	first := require[IssuedGrant](t)(f.Invite(ctx, admin, memberEmail))
	if !first.Link.ExpiresAt.Equal(f.nowAt.AddDate(0, 0, 7)) {
		t.Fatal("invite expiry differs from seven days")
	}
	_, err = f.Invite(ctx, admin, " member+OPS@example.test ")
	checkErr(t, err, ErrInviteExists)
	oldToken := linkToken(t, first.Link, store.GrantInvite)
	f.nowAt = first.Link.ExpiresAt
	_, err = f.RedeemInvite(ctx, oldToken, "Member", firstPass)
	checkErr(t, err, ErrInvite)
	invites := require[[]Invitation](t)(f.ListInvites(ctx, admin, 0, 10))
	if len(invites) != 1 || invites[0].Status != "expired" {
		t.Fatal("expired invitation missing from history")
	}
	second := require[IssuedGrant](t)(f.ReissueInvite(ctx, admin, first.Grant.ID))
	checkErr(t, f.RevokeInvite(ctx, admin, second.Grant.ID), nil)
	_, err = f.RedeemInvite(ctx, linkToken(t, second.Link, store.GrantInvite), "Member", firstPass)
	checkErr(t, err, ErrInvite)
	third := require[IssuedGrant](t)(f.Invite(ctx, admin, memberEmail))
	token := linkToken(t, third.Link, store.GrantInvite)
	info := require[GrantInfo](t)(f.InspectGrant(ctx, token, store.GrantInvite))
	if info.Email == nil || *info.Email != memberEmail {
		t.Fatal("invite lost fixed address")
	}
	results, errs := raceTwo(func(int) (SignInResult, error) {
		return f.RedeemInvite(ctx, token, "Member", firstPass)
	})
	member := results[winner(t, errs, ErrInvite)].Account
	if member.Email == nil || *member.Email != memberEmail || member.Role != store.RoleMember || member.ID == admin || member.EmailVerifiedAt != nil {
		t.Fatal("redemption did not create a distinct member with the fixed address")
	}
	_, err = f.Invite(ctx, admin, "member+ops@EXAMPLE.TEST")
	checkErr(t, err, ErrAccountExists)
	_, err = f.Invite(ctx, member.ID, "someone@example.test")
	checkErr(t, err, ErrForbidden)
	invites = require[[]Invitation](t)(f.ListInvites(ctx, admin, 0, 10))
	if len(invites) != 3 || invites[0].Status != "used" || invites[1].Status != "revoked" || invites[2].Status != "revoked" {
		t.Fatal("invitation history lost redemption or revocation")
	}
}

func TestConcurrentDemotionAndDisablePreserveLastAdmin(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	owner := claim(t, f)
	member := inviteMember(t, f)
	checkErr(t, f.Disable(ctx, owner.Account.ID, owner.Account.ID), ErrLastAdmin)
	checkErr(t, f.SetRole(ctx, owner.Account.ID, member.Account.ID, store.RoleAdmin), nil)
	identity := require[Identity](t)(f.Authenticate(ctx, member.Token.Reveal(), store.SessionBrowser, false))
	if identity.Account.Role != store.RoleAdmin {
		t.Fatal("session role did not follow promotion")
	}
	_, errs := raceTwo(func(i int) (bool, error) {
		if i == 0 {
			return true, f.SetRole(ctx, owner.Account.ID, owner.Account.ID, store.RoleMember)
		}
		return true, f.Disable(ctx, member.Account.ID, member.Account.ID)
	})
	won := winner(t, errs, ErrLastAdmin)
	survivor := []int64{member.Account.ID, owner.Account.ID}[won]
	people := require[[]store.Account](t)(f.ListAccounts(ctx, survivor, 0, 10))
	if len(people) != 2 {
		t.Fatal("account history lost an ID")
	}
	admins := 0
	for _, p := range people {
		if p.Role == store.RoleAdmin && p.Status == store.AccountActive {
			admins++
		}
	}
	if admins != 1 {
		t.Fatalf("active admins = %d", admins)
	}
}

func rememberAgent(t *testing.T, f *fixture, id int64) Secret {
	t.Helper()
	token, digest := newSecret()
	checkErr(t, f.database.WriteAccess(t.Context(), func(r store.Access) error {
		_, err := r.InsertSession(t.Context(), digest, store.AuthSession{UserID: id, Audience: store.SessionAgent,
			CreatedAt: f.nowAt, ExpiresAt: f.nowAt.Add(sessionLifetime)})
		return err
	}), nil)
	return token
}

func signIn(ctx context.Context, f *fixture, email, password string) (SignInResult, error) {
	return f.SignIn(ctx, email, password, testPeer, store.SessionBrowser)
}

func TestAccountRecoveryAndRevocation(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	owner, member := claim(t, f), inviteMember(t, f)
	admin, id := owner.Account.ID, member.Account.ID
	oldAgent := rememberAgent(t, f, id)
	cli := require[SignInResult](t)(f.SignIn(ctx, "member+OPS@example.test", firstPass, testPeer, store.SessionCLI))
	_, err := f.Authenticate(ctx, cli.Token.Reveal(), store.SessionBrowser, false)
	checkErr(t, err, ErrSession)
	_, err = f.ResetMember(ctx, admin, admin)
	checkErr(t, err, ErrAccountUnavailable)
	_, err = f.RecoverAdmin(ctx, memberEmail)
	checkErr(t, err, ErrAccountUnavailable)
	reset := require[IssuedGrant](t)(f.ResetMember(ctx, admin, id))
	checkErr(t, f.ChangePassword(ctx, id, secondPass, thirdPass), ErrCurrentPassword)
	checkErr(t, f.ChangePassword(ctx, id, firstPass, secondPass), nil)
	for audience, token := range map[store.SessionAudience]Secret{
		store.SessionBrowser: member.Token, store.SessionCLI: cli.Token, store.SessionAgent: oldAgent,
	} {
		_, err := f.Authenticate(ctx, token.Reveal(), audience, false)
		checkErr(t, err, ErrSession)
	}
	checkErr(t, f.RedeemReset(ctx, linkToken(t, reset.Link, store.GrantPasswordReset), thirdPass), ErrReset)
	_, err = signIn(ctx, f, memberEmail, firstPass)
	checkErr(t, err, ErrSignIn)
	current := require[SignInResult](t)(signIn(ctx, f, memberEmail, secondPass))
	reset = require[IssuedGrant](t)(f.ResetMember(ctx, admin, id))
	if !reset.Link.ExpiresAt.Equal(f.nowAt.Add(30 * time.Minute)) {
		t.Fatal("member reset expiry differs from 30 minutes")
	}
	rotated := require[IssuedGrant](t)(f.ResetMember(ctx, admin, id))
	checkErr(t, f.RedeemReset(ctx, linkToken(t, reset.Link, store.GrantPasswordReset), thirdPass), ErrReset)
	token := linkToken(t, rotated.Link, store.GrantPasswordReset)
	checkErr(t, f.RedeemReset(ctx, token, thirdPass), nil)
	checkErr(t, f.RedeemReset(ctx, token, thirdPass), ErrReset)
	_, err = f.Authenticate(ctx, current.Token.Reveal(), store.SessionBrowser, false)
	checkErr(t, err, ErrSession)
	current = require[SignInResult](t)(signIn(ctx, f, memberEmail, thirdPass))
	if current.Account.ID != id || current.Account.Role != store.RoleMember {
		t.Fatal("member reset changed identity or role")
	}
	recovery := require[Link](t)(f.RecoverAdmin(ctx, ownerEmail))
	f.nowAt = recovery.ExpiresAt
	checkErr(t, f.RedeemReset(ctx, linkToken(t, recovery, store.GrantPasswordReset), secondPass), ErrReset)
	recovery = require[Link](t)(f.RecoverAdmin(ctx, ownerEmail))
	checkErr(t, f.RedeemReset(ctx, linkToken(t, recovery, store.GrantPasswordReset), secondPass), nil)
	_, err = f.Authenticate(ctx, owner.Token.Reveal(), store.SessionBrowser, false)
	checkErr(t, err, ErrSession)
	newOwner := require[SignInResult](t)(signIn(ctx, f, ownerEmail, secondPass))
	if newOwner.Account.ID != admin || newOwner.Account.Role != store.RoleAdmin {
		t.Fatal("owner recovery changed identity or role")
	}
	checkErr(t, f.Disable(ctx, admin, id), nil)
	_, err = f.Authenticate(ctx, current.Token.Reveal(), store.SessionBrowser, false)
	checkErr(t, err, ErrSession)
	for _, address := range []string{memberEmail, "unknown@example.test", "malformed"} {
		_, err = signIn(ctx, f, address, thirdPass)
		checkErr(t, err, ErrSignIn)
	}
	people := require[[]store.Account](t)(f.ListAccounts(ctx, admin, 0, 10))
	if len(people) != 2 || people[0].ID != id || people[0].Status != store.AccountDisabled {
		t.Fatal("disabled identity was lost")
	}
}

func TestBrowserSessionIdleAbsoluteAndSignOut(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	first := claim(t, f)
	start := f.nowAt
	f.nowAt = start.Add(6 * 24 * time.Hour)
	passive := require[Identity](t)(f.Authenticate(ctx, first.Token.Reveal(), store.SessionBrowser, false))
	if passive.Session.IdleExpiresAt == nil || !passive.Session.IdleExpiresAt.Equal(start.Add(browserIdle)) {
		t.Fatal("passive use extended idle time")
	}
	f.nowAt = start.Add(browserIdle)
	_, err := f.Authenticate(ctx, first.Token.Reveal(), store.SessionBrowser, true)
	checkErr(t, err, ErrSession)
	second := require[SignInResult](t)(signIn(ctx, f, ownerEmail, firstPass))
	start = f.nowAt
	for day := 6; day < 30; day += 6 {
		f.nowAt = start.AddDate(0, 0, day)
		a := require[Identity](t)(f.Authenticate(ctx, second.Token.Reveal(), store.SessionBrowser, true))
		want := minDeadline(f.nowAt.Add(browserIdle), start.Add(sessionLifetime))
		if a.Session.IdleExpiresAt == nil || !a.Session.IdleExpiresAt.Equal(want) {
			t.Fatal("browser idle deadline did not follow activity or cap at absolute expiry")
		}
	}
	f.nowAt = start.Add(sessionLifetime)
	_, err = f.Authenticate(ctx, second.Token.Reveal(), store.SessionBrowser, true)
	checkErr(t, err, ErrSession)
	third := require[SignInResult](t)(signIn(ctx, f, ownerEmail, firstPass))
	checkErr(t, f.SignOut(ctx, third.Token.Reveal(), store.SessionBrowser), nil)
	checkErr(t, f.SignOut(ctx, third.Token.Reveal(), store.SessionBrowser), nil)
	_, err = f.Authenticate(ctx, third.Token.Reveal(), store.SessionBrowser, false)
	checkErr(t, err, ErrSession)
}

func minDeadline(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
