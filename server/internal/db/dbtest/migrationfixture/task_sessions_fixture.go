package migrationfixture

import (
	_ "embed"
	"testing"
)

//go:embed task_sessions_seed.sql
var taskSessionsSeed string

//go:embed task_sessions_checks.sql
var taskSessionsChecks string

func SeedTaskSessions(t *testing.T, exec func(string) error) {
	t.Helper()
	seed(t, exec, taskSessionsSeed)
}

func CheckTaskSessions(t *testing.T, count func(string) (int, error)) {
	t.Helper()
	for _, query := range groupStatements(taskSessionsChecks) {
		if n, err := count(query); err != nil || n != 1 {
			t.Errorf("%s: got %d, %v, want 1", query, n, err)
		}
	}
}
