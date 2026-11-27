package api

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/flexdinesh/stage/server/internal/auth"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/principal"
)

type accountView struct {
	store.User
	Email  *string             `json:"email"`
	Role   store.AccountRole   `json:"role"`
	Status store.AccountStatus `json:"status"`
}

func viewAccount(a store.Account) accountView {
	return accountView{a.User, a.Email, a.Role, a.Status}
}

type issuedCredential struct {
	Token          string    `json:"token"`
	ExpiresAt      time.Time `json:"expiresAt"`
	InstallationID string    `json:"installationId"`
}

func (s *server) registerAuth(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/status", s.authStatus)
	if s.Access == nil {
		return
	}
	public := func(pattern string, native bool, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if _, ok := principal.From(r.Context()); ok {
				accessError(w, http.StatusForbidden, "principal_forbidden", "Use the Stage server to sign in or open an account link.")
				return
			}
			allowed := validBrowserWrite(s.Access, r)
			if native {
				allowed = nativeLogin(s.Access, r)
				if !allowed {
					accessError(w, http.StatusForbidden, "native_client_required", "Use a Stage CLI to sign in at this endpoint.")
					return
				}
			}
			if !allowed {
				writeAccessError(w, errBrowserOrigin)
				return
			}
			ctx := principal.With(r.Context(), principal.Actor{Kind: principal.Public})
			writeAccountError(w, fn(w, r.WithContext(ctx)))
		})
	}
	protected := func(pattern string, scope routeScope, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, s.authorize(scope, func(w http.ResponseWriter, r *http.Request) {
			writeAccountError(w, fn(w, r))
		}))
	}
	public("POST /api/auth/login", false, s.signIn(store.SessionBrowser))
	public("POST /api/auth/cli/login", true, s.signIn(store.SessionCLI))
	for endpoint, purpose := range map[string]store.GrantPurpose{
		"setup": store.GrantSetup, "invite": store.GrantInvite, "reset": store.GrantPasswordReset,
	} {
		public("POST /api/auth/"+endpoint+"/inspect", false, s.inspectGrant(purpose))
		public("POST /api/auth/"+endpoint, false, s.redeemGrant(purpose))
	}
	protected("POST /api/auth/logout", humanRoute, s.signOut)
	protected("PATCH /api/account", humanRoute, s.setName)
	protected("POST /api/account/password", humanRoute, s.changePassword)
	protected("POST /api/account/agent-credential", humanRoute, s.rotateAgent)
	protected("DELETE /api/account/agent-credential", humanRoute, s.revokeAgent)
	protected("GET /api/admin/accounts", adminRoute, s.listAccounts)
	protected("PATCH /api/admin/accounts/{id}/role", adminRoute, s.setRole)
	protected("POST /api/admin/accounts/{id}/disable", adminRoute, s.disableAccount)
	protected("POST /api/admin/accounts/{id}/reset", adminRoute, s.resetMember)
	protected("GET /api/admin/invitations", adminRoute, s.listInvitations)
	protected("POST /api/admin/invitations", adminRoute, s.issueInvite)
	protected("POST /api/admin/invitations/{id}/replace", adminRoute, s.replaceInvite)
	protected("DELETE /api/admin/invitations/{id}", adminRoute, s.revokeInvite)
}

func (s *server) authStatus(w http.ResponseWriter, _ *http.Request) {
	var mode = store.AccessPersonal
	id := s.InstallationID
	if s.Access != nil {
		mode, id = store.AccessClaimed, s.Access.id
		if s.Access.Pending() {
			mode = store.AccessPending
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"mode": string(mode), "installationId": id})
}

func (a *Access) setCookie(w http.ResponseWriter, token string, expiry time.Time, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: a.secure, SameSite: http.SameSiteLaxMode, Expires: expiry, MaxAge: maxAge,
	})
}

func (s *server) signedIn(w http.ResponseWriter, result auth.SignInResult) {
	if result.Session.Audience == store.SessionCLI {
		s.writeCredential(w, result)
		return
	}
	s.Access.setCookie(w, result.Token.Reveal(), result.Session.ExpiresAt, 0)
	writeJSON(w, http.StatusOK, viewAccount(result.Account))
}

func (s *server) writeCredential(w http.ResponseWriter, result auth.SignInResult) {
	writeJSON(w, http.StatusOK, issuedCredential{result.Token.Reveal(), result.Session.ExpiresAt, s.Access.id})
}

