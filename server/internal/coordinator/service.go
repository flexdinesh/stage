// Package coordinator coordinates Stage-owned execution and recovery.
package coordinator

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/workevent"
)

type Service struct {
	store      *db.Store
	conns      *machine.Connections
	workspaces Workspaces
	log        *slog.Logger
	locks      sync.Map

	PublicURL            string // STAGE_PUBLIC_URL, named in launch journal instructions when set
	SharedAccess         bool   // Shared launch guidance requires an authorized agent transport.
	CLI                  string // installed CLI name (install.CLIName), named in launch journal instructions
	LaunchAttemptTimeout time.Duration
	OnTransition         func(context.Context, workevent.TaskChange)
	OnObservedTransition func(context.Context, workevent.TaskChange) // observation inbox writes have already committed
	OnAccepted           func()                                      // a completion was inferred (tree release may follow)
}

type Workspaces interface {
	Inventory(context.Context, int64) ([]execution.WorkspaceInfo, error)
}

type Options struct {
	Store       *db.Store
	Connections *machine.Connections
	Workspaces  Workspaces
	Log         *slog.Logger
}

// New coordinates execution across hosted harness connections.
func New(o Options) *Service {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &Service{
		store: o.Store, conns: o.Connections, workspaces: o.Workspaces,
		log: o.Log, LaunchAttemptTimeout: 40 * time.Second,
	}
}

func (s *Service) lock(id int64) func() {
	value, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
func (s *Service) RecoverLaunching(ctx context.Context) {
	if err := s.store.RecoverAttempts(ctx); err != nil {
		s.log.Error("recover attempts", "err", err)
	}
}
func (s *Service) transition(ctx context.Context, t db.Transition) (db.TransitionResult, error) {
	r, e := s.store.TransitionTask(ctx, t)
	if e == nil && r.Changed && s.OnTransition != nil {
		s.OnTransition(ctx, workevent.TaskChange{TaskID: t.TaskID, From: r.From, To: t.To, Detail: t.Detail, Attribution: r.Attribution})
	}
	return r, e
}
func harnessErr(err error) error {
	if err == nil {
		return nil
	}
	var refused *execution.Refused
	if errors.As(err, &refused) {
		return &db.ConflictError{Code: "session_refused", Msg: refused.Reason}
	}
	var busy *execution.Busy
	if errors.As(err, &busy) {
		return &db.ConflictError{Code: "session_busy", Msg: busy.Reason}
	}
	return &db.HarnessError{Err: err}
}

// httpAPI names the HTTP API for a session on connection's machine: the
// public URL locally, the worker's agent gateway on a remote machine.
func (s *Service) httpAPI(connection int64) string {
	remote, gateways := s.conns.Machines().AgentGateways(s.store.ConnectionMachine(connection))
	if s.SharedAccess {
		if remote {
			return "the HTTP API through this machine's private Stage worker socket"
		}
		return "the Stage server's HTTP API with a configured agent credential"
	}
	return httpAPI(s.PublicURL, remote, gateways)
}

// attribution is kind's caller attribution (zero when unknown).
func (s *Service) attribution(kind string) harness.Attribution {
	d, _ := s.conns.Descriptor(kind)
	return d.Attribution
}
