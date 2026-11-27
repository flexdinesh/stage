//go:build postgres

package postgres

func attributionColumns(column string) string {
	return column + `, COALESCE((SELECT display_name FROM users WHERE id = COALESCE((` + column + `::jsonb #>> '{actor,userId}')::bigint, (` + column + `::jsonb #>> '{actor,initiatorUserId}')::bigint)), '')`
}
