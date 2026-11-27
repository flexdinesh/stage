package outbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/observe"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

const joined = "server:machine:join"

func openQueue(t *testing.T, dir, identity string, limits Limits) *Queue {
	t.Helper()
	q, err := Open(dir, identity, limits)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func sample(session, state string, second int) observe.Record {
	at := time.Date(2026, 11, 26, 9, 0, second, 0, time.UTC)
	return observe.Record{
		ConnectionID: 1, ExternalSessionID: session, ObservedAt: at,
		Observation: &execution.Observation{
			Session: execution.SessionRef{ConnectionID: 1, ExternalSessionID: session},
			Project: "test", State: state, UpdatedAt: at, ExecutionID: "run",
			Active: state == "running" || state == "needs_input",
		},
	}
}

func enqueue(t *testing.T, q *Queue, records ...observe.Record) {
	t.Helper()
	for _, record := range records {
		if err := q.Enqueue(record); err != nil {
			t.Fatal(err)
		}
	}
}

func seal(t *testing.T, q *Queue) observe.Batch {
	t.Helper()
	b, err := q.Seal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ack(t *testing.T, q *Queue, cursor int64) {
	t.Helper()
	if err := q.Ack(cursor); err != nil {
		t.Fatal(err)
	}
}

func checkSamples(t *testing.T, batch observe.Batch, first int64, want ...observe.Record) {
	t.Helper()
	if len(batch.Records) != len(want) {
		t.Fatalf("sealed %d records, want %d", len(batch.Records), len(want))
	}
	for i, got := range batch.Records {
		r := want[i]
		r.Cursor = first + int64(i)
		if !reflect.DeepEqual(got, r) {
			t.Fatalf("record %d = %+v, want %+v", i, got, r)
		}
	}
	if len(want) != 0 && batch.Cursor != first+int64(len(want))-1 {
		t.Fatalf("batch cursor = %d, starting at %d with %d records", batch.Cursor, first, len(want))
	}
}

func TestCoalescesSnapshotsAndKeepsTransitionsInOrder(t *testing.T) {
	dir := t.TempDir()
	q := openQueue(t, dir, joined, Limits{})
	first := sample("a", "running", 1)
	other := sample("b", "running", 2)
	latest := sample("a", "running", 3)
	need := sample("a", "needs_input", 4)
	waiting := sample("a", "needs_input", 5)
	resumed := sample("a", "running", 6)
	stillRunning := sample("a", "running", 7)
	needsAgain := sample("a", "needs_input", 8)
	done := sample("a", "done", 9)
	failed := sample("a", "failed", 10)
	enqueue(t, q, first, other, latest, need, waiting, resumed, stillRunning, needsAgain, done)
	q = openQueue(t, dir, joined, Limits{})
	enqueue(t, q, first, failed) // a delayed read cannot replace or reopen the run
	checkSamples(t, seal(t, q), 1, other, latest, need, waiting, resumed, stillRunning, needsAgain, done, failed)
	if q.Lost() != 0 {
		t.Fatalf("coalescing reported %d lost records", q.Lost())
	}
}

func TestAttentionAndTaskSourcesShareTransitionOrder(t *testing.T) {
	q := openQueue(t, t.TempDir(), joined, Limits{})
	need := sample("a", "needs_input", 1)
	attention := sample("a", "needs_input", 2)
	attention.Attention, attention.Quiet = true, true
	resume := sample("a", "running", 3)
	needsAgain := sample("a", "needs_input", 4)
	needsAgain.Attention = true
	resumeAgain := sample("a", "running", 5)
	enqueue(t, q, need, attention, resume, needsAgain, resumeAgain)
	checkSamples(t, seal(t, q), 1, need, attention, resume, needsAgain, resumeAgain)
}

func TestCoalescingRetainsMeaningfulDepartureAndClosure(t *testing.T) {
	q := openQueue(t, t.TempDir(), joined, Limits{})
	need := sample("a", "needs_input", 1)
	idle := sample("a", "idle", 2)
	running := sample("a", "running", 3)
	idleAgain := sample("a", "idle", 4)
	needsAgain := sample("a", "needs_input", 5)
	done := sample("a", "done", 6)
	closed := sample("a", "done", 7)
	closed.Observation.Closed = &execution.Closure{Signal: execution.ClosedSettled}
	latest := sample("a", "done", 8)
	enqueue(t, q, need, idle, running, idleAgain, needsAgain, done, closed, latest)
	checkSamples(t, seal(t, q), 1, need, idle, running, idleAgain, needsAgain, done, closed, latest)
}

func TestSealedBatchSurvivesPartialAckAndRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outbox")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	limits := Limits{BatchRecords: 2}
	q := openQueue(t, dir, joined, limits)
	a, b, c := sample("a", "running", 1), sample("b", "needs_input", 2), sample("c", "running", 3)
	enqueue(t, q, a, b, c)
	first := seal(t, q)
	checkSamples(t, first, 1, a, b)
	firstBytes := encode(t, first)
	a.Observation.State = "done"
	first.Records[1].Observation.State = "failed"
	newer := sample("a", "done", 4)
	enqueue(t, q, newer)
	ack(t, q, 1)
	if err := q.Ack(3); err == nil {
		t.Fatal("accepted a cursor beyond the sealed batch")
	}
	q = openQueue(t, dir, joined, limits)
	if q.state.Acked != 1 || !reflect.DeepEqual(encode(t, seal(t, q)), firstBytes) {
		t.Fatal("restart lost the durable prefix ack or changed the sealed batch")
	}
	ack(t, q, 2)
	ack(t, q, 1)
	q = openQueue(t, dir, joined, limits)
	checkSamples(t, seal(t, q), 3, c, newer)
	ack(t, q, 4)
	q = openQueue(t, dir, joined, limits)
	if b := seal(t, q); len(b.Records) != 0 || b.Cursor != 4 {
		t.Fatalf("fully acknowledged queue = %+v", b)
	}
	checkMode(t, dir, 0o700)
	checkMode(t, filepath.Join(dir, stateFile), 0o600)
}

func TestConcurrentEnqueuePreservesAllSamples(t *testing.T) {
	dir := t.TempDir()
	q := openQueue(t, dir, joined, Limits{})
	const count = 24
	var writers sync.WaitGroup
	for i := range count {
		writers.Go(func() {
			if err := q.Enqueue(sample(strconv.Itoa(i), "done", i)); err != nil {
				t.Error(err)
			}
		})
	}
	writers.Wait()
	q = openQueue(t, dir, joined, Limits{})
	b := seal(t, q)
	if len(b.Records) != count || b.Cursor != count {
		t.Fatalf("sealed %d records through %d, want %d", len(b.Records), b.Cursor, count)
	}
	seen := make(map[string]bool, count)
	for i, r := range b.Records {
		if seen[r.ExternalSessionID] || r.Cursor != int64(i+1) {
			t.Fatalf("expected a unique session and contiguous cursor, got %+v", r)
		}
		seen[r.ExternalSessionID] = true
	}
}

func TestOverflowPreservesProtectedRecordsAndReportsDurableGap(t *testing.T) {
	dir := t.TempDir()
	limits := Limits{MaxRecords: 3, BatchRecords: 2}
	q := openQueue(t, dir, joined, limits)
	need := sample("a", "needs_input", 1)
	resumed := sample("a", "running", 2)
	done := sample("a", "done", 3)
	droppedA := sample("a", "failed", 4)
	droppedB := sample("b", "needs_input", 5)
	enqueue(t, q, need, resumed, done, droppedA, droppedB)
	q = openQueue(t, dir, joined, limits)
	if q.Lost() != 2 {
		t.Fatalf("lost = %d, want 2", q.Lost())
	}
	first := seal(t, q)
	checkSamples(t, first, 1, need, resumed)
	ack(t, q, first.Cursor)
	gap := gapRecord(&observe.Gap{From: droppedA.ObservedAt, To: droppedB.ObservedAt, Count: 2})
	second := seal(t, q)
	checkSamples(t, second, 3, gap, done)
	q = openQueue(t, dir, joined, limits)
	checkSamples(t, seal(t, q), 3, gap, done)
	ack(t, q, second.Cursor)
	if b := seal(t, q); len(b.Records) != 0 || q.Lost() != 2 {
		t.Fatalf("gap acknowledgement left a batch or removed loss count: %+v, lost %d", b, q.Lost())
	}
}

func TestOverflowReclaimsSnapshotsBeforeProtectedTransitions(t *testing.T) {
	dir := t.TempDir()
	q := openQueue(t, dir, joined, Limits{MaxRecords: 3})
	a, b := sample("a", "running", 1), sample("b", "running", 2)
	need, done := sample("c", "needs_input", 3), sample("d", "done", 4)
	enqueue(t, q, a, b, need, done)
	checkSamples(t, seal(t, q), 1, b, need, done)
	ack(t, q, 3)
	gap := gapRecord(&observe.Gap{From: a.ObservedAt, To: a.ObservedAt, Count: 1})
	checkSamples(t, seal(t, q), 4, gap)
}

func TestNewLossCannotMutateSealedGap(t *testing.T) {
	dir := t.TempDir()
	limits := Limits{MaxRecords: 1}
	q := openQueue(t, dir, joined, limits)
	first := sample("a", "done", 1)
	second := sample("a", "failed", 2)
	third := sample("b", "needs_input", 3)
	enqueue(t, q, first, second)
	checkSamples(t, seal(t, q), 1, first)
	ack(t, q, 1)
	firstGap := gapRecord(&observe.Gap{From: second.ObservedAt, To: second.ObservedAt, Count: 1})
	checkSamples(t, seal(t, q), 2, firstGap)
	enqueue(t, q, third)
	q = openQueue(t, dir, joined, limits)
	checkSamples(t, seal(t, q), 2, firstGap)
	ack(t, q, 2)
	checkSamples(t, seal(t, q), 3, gapRecord(&observe.Gap{From: third.ObservedAt, To: third.ObservedAt, Count: 1}))
}

func TestBatchAndHardByteLimits(t *testing.T) {
	t.Run("single oversized record within both hard limits", func(t *testing.T) {
		dir := t.TempDir()
		limits := Limits{MaxBytes: 8192, BatchBytes: 1000}
		q := openQueue(t, dir, joined, limits)
		long := sample("large", "done", 1)
		long.Observation.Title = strings.Repeat("<", 800) // JSON escaping also counts
		good := sample("small", "running", 2)
		enqueue(t, q, long, good)
		checkSize(t, dir, limits.MaxBytes)
		batch := seal(t, q)
		checkSamples(t, batch, 1, long)
		wire := encode(t, workerchan.Envelope{ID: "w1", Kind: observe.KindBatch, Body: encode(t, batch)})
		if len(wire) <= limits.BatchBytes || len(wire) > workerchan.MaxPostBytes {
			t.Fatalf("oversized record envelope = %d bytes", len(wire))
		}
		checkSize(t, dir, limits.MaxBytes)
		ack(t, q, 1)
		checkSamples(t, seal(t, q), 2, good)
	})
	t.Run("batch byte limit splits otherwise valid samples", func(t *testing.T) {
		q := openQueue(t, t.TempDir(), joined, Limits{BatchBytes: 650})
		x, y := sample("a", "running", 1), sample("b", "running", 2)
		enqueue(t, q, x, y)
		first := seal(t, q)
		checkSamples(t, first, 1, x)
		if n := len(encode(t, first)); n > 650 {
			t.Fatalf("one sample = %d bytes, expected it to be within the batch limit", n)
		}
		ack(t, q, first.Cursor)
		checkSamples(t, seal(t, q), 2, y)
	})
	t.Run("record beyond hard outbox bound becomes a gap", func(t *testing.T) {
		dir := t.TempDir()
		limits := Limits{MaxBytes: 1400}
		q := openQueue(t, dir, joined, limits)
		good := sample("a", "running", 1)
		long := sample("a", "running", 2)
		long.Observation.Title = strings.Repeat("x", limits.MaxBytes)
		enqueue(t, q, good, long)
		checkSize(t, dir, limits.MaxBytes)
		q = openQueue(t, dir, joined, limits)
		gap := gapRecord(&observe.Gap{From: long.ObservedAt, To: long.ObservedAt, Count: 1})
		checkSamples(t, seal(t, q), 1, gap, good)
		checkSize(t, dir, limits.MaxBytes)
	})
	t.Run("record beyond transport bound becomes a gap", func(t *testing.T) {
		q := openQueue(t, t.TempDir(), joined, Limits{MaxBytes: 40 << 20})
		long := sample("a", "done", 1)
		long.Observation.Title = strings.Repeat("\x01", workerchan.MaxPostBytes/6+1)
		enqueue(t, q, long)
		gap := gapRecord(&observe.Gap{From: long.ObservedAt, To: long.ObservedAt, Count: 1})
		checkSamples(t, seal(t, q), 1, gap)
	})
}

func TestJoinIdentityChangesProducerAndDiscardsPriorRecords(t *testing.T) {
	dir := t.TempDir()
	q := openQueue(t, dir, joined, Limits{})
	enqueue(t, q, sample("a", "done", 1))
	checkSamples(t, seal(t, q), 1, sample("a", "done", 1))
	replacement := openQueue(t, dir, joined+":replacement", Limits{})
	b := seal(t, replacement)
	if b.Producer == q.Producer() || len(b.Records) != 0 || b.Cursor != 0 {
		t.Fatalf("replacement reused old queue: %+v", b)
	}
}

func TestCorruptFileIsDistinguishedFromIOFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, stateFile)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, joined, Limits{}); err == nil || errors.Is(err, ErrCorrupt) {
		t.Fatalf("opening a directory as state = %v, want I/O error", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	bad := []byte("{invalid")
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, joined, Limits{}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("invalid state = %v, want ErrCorrupt", err)
	}
	if got, err := os.ReadFile(path); err != nil || !reflect.DeepEqual(got, bad) {
		t.Fatalf("invalid state changed: data %q, err %v", got, err)
	}
}

func TestFailedWriteLeavesQueueRetryable(t *testing.T) {
	dir := t.TempDir()
	q := openQueue(t, dir, joined, Limits{})
	before := sample("a", "needs_input", 1)
	after := sample("a", "running", 2)
	enqueue(t, q, before)
	path := filepath.Join(dir, tempFile)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(after); err == nil {
		t.Fatal("write should fail before replacing the state file")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	checkSamples(t, seal(t, q), 1, before)
	enqueue(t, q, after)
	ack(t, q, 1)
	checkSamples(t, seal(t, q), 2, after)
}

func encode(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func checkMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != mode {
		t.Fatalf("%s mode = %o, want %o", path, info.Mode().Perm(), mode)
	}
}

func checkSize(t *testing.T, dir string, max int) {
	t.Helper()
	info, err := os.Stat(filepath.Join(dir, stateFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > int64(max) {
		t.Fatalf("persisted state = %d bytes, limit %d", info.Size(), max)
	}
}
