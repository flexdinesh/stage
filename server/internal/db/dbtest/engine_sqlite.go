//go:build !postgres

package dbtest

import "testing"

func notePostgres(testing.TB) {}
