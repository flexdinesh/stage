package execution

import (
	"context"
	"testing"
)

type roleWithEvents struct {
	Inventory
	events bool
}

func (a *roleWithEvents) Events(context.Context, Sink) error { return nil }

func (a *roleWithEvents) Implements(trait string) bool {
	return a.events && trait == TraitOf[EventSource]()
}

func TestAdapterIsItsRoles(t *testing.T) {
	var (
		_ Catalog   = Adapter(nil)
		_ Inventory = Adapter(nil)
		_ Sessions  = Adapter(nil)
		_ Launcher  = Adapter(nil)
	)

	a := &roleWithEvents{events: true}
	var inventory Inventory = a
	if events, ok := As[EventSource](inventory); !ok || events != a {
		t.Fatalf("optional trait from inventory role = %v, %t", events, ok)
	}
	a.events = false
	if events, ok := As[EventSource](inventory); ok || events != nil {
		t.Fatalf("undeclared trait from inventory role = %v, %t", events, ok)
	}
}
