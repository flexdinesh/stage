package db

import (
	"errors"
	"testing"
)

func TestSchemaTooNewError(t *testing.T) {
	err := error(&SchemaTooNewError{Current: 9, Known: 4})
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatal("not ErrSchemaTooNew")
	}
	want := "database schema 9 is newer than this Stage (knows 4): install a newer Stage or restore a backup (docs/install.md#recovery)"
	if err.Error() != want {
		t.Fatalf("message %q", err.Error())
	}
}
