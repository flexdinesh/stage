// Package principal describes an actor admitted by a Stage boundary.
// Transport hints and legacy creator IDs are independent of authorization.
package principal

import "context"

type Kind uint8

const (
	Public Kind = iota
	Personal
	Human
	Admin
	Agent
	Machine
	System
	Owner
)

type Actor struct {
	Kind      Kind
	AccountID int64
	MachineID int64
}

func (a Actor) IsHuman() bool { return a.Kind == Human || a.Kind == Admin }

type actorKey struct{}

func With(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

func From(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	return a, ok
}
