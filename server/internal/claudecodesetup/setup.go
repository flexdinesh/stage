// Package claudecodesetup sets up Claude Code on a machine: the claude
// command, a connect action that checks the CLI and its login and records
// the connection, and disconnect. Stage stores no Claude credentials:
// Claude's own login is used.
package claudecodesetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
)

// Error codes (409 body "code").
const (
	CodeNotFound     = "claude_not_found"
	CodeUnresponsive = "claude_unresponsive"
	CodeLoggedOut    = "claude_logged_out"
)

// Setup states (harness.Status.State).
const (
	StateConnected    = "connected"
	StateDisconnected = "disconnected"
	StateNotFound     = "not_found"
	StateLoggedOut    = "logged_out"
	StateError        = "error"
)

// checkTimeout bounds each CLI check (--version, auth status).
const checkTimeout = 20 * time.Second

// Settings persists the machine's Claude Code setup (claude_settings).
type Settings interface {
	ClaudeSettings(ctx context.Context) (config.ClaudeSettings, error)
	SaveClaudeSettings(ctx context.Context, c config.ClaudeSettings) error
}

// Ports are what the setup needs from its machine and the main server;
// never the store itself.
type Ports struct {
	Settings Settings
	Identity machine.Identity
	Install  machine.Install
}

// Options are the machine-local environment.
type Options struct {
	// Home is the user's home ("": os.UserHomeDir).
	Home string
	// Environ is the environment passed on to claude (nil: os.Environ()).
	Environ []string
	// LookPath resolves the command (nil: exec.LookPath).
	LookPath func(string) (string, error)
	// Scope runs background launches in their own systemd scope
	// (claudecode.ScopeAvailable).
	Scope bool
	// CheckTimeout bounds each CLI check (default 20s).
	CheckTimeout time.Duration
	// StateDir holds the hook settings file Claude sessions Stage launches
	// read (claudecode.HooksFile) and, in images/, the launches' images:
	// <data dir>/claude. Empty: no push, polling alone follows sessions, and
	// no images.
	StateDir string
	// NewAdapter builds the adapter (nil: claudecode.New); tests shorten
	// its timings.
	NewAdapter func(claudecode.Options) execution.Adapter
	Logger     *slog.Logger
}

// Details is the status's harness-specific part (a host's panel for the
// kind may read it).
type Details struct {
	Binary          string `json:"binary,omitempty"`
	Version         string `json:"version,omitempty"`
	ConfigDirectory string `json:"configDirectory,omitempty"`
	LoggedIn        bool   `json:"loggedIn"`
	Scope           bool   `json:"scope"`
	// Events is how Stage hears about the sessions it launches (set up
	// connections only).
	Events *claudecode.EventsStatus `json:"events,omitempty"`
}

// Service is Claude Code's setup on one machine.
type Service struct {
	mu     sync.Mutex
	ports  Ports
	opts   Options
	log    *slog.Logger
	saved  config.ClaudeSettings
	id     int64 // the machine's Claude Code connection (0: none yet)
	active bool  // an adapter is installed on id
	// last check (Status refresh, connect)
	checked bool
	state   string
	reason  string
	details Details
	// life is the server's (or worker's) lifetime: the hook listener closes
	// with it.
	life context.Context
	// hooks is the machine's hook receiver, opened with the first adapter;
	// hooksErr says why it is not (once tried).
	hooks      *claudecode.Hooks
	hooksTried bool
	hooksErr   string
}

