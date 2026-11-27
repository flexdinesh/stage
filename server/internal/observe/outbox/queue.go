// Package outbox durably queues the observations of one joined worker.
package outbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/observe"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// Leave room for the channel envelope around the encoded batch.
const maxWireBytes = workerchan.MaxPostBytes - 1024

// Limits bounds persisted state and outgoing batches. Zero fields use defaults.
type Limits struct {
	MaxBytes     int // 16 MiB
	MaxRecords   int // 10,000 across sealed and pending
	BatchBytes   int // 256 KiB, except a single oversized record
	BatchRecords int // 100
}

func (l Limits) defaults() Limits {
	if l.MaxBytes == 0 {
		l.MaxBytes = 16 << 20
	}
	if l.MaxRecords == 0 {
		l.MaxRecords = 10_000
	}
	if l.BatchBytes == 0 {
		l.BatchBytes = 256 << 10
	}
	if l.BatchRecords == 0 {
		l.BatchRecords = 100
	}
	return l
}

// Queue is safe for concurrent calls. The worker's directory lock must cover
// Open and the queue's lifetime to keep one writer across processes.
type Queue struct {
	mu sync.Mutex

	dir       string
	producer  string
	limits    Limits
	stateBase int
	batchBase int
	state     diskState
	uncertain bool // rename succeeded but its directory sync failed
}

type priority uint8

const (
	snapshot priority = iota
	protected
)

type sampleKind uint8

const (
	observation sampleKind = iota + 1
	unavailable
	streamStatus
)

type sampleKey struct {
	connection int64
	session    string
	kind       sampleKind
}

type entry struct {
	Data     json.RawMessage `json:"record"`
	Priority priority        `json:"priority,omitempty"`

	key         sampleKey
	attention   bool
	state       string
	executionID string
	closure     string
	observedAt  time.Time
	updatedAt   time.Time
	bytes       int
}

func (e *entry) index(r observe.Record) {
	e.observedAt = r.ObservedAt
	e.key.connection = r.ConnectionID
	e.key.session = r.ExternalSessionID
	switch {
	case r.Observation != nil:
		e.key.kind = observation
		e.attention = r.Attention
		e.state = r.Observation.State
		e.executionID = r.Observation.ExecutionID
		e.updatedAt = r.Observation.UpdatedAt
		if r.Observation.Closed != nil {
			e.closure = r.Observation.Closed.Signal
		}
	case r.Unavailable:
		e.key.kind = unavailable
	case r.StreamError != nil:
		e.key.kind = streamStatus
	}
}

func (e *entry) measure() error {
	b, err := json.Marshal(e)
	e.bytes = len(b)
	return err
}

func protectedState(state string) bool {
	return state == "needs_input" || state == "done" || state == "failed"
}

func last(entries []entry, key sampleKey, attentionOnly bool) int {
	if key.kind == 0 || key.connection <= 0 {
		return -1
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].key == key && (!attentionOnly || entries[i].attention) {
			return i
		}
	}
	return -1
}

func (s diskState) previous(key sampleKey, attentionOnly bool) (entry, int) {
	if i := last(s.Pending, key, attentionOnly); i >= 0 {
		return s.Pending[i], i
	}
	if i := last(s.Sealed, key, attentionOnly); i >= 0 {
		return s.Sealed[i], -1
	}
	return entry{}, -1
}

func sameRun(before, after entry) bool {
	return before.executionID == "" || after.executionID == "" || before.executionID == after.executionID
}

func protects(before, after entry) bool {
	newRun := !sameRun(before, after)
	return (before.state != after.state || newRun) && (protectedState(before.state) || protectedState(after.state)) ||
		after.closure != "" && (before.closure != after.closure || newRun)
}

// replacement finds a redundant snapshot in the current session segment.
// Task and attention samples may interleave, and only attention samples are
// applied for unowned sessions.
func replacement(entries []entry, e entry, previous int) int {
	for i := previous; i >= 0; i-- {
		prior := entries[i]
		if prior.key != e.key {
			continue
		}
		if prior.state != e.state || !sameRun(prior, e) {
			break
		}
		if prior.attention == e.attention {
			if prior.Priority == snapshot && !prior.updatedAt.After(e.updatedAt) {
				return i
			}
			break
		}
	}
	return -1
}

