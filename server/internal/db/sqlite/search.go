package sqlite

import (
	"database/sql/driver"
	"strings"
	"unicode/utf8"

	"modernc.org/sqlite"
)

// Search (docs/storage.md "Search"): TaskFilter.Q and JournalQuery.Q match
// as case-insensitive substrings with LIKE metacharacters literal. Queries
// of 3 or more characters use the FTS5 trigram indexes from migration 0029
// (kept in step by triggers); shorter ones cannot form a trigram and scan
// with LIKE, folded by stage_lower.

// minTrigram is the shortest query (in characters) the trigram index serves.
const minTrigram = 3

func init() {
	// stage_lower lowercases Unicode text (SQLite's lower() folds ASCII
	// only), so short queries fold like the trigram index does.
	sqlite.MustRegisterDeterministicScalarFunction("stage_lower", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if s, ok := args[0].(string); ok {
			return strings.ToLower(s), nil
		}
		return args[0], nil
	})
}

// searchTasks matches q against the task title or prompt.
func searchTasks(w *where, q string) string {
	if utf8.RuneCountInString(q) < minTrigram {
		p := w.arg(likePattern(q))
		return "(" + likeSQL("t.title", p) + " OR " + likeSQL("t.prompt", p) + ")"
	}
	return "t.id IN (SELECT rowid FROM tasks_fts WHERE tasks_fts MATCH " + w.arg(ftsPhrase(q)) + ")"
}

// searchJournal matches q against the journal entry text.
func searchJournal(w *where, q string) string {
	if utf8.RuneCountInString(q) < minTrigram {
		return likeSQL("text", w.arg(likePattern(q)))
	}
	return "id IN (SELECT rowid FROM journal_fts WHERE journal_fts MATCH " + w.arg(ftsPhrase(q)) + ")"
}

// ftsPhrase quotes q as one FTS5 phrase: with the trigram tokenizer it
// matches q as a substring, every character literal.
func ftsPhrase(q string) string {
	return `"` + strings.ReplaceAll(q, `"`, `""`) + `"`
}

// likePattern escapes LIKE metacharacters (escape character \) for a
// substring match; use the placeholder with likeSQL.
func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

// likeSQL matches col against a likePattern placeholder case-insensitively
// (Unicode lowercase on both sides).
func likeSQL(col, pattern string) string {
	return "stage_lower(" + col + ") LIKE stage_lower(" + pattern + `) ESCAPE '\'`
}