func (s *server) signIn(audience store.SessionAudience) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		if s.Access.Pending() {
			pending(w)
			return nil
		}
		var in struct{ Email, Password string }
		if err := accountBody(r, &in); err != nil {
			return err
		}
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		peer, _ := netip.ParseAddr(host)
		result, err := s.Access.service.SignIn(r.Context(), in.Email, in.Password, peer.Unmap(), audience)
		if err == nil {
			s.signedIn(w, result)
		}
		return err
	}
}

func (s *server) signOut(w http.ResponseWriter, r *http.Request) error {
	c, _ := credentialFrom(r.Context())
	err := s.Access.service.SignOut(r.Context(), c.token, c.identity.Session.Audience)
	if err == nil {
		if c.identity.Session.Audience == store.SessionBrowser {
			s.Access.setCookie(w, "", time.Unix(1, 0), -1)
		}
		w.WriteHeader(http.StatusNoContent)
	}
	return err
}

func (s *server) inspectGrant(purpose store.GrantPurpose) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		if s.Access.Pending() && purpose != store.GrantSetup {
			pending(w)
			return nil
		}
		var in struct{ Token string }
		if err := accountBody(r, &in); err != nil {
			return err
		}
		info, err := s.Access.service.InspectGrant(r.Context(), in.Token, purpose)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"email": info.Email, "expiresAt": info.ExpiresAt})
		}
		return err
	}
}

func (s *server) redeemGrant(purpose store.GrantPurpose) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		if s.Access.Pending() && purpose != store.GrantSetup {
			pending(w)
			return nil
		}
		var in struct{ Token, Email, Name, Password string }
		if err := accountBody(r, &in); err != nil {
			return err
		}
		ctx := r.Context()
		var result auth.SignInResult
		var err error
		switch purpose {
		case store.GrantSetup:
			result, err = s.Access.service.ClaimSetup(ctx, in.Token, in.Email, in.Name, in.Password)
			if err == nil {
				s.Access.markClaimed(result.Session.CreatedAt)
			}
		case store.GrantInvite:
			result, err = s.Access.service.RedeemInvite(ctx, in.Token, in.Name, in.Password)
		case store.GrantPasswordReset:
			err = s.Access.service.RedeemReset(ctx, in.Token, in.Password)
			if err == nil {
				w.WriteHeader(http.StatusNoContent)
			}
			return err
		}
		if err == nil {
			s.signedIn(w, result)
		}
		return err
	}
}

func (s *server) setName(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Name string }
	if err := accountBody(r, &in); err != nil {
		return err
	}
	a, err := s.Access.service.SetDisplayName(r.Context(), db.ActingUser(r.Context()), in.Name)
	if err == nil {
		writeJSON(w, http.StatusOK, viewAccount(a))
	}
	return err
}

func (s *server) changePassword(w http.ResponseWriter, r *http.Request) error {
	var in struct{ CurrentPassword, Password string }
	if err := accountBody(r, &in); err != nil {
		return err
	}
	err := s.Access.service.ChangePassword(r.Context(), db.ActingUser(r.Context()), in.CurrentPassword, in.Password)
	if err == nil {
		if c, _ := credentialFrom(r.Context()); c.identity.Session.Audience == store.SessionBrowser {
			s.Access.setCookie(w, "", time.Unix(1, 0), -1)
		}
		w.WriteHeader(http.StatusNoContent)
	}
	return err
}

func (s *server) rotateAgent(w http.ResponseWriter, r *http.Request) error {
	c, _ := credentialFrom(r.Context())
	result, err := s.Access.service.RotateAgent(r.Context(), c.token, c.identity.Session.Audience)
	if err == nil {
		s.writeCredential(w, result)
	}
	return err
}

func (s *server) revokeAgent(w http.ResponseWriter, r *http.Request) error {
	c, _ := credentialFrom(r.Context())
	err := s.Access.service.RevokeAgent(r.Context(), c.token, c.identity.Session.Audience)
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
	}
	return err
}

func (s *server) listAccounts(w http.ResponseWriter, r *http.Request) error {
	before, limit, err := accountPage(r)
	if err != nil {
		return err
	}
	accounts, err := s.Access.service.ListAccounts(r.Context(), db.ActingUser(r.Context()), before, limit)
	if err == nil {
		out := make([]accountView, 0, len(accounts))
		for _, a := range accounts {
			out = append(out, viewAccount(a))
		}
		writeJSON(w, http.StatusOK, out)
	}
	return err
}

