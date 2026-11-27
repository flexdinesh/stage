package db

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

var (
	ErrSharedAccessRequired = errors.New("this database uses shared access. Remove the false STAGE_MULTI_USER or --multi-user setting and restart")
	ErrSetupUnavailable     = errors.New("setup is unavailable. Ask the installation owner for a current setup link")
)

// ReadAccess exposes account storage to the auth service without exposing
// the engine or involving the product's operational user projection.
func (s *Store) ReadAccess(ctx context.Context, fn func(store.Access) error) error {
	return s.e.Read(ctx, func(tx store.Tx) error { return fn(tx.Access()) })
}

// WriteAccess runs a transactional auth use case. The callback can be retried
// by the engine and must have no side effects outside the transaction.
func (s *Store) WriteAccess(ctx context.Context, fn func(store.Access) error) error {
	return s.e.Write(ctx, func(tx store.Tx) error { return fn(tx.Access()) })
}

// ConfigureAccess resolves startup intent under the installation lock. A nil
// intent follows the persisted mode. The returned state has been committed.
func (s *Store) ConfigureAccess(ctx context.Context, shared *bool) (store.InstallationAccess, error) {
	return write(ctx, s, func(tx store.Tx) (store.InstallationAccess, error) {
		return ConfigureAccessTx(ctx, tx.Access(), shared)
	})
}

// ConfigureAccessTx resolves startup intent inside a write transaction so
// callers can issue the first setup grant in the same commit.
func ConfigureAccessTx(ctx context.Context, r store.Access, shared *bool) (store.InstallationAccess, error) {
	a, err := r.InstallationForUpdate(ctx)
	if err != nil {
		return store.InstallationAccess{}, err
	}
	if shared != nil && !*shared && a.Mode != store.AccessPersonal {
		return store.InstallationAccess{}, ErrSharedAccessRequired
	}
	mode := a.Mode
	if shared != nil && *shared && mode == store.AccessPersonal {
		mode = store.AccessPending
	}
	if a.ID == "" || a.Mode != mode {
		if a.ID == "" {
			a.ID = rand.Text()
		}
		if err := r.InitializeInstallation(ctx, a.ID, mode); err != nil {
			return store.InstallationAccess{}, err
		}
		a.Mode = mode
	}
	return a, nil
}

// ClaimInstallation consumes the setup grant, adds the local credential to
// user 1, and stamps the attribution boundary in one committed change.
func (s *Store) ClaimInstallation(ctx context.Context, digest store.TokenDigest, input store.AccountInput, verifier string, now time.Time) (store.Account, error) {
	return write(ctx, s, func(tx store.Tx) (store.Account, error) {
		a, err := ClaimInstallationTx(ctx, tx.Access(), digest, input, verifier, now)
		var claimErr claimOwnerError
		if errors.As(err, &claimErr) && s.e.Errors().IsUniqueViolation(claimErr.error) {
			return store.Account{}, Conflict("This email address is already in use.")
		}
		return a, err
	})
}

type claimOwnerError struct{ error }

func (e claimOwnerError) Unwrap() error { return e.error }

// ClaimInstallationTx claims user 1 in an open write transaction so the
// initiating session and its audit record can commit with the claim.
func ClaimInstallationTx(ctx context.Context, r store.Access, digest store.TokenDigest, input store.AccountInput, verifier string, now time.Time) (store.Account, error) {
	if strings.TrimSpace(input.DisplayName) == "" || store.EmailKey(input.Email) == "" || verifier == "" {
		return store.Account{}, Invalid("Enter a name, email address and password.")
	}
	a, err := r.InstallationForUpdate(ctx)
	if err != nil {
		return store.Account{}, err
	}
	if a.Mode != store.AccessPending {
		return store.Account{}, ErrSetupUnavailable
	}
	if _, err := r.ConsumeGrant(ctx, digest, store.GrantSetup, now); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Account{}, ErrSetupUnavailable
		}
		return store.Account{}, err
	}
	if err := r.ClaimOwner(ctx, input); err != nil {
		return store.Account{}, claimOwnerError{err}
	}
	if err := r.MarkClaimed(ctx, now); err != nil {
		return store.Account{}, err
	}
	if err := r.InsertLocalCredential(ctx, DefaultUser, verifier); err != nil {
		return store.Account{}, err
	}
	return r.Account(ctx, DefaultUser)
}

// InvalidateRestoredAccess is for an explicit offline restore/import while
// holding installation ownership. Ordinary startup keeps active credentials.
func (s *Store) InvalidateRestoredAccess(ctx context.Context) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		r := tx.Access()
		if _, err := r.InstallationForUpdate(ctx); err != nil {
			return err
		}
		return r.DiscardTransientAccess(ctx)
	})
}
