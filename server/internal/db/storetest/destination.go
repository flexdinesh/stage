package storetest

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func init() {
	register("NewTaskDestination", caseNewTaskDestination)
}

func caseNewTaskDestination(t *testing.T, open Factory) {
	// Connections 1 and 2 on the main machine, 3 and 4 on machine 2.
	machine := func(id int64) int64 {
		if id >= 3 {
			return 2
		}
		return 1
	}
	b := func(id int64, status string, active bool) db.ProjectBinding {
		return db.ProjectBinding{ConnectionID: id, Status: status, ConnectionActive: active}
	}
	ptr := func(id int64) *int64 { return &id }
	for _, tc := range []struct {
		name     string
		bindings []db.ProjectBinding
		def      *int64
		want     int64
	}{
		{"no default, one binding", []db.ProjectBinding{b(3, "available", true)}, nil, 3},
		{"no default, one binding missing", []db.ProjectBinding{b(3, "missing", true)}, nil, 3},
		{"no default, two bindings", []db.ProjectBinding{b(1, "available", true), b(3, "available", true)}, nil, 0},
		{"no default, replaced binding only", []db.ProjectBinding{b(1, "available", false)}, nil, 0},
		{"no bindings", nil, ptr(1), 0},
		{"default bound", []db.ProjectBinding{b(1, "available", true), b(3, "available", true)}, ptr(3), 3},
		{"default bound but missing", []db.ProjectBinding{b(1, "available", true), b(3, "missing", true)}, ptr(3), 1},
		{"default unbound, one binding", []db.ProjectBinding{b(4, "missing", true)}, ptr(1), 4},
		{"default unbound, main machine first", []db.ProjectBinding{b(3, "available", true), b(2, "available", true), b(4, "available", true)}, ptr(1), 2},
		{"default unbound, then by machine and id", []db.ProjectBinding{b(4, "available", true), b(3, "available", true)}, ptr(1), 3},
		{"default unbound, none available", []db.ProjectBinding{b(3, "missing", true), b(4, "changed", true)}, ptr(1), 0},
		{"default on a replaced binding", []db.ProjectBinding{b(1, "available", false), b(3, "available", true)}, ptr(1), 3},
	} {
		if got := db.NewTaskDestination(tc.bindings, tc.def, machine); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}