// Producer is the durable producer epoch carried in the worker's batches.
func (q *Queue) Producer() string { return q.producer }

// Lost is the count of observations dropped for space in this producer epoch.
func (q *Queue) Lost() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.state.Lost
}

// ActiveAttention returns discovered sessions whose last queued attention
// sample is active. The restored watch will keep following them offline.
func (q *Queue) ActiveAttention() []execution.SessionRef {
	q.mu.Lock()
	defer q.mu.Unlock()
	active := make(map[execution.SessionRef]bool)
	for _, entries := range [][]entry{q.state.Sealed, q.state.Pending} {
		for _, e := range entries {
			if e.key.kind != observation || !e.attention {
				continue
			}
			ref := execution.SessionRef{ConnectionID: e.key.connection, ExternalSessionID: e.key.session}
			active[ref] = e.state == "running" || e.state == "needs_input"
		}
	}
	refs := make([]execution.SessionRef, 0, len(active))
	for ref, keep := range active {
		if keep {
			refs = append(refs, ref)
		}
	}
	return refs
}

// Enqueue durably records a sample. If the queue cannot fit it, Enqueue
// durably accounts for the loss instead. Cursors are assigned by Seal.
func (q *Queue) Enqueue(r observe.Record) error {
	if r.Gap != nil {
		return errors.New("outbox produces its own gaps")
	}
	if r.ObservedAt.IsZero() {
		return errors.New("outbox requires an observed time")
	}
	r.Cursor = 0
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.ensureSynced(); err != nil {
		return err
	}
	next := q.state
	if len(data)+q.batchBase+2*decimalBytes(math.MaxInt64)+len(`,"cursor":`) > maxWireBytes {
		next.drop(r.ObservedAt)
		return q.save(next)
	}
	e := entry{Data: data}
	e.index(r)
	prior, previous := next.previous(e.key, false)
	if prior.attention == e.attention && prior.updatedAt.After(e.updatedAt) {
		return nil // the shared ingest path would also keep the newer sample
	}
	if protects(prior, e) {
		e.Priority = protected // entry into or first departure from a protected state
	} else if e.attention && !prior.attention {
		// Only attention samples are applied for unowned sessions.
		before, _ := next.previous(e.key, true)
		if protects(before, e) {
			e.Priority = protected
		}
	}
	if err := e.measure(); err != nil {
		return err
	}
	next.Pending = slices.Clone(next.Pending)
	if i := replacement(next.Pending, e, previous); i >= 0 {
		next.Pending = slices.Delete(next.Pending, i, i+1)
	}
	if q.fit(&next, e) {
		next.Pending = append(next.Pending, e)
	} else {
		// Preserve an existing snapshot if its replacement cannot fit.
		next = q.state
		next.drop(e.observedAt)
	}
	return q.save(next)
}

// fit evicts oldest snapshots only if doing so makes room for the new record.
// The staged gap is fixed-size metadata until there is space to seal it.
func (q *Queue) fit(s *diskState, e entry) bool {
	if q.stateBase+e.bytes+1 > q.limits.MaxBytes {
		return false
	}
	count := len(s.Sealed) + len(s.Pending) + 1
	if s.SealedGap != nil {
		count++
	}
	room := q.limits.MaxRecords
	if s.Gap != nil {
		room-- // make the next record slot available for the gap
	}
	// Charge one separator per entry, including one extra per nonempty slice.
	bytes := q.stateBase + charge(s.Sealed) + charge(s.Pending) + e.bytes + 1
	if count <= room && bytes <= q.limits.MaxBytes {
		return true
	}
	var keep []entry
	for _, record := range s.Pending {
		if (count > room || bytes > q.limits.MaxBytes) && record.Priority == snapshot {
			s.drop(record.observedAt)
			count--
			bytes -= record.bytes + 1
			continue
		}
		keep = append(keep, record)
	}
	s.Pending = keep
	return count <= room && bytes <= q.limits.MaxBytes
}

func charge(entries []entry) int {
	bytes := 0
	for _, e := range entries {
		bytes += e.bytes + 1
	}
	return bytes
}

func saturatingIncrement(n uint64) uint64 {
	if n == math.MaxUint64 {
		return n
	}
	return n + 1
}

