package db

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/principal"
)

// DefaultUser is the user migration 0017 seeds. Legacy creator fields
// use it when no account is available (docs/architecture.md "Users").
const DefaultUser int64 = 1

type User = store.User

type actingUserKey struct{}

// WithUser sets the legacy creator, such as the authenticated account or
// the creator of a schedule template.
func WithUser(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, actingUserKey{}, id)
}

// ActingUser is the one resolver for write attribution (created_by_user_id):
// the user WithUser set on ctx, else DefaultUser.
func ActingUser(ctx context.Context) int64 {
	if id, ok := ctx.Value(actingUserKey{}).(int64); ok {
		return id
	}
	return DefaultUser
}

type actingMachineKey struct{}

// WithMachine returns ctx with id as the forwarding machine: the request
// arrived through that machine's agent gateway (its machine token). A
// machine is a principal, never a user: ActingUser is unchanged.
func WithMachine(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, actingMachineKey{}, id)
}

// ActingMachine is the one resolver for source_machine_id: the machine
// WithMachine set on ctx, else nil (not through a gateway).
func ActingMachine(ctx context.Context) *int64 {
	if id, ok := ctx.Value(actingMachineKey{}).(int64); ok {
		return &id
	}
	return nil
}

// withoutMachine returns ctx with no forwarding machine (background work a
// request only triggered, such as a schedule spawn).
func withoutMachine(ctx context.Context) context.Context {
	return context.WithValue(ctx, actingMachineKey{}, nil)
}

// GetUser reads a user.
func (s *Store) GetUser(ctx context.Context, id int64) (User, error) {
	return read(ctx, s, func(tx store.Tx) (User, error) { return tx.Users().Get(ctx, id) })
}

type Attribution = store.Attribution
type Cause = store.Cause

type attributionKey struct{}

// WorkAttribution projects verified context into durable provenance. Legacy
// creator and caller-session values never establish a person's identity.
func WorkAttribution(ctx context.Context) Attribution {
	if value, ok := ctx.Value(attributionKey{}).(Attribution); ok {
		return value
	}
	p, _ := principal.From(ctx)
	a := Attribution{Actor: store.Actor{Kind: "unknown"}, SourceMachine: ActingMachine(ctx)}
	switch p.Kind {
	case principal.Personal:
		a.Actor.Kind = "personal"
	case principal.Human, principal.Admin:
		if p.AccountID > 0 {
			a.Actor.Kind, a.Actor.UserID = "human", p.AccountID
		}
	case principal.Agent:
		a.Actor.Kind = "agent"
		a.Actor.InitiatorUserID = p.InitiatorID
		if p.InitiatorID == 0 {
			a.Actor.CredentialAccountID = p.AccountID
		}
	case principal.Machine:
		a.Actor.Kind = "machine"
	case principal.System:
		a.Actor.Kind = "system"
	}
	if a.SourceMachine == nil && p.MachineID != 0 {
		a.SourceMachine = &p.MachineID
	}
	return a
}

func WithAttribution(ctx context.Context, attribution Attribution) context.Context {
	return context.WithValue(ctx, attributionKey{}, attribution)
}

func WithWorkCause(ctx context.Context, cause Cause) context.Context {
	a := WorkAttribution(ctx)
	a.Cause = &cause
	return WithAttribution(ctx, a)
}

// AutomatedWork records a Stage action caused by earlier work. The derived
// stamp carries provenance only and leaves the admitted principal alone.
func AutomatedWork(ctx context.Context, kind string, cause Cause, source Attribution) context.Context {
	initiator := source.Actor.UserID
	if initiator == 0 {
		initiator = source.Actor.InitiatorUserID
	}
	return WithAttribution(ctx, Attribution{
		Actor: store.Actor{Kind: kind, InitiatorUserID: initiator},
		Cause: &cause, SourceMachine: source.SourceMachine,
	})
}

// ObservedWork is used after validating the observation's connection. The
// submitter may have prompted a poll, but the state came from the harness.
func ObservedWork(ctx context.Context, ref SessionRef, machine int64) context.Context {
	var source Attribution
	if machine != 0 {
		source.SourceMachine = &machine
	}
	return AutomatedWork(ctx, "system", Cause{Kind: "harness_observation", Session: &ref}, source)
}
