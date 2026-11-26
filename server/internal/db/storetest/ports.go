package storetest

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Port-level cases: the engine's unit of work, error classification and
// encodings (docs/storage.md "Ports", "Encodings", "Errors"), reached
// through the ports and through db.Store.
func init() {
	register("PortUnitOfWork", casePortUnitOfWork)
	register("PortErrors", casePortErrors)
	register("PortOrdering", casePortOrdering)
	register("PortTimeMicroseconds", casePortTimeMicroseconds)
	register("PortJSONEvidenceBytes", casePortJSONEvidenceBytes)
	register("PortBlobs", casePortBlobs)
	register("PortUnicode", casePortUnicode)
}

func snippetPatch(name, body string, project *int64) store.SnippetPatch {
	p := store.SnippetPatch{Name: store.Optional[string]{Set: true, V: name}, Body: store.Optional[string]{Set: true, V: body}}
	if project != nil {
		p.ProjectID = store.Optional[int64]{Set: true, V: *project}
	}
	return p
}

// Write commits when fn returns nil and rolls back on error or panic,
// leaving the engine usable; Read sees committed writes.
func casePortUnitOfWork(t *testing.T, open Factory) {
	e := open(t)
	ctx := context.Background()
	snippets := func() []store.Snippet {
		t.Helper()
		var out []store.Snippet
		if err := e.Read(ctx, func(tx store.Tx) error {
			var err error
			out, err = tx.Snippets().List(ctx)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}

	boom := errors.New("boom")
	err := e.Write(ctx, func(tx store.Tx) error {
		if _, err := tx.Snippets().Create(ctx, snippetPatch("rolled-back", "b", nil)); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("write error = %v, want fn's error", err)
	}
	if got := snippets(); len(got) != 0 {
		t.Fatalf("failed write left %+v", got)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic in fn did not propagate")
			}
		}()
		_ = e.Write(ctx, func(tx store.Tx) error {
			if _, err := tx.Snippets().Create(ctx, snippetPatch("panicked", "b", nil)); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if got := snippets(); len(got) != 0 {
		t.Fatalf("panicked write left %+v", got)
	}

	var created store.Snippet
	if err := e.Write(ctx, func(tx store.Tx) error {
		var err error
		if created, err = tx.Snippets().Create(ctx, snippetPatch("kept", "b", nil)); err != nil {
			return err
		}
		// A write sees its own writes.
		got, err := tx.Snippets().Get(ctx, created.ID)
		if err != nil || got.Name != "kept" {
			t.Errorf("read own write = %+v %v", got, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("write after rollbacks: %v", err)
	}
	if got := snippets(); len(got) != 1 || got[0].ID != created.ID {
		t.Fatalf("committed write = %+v", got)
	}
}

// Unique and foreign-key violations come back from repositories unchanged
// and the engine's ErrorClassifier names them; rules turn them into app
// errors. A missing row is ErrNotFound at both levels.
func casePortErrors(t *testing.T, open Factory) {
	e := open(t)
	ctx := context.Background()
	write := func(p store.SnippetPatch) error {
		return e.Write(ctx, func(tx store.Tx) error {
			_, err := tx.Snippets().Create(ctx, p)
			return err
		})
	}
	if err := write(snippetPatch("dup", "first", nil)); err != nil {
		t.Fatal(err)
	}
	classes := e.Errors()

	err := write(snippetPatch("dup", "second", nil))
	if err == nil || !classes.IsUniqueViolation(err) || classes.IsForeignKeyViolation(err) {
		t.Fatalf("duplicate name: %v (unique %v, foreign key %v)", err, classes.IsUniqueViolation(err), classes.IsForeignKeyViolation(err))
	}
	missing := int64(999999)
	err = write(snippetPatch("orphan", "b", &missing))
	if err == nil || !classes.IsForeignKeyViolation(err) || classes.IsUniqueViolation(err) {
		t.Fatalf("unknown project: %v (unique %v, foreign key %v)", err, classes.IsUniqueViolation(err), classes.IsForeignKeyViolation(err))
	}
	if classes.IsUniqueViolation(errors.New("x")) || classes.IsForeignKeyViolation(errors.New("x")) || classes.IsUniqueViolation(nil) {
		t.Fatal("classifier matched a non-engine error")
	}
	err = e.Read(ctx, func(tx store.Tx) error {
		_, err := tx.Snippets().Get(ctx, missing)
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing snippet via port = %v, want ErrNotFound", err)
	}

	s := db.NewStore(e)
	var conflict *db.ConflictError
	if _, err := s.CreateSnippet(ctx, snippetPatch("dup", "third", nil)); !errors.As(err, &conflict) {
		t.Fatalf("duplicate via Store = %v, want conflict", err)
	}
	var invalid *db.InvalidError
	if _, err := s.CreateSnippet(ctx, snippetPatch("orphan", "b", &missing)); !errors.As(err, &invalid) {
		t.Fatalf("unknown project via Store = %v, want invalid", err)
	}
	if _, err := s.GetSnippet(ctx, missing); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing snippet via Store = %v", err)
	}
	if _, err := s.GetTask(ctx, missing); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("missing task via Store = %v", err)
	}
	if got, err := s.ListSnippets(ctx); err != nil || len(got) != 1 {
		t.Fatalf("rejected writes left %+v %v", got, err)
	}
}

// Ordering a repository documents holds on every engine (journal paging:
// JournalConcurrentRetryAndPagination).
func casePortOrdering(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	beta := mustProject(t, s, ctx, "beta").ID
	alpha := mustProject(t, s, ctx, "alpha").ID
	for _, sn := range []struct {
		name    string
		project *int64
	}{{"m", &beta}, {"z", nil}, {"b", &alpha}, {"a", nil}, {"c", &beta}} {
		if _, err := s.CreateSnippet(ctx, snippetPatch(sn.name, "body", sn.project)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListSnippets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, sn := range got {
		names = append(names, sn.Name)
	}
	// Global first, then by project title, then name.
	if want := []string{"a", "z", "b", "c", "m"}; !slices.Equal(names, want) {
		t.Fatalf("snippets %v, want %v", names, want)
	}

	sc := mustSchedule(t, s, ctx, alpha, "*/5 * * * *")
	var fired []time.Time
	for range 3 {
		next, err := s.GetSchedule(ctx, sc.ID)
		if err != nil || next.NextRunAt == nil {
			t.Fatalf("next run %+v %v", next.NextRunAt, err)
		}
		res := mustFire(t, s, ctx, sc.ID, next.NextRunAt.Add(time.Second))
		if res.TaskID == nil {
			t.Fatalf("fire %v = %+v", *next.NextRunAt, res)
		}
		fired = append(fired, *next.NextRunAt)
		// Settle the spawned task so the next fire spawns again.
		setState(t, s, ctx, *res.TaskID, db.StateCancelled)
	}
	runs, err := s.ScheduleRuns(ctx, sc.ID, 2)
	if err != nil || len(runs) != 2 {
		t.Fatalf("limited runs %+v %v", runs, err)
	}
	// Newest first, limited.
	if !runs[0].FireAt.Equal(fired[2]) || !runs[1].FireAt.Equal(fired[1]) {
		t.Fatalf("runs %v then %v, fired %v", runs[0].FireAt, runs[1].FireAt, fired)
	}
}

// Times cross the port boundary as microseconds in UTC: a nanosecond value
// in another zone reads back equal to its microsecond truncation, and
// values a microsecond apart keep their order.
func casePortTimeMicroseconds(t *testing.T, open Factory) {
	e := open(t)
	ctx := context.Background()
	zone := time.FixedZone("plus0530", 5*3600+30*60)
	in := time.Date(2031, 7, 4, 12, 34, 56, 123456789, zone)
	want := in.Truncate(time.Microsecond).UTC()
	hash := bytes.Repeat([]byte{7}, 32)

	var pairing, machine int64
	if err := e.Write(ctx, func(tx store.Tx) error {
		var err error
		if pairing, err = tx.Machines().InsertPairing(ctx, hash, "code", db.DefaultUser, in); err != nil {
			return err
		}
		if machine, err = tx.Machines().InsertMachine(ctx, "worker", db.DefaultUser, "v1"); err != nil {
			return err
		}
		// Touch never moves back: one microsecond decides.
		if err := tx.Machines().Touch(ctx, machine, in.Add(time.Microsecond), ""); err != nil {
			return err
		}
		return tx.Machines().Touch(ctx, machine, in, "")
	}); err != nil {
		t.Fatal(err)
	}
	var row store.PairingRow
	var info []store.MachineInfo
	if err := e.Read(ctx, func(tx store.Tx) error {
		var err error
		if row, err = tx.Machines().Pairing(ctx, pairing); err != nil {
			return err
		}
		info, err = tx.Machines().List(ctx, machine)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !row.ExpiresAt.Equal(want) || row.ExpiresAt.Location() != time.UTC || row.ExpiresAt.Nanosecond() != 123456000 {
		t.Fatalf("expiresAt %v (%v), want %v UTC", row.ExpiresAt, row.ExpiresAt.Location(), want)
	}
	if !row.Live {
		t.Fatal("future pairing not live")
	}
	if len(info) != 1 || info[0].LastSeenAt == nil || !info[0].LastSeenAt.Equal(want.Add(time.Microsecond)) {
		t.Fatalf("lastSeenAt %+v, want %v", info, want.Add(time.Microsecond))
	}

	// Through the Store: a task's one-shot start.
	s := db.NewStore(e)
	p := mustProject(t, s, ctx, "p")
	tz := "Asia/Kolkata"
	task, err := s.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: p.ID, Title: "later", Prompt: "p", StartAt: &in, StartTz: &tz})
	if err != nil {
		t.Fatal(err)
	}
	got := getTask(t, s, ctx, task.ID)
	if got.StartAt == nil || !got.StartAt.Equal(want) || got.StartAt.Location() != time.UTC {
		t.Fatalf("startAt %v, want %v UTC", got.StartAt, want)
	}
}

// Evidence JSON keeps its exact bytes (key order, whitespace, number
// spelling); queried JSON round-trips its value.
func casePortJSONEvidenceBytes(t *testing.T, open Factory) {
	e := open(t)
	ctx := context.Background()
	s := db.NewStore(e)
	p := mustProject(t, s, ctx, "p")
	task := mustTask(t, s, ctx, p.ID, "t", nil)
	input := "{\"z\":1,  \"a\":\"café \U0001F600\",\n\t\"n\":[1.50, 1e3, -0], \"esc\":\"\\u00e9\\n\"}"
	acc := int64(42)
	result := store.AcceptanceResult{Revision: 3, AcceptanceID: &acc}
	if err := e.Write(ctx, func(tx store.Tx) error {
		return tx.Acceptances().SaveRequest(ctx, task.ID, "req", input, result)
	}); err != nil {
		t.Fatal(err)
	}
	var gotInput string
	var gotResult store.AcceptanceResult
	if err := e.Read(ctx, func(tx store.Tx) error {
		var err error
		gotInput, gotResult, err = tx.Acceptances().Request(ctx, task.ID, "req")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if gotInput != input {
		t.Fatalf("evidence bytes changed:\n got %q\nwant %q", gotInput, input)
	}
	if gotResult.Revision != 3 || gotResult.AcceptanceID == nil || *gotResult.AcceptanceID != 42 {
		t.Fatalf("result %+v", gotResult)
	}
	if err := e.Read(ctx, func(tx store.Tx) error {
		_, _, err := tx.Acceptances().Request(ctx, task.ID, "other")
		return err
	}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown request = %v, want ErrNotFound", err)
	}
}

// Blobs keep every byte, up to the attachment limit; secret hashes are 32
// bytes.
func casePortBlobs(t *testing.T, open Factory) {
	e := open(t)
	ctx := context.Background()
	create := func(data []byte) (store.Attachment, error) {
		var a store.Attachment
		err := e.Write(ctx, func(tx store.Tx) error {
			var err error
			a, err = tx.Attachments().Create(ctx, "image.png", "image/png", data)
			return err
		})
		return a, err
	}
	get := func(id int64) store.AttachmentData {
		t.Helper()
		var a store.AttachmentData
		if err := e.Read(ctx, func(tx store.Tx) error {
			var err error
			a, err = tx.Attachments().Get(ctx, id)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return a
	}

	every := []byte{0, 0}
	for i := range 256 {
		every = append(every, byte(i))
	}
	every = append(every, 0, 0xff, 0)
	full := bytes.Repeat([]byte{0, 0x80, 0xff, 'x'}, db.MaxAttachmentBytes/4)
	for _, data := range [][]byte{every, full} {
		a, err := create(data)
		if err != nil {
			t.Fatalf("%d bytes: %v", len(data), err)
		}
		got := get(a.ID)
		if !bytes.Equal(got.Data, data) || got.SizeBytes != len(data) || a.SizeBytes != len(data) {
			t.Fatalf("%d bytes came back as %d (size %d)", len(data), len(got.Data), got.SizeBytes)
		}
	}
	for _, data := range [][]byte{append(bytes.Clone(full), 0), {}} {
		if _, err := create(data); err == nil {
			t.Fatalf("%d-byte attachment accepted", len(data))
		}
	}

	hash := bytes.Repeat([]byte{0, 0xff}, 16)
	var token int64
	if err := e.Write(ctx, func(tx store.Tx) error {
		machine, err := tx.Machines().InsertMachine(ctx, "worker", db.DefaultUser, "v1")
		if err != nil {
			return err
		}
		token, err = tx.Machines().InsertToken(ctx, machine, hash)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.Read(ctx, func(tx store.Tx) error {
		_, got, err := tx.Machines().Token(ctx, token)
		if err == nil && !bytes.Equal(got, hash) {
			t.Errorf("hash %x, want %x", got, hash)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// Text keeps every Unicode code point it is given (NUL aside).
func casePortUnicode(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	text := "Ünïcödé 日本語 한국어 עברית العربية é \U0001F469‍\U0001F4BB \U0001F1EF\U0001F1F5   tab\tnew\nline"
	p, err := s.InsertProject(ctx, db.Project{Title: text, Bindings: []db.ProjectBinding{{ConnectionID: 1, ExternalProjectID: "u", Workspace: "/w/" + text}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, db.NewTask{ConnectionID: dbtest.T3Connection, ProjectID: p.ID, Title: text, Prompt: text})
	if err != nil {
		t.Fatal(err)
	}
	tree := mustTree(t, s, ctx, text, "", db.TreeMember{TaskID: task.ID})
	entry, err := s.AppendNote(ctx, db.JournalOwner{TaskID: &task.ID}, db.AppendNoteInput{Text: text, RequestID: text})
	if err != nil {
		t.Fatal(err)
	}
	sn, err := s.CreateSnippet(ctx, snippetPatch(text, text, nil))
	if err != nil {
		t.Fatal(err)
	}

	gotProject, err := s.GetProject(ctx, p.ID)
	if err != nil || gotProject.Title != text || gotProject.Bindings[0].Workspace != "/w/"+text {
		t.Fatalf("project %+v %v", gotProject, err)
	}
	gotTask := getTask(t, s, ctx, task.ID)
	if gotTask.Title != text || gotTask.Prompt != text {
		t.Fatalf("task title %q prompt %q", gotTask.Title, gotTask.Prompt)
	}
	gotTree, err := s.GetTree(ctx, tree.ID)
	if err != nil || gotTree.Title != text {
		t.Fatalf("tree %q %v", gotTree.Title, err)
	}
	gotEntry, err := s.GetJournalEntry(ctx, entry.ID)
	if err != nil || gotEntry.Text != text {
		t.Fatalf("journal %q %v", gotEntry.Text, err)
	}
	// The same Unicode requestId is the same request.
	again, err := s.AppendNote(ctx, db.JournalOwner{TaskID: &task.ID}, db.AppendNoteInput{Text: text, RequestID: text})
	if err != nil || again.ID != entry.ID {
		t.Fatalf("retry %+v %v, want entry %d", again, err, entry.ID)
	}
	gotSnippet, err := s.GetSnippet(ctx, sn.ID)
	if err != nil || gotSnippet.Name != text || gotSnippet.Body != text {
		t.Fatalf("snippet %+v %v", gotSnippet, err)
	}
}
