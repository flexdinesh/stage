// Package codexsetup is the Codex connection setup on one machine: the
// Codex CLI path, the connect action (start the daemon if needed, check
// the login, record the Codex home as the connection's identity) and
// disconnect. Both the server and worker compose it through the module.
package codexsetup

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
)

// Ports are the machine's setup dependencies.
type Ports = harness.Ports

// Options configure a Service.
type Options struct {
	Ports
	// Version is Stage's version, sent as the client version.
	Version string
	// Daemon runs the CLI's daemon commands for binary (default codex.CLI).
	Daemon func(binary string) codex.Daemon
	Logger *slog.Logger
}

// Setup states (harness.Status.State).
const (
	StateConnected    = "connected"
	StateNotConnected = "not_connected"
	StateSignedOut    = "signed_out"
	StateUnreachable  = "unreachable"
	StateNotInstalled = "not_installed"
)

// Conflict codes of a refused connect.
const (
	CodeNotInstalled = "codex_not_installed"
	CodeSignedOut    = "codex_signed_out"
	CodeUnavailable  = "codex_unavailable"
)

// statusTTL: a status probe is reused this long.
const statusTTL = 5 * time.Second

// Details is the Codex panel's setup state (harness.Status.Details).
type Details struct {
	// Binary is the configured CLI ("" = codex on PATH).
	Binary           string         `json:"binary"`
	CLIVersion       string         `json:"cliVersion"`
	AppServerVersion string         `json:"appServerVersion"`
	CodexHome        string         `json:"codexHome"`
	Daemon           string         `json:"daemon"`
	Account          *codex.Account `json:"account"`
	// Fix is a shell command that fixes the current problem ("" none).
	Fix       string     `json:"fix"`
	CheckedAt *time.Time `json:"checkedAt"`
}

// Service is one machine's Codex setup.
type Service struct {
	opts Options

	mu      sync.Mutex
	saved   SetupSettings
	id      int64
	client  *codex.Client
	active  bool
	state   string
	reason  string
	details Details
	checked time.Time
}

// New loads the machine's setup and, when it was connected, installs the
// adapter on its active connection (the daemon starts on first use).
func New(ctx context.Context, opts Options) (*Service, error) {
	if opts.Daemon == nil {
		opts.Daemon = func(binary string) codex.Daemon { return codex.CLI{Binary: binary} }
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	saved, err := loadSettings(ctx, opts.Settings)
	if err != nil {
		return nil, err
	}
	id, err := opts.Identity.Active(ctx, codex.Kind)
	if err != nil {
		return nil, err
	}
	s := &Service{opts: opts, saved: saved, id: id, state: StateNotConnected}
	s.details.Binary, s.details.Daemon = saved.Binary, "unknown"
	if saved.Connected && id != 0 {
		s.client = s.newClient(saved.Binary)
		s.active = true
		s.state = ""
		opts.Install(id, id, codex.New(s.client, id, saved.Binary))
	}
	return s, nil
}

func (s *Service) newClient(binary string) *codex.Client {
	return codex.NewClient(s.opts.Daemon(binary), s.opts.Version)
}

// Close drops the daemon connection (the daemon keeps running).
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		s.client.Close()
	}
}

// Connect starts the daemon when needed, checks the login and records the
// Codex home as the machine's Codex connection (machine.Connect).
func (s *Service) Connect(ctx context.Context) (harness.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	client := s.newClient(s.saved.Binary)
	p, err := client.Probe(ctx)
	if err != nil {
		client.Close()
		s.fail(err)
		return harness.Result{}, &db.ConflictError{Code: conflictCode(err), Msg: codex.Problem(err)}
	}
	s.observe(p)
	if !p.SignedIn() {
		client.Close()
		s.state, s.reason, s.details.Fix = StateSignedOut, signedOutReason, "codex login"
		return harness.Result{}, &db.ConflictError{Code: CodeSignedOut, Msg: signedOutReason}
	}
	binary := s.saved.Binary
	res, err := machine.Connect(ctx, s.opts.Identity, s.opts.Install, codex.Kind, p.CodexHome, p.Label(), func(conn int64) execution.Adapter {
		return codex.New(client, conn, binary)
	})
	if err != nil {
		client.Close()
		return harness.Result{}, err
	}
	id, err := s.opts.Identity.Active(ctx, codex.Kind)
	if err != nil {
		return res, err
	}
	next := s.saved
	next.Connected = true
	if err := saveSettings(ctx, s.opts.Settings, map[string]bool{"connected": true}); err != nil {
		return res, err
	}
	if s.client != nil && s.client != client {
		s.client.Close()
	}
	s.saved, s.id, s.client, s.active = next, id, client, true
	s.state, s.reason, s.details.Fix = StateConnected, "", ""
	return res, nil
}

const signedOutReason = "Codex isn't signed in. Run codex login, then connect again."

func conflictCode(err error) string {
	if errors.Is(err, codex.ErrNotInstalled) {
		return CodeNotInstalled
	}
	return CodeUnavailable
}