func accountPage(r *http.Request) (int64, int, error) {
	q := r.URL.Query()
	before, err := queryInt64(q, "beforeId")
	if err != nil {
		return 0, 0, err
	}
	limit, err := queryInt64(q, "limit")
	if err != nil {
		return 0, 0, err
	}
	var id, n int64
	if before != nil {
		id = *before
	}
	if limit != nil {
		n = *limit
	}
	if id < 0 || n < 0 || n > 100 {
		return 0, 0, db.Invalid("Use a positive cursor and a limit up to 100.")
	}
	return id, int(n), nil
}

func (s *server) setRole(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct{ Role store.AccountRole }
	if err := accountBody(r, &in); err != nil {
		return err
	}
	err = s.Access.service.SetRole(r.Context(), db.ActingUser(r.Context()), id, in.Role)
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
	}
	return err
}

func (s *server) disableAccount(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	err = s.Access.service.Disable(r.Context(), db.ActingUser(r.Context()), id)
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
	}
	return err
}

func (s *server) resetMember(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	g, err := s.Access.service.ResetMember(r.Context(), db.ActingUser(r.Context()), id)
	return respondGrant(w, g, err)
}

type invitationView struct {
	ID           int64      `json:"id"`
	Email        *string    `json:"email"`
	IssuerUserID *int64     `json:"issuerUserId"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"createdAt"`
	ExpiresAt    time.Time  `json:"expiresAt"`
	ConsumedAt   *time.Time `json:"consumedAt"`
	RevokedAt    *time.Time `json:"revokedAt"`
}

func (s *server) listInvitations(w http.ResponseWriter, r *http.Request) error {
	before, limit, err := accountPage(r)
	if err != nil {
		return err
	}
	invites, err := s.Access.service.ListInvites(r.Context(), db.ActingUser(r.Context()), before, limit)
	if err == nil {
		out := make([]invitationView, 0, len(invites))
		for _, i := range invites {
			out = append(out, invitationView{i.ID, i.Email, i.IssuerUserID, i.Status, i.CreatedAt, i.ExpiresAt, i.ConsumedAt, i.RevokedAt})
		}
		writeJSON(w, http.StatusOK, out)
	}
	return err
}

func (s *server) issueInvite(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Email string }
	if err := accountBody(r, &in); err != nil {
		return err
	}
	g, err := s.Access.service.Invite(r.Context(), db.ActingUser(r.Context()), in.Email)
	return respondGrant(w, g, err)
}

func (s *server) replaceInvite(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	g, err := s.Access.service.ReissueInvite(r.Context(), db.ActingUser(r.Context()), id)
	return respondGrant(w, g, err)
}

func (s *server) revokeInvite(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	err = s.Access.service.RevokeInvite(r.Context(), db.ActingUser(r.Context()), id)
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
	}
	return err
}

func respondGrant(w http.ResponseWriter, g auth.IssuedGrant, err error) error {
	if err == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"id": g.Grant.ID, "url": g.Link.URL(), "expiresAt": g.Link.ExpiresAt})
	}
	return err
}

func accountBody(r *http.Request, out any) error {
	if err := decodeStrict(r, out); err != nil {
		return db.Invalid("Check the form fields and try again.")
	}
	return nil
}

func writeAccountError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	var invalid *db.InvalidError
	status, code := http.StatusBadRequest, "invalid_account_input"
	switch {
	case errors.As(err, &invalid):
		accessError(w, status, code, "Check the form fields and try again.")
		return
	case errors.Is(err, auth.ErrSignIn):
		status, code = http.StatusUnauthorized, "sign_in_failed"
	case errors.Is(err, auth.ErrSession):
		status, code = http.StatusUnauthorized, "authentication_required"
	case errors.Is(err, auth.ErrForbidden):
		status, code = http.StatusForbidden, "principal_forbidden"
	case errors.Is(err, auth.ErrPending), errors.Is(err, auth.ErrInvite), errors.Is(err, auth.ErrReset):
		status, code = http.StatusForbidden, "grant_unavailable"
	case errors.Is(err, auth.ErrAccountExists), errors.Is(err, auth.ErrInviteExists), errors.Is(err, auth.ErrLastAdmin):
		status, code = http.StatusConflict, "account_conflict"
	case errors.Is(err, auth.ErrAccountUnavailable):
		status, code = http.StatusNotFound, "account_unavailable"
	case errors.Is(err, auth.ErrEmail), errors.Is(err, auth.ErrName), errors.Is(err, auth.ErrPasswordLength),
		errors.Is(err, auth.ErrCommonPassword), errors.Is(err, auth.ErrCurrentPassword), errors.Is(err, auth.ErrRole):
	default:
		status, code = http.StatusServiceUnavailable, "authentication_unavailable"
	}
	accessError(w, status, code, auth.PublicMessage(err))
}