func (s *diskState) drop(at time.Time) {
	s.Lost = saturatingIncrement(s.Lost)
	gap := observe.Gap{From: at, To: at}
	if s.Gap != nil {
		gap = *s.Gap
		if at.Before(gap.From) {
			gap.From = at
		}
		if at.After(gap.To) {
			gap.To = at
		}
	}
	gap.Count = saturatingIncrement(gap.Count)
	s.Gap = &gap
}

// Seal returns the current immutable batch or seals the next pending prefix.
// Replays include the whole sealed batch, even after a partial acknowledgement.
func (q *Queue) Seal() (observe.Batch, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.ensureSynced(); err != nil {
		return observe.Batch{}, err
	}
	if q.state.sealedCount() > 0 || (len(q.state.Pending) == 0 && q.state.Gap == nil) {
		return q.state.batch()
	}

	next := q.state
	var size, n int
	if next.Gap != nil && len(next.Pending) < q.limits.MaxRecords {
		gap, err := json.Marshal(gapRecord(next.Gap))
		if err != nil {
			return observe.Batch{}, err
		}
		next.SealedGap, next.Gap = next.Gap, nil
		size = recordBytes(len(gap), next.Cursor+1)
	}
	for _, e := range next.Pending {
		count := n
		if next.SealedGap != nil {
			count++
		}
		if count >= q.limits.BatchRecords {
			break
		}
		if next.Cursor >= math.MaxInt64-int64(count) {
			return observe.Batch{}, errors.New("outbox cursor exhausted")
		}
		cursor := next.Cursor + int64(count) + 1
		length := size + recordBytes(len(e.Data), cursor)
		if count != 0 {
			length++ // separator
			if q.batchBase+decimalBytes(cursor)+length > min(q.limits.BatchBytes, maxWireBytes) {
				break
			}
		}
		size = length
		n++
	}
	next.Sealed, next.Pending = next.Pending[:n], next.Pending[n:]
	if next.Cursor > math.MaxInt64-int64(next.sealedCount()) {
		return observe.Batch{}, errors.New("outbox cursor exhausted")
	}
	next.Cursor += int64(next.sealedCount())
	batch, err := next.batch()
	if err != nil {
		return observe.Batch{}, err
	}
	if err := q.save(next); err != nil {
		return observe.Batch{}, err
	}
	return batch, nil
}

func recordBytes(size int, cursor int64) int {
	return size + len(`,"cursor":`) + decimalBytes(cursor)
}

func decimalBytes(cursor int64) int { return len(strconv.FormatInt(cursor, 10)) }

func gapRecord(gap *observe.Gap) observe.Record {
	return observe.Record{ObservedAt: gap.To, Gap: gap}
}

func (s diskState) sealedCount() int {
	if s.SealedGap != nil {
		return len(s.Sealed) + 1
	}
	return len(s.Sealed)
}

func (s diskState) batch() (observe.Batch, error) {
	b := observe.Batch{Producer: s.Producer, Cursor: s.Cursor}
	if s.sealedCount() == 0 {
		return b, nil
	}
	b.Records = make([]observe.Record, 0, s.sealedCount())
	if s.SealedGap != nil {
		gap := *s.SealedGap
		b.Records = append(b.Records, gapRecord(&gap))
	}
	for _, e := range s.Sealed {
		var r observe.Record
		if err := json.Unmarshal(e.Data, &r); err != nil {
			return observe.Batch{}, err
		}
		b.Records = append(b.Records, r)
	}
	for i := range b.Records {
		b.Records[i].Cursor = s.Cursor - int64(len(b.Records)-1-i)
	}
	return b, nil
}

// Ack durably advances the confirmed prefix and releases the fully confirmed
// sealed batch. A stale acknowledgement is safe to repeat.
func (q *Queue) Ack(cursor int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.ensureSynced(); err != nil {
		return err
	}
	if cursor < 0 || cursor > q.state.Cursor {
		return fmt.Errorf("outbox acknowledgement %d is outside assigned cursors", cursor)
	}
	if cursor <= q.state.Acked {
		return nil
	}
	next := q.state
	next.Acked = cursor
	if cursor == next.Cursor {
		next.Sealed = nil
		next.SealedGap = nil
	}
	return q.save(next)
}