// Disconnect uninstalls the adapter and records it; the connection keeps
// its identity and history, and the daemon keeps running.
func (s *Service) Disconnect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.saved
	next.Connected = false
	if err := saveSettings(ctx, s.opts.Settings, map[string]bool{"connected": false}); err != nil {
		return err
	}
	if s.active {
		s.opts.Install(s.id, s.id, nil)
	}
	if s.client != nil {
		s.client.Close()
	}
	s.saved, s.client, s.active = next, nil, false
	s.state, s.reason, s.details.Fix = StateNotConnected, "", ""
	return nil
}

// SetBinary saves the CLI path ("" = codex on PATH). A connected Codex
// switches to it at once; the next check reports whether it works.
func (s *Service) SetBinary(ctx context.Context, binary string) error {
	binary = strings.TrimSpace(binary)
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.saved
	next.Binary = binary
	var savedBinary *string
	if binary != "" {
		savedBinary = &binary
	}
	if err := saveSettings(ctx, s.opts.Settings, map[string]*string{"binary": savedBinary}); err != nil {
		return err
	}
	s.saved, s.details.Binary = next, binary
	if s.active {
		old := s.client
		s.client = s.newClient(binary)
		s.opts.Install(s.id, s.id, codex.New(s.client, s.id, binary))
		if old != nil {
			old.Close()
		}
	}
	s.checked = time.Time{}
	return nil
}

// Summary is memory only.
func (s *Service) Summary() harness.Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return harness.Summary{ConnectionID: s.id, Configured: s.active, Connected: s.active && s.state == StateConnected, LastError: s.reason}
}

// Status reports the setup. Connected: probes the daemon through the
// active client (reused statusTTL, or fresh on refresh). Not connected:
// reads the daemon's version only, never starting it.
func (s *Service) Status(ctx context.Context, refresh bool) (string, string, Details) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if refresh || s.checked.IsZero() || time.Since(s.checked) > statusTTL {
		s.check(ctx)
	}
	return s.state, s.reason, s.details
}

// check refreshes the state (s.mu held).
func (s *Service) check(ctx context.Context) {
	now := time.Now().UTC()
	s.checked, s.details.CheckedAt = now, &now
	if !s.active {
		s.state, s.reason, s.details.Fix = StateNotConnected, "", ""
		info, err := s.opts.Daemon(s.saved.Binary).Version(ctx)
		switch {
		case errors.Is(err, codex.ErrNotInstalled):
			s.state, s.reason = StateNotInstalled, codex.Problem(err)
			s.details.Daemon, s.details.CLIVersion, s.details.AppServerVersion = "unknown", "", ""
		case err != nil:
			s.details.Daemon = "unknown"
		default:
			s.details.CLIVersion, s.details.AppServerVersion = info.CLIVersion, info.AppServerVersion
			s.details.Daemon = daemonWord(info)
			if info.Up() {
				// A running daemon answers whether Codex is signed in, so
				// the panel can name the fix before connecting.
				s.preview(ctx, info)
			}
		}
		return
	}
	p, err := s.client.Probe(ctx)
	if err != nil {
		s.fail(err)
		return
	}
	s.observe(p)
	if !p.SignedIn() {
		s.state, s.reason, s.details.Fix = StateSignedOut, signedOutReason, "codex login"
		return
	}
	s.state, s.reason, s.details.Fix = StateConnected, "", ""
}

// preview reads the account through a running daemon without connecting
// Stage to it (s.mu held). The daemon is never started here.
func (s *Service) preview(ctx context.Context, info codex.DaemonInfo) {
	c := codex.NewClient(running{info}, s.opts.Version)
	defer c.Close()
	p, err := c.Probe(ctx)
	if err != nil {
		return
	}
	s.observe(p)
	if !p.SignedIn() {
		s.state, s.reason, s.details.Fix = StateSignedOut, signedOutReason, "codex login"
	}
}

// running is a daemon known to be up: it reports info and never starts.
type running struct{ info codex.DaemonInfo }

func (r running) Version(context.Context) (codex.DaemonInfo, error) { return r.info, nil }
func (r running) Start(context.Context) (codex.DaemonInfo, error) {
	return codex.DaemonInfo{}, errors.New("not started from a status check")
}

// observe records a successful probe's facts (s.mu held).
func (s *Service) observe(p codex.Probe) {
	s.details.CLIVersion, s.details.AppServerVersion = p.Daemon.CLIVersion, p.Daemon.AppServerVersion
	s.details.CodexHome, s.details.Daemon, s.details.Account = p.CodexHome, daemonWord(p.Daemon), p.Account
}

// fail records a failed probe (s.mu held).
func (s *Service) fail(err error) {
	s.reason = codex.Problem(err)
	s.details.Account = nil
	var down *codex.DaemonDownError
	switch {
	case errors.Is(err, codex.ErrNotInstalled):
		s.state, s.details.Daemon, s.details.Fix = StateNotInstalled, "unknown", ""
	case errors.As(err, &down):
		s.state, s.details.Daemon = StateUnreachable, "stopped"
		s.details.Fix = shellWord(codex.CLI{Binary: s.saved.Binary}.Path()) + " app-server daemon start"
	default:
		s.state, s.details.Fix = StateUnreachable, ""
	}
}

func daemonWord(d codex.DaemonInfo) string {
	if d.Up() {
		return "running"
	}
	return "stopped"
}

// shellWord quotes a path for a copyable command.
func shellWord(s string) string {
	if strings.ContainsAny(s, " '\"\\$`!&|;<>()*?[]#~") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}
