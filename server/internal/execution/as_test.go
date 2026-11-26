package execution_test

import (
	"context"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
)

type plain struct{ execution.Adapter }

type resolving struct{ execution.Adapter }

func (resolving) ResolveLink(context.Context, string) (execution.SessionRef, error) {
	return execution.SessionRef{}, nil
}

// partial implements every optional interface but forwards only traits.
type partial struct {
	resolving
	traits []string
}

func (partial) Correlate(context.Context, execution.Intent) (execution.Observation, error) {
	return execution.Observation{}, nil
}
func (p partial) Implements(trait string) bool { return slices.Contains(p.traits, trait) }

func TestAsHonorsPartialTraits(t *testing.T) {
	if _, ok := execution.As[execution.LinkResolver](plain{}); ok {
		t.Fatal("plain adapter resolves links")
	}
	if _, ok := execution.As[execution.LinkResolver](resolving{}); !ok {
		t.Fatal("resolver not found")
	}
	p := partial{traits: []string{execution.TraitOf[execution.LinkResolver](), "futureTrait"}}
	if _, ok := execution.As[execution.LinkResolver](p); !ok {
		t.Fatal("forwarded trait hidden")
	}
	if _, ok := execution.As[execution.Correlator](p); ok {
		t.Fatal("unforwarded trait offered")
	}
	if got := execution.Traits(resolving{}); !slices.Equal(got, []string{execution.TraitOf[execution.LinkResolver]()}) {
		t.Fatal(got)
	}
	if got := execution.Traits(p); !slices.Equal(got, []string{execution.TraitOf[execution.LinkResolver]()}) {
		t.Fatal(got)
	}
}
