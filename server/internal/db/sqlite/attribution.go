package sqlite

func attributionColumns(column string) string {
	return column + `, COALESCE((SELECT display_name FROM users WHERE id = COALESCE(json_extract(` + column + `,'$.actor.userId'),json_extract(` + column + `,'$.actor.initiatorUserId'))), '')`
}
