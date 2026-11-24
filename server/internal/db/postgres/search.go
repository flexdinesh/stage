//go:build postgres

package postgres

import "strings"

// Search (docs/storage.md "Search"): TaskFilter.Q and JournalQuery.Q match
// as case-insensitive substrings with LIKE metacharacters literal, by ILIKE.
// The pg_trgm GIN indexes from migration 0029 serve queries of 3 or more
// characters; the planner scans for shorter ones. Case folding follows the
// database's LC_CTYPE.

// searchTasks matches q against the task title or prompt.
func searchTasks(w *where, q string) string {
	p := w.arg(likePattern(q))
	return "(" + likeSQL("t.title", p) + " OR " + likeSQL("t.prompt", p) + ")"
}

// searchJournal matches q against the journal entry text.
func searchJournal(w *where, q string) string {
	return likeSQL("text", w.arg(likePattern(q)))
}

// likePattern escapes LIKE metacharacters (escape character \) for a
// substring match; use the placeholder with likeSQL.
func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

// likeSQL matches col against a likePattern placeholder case-insensitively.
func likeSQL(col, pattern string) string {
	return col + " ILIKE " + pattern + `::text ESCAPE '\'`
}
