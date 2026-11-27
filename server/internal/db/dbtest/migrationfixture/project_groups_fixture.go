// Package migrationfixture supplies portable seed data and assertions for
// upgrade scenarios that run unchanged on both storage engines.
package migrationfixture

import (
	_ "embed"
	"strings"
	"testing"
)

const stamp = `{ "cause": { "kind": "accepted", "requestId": "old-caüse" }, "actor": { "kind": "human", "userId": 7 }, "sourceMachineId": 2 }`
const evidence = `{ "projectId": 4, "taskId": 6, "reply": "old result ✓" }`

//go:embed project_groups_seed.sql
var projectGroupsSeed string

//go:embed project_groups_checks.sql
var projectGroupsChecks string

func groupStatements(source string) []string {
	text := strings.NewReplacer("{{attribution}}", stamp, "{{evidence}}", evidence).Replace(source)
	return strings.Split(strings.TrimSuffix(strings.TrimSpace(text), ";"), ";\n")
}

func SeedProjectGroups(t *testing.T, exec func(string) error) {
	t.Helper()
	for _, query := range groupStatements(projectGroupsSeed) {
		if err := exec(query); err != nil {
			t.Fatalf("seed %s: %v", query, err)
		}
	}
}

func CheckProjectGroups(t *testing.T, count func(string) (int, error)) {
	t.Helper()
	for _, query := range groupStatements(projectGroupsChecks) {
		if n, err := count(query); err != nil || n != 1 {
			t.Errorf("%s: got %d, %v, want 1", query, n, err)
		}
	}
}
