//go:build postgres

package postgres

// SQL fragments shared by several repositories.
const (
	legacyT3SQL      = `(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active)`
	t3ConnectionsSQL = `(SELECT id FROM harness_connections WHERE harness='t3')`
)

// externalNeedsInput: external threads needing input (badge,
// threads?needsInput=1). Needs th + LEFT JOIN tasks tk. Subagents excluded,
// forks included.
const externalNeedsInput = `th.observed_state = 'needs_input'
  AND th.relationship IS DISTINCT FROM 'subagent' AND tk.id IS NULL`
