package coordinator

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// SessionRenameTimeout bounds one session rename (operation sessionRename).
const SessionRenameTimeout = 10 * time.Second

// renamer serializes renames per task and lets tests wait for background ones.
type renamer struct {
	locks sync.Map // task id -> *sync.Mutex
	wg    sync.WaitGroup
}

func (r *renamer) lock(id int64) func() {
	value, _ := r.locks.LoadOrStore(id, &sync.Mutex{})
	mu, ok := value.(*sync.Mutex)
	if !ok {
		return func() {}
	}
	mu.Lock()
	return mu.Unlock
}

// RenameSessionLater renames task id's session in the background after its
// title changed (RenameSession). It never fails the caller.
func (s *Service) RenameSessionLater(ctx context.Context, id int64) {
	ctx = context.WithoutCancel(ctx)
	s.renames.wg.Go(func() { s.RenameSession(ctx, id) })
}

// WaitRenames waits for background renames to finish (tests, shutdown).
func (s *Service) WaitRenames() { s.renames.wg.Wait() }

// RenameSession gives the session Stage launched for task id the task's
// current title, when its harness offers sessionRename. Adopted sessions
// keep their harness names, and a declared-unavailable operation is
// skipped. Any other failure appends a system note to the task's journal.
// Renames of one task run in order, each with the title current when it
// runs, so the last edit wins.
func (s *Service) RenameSession(ctx context.Context, id int64) {
	defer s.renames.lock(id)()
	ctx, cancel := context.WithTimeout(ctx, SessionRenameTimeout)
	defer cancel()
	t, err := s.store.GetTask(ctx, id)
	if err != nil {
		s.log.Warn("rename session: read task", "task", id, "err", err)
		return
	}
	if t.Adopted || t.Session == nil || strings.TrimSpace(t.Title) == "" {
		return
	}
	ref := *t.Session
	skipped, err := s.renameSession(ctx, ref, t.Title)
	if skipped || err == nil {
		return
	}
	s.noteRenameFailure(ctx, t, ref, err)
}

// renameSession reports skipped when the harness declares the operation
// unavailable.
func (s *Service) renameSession(ctx context.Context, ref execution.SessionRef, title string) (bool, error) {
	a, err := s.conns.Catalog(ref.ConnectionID)
	if err != nil {
		return false, err
	}
	features, err := a.Features(ctx)
	if err != nil {
		return false, err
	}
	if !features.Operations[execution.OpSessionRename].Available {
		return true, nil
	}
	namer, ok := execution.As[execution.SessionNamer](a)
	if !ok {
		return false, errors.New("this connection does not provide session rename")
	}
	return false, namer.RenameSession(ctx, ref, title)
}

func (s *Service) noteRenameFailure(ctx context.Context, t db.Task, ref execution.SessionRef, cause error) {
	name := "harness"
	if d, ok := s.conns.Descriptor(s.store.Harness(ref.ConnectionID)); ok && d.Name != "" {
		name = d.Name
	}
	reason := strings.TrimSpace(cause.Error())
	if errors.Is(cause, context.DeadlineExceeded) {
		reason = fmt.Sprintf("%s did not answer within %s", name, SessionRenameTimeout)
	}
	if len(reason) > 1024 {
		reason = reason[:1024] + "..."
	}
	// The note is recorded even when the rename used up the timeout.
	record, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	record = db.AutomatedWork(record, "system", db.Cause{Kind: "session_rename", TaskID: t.ID, Session: &ref}, db.WorkAttribution(ctx))
	in := db.AppendNoteInput{
		Text:      fmt.Sprintf("Could not rename the %s session: %s", name, reason),
		RequestID: "session-rename:" + rand.Text(),
	}
	if _, err := s.store.AppendNote(record, db.JournalOwner{TaskID: &t.ID}, in); err != nil {
		s.log.Warn("rename session: journal note", "task", t.ID, "err", err)
	}
}
