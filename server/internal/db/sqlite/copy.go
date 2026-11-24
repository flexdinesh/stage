package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/pressly/goose/v3"
)

// CopySource is a SQLite database opened read-only for `stage db copy`
// (docs/storage.md "Data copy and cutover"): the connection cannot write
// (mode=ro, query_only), so the file is left as it was.
type CopySource struct {
	db *DB
}

var _ store.CopySource = (*CopySource)(nil)

// OpenCopySource opens the existing SQLite database at path read-only. The
// caller holds the data dir's lock, so nothing writes it meanwhile. Without
// a WAL file (a cleanly stopped server checkpoints and removes it) it opens
// immutable: no locking and no -wal or -shm file, so the directory stays as
// it was. With one (a server that crashed) it opens mode=ro, which reads the
// WAL's commits and may create a -shm file.
func OpenCopySource(ctx context.Context, path string) (*CopySource, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("source database: %w", err)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("source database %s is not a file", path)
	}
	q := url.Values{}
	q.Set("mode", "ro")
	if _, err := os.Stat(path + "-wal"); errors.Is(err, os.ErrNotExist) {
		q.Set("immutable", "1")
	}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMs))
	q.Add("_pragma", "query_only(1)")
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	pool, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open source database: %w", err)
	}
	if err := pool.PingContext(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("open source database %s: %w", path, err)
	}
	return &CopySource{db: &DB{DB: pool, path: path}}, nil
}

// Close closes the database.
func (s *CopySource) Close() error { return s.db.Close() }

func (s *CopySource) Version(ctx context.Context) (int64, error) {
	return CurrentVersion(ctx, s.db)
}

// Tables lists the Stage tables: not SQLite's own (sqlite_*), goose's
// version table, or the FTS5 search tables and their shadow tables, which
// Postgres replaces with trigram indexes over the copied rows.
func (s *CopySource) Tables(ctx context.Context) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, COALESCE(sql, '') FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("source tables: %w", err)
	}
	type entry struct{ name, ddl string }
	var all []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.name, &e.ddl); err != nil {
			rows.Close()
			return nil, fmt.Errorf("source tables: %w", err)
		}
		all = append(all, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("source tables: %w", err)
	}
	var virtual []string
	for _, e := range all {
		if strings.HasPrefix(strings.ToUpper(e.ddl), "CREATE VIRTUAL TABLE") {
			virtual = append(virtual, e.name)
		}
	}
	shadow := func(name string) bool {
		for _, v := range virtual {
			if name == v || strings.HasPrefix(name, v+"_") {
				return true
			}
		}
		return false
	}
	tables := map[string][]string{}
	for _, e := range all {
		if e.name == goose.DefaultTablename || shadow(e.name) {
			continue
		}
		cols, err := s.columns(ctx, e.name)
		if err != nil {
			return nil, err
		}
		tables[e.name] = cols
	}
	return tables, nil
}

func (s *CopySource) columns(ctx context.Context, table string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return nil, fmt.Errorf("source columns of %s: %w", table, err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("source columns of %s: %w", table, err)
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// Sequences reads sqlite_sequence (absent until an AUTOINCREMENT table
// first issues an ID).
func (s *CopySource) Sequences(ctx context.Context) (map[string]int64, error) {
	seqs := map[string]int64{}
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'sqlite_sequence'`).Scan(&n); err != nil {
		return nil, fmt.Errorf("source sequences: %w", err)
	}
	if n == 0 {
		return seqs, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, seq FROM sqlite_sequence`)
	if err != nil {
		return nil, fmt.Errorf("source sequences: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var seq int64
		if err := rows.Scan(&name, &seq); err != nil {
			return nil, fmt.Errorf("source sequences: %w", err)
		}
		seqs[name] = seq
	}
	return seqs, rows.Err()
}

// Rows reads t's columns and converts each value from its SQLite encoding
// (docs/storage.md "Encodings"): TEXT time to time.Time (UTC,
// microseconds), INTEGER 0/1 to bool, TEXT JSON to json.RawMessage (must be
// valid), BLOB to []byte; text must be valid UTF-8 without NUL (Postgres
// text cannot hold either).
func (s *CopySource) Rows(ctx context.Context, t store.CopyTable, fn func(store.CopyRow) error) error {
	names := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		names[i] = quoteIdent(c.Name)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+strings.Join(names, ", ")+` FROM `+quoteIdent(t.Name))
	if err != nil {
		return fmt.Errorf("read %s: %w", t.Name, err)
	}
	defer rows.Close()
	raw := make([]any, len(t.Columns))
	dest := make([]any, len(t.Columns))
	for i := range raw {
		dest[i] = &raw[i]
	}
	for n := 1; rows.Next(); n++ {
		if err := rows.Scan(dest...); err != nil {
			return fmt.Errorf("read %s: %w", t.Name, err)
		}
		row := make(store.CopyRow, len(t.Columns))
		for i, c := range t.Columns {
			v, err := copyValue(raw[i], c.Kind)
			if err != nil {
				return fmt.Errorf("%s.%s, row %d: %w", t.Name, c.Name, n, err)
			}
			row[i] = v
		}
		if err := fn(row); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read %s: %w", t.Name, err)
	}
	return nil
}

// copyValue converts one SQLite value (nil, int64, float64, string, []byte)
// to kind's Go type.
func copyValue(v any, kind store.CopyKind) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch kind {
	case store.CopyInt:
		if n, ok := v.(int64); ok {
			return n, nil
		}
	case store.CopyBool:
		if n, ok := v.(int64); ok {
			switch n {
			case 0:
				return false, nil
			case 1:
				return true, nil
			}
			return nil, fmt.Errorf("boolean %d is not 0 or 1", n)
		}
	case store.CopyReal:
		switch n := v.(type) {
		case float64:
			return n, nil
		case int64:
			return float64(n), nil
		}
	case store.CopyText:
		s, ok := textOf(v)
		if !ok {
			break
		}
		if err := checkText(s); err != nil {
			return nil, err
		}
		return s, nil
	case store.CopyTime:
		if _, ok := v.(int64); ok {
			break
		}
		if _, ok := v.(float64); ok {
			break
		}
		t, err := parseTime(v)
		if err != nil {
			return nil, fmt.Errorf("timestamp %q: %w", fmt.Sprint(v), err)
		}
		return t.UTC().Truncate(time.Microsecond), nil
	case store.CopyJSON:
		s, ok := textOf(v)
		if !ok {
			break
		}
		if !json.Valid([]byte(s)) {
			return nil, errors.New("invalid JSON")
		}
		return json.RawMessage(s), nil
	case store.CopyBytes:
		switch b := v.(type) {
		case []byte:
			return b, nil
		case string:
			return []byte(b), nil
		}
	}
	return nil, fmt.Errorf("SQLite %T value does not convert to %s", v, kind)
}

func textOf(v any) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, true
	case []byte:
		return string(s), true
	}
	return "", false
}

func checkText(s string) error {
	if !utf8.ValidString(s) {
		return errors.New("text is not valid UTF-8")
	}
	if strings.ContainsRune(s, 0) {
		return errors.New("text contains a NUL character")
	}
	return nil
}

// quoteIdent quotes a SQLite identifier.
func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
