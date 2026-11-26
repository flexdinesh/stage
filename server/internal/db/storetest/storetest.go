// Package storetest is the adapter-agnostic storage contract suite
// (docs/storage.md "Contract suite"). Every case runs through db.Store and
// the storage ports of engines from a Factory, never one engine's SQL, so a
// case written once runs on every engine.
package storetest

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Factory opens a fresh, migrated, empty engine for t, closed when t ends.
// Engines from separate calls share no data.
type Factory func(t testing.TB) store.Engine

type testCase struct {
	name string
	run  func(t *testing.T, open Factory)
}

var cases []testCase

// register adds a case; each file registers its cases in init.
func register(name string, run func(t *testing.T, open Factory)) {
	for _, c := range cases {
		if c.name == name {
			panic(fmt.Sprintf("storetest: case %q registered twice", name))
		}
	}
	cases = append(cases, testCase{name, run})
}

// Run runs every case as a subtest of t (named after the case, in name
// order), each opening its engines from open.
func Run(t *testing.T, open Factory) {
	sorted := slices.SortedFunc(slices.Values(cases), func(a, b testCase) int { return cmp.Compare(a.name, b.name) })
	for _, c := range sorted {
		t.Run(c.name, func(t *testing.T) { c.run(t, open) })
	}
}

func ptr[T any](v T) *T { return &v }

// setup is a Store over a fresh engine.
func setup(t *testing.T, open Factory) (*db.Store, context.Context) {
	return db.NewStore(open(t)), context.Background()
}

// race runs fn(0..n-1) on n goroutines released together and returns each
// call's error, so a case can assert the invariant a concurrent use case
// must keep (docs/storage.md "Transactions and locking").
func race(n int, fn func(i int) error) []error {
	start := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			errs[i] = fn(i)
		})
	}
	close(start)
	wg.Wait()
	return errs
}
