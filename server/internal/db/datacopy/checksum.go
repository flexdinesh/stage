package datacopy

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"slices"
	"sort"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// A table checksum is the hex sha256 of its rows' sha256 digests, sorted:
// independent of row order (engines order and collate differently), exact
// on the multiset of rows. A row digest hashes each value's canonical
// encoding in column order:
//
//   - NULL, int64, bool, float64 (IEEE bits), string and []byte as tagged,
//     length-prefixed bytes;
//   - time.Time as UTC Unix microseconds (the storage precision);
//   - JSON (queried, jsonb on Postgres) by value: objects with sorted keys,
//     numbers as exact rationals, no whitespace. jsonb reorders keys,
//     normalizes numbers and drops whitespace, so bytes cannot be compared.
//     Evidence JSON is text and compared byte for byte.

// tableSum accumulates one table's rows.
type tableSum struct {
	t        store.CopyTable
	identity int // column index of t.Identity, -1 for none
	rows     int64
	maxID    int64
	digests  [][sha256.Size]byte
}

func newSum(t store.CopyTable) tableSum {
	s := tableSum{t: t, identity: -1}
	for i, c := range t.Columns {
		if c.Name == t.Identity && t.Identity != "" {
			s.identity = i
		}
	}
	return s
}

func (s *tableSum) add(row store.CopyRow) error {
	if len(row) != len(s.t.Columns) {
		return fmt.Errorf("%s: row has %d values for %d columns", s.t.Name, len(row), len(s.t.Columns))
	}
	h := sha256.New()
	var buf []byte
	for i, c := range s.t.Columns {
		var err error
		buf, err = appendValue(buf[:0], row[i], c.Kind)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", s.t.Name, c.Name, err)
		}
		h.Write(buf)
	}
	var d [sha256.Size]byte
	h.Sum(d[:0])
	s.digests = append(s.digests, d)
	s.rows++
	if s.identity >= 0 {
		if id, ok := row[s.identity].(int64); ok && id > s.maxID {
			s.maxID = id
		}
	}
	return nil
}

func (s *tableSum) checksum() string {
	ds := slices.Clone(s.digests)
	slices.SortFunc(ds, func(a, b [sha256.Size]byte) int { return bytes.Compare(a[:], b[:]) })
	h := sha256.New()
	for _, d := range ds {
		h.Write(d[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// appendValue appends v's canonical encoding; v must be nil or kind's Go
// type.
func appendValue(b []byte, v any, kind store.CopyKind) ([]byte, error) {
	if v == nil {
		return append(b, 'n'), nil
	}
	switch kind {
	case store.CopyInt:
		if n, ok := v.(int64); ok {
			return binary.BigEndian.AppendUint64(append(b, 'i'), uint64(n)), nil
		}
	case store.CopyBool:
		if t, ok := v.(bool); ok {
			if t {
				return append(b, 'b', 1), nil
			}
			return append(b, 'b', 0), nil
		}
	case store.CopyReal:
		if f, ok := v.(float64); ok {
			return binary.BigEndian.AppendUint64(append(b, 'r'), math.Float64bits(f)), nil
		}
	case store.CopyText:
		if s, ok := v.(string); ok {
			return appendBytes(append(b, 's'), []byte(s)), nil
		}
	case store.CopyTime:
		if t, ok := v.(time.Time); ok {
			return binary.BigEndian.AppendUint64(append(b, 't'), uint64(t.UTC().Truncate(time.Microsecond).UnixMicro())), nil
		}
	case store.CopyJSON:
		if j, ok := v.(json.RawMessage); ok {
			c, err := canonicalJSON(j)
			if err != nil {
				return nil, err
			}
			return appendBytes(append(b, 'j'), c), nil
		}
	case store.CopyBytes:
		if x, ok := v.([]byte); ok {
			return appendBytes(append(b, 'x'), x), nil
		}
	}
	return nil, fmt.Errorf("%T value for a %s column", v, kind)
}

func appendBytes(b, v []byte) []byte {
	return append(binary.BigEndian.AppendUint64(b, uint64(len(v))), v...)
}

// canonicalJSON re-encodes JSON by value: sorted object keys, numbers as
// exact rationals (1.0, 1e0 and 1 are one value), no whitespace.
func canonicalJSON(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("invalid JSON: trailing data")
	}
	var out bytes.Buffer
	if err := writeCanonical(&out, v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeCanonical(w *bytes.Buffer, v any) error {
	switch v := v.(type) {
	case nil:
		w.WriteString("null")
	case bool:
		if v {
			w.WriteString("true")
		} else {
			w.WriteString("false")
		}
	case json.Number:
		r, ok := new(big.Rat).SetString(v.String())
		if !ok {
			return fmt.Errorf("invalid JSON number %s", v)
		}
		w.WriteString(r.RatString())
	case string:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		w.Write(b)
	case []any:
		w.WriteByte('[')
		for i, e := range v {
			if i > 0 {
				w.WriteByte(',')
			}
			if err := writeCanonical(w, e); err != nil {
				return err
			}
		}
		w.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		w.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				w.WriteByte(',')
			}
			b, err := json.Marshal(k)
			if err != nil {
				return err
			}
			w.Write(b)
			w.WriteByte(':')
			if err := writeCanonical(w, v[k]); err != nil {
				return err
			}
		}
		w.WriteByte('}')
	default:
		return fmt.Errorf("unexpected JSON value %T", v)
	}
	return nil
}