// New loads the setup and, when Claude Code is connected here, installs
// its adapter without probing (health reports problems).
func New(ctx context.Context, p Ports, o Options) (*Service, error) {
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	if o.CheckTimeout == 0 {
		o.CheckTimeout = checkTimeout
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.NewAdapter == nil {
		o.NewAdapter = func(opts claudecode.Options) execution.Adapter { return claudecode.New(opts) }
	}
	saved, err := p.Settings.ClaudeSettings(ctx)
	if err != nil {
		return nil, err
	}
	id, err := p.Identity.Active(ctx, claudecode.Kind)
	if err != nil {
		return nil, err
	}
	s := &Service{ports: p, opts: o, log: o.Logger, saved: saved, id: id, life: ctx}
	if id != 0 && !saved.Disconnected {
		if bin := s.find(); bin != "" {
			s.install(bin, saved.ConfigDir)
		} else {
			s.state, s.reason = StateNotFound, s.notFound()
		}
	}
	return s, nil
}

func (s *Service) find() string {
	return claudecode.Find(s.saved.Binary, s.opts.Home, s.opts.LookPath)
}

func (s *Service) cli(bin string) *claudecode.CLI {
	return &claudecode.CLI{Bin: bin, Home: s.opts.Home, Environ: s.opts.Environ, Scope: s.opts.Scope}
}

// install puts the adapter for bin on the connection (s.mu held or setup).
func (s *Service) install(bin, identity string) {
	s.ports.Install(s.id, s.id, s.adapter(s.id, bin, identity))
	s.active = true
}

// adapter builds the connection's adapter on the machine's hook receiver,
// which it opens first if it was never tried; the previous adapter's
// subscription ends (s.mu held or setup).
func (s *Service) adapter(conn int64, bin, identity string) execution.Adapter {
	s.openHooks()
	if s.hooks != nil {
		s.hooks.Reset()
	}
	return s.opts.NewAdapter(claudecode.Options{Connection: conn, CLI: s.cli(bin), Identity: identity, ImageRoot: s.imageRoot(), Hooks: s.hooks, Logger: s.log})
}

// imageRoot is where launches write their images: <StateDir>/images.
func (s *Service) imageRoot() string {
	if s.opts.StateDir == "" {
		return ""
	}
	return filepath.Join(s.opts.StateDir, "images")
}

// openHooks starts the hook receiver once; it closes with s.life. A
// failure leaves launches without hooks (s.mu held or setup).
func (s *Service) openHooks() {
	if s.hooksTried {
		return
	}
	s.hooksTried = true
	h, err := claudecode.OpenHooks(claudecode.HooksOptions{Dir: s.opts.StateDir, ImageRoot: s.imageRoot(), Logger: s.log})
	if err != nil {
		s.hooksErr = err.Error() + " It checks Claude Code every few seconds instead."
		s.log.Warn("claude hooks unavailable; polling only", "err", err)
		return
	}
	s.hooks = h
	context.AfterFunc(s.life, func() { _ = h.Close() })
}

// uninstall removes the connection's adapter and ends its push
// subscription (s.mu held).
func (s *Service) uninstall() {
	if s.hooks != nil {
		s.hooks.Reset()
	}
	s.ports.Install(s.id, s.id, nil)
}

// events is details.events: nil before the connection is set up (s.mu
// held).
func (s *Service) events(sum harness.Summary) *claudecode.EventsStatus {
	switch {
	case !sum.Configured || !s.hooksTried:
		return nil
	case s.hooks != nil:
		st := s.hooks.Status()
		return &st
	}
	return &claudecode.EventsStatus{Mode: claudecode.ModePolling, Reason: s.hooksErr}
}

func (s *Service) notFound() string {
	if s.saved.Binary != "" {
		return fmt.Sprintf("Stage can't find %s. Check the Claude command setting.", s.saved.Binary)
	}
	return "Stage can't find the claude command. Install Claude Code, or set the Claude command to its full path."
}

// check runs the CLI checks: the command, its version and its login. It
// returns the state, a reason for anything but connected-ready, and the
// auth status.
func (s *Service) check(ctx context.Context) (string, string, Details, claudecode.Auth) {
	d := Details{Scope: s.opts.Scope}
	bin := s.find()
	if bin == "" {
		return StateNotFound, s.notFound(), d, claudecode.Auth{}
	}
	d.Binary = bin
	cli := s.cli(bin)
	vctx, cancel := context.WithTimeout(ctx, s.opts.CheckTimeout)
	v, err := claudecode.ReadVersion(vctx, cli)
	cancel()
	if errors.Is(err, context.DeadlineExceeded) {
		return StateError, fmt.Sprintf("claude --version didn't answer within %s. Check the command, then connect again.", s.opts.CheckTimeout.Round(time.Second)), d, claudecode.Auth{}
	}
	if err != nil {
		return StateError, "Stage can't run Claude Code: " + err.Error(), d, claudecode.Auth{}
	}
	d.Version = v
	actx, cancel := context.WithTimeout(ctx, s.opts.CheckTimeout)
	au, err := claudecode.ReadAuth(actx, cli)
	cancel()
	if err != nil {
		return StateError, "Stage can't read Claude Code's login: " + err.Error(), d, claudecode.Auth{}
	}
	d.ConfigDirectory, d.LoggedIn = au.ConfigDirectory, au.LoggedIn
	if !au.LoggedIn {
		return StateLoggedOut, "Claude Code isn't logged in. Run `claude auth login` in a terminal, then connect again.", d, au
	}
	return "", "", d, au
}

// label names the connection by its config directory, never the account.
func label(configDir, home string) string {
	if home != "" {
		if rel, err := filepath.Rel(home, configDir); err == nil && !strings.HasPrefix(rel, "..") {
			configDir = filepath.Join("~", rel)
		}
	}
	return "Claude Code (" + configDir + ")"
}

// Connect checks the command and its login, records the machine's
// connection with Claude's config directory as identity and installs the
// adapter.
func (s *Service) Connect(ctx context.Context) (harness.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, reason, d, au := s.check(ctx)
	s.checked, s.details = true, d
	if state != "" {
		s.state, s.reason = state, reason
		code := map[string]string{StateNotFound: CodeNotFound, StateLoggedOut: CodeLoggedOut}[state]
		if code == "" {
			code = CodeUnresponsive
		}
		return harness.Result{}, &db.ConflictError{Code: code, Msg: reason}
	}
	next := s.saved
	next.ConfigDir, next.Disconnected = au.ConfigDirectory, false
	if err := s.ports.Settings.SaveClaudeSettings(ctx, next); err != nil {
		return harness.Result{}, err
	}
	s.saved = next
	res, err := machine.Connect(ctx, s.ports.Identity, s.ports.Install, claudecode.Kind, au.ConfigDirectory, label(au.ConfigDirectory, s.opts.Home), func(conn int64) execution.Adapter {
		s.id = conn
		return s.adapter(conn, d.Binary, au.ConfigDirectory)
	})
	if err != nil {
		return res, err
	}
	s.active, s.state, s.reason = true, StateConnected, ""
	return res, nil
}

// Disconnect stops Stage using Claude Code here: the adapter is removed
// and stays removed across restarts. The connection keeps its history; the
// next connect with the same config directory resumes it.
func (s *Service) Disconnect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.saved
	next.Disconnected = true
	if err := s.ports.Settings.SaveClaudeSettings(ctx, next); err != nil {
		return err
	}
	s.saved = next
	if s.active {
		s.uninstall()
	}
	s.active, s.state, s.reason = false, StateDisconnected, ""
	return nil
}

