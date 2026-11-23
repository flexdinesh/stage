package db

import (
	"cmp"
	"slices"
)

// NewTaskDestination picks the connection a new task that names none starts
// on, from its project's bindings and the default connection (nil = none):
// the default when the project is available there, else the project's only
// active binding, else (with a default set) the first available binding, the
// main machine's first, then by machine and connection id. 0: none (the
// caller asks for a connection). machine maps a connection to its machine.
func NewTaskDestination(bindings []ProjectBinding, def *int64, machine func(int64) int64) int64 {
	var active, available []ProjectBinding
	for _, b := range bindings {
		if !b.ConnectionActive {
			continue
		}
		active = append(active, b)
		if b.Status == "available" {
			available = append(available, b)
		}
	}
	if def != nil && slices.ContainsFunc(available, func(b ProjectBinding) bool { return b.ConnectionID == *def }) {
		return *def
	}
	if len(active) == 1 {
		return active[0].ConnectionID
	}
	if def == nil || len(available) == 0 {
		return 0
	}
	first := slices.MinFunc(available, func(a, b ProjectBinding) int {
		return cmp.Or(cmp.Compare(machine(a.ConnectionID), machine(b.ConnectionID)), cmp.Compare(a.ConnectionID, b.ConnectionID))
	})
	return first.ConnectionID
}
