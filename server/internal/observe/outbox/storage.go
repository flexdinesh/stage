package outbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/flexdinesh/stage/server/internal/observe"
	"github.com/google/uuid"
)

const (
	stateFile = "state.json"
	tempFile  = "state.tmp"
	schema    = 1
)

// ErrCorrupt reports an invalid persisted outbox. An I/O failure is returned
// separately so a caller can retry without discarding the queue.
var ErrCorrupt = errors.New("corrupt outbox")

type diskState struct {
	Schema    int          `json:"schema"`
	Identity  string       `json:"identity"`
	Producer  string       `json:"producer"`
	Acked     int64        `json:"acked"`
	Cursor    int64        `json:"cursor"`
	Lost      uint64       `json:"lost"`
	Gap       *observe.Gap `json:"gap,omitempty"`
	SealedGap *observe.Gap `json:"sealedGap,omitempty"`
	Sealed    []entry      `json:"sealed,omitempty"`
	Pending   []entry      `json:"pending,omitempty"`
}

// Open loads or creates the queue for a joined worker. A different identity
// starts an empty queue with a fresh producer. A corrupt file returns an error.
func Open(dir, identity string, limits Limits) (*Queue, error) {
	if identity == "" {
		return nil, errors.New("outbox requires a joined identity")
	}
	limits = limits.defaults()
	if limits.MaxBytes < 1 || limits.MaxBytes == math.MaxInt || limits.MaxRecords < 1 || limits.BatchBytes < 1 || limits.BatchRecords < 1 {
		return nil, errors.New("outbox limits must be positive")
	}
	digest := sha256.Sum256([]byte(identity))
	id := hex.EncodeToString(digest[:])
	q := &Queue{dir: dir, limits: limits}
	next := diskState{Schema: schema, Identity: id}
	var err error
	q.stateBase, err = baseStateBytes(next)
	if err != nil {
		return nil, err
	}
	if limits.MaxBytes < q.stateBase {
		return nil, errors.New("outbox limit is too small for gap metadata")
	}
	if err := openDir(dir); err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(dir, tempFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	loaded, ok, err := q.read()
	if err != nil {
		return nil, fmt.Errorf("load outbox: %w", err)
	}
	if ok && loaded.Identity == id {
		if err := q.check(loaded); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		next = loaded
		q.state = loaded
	} else {
		producer, err := uuid.NewRandom()
		if err != nil {
			return nil, err
		}
		next.Producer = producer.String()
		if err := q.save(next); err != nil {
			return nil, err
		}
	}
	q.producer = next.Producer
	b, err := json.Marshal(observe.Batch{Producer: q.producer, Cursor: 1, Records: []observe.Record{}})
	if err != nil {
		return nil, err
	}
	q.batchBase = len(b) - 1 // exclude the cursor's decimal digits
	return q, nil
}

// Reserve metadata at its maximum encoded size. The reserve covers another
// gap even when a previous gap is sealed, and cursor growth needs no eviction.
func baseStateBytes(s diskState) (int, error) {
	at := time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.FixedZone("", 23*60*60+59*60))
	gap := observe.Gap{From: at, To: at, Count: math.MaxUint64}
	s.Producer = uuid.Nil.String()
	s.Acked, s.Cursor, s.Lost = math.MaxInt64, math.MaxInt64, math.MaxUint64
	s.Gap, s.SealedGap = &gap, &gap
	b, err := json.Marshal(s)
	return len(b) + len(`,"sealed":[],"pending":[]`), err
}

func openDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("outbox path is not a directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	// Persist the directory entry before relying on renames inside it.
	return syncDir(filepath.Dir(dir))
}

func (q *Queue) read() (diskState, bool, error) {
	path := filepath.Join(q.dir, stateFile)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return diskState{}, false, nil
	}
	if err != nil {
		return diskState{}, false, err
	}
	if !info.Mode().IsRegular() {
		return diskState{}, false, errors.New("outbox state path is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return diskState{}, false, err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return diskState{}, false, err
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(q.limits.MaxBytes)+1))
	if err != nil {
		return diskState{}, false, err
	}
	if len(b) > q.limits.MaxBytes {
		return diskState{}, false, fmt.Errorf("%w: persisted state exceeds byte limit", ErrCorrupt)
	}
	var s diskState
	if err := json.Unmarshal(b, &s); err != nil {
		return diskState{}, false, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if s.Identity == "" {
		return diskState{}, false, fmt.Errorf("%w: missing identity", ErrCorrupt)
	}
	return s, true, nil
}

func (q *Queue) check(s diskState) error {
	if s.Schema != schema {
		return fmt.Errorf("unsupported outbox schema %d", s.Schema)
	}
	producer, err := uuid.Parse(s.Producer)
	if err != nil || producer == uuid.Nil || producer.String() != s.Producer {
		return errors.New("invalid outbox producer")
	}
	count := s.sealedCount()
	if s.Acked < 0 || s.Acked > s.Cursor || s.Cursor < int64(count) ||
		(count == 0 && s.Acked != s.Cursor) ||
		(count > 0 && (s.Acked >= s.Cursor || s.Acked < s.Cursor-int64(count))) {
		return errors.New("invalid outbox cursors")
	}
	if count+len(s.Pending) > q.limits.MaxRecords {
		return errors.New("persisted state exceeds record limit")
	}
	for _, gap := range []*observe.Gap{s.Gap, s.SealedGap} {
		if gap != nil && (gap.Count == 0 || gap.Count > s.Lost || gap.To.Before(gap.From)) {
			return errors.New("invalid outbox gap")
		}
	}
	for _, entries := range [][]entry{s.Sealed, s.Pending} {
		for i := range entries {
			e := &entries[i]
			var r observe.Record
			if err := json.Unmarshal(e.Data, &r); err != nil {
				return err
			}
			if e.Priority > protected || r.Cursor != 0 || r.Gap != nil || r.ObservedAt.IsZero() {
				return errors.New("invalid stored outbox record")
			}
			e.index(r)
			if err := e.measure(); err != nil {
				return err
			}
		}
	}
	if q.stateBase+charge(s.Sealed)+charge(s.Pending) > q.limits.MaxBytes {
		return errors.New("persisted state exceeds outbox budget")
	}
	return nil
}

func (q *Queue) save(next diskState) error {
	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if len(b) > q.limits.MaxBytes {
		return errors.New("outbox state exceeds byte limit")
	}
	if err := replace(q.dir, b); err != nil {
		return fmt.Errorf("persist outbox: %w", err)
	}
	q.state = next
	q.uncertain = true
	return q.ensureSynced()
}

func (q *Queue) ensureSynced() error {
	if !q.uncertain {
		return nil
	}
	if err := syncDir(q.dir); err != nil {
		return fmt.Errorf("sync outbox directory: %w", err)
	}
	q.uncertain = false
	return nil
}

func replace(dir string, data []byte) error {
	path := filepath.Join(dir, tempFile)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(path)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(dir, stateFile))
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