// Configure applies {"binary": string|null}; null resets to the default
// lookup. A set-up connection moves to the new command: its adapter is
// (re)installed when the command is found (also after a command that was
// not), and removed when it is not.
func (s *Service) Configure(ctx context.Context, raw json.RawMessage) error {
	var in struct {
		Binary *string `json:"binary"`
	}
	if err := db.DecodeStrict(raw, &in); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.saved
	next.Binary = ""
	if in.Binary != nil {
		next.Binary = strings.TrimSpace(*in.Binary)
	}
	if err := s.ports.Settings.SaveClaudeSettings(ctx, next); err != nil {
		return err
	}
	s.saved, s.checked = next, false
	if s.active || (s.id != 0 && !s.saved.Disconnected) {
		if bin := s.find(); bin != "" {
			s.install(bin, s.saved.ConfigDir)
		} else {
			s.uninstall()
			s.active, s.state, s.reason = false, StateNotFound, s.notFound()
		}
	}
	return nil
}

// summary is the memory-only state (s.mu held).
func (s *Service) summary() harness.Summary {
	sum := harness.Summary{ConnectionID: s.id, Configured: s.id != 0 && !s.saved.Disconnected}
	sum.Connected = sum.Configured && s.active && (s.state == "" || s.state == StateConnected)
	if !sum.Connected && sum.Configured {
		sum.LastError = s.reason
	}
	return sum
}

// Summary never probes.
func (s *Service) Summary() harness.Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summary()
}

// Status reports the setup; refresh (or the first call) runs the CLI
// checks, bounded.
func (s *Service) Status(ctx context.Context, refresh bool) harness.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if refresh || !s.checked {
		state, reason, d, au := s.check(ctx)
		s.checked, s.details = true, d
		switch {
		case state != "":
			s.state, s.reason = state, reason
		case s.active && s.saved.ConfigDir != "" && au.ConfigDirectory != s.saved.ConfigDir:
			s.state, s.reason = StateError, fmt.Sprintf("Claude Code now uses %s instead of %s. Connect again to use it.", au.ConfigDirectory, s.saved.ConfigDir)
		default:
			s.state, s.reason = StateConnected, ""
		}
	}
	sum := s.summary()
	state, reason := s.state, s.reason
	if !sum.Configured {
		// Ready to connect, or why not.
		if state == StateConnected || state == "" {
			state, reason = StateDisconnected, ""
		}
	}
	details := s.details
	details.Events = s.events(sum)
	src := "saved"
	if s.saved.Binary == "" {
		src = "default"
	}
	return harness.Status{
		Summary: sum,
		State:   state,
		Reason:  reason,
		Fields:  []harness.FieldState{{ID: "binary", Value: s.saved.Binary, Set: s.saved.Binary != "", Source: src}},
		Actions: []harness.ActionState{{ID: claudecode.ActionConnect, Available: true}},
		Details: details,
	}
}
