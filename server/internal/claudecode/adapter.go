package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Options build an Adapter.
type Options struct {
	// Connection is the Stage connection the adapter serves.
	Connection int64
	CLI        *CLI
	// Identity is the connection's config directory (its stable identity):
	// health is unavailable once Claude reports another one.
	Identity string
	// RosterTTL is how long one session list read is reused (default 2s).
	RosterTTL time.Duration
	// LaunchWait bounds the wait for a launched session to appear in the
	// session list (default 5s).
	LaunchWait time.Duration
	// ImageRoot is the folder under Stage's data dir (on a worker, the
	// worker's) that launches write their images to (images.go); Hooks
	// should be opened with the same one. Empty: images are unavailable.
	ImageRoot string
	// Hooks is the machine's hook receiver (nil: no push and no approvals
	// in Stage; launches pass no --settings, polling alone follows
	// sessions, and their prompts are answered in Claude Code).
	Hooks *Hooks
	// SettleDelays are the waits before each read after a hook (default
	// 0, 250ms, 500ms, 1s, 2s); tests shorten them.
	SettleDelays []time.Duration
	// ProcessAlive reports whether a session's process (its list pid)
	// still runs (default: signal 0); tests of a fake CLI, whose pids are
	// made up, report false.
	ProcessAlive func(pid int) bool
	Now          func() time.Time
	Logger       *slog.Logger
}

// Adapter is the Claude Code execution.Adapter for one connection.
type Adapter struct {
	conn       int64
	cli        *CLI
	identity   string
	roster     *roster
	logs       *transcripts
	launchWait time.Duration
	hooks      *Hooks
	imageRoot  string
	now        func() time.Time
	log        *slog.Logger
	// settleDelays are the waits before each read after a hook (Events).
	settleDelays []time.Duration
	alive        func(pid int) bool

	authMu sync.Mutex
	auth   authCache

	invMu  sync.Mutex
	invAt  time.Time // ~/.claude.json mtime of inv
	invKey string
	inv    []execution.WorkspaceInfo
	legacy sync.Once // removeLegacyImages over the first inventory

	// sendMu serializes Send; wakeMu guards waking, the sessions a Send is
	// delivering to, each until its deadline (send.go).
	sendMu sync.Mutex
	wakeMu sync.Mutex
	waking map[string]time.Time
}

var (
	_ execution.Adapter      = (*Adapter)(nil)
	_ execution.Correlator   = (*Adapter)(nil)
	_ execution.LinkResolver = (*Adapter)(nil)
	_ execution.EventSource  = (*Adapter)(nil)
)

// New builds the adapter; it runs nothing until called.
func New(o Options) *Adapter {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RosterTTL == 0 {
		o.RosterTTL = 2 * time.Second
	}
	if o.LaunchWait == 0 {
		o.LaunchWait = 5 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if len(o.SettleDelays) == 0 {
		o.SettleDelays = settleDelays
	}
	if o.ProcessAlive == nil {
		o.ProcessAlive = processAlive
	}
	return &Adapter{
		conn: o.Connection, cli: o.CLI, identity: o.Identity, launchWait: o.LaunchWait, now: o.Now, log: o.Logger,
		hooks: o.Hooks, imageRoot: o.ImageRoot, settleDelays: o.SettleDelays, alive: o.ProcessAlive,
		roster: &roster{cli: o.CLI, ttl: o.RosterTTL, now: o.Now},
		logs:   &transcripts{now: o.Now},
	}
}

// authStatus is the cached auth status (read again after authTTL).
func (a *Adapter) authStatus(ctx context.Context) (Auth, error) {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if !a.auth.at.IsZero() && a.now().Sub(a.auth.at) < authTTL {
		return a.auth.auth, a.auth.err
	}
	call, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	au, err := ReadAuth(call, a.cli)
	if err != nil && ctx.Err() != nil {
		return au, err
	}
	a.auth = authCache{at: a.now(), auth: au, err: err}
	return au, err
}

// projects is the transcript root ("" when Claude's status is unknown).
func (a *Adapter) projects(ctx context.Context) string {
	au, err := a.authStatus(ctx)
	if err != nil {
		return ""
	}
	return au.ProjectsDirectory
}

// Health: Claude Code answers its auth status, is logged in, and still
// uses the connection's config directory.
func (a *Adapter) Health(ctx context.Context) execution.Health {
	h := execution.Health{Identity: a.identity, CheckedAt: a.now().UTC()}
	au, err := a.authStatus(ctx)
	switch {
	case err != nil:
		h.Reason = "Stage can't run Claude Code: " + err.Error()
	case !au.LoggedIn:
		h.Reason = "Claude Code isn't logged in. Run `claude auth login` in a terminal."
	case a.identity != "" && au.ConfigDirectory != a.identity:
		h.Reason = fmt.Sprintf("Claude Code now uses %s instead of %s. Connect Claude Code again in Settings.", au.ConfigDirectory, a.identity)
	default:
		h.Available = true
	}
	return h
}

// Models: Claude's default (an empty model) and its aliases for the latest
// models.
var models = []execution.Model{
	{Choice: choice("opus", "Opus")},
	{Choice: choice("sonnet", "Sonnet")},
	{Choice: choice("haiku", "Haiku")},
	{Choice: choice("fable", "Fable")},
}

// Effort is the harness-wide effort option (--effort).
const optionEffort = "effort"

var efforts = []struct{ id, label string }{
	{"low", "Low"},
	{"medium", "Medium"},
	{"high", "High"},
	{"xhigh", "Extra high"},
	{"max", "Maximum"},
}

// Permission modes (--permission-mode).
var permissions = []execution.Choice{
	choice("manual", "Ask before acting"),
	choice("acceptEdits", "Accept edits"),
	choice("auto", "Auto"),
	choice("plan", "Plan only"),
	choice("dontAsk", "Don't ask"),
	choice("bypassPermissions", "Bypass permissions"),
}

// optionWorktree is the worktree launch option: `claude -w` with a name
// per attempt (WorktreeName).
const optionWorktree = "worktree"

func choice(id, label string) execution.Choice {
	return execution.Choice{ID: id, Label: label, Availability: execution.Availability{Available: true}}
}

var available = execution.Availability{Available: true}

// Unavailable operation reasons.
const (
	reasonAnswer = "Answer in Claude Code: copy the resume command and run it in a terminal."
	reasonImages = "Stage has no folder for Claude Code's images on this machine."
)

// Features is static: the catalog is Claude's aliases, not a probe.
func (a *Adapter) Features(context.Context) (execution.Capabilities, error) {
	off := execution.BoolValue(false)
	effort := execution.Option{ID: optionEffort, Kind: execution.OptionChoice, Label: "Effort", ValueLabels: make(map[string]string, len(efforts))}
	worktree := execution.Option{ID: optionWorktree, Kind: execution.OptionBoolean, Label: "Claude Code worktree", Values: []execution.OptionValue{execution.BoolValue(true), off}, Default: &off}
	for _, e := range efforts {
		effort.Values = append(effort.Values, execution.StringValue(e.id))
		effort.ValueLabels[e.id] = e.label
	}
	return execution.Capabilities{
		Operations: map[execution.Op]execution.Availability{
			execution.OpLaunch: available, execution.OpStop: available, execution.OpAdopt: available, execution.OpReply: available, execution.OpOpening: available, execution.OpImages: a.images(),
			execution.OpAttention: available,
			execution.OpRespond:   a.hooksRunning(),
			execution.OpSend:      available,
		},
		Models:      slices.Clone(models),
		Options:     []execution.Option{effort, worktree},
		Permissions: slices.Clone(permissions),
		Modes:       []execution.Choice{},
	}, nil
}

// images is the images operation: available with an image root.
func (a *Adapter) images() execution.Availability {
	if a.imageRoot == "" {
		return execution.Availability{Reason: reasonImages}
	}
	return available
}

// claudeJSON is the user's Claude state file: ~/.claude.json, or
// .claude.json in the config directory when it is not ~/.claude
// (CLAUDE_CONFIG_DIR).
func claudeJSON(configDir, home string) string {
	if configDir == "" || (home != "" && filepath.Clean(configDir) == filepath.Join(home, ".claude")) {
		return filepath.Join(home, ".claude.json")
	}
	return filepath.Join(configDir, ".claude.json")
}

// worktreeMarker is the path segment of a Claude-managed worktree.
const worktreeMarker = "/.claude/worktrees/"

// Workspaces are the folders Claude Code knows (the projects of its state
// file), local to the machine; a Git checkout's top is importable, which
// the machine's ops decide (ImportCheckoutTop). No file: none. An
// unreadable or malformed file is an error, so discovery keeps its last
// inventory.
func (a *Adapter) Workspaces(ctx context.Context) ([]execution.WorkspaceInfo, error) {
	home := a.cli.home()
	configDir := a.identity
	if au, err := a.authStatus(ctx); err == nil {
		configDir = au.ConfigDirectory
	}
	path := claudeJSON(configDir, home)
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return []execution.WorkspaceInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	a.invMu.Lock()
	defer a.invMu.Unlock()
	if a.invKey == path && a.invAt.Equal(st.ModTime()) {
		return slices.Clone(a.inv), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state struct {
		Projects map[string]json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	out := []execution.WorkspaceInfo{}
	for dir := range state.Projects {
		if !filepath.IsAbs(dir) || strings.Contains(dir+"/", worktreeMarker) {
			continue
		}
		dir = filepath.Clean(dir)
		out = append(out, execution.WorkspaceInfo{ID: dir, Title: filepath.Base(dir), WorkspaceRoot: dir, CreatedAt: st.ModTime().UTC(), UpdatedAt: st.ModTime().UTC(), Local: true, ImportIf: execution.ImportCheckoutTop})
	}
	slices.SortFunc(out, func(x, y execution.WorkspaceInfo) int { return strings.Compare(x.ID, y.ID) })
	a.inv, a.invAt, a.invKey = out, st.ModTime(), path
	// Once per adapter: remove what earlier versions wrote in the projects
	// (best effort).
	a.legacy.Do(func() {
		for _, w := range out {
			if err := removeLegacyImages(w.WorkspaceRoot); err != nil {
				a.log.Warn("claude: remove old project images", "dir", LegacyImageRoot(w.WorkspaceRoot), "err", err)
			}
		}
	})
	return slices.Clone(out), nil
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var shortRe = regexp.MustCompile(`^[0-9a-f]{8}$`)

func validUUID(id string) bool { return uuidRe.MatchString(id) }

// projectOf is the workspace a session's cwd belongs to: a Claude-managed
// worktree's repository, else the cwd itself.
func projectOf(cwd string) string {
	if i := strings.Index(cwd, worktreeMarker); i > 0 {
		return cwd[:i]
	}
	return cwd
}

// session is what Stage knows of one session: its list entry and
// transcript, either may be missing.
type session struct {
	id         string
	entry      Entry
	listed     bool
	transcript Transcript
	hasLog     bool
	// logPath is the transcript's path when hasLog.
	logPath string
}

func (s session) cwd() string {
	if s.hasLog && s.transcript.Cwd != "" {
		return s.transcript.Cwd
	}
	return s.entry.Cwd
}

// read gathers a session from the session list (fresh: read the CLI again)
// and its transcript. ErrNotFound: neither exists.
func (a *Adapter) read(ctx context.Context, id string, fresh bool) (session, error) {
	if !validUUID(id) {
		return session{}, execution.ErrNotFound
	}
	es, err := a.roster.list(ctx, fresh)
	if err != nil {
		return session{}, err
	}
	return a.readListed(ctx, id, es)
}

// readListed gathers a session from the session list es and its
// transcript (read).
func (a *Adapter) readListed(ctx context.Context, id string, es []Entry) (session, error) {
	s := session{id: id}
	s.entry, s.listed = findSession(es, id)
	if p, ok := transcriptPath(a.projects(ctx), s.entry.Cwd, id); ok {
		if t, err := a.logs.read(p); err == nil {
			s.transcript, s.hasLog, s.logPath = t, true, p
		}
	}
	if !s.listed && !s.hasLog {
		return s, execution.ErrNotFound
	}
	return s, nil
}

// maxReply bounds a reply (characters).
const maxReply = 4000

func truncate(s string, n int) (string, bool) {
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	r := []rune(s)
	return string(r[:n]), true
}

// removed reports a `claude rm` of a background session whose last turn had
// ended: the list no longer has it but its transcript is kept. The idle reap
// keeps the entry, an interactive session leaves the list when its process
// exits, and `rm` mid-turn ends the turn unfinished: none is a removal.
func (s session) removed() bool {
	return !s.listed && s.hasLog && s.transcript.Background && s.transcript.TurnOver
}

// observation maps a session (ADR 0005 decision 4). Not listed: idle with
// the reply (the transcript exists), closed (archived) when removed. A
// session a Send stopped to wake reads running meanwhile (send.go).
func (a *Adapter) observation(s session) (execution.Observation, error) {
	state := "idle"
	if s.listed {
		var err error
		if state, err = MapState(s.entry); err != nil {
			return execution.Observation{}, err
		}
		if state == "interrupted" && a.isWaking(s.id) {
			state = "running"
		}
	}
	cwd := s.cwd()
	t := s.transcript
	o := execution.Observation{
		Session:     execution.SessionRef{ConnectionID: a.conn, ExternalSessionID: s.id},
		Project:     projectOf(cwd),
		Directory:   cwd,
		Title:       t.Title,
		State:       state,
		Active:      state == "running" || state == "needs_input",
		Branch:      t.Branch,
		ExecutionID: t.LastPromptID,
		Links:       a.links(s),
		Config:      execution.Config{Options: []execution.ModelOption{}},
		Origin:      t.Entrypoint,
	}
	if o.Title == "" {
		o.Title = s.entry.Name
	}
	if s.removed() {
		o.Closed = &execution.Closure{Signal: execution.ClosedArchived}
	}
	created := t.FirstAt
	if created.IsZero() && s.entry.StartedAt > 0 {
		created = time.UnixMilli(s.entry.StartedAt)
	}
	updated := t.LastAt
	if updated.IsZero() {
		updated = created
	}
	o.CreatedAt, o.UpdatedAt = created.UTC(), updated.UTC()
	if !created.IsZero() {
		c := o.CreatedAt
		o.StartedAt = &c
	}
	if (state == "done" || state == "failed" || state == "interrupted") && !updated.IsZero() {
		u := o.UpdatedAt
		o.EndedAt = &u
	}
	if s.hasLog && t.Reply != "" && state != "running" {
		text, cut := truncate(t.Reply, maxReply)
		o.Reply = &execution.Reply{ItemID: t.ReplyID, RunID: t.LastPromptID, Text: text, Truncated: cut, UpdatedAt: t.ReplyAt.UTC()}
	}
	o.Usage = a.usage(s)
	if o.Project == "" {
		return execution.Observation{}, errors.New("claude session has no working directory")
	}
	return o, nil
}

// usage is the session's usage from its transcript and its subagents'
// (<transcript without .jsonl>/subagents/*.jsonl, tokens only); nil before
// its first turn. Claude records no cost for a background session, so
// CostUSD stays nil (ADR 0005 decision 17).
func (a *Adapter) usage(s session) *execution.Usage {
	t := s.transcript
	if !s.hasLog || t.Turns == 0 {
		return nil
	}
	tok := t.Tokens
	subagents, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(s.logPath, ".jsonl"), "subagents", "*.jsonl"))
	for _, p := range subagents {
		if sub, err := a.logs.read(p); err == nil {
			tok = tok.add(sub.Tokens)
		}
	}
	return &execution.Usage{Turns: t.Turns, InputTokens: tok.Input, CacheReadTokens: tok.CacheRead,
		CacheWriteTokens: tok.CacheWrite, OutputTokens: tok.Output, WorkingMs: t.WorkingMs}
}

// Observe reads the session list (shared, cached) and the transcript.
func (a *Adapter) Observe(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	if ref.ConnectionID != a.conn {
		return execution.Observation{}, execution.ErrNotFound
	}
	s, err := a.read(ctx, ref.ExternalSessionID, false)
	if err != nil {
		return execution.Observation{}, err
	}
	return a.observation(s)
}

// Stop stops a background session (`claude stop <short>`).
func (a *Adapter) Stop(ctx context.Context, ref execution.SessionRef) error {
	if ref.ConnectionID != a.conn {
		return execution.ErrNotFound
	}
	s, err := a.read(ctx, ref.ExternalSessionID, true)
	if err != nil {
		return err
	}
	if !s.listed {
		return execution.ErrNotFound
	}
	if !s.entry.Background() {
		return errors.New("this Claude session runs in a terminal or another app; stop it there")
	}
	defer a.roster.invalidate()
	call, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := a.cli.Run(call, "", "stop", s.entry.ID)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		if strings.Contains(res.Stderr+res.Stdout, "No session matching") {
			return execution.ErrNotFound
		}
		return exitError("stop", res)
	}
	return nil
}

// Attention is every listed session waiting for its user (ADR 0005
// decision 9): a blocked background session, also one Claude's daemon
// retired (attach wakes it), and a waiting terminal, IDE or SDK session.
// Discovery surfaces, adoption tracks: the descriptor's silent attention
// policy keeps these to a waiting row, and the coordinator leaves out a
// session another harness wraps (its observed origin). An unreadable list
// is an error.
func (a *Adapter) Attention(ctx context.Context) ([]execution.SessionRef, error) {
	es, err := a.roster.list(ctx, false)
	if err != nil {
		return nil, err
	}
	out := []execution.SessionRef{}
	for _, e := range es {
		if st, err := MapState(e); err != nil || st != "needs_input" || !validUUID(e.SessionID) {
			continue
		}
		out = append(out, execution.SessionRef{ConnectionID: a.conn, ExternalSessionID: e.SessionID})
	}
	return out, nil
}

// Opening is the session's first typed prompt.
func (a *Adapter) Opening(ctx context.Context, ref execution.SessionRef, full bool) (*execution.Reply, error) {
	if ref.ConnectionID != a.conn {
		return nil, execution.ErrNotFound
	}
	s, err := a.read(ctx, ref.ExternalSessionID, false)
	if err != nil {
		return nil, err
	}
	if !s.hasLog || s.transcript.FirstPrompt == "" {
		return nil, execution.ErrNoReply
	}
	limit := 2000
	if full {
		limit = 1_000_000
	}
	text, cut := truncate(s.transcript.FirstPrompt, limit)
	return &execution.Reply{Text: text, Truncated: cut, UpdatedAt: s.transcript.FirstAt.UTC()}, nil
}

// links: attach while Claude lists the background session, else resume in
// its folder. A terminal, IDE or SDK session whose process runs has none:
// it is answered where it runs (a resume there would open a second client
// on the same session).
func (a *Adapter) links(s session) execution.Links {
	if s.listed && s.entry.Background() && s.entry.ID != "" {
		return execution.Links{Command: "claude attach " + s.entry.ID}
	}
	if s.listed && !s.entry.Background() && s.entry.PID > 0 {
		return execution.Links{}
	}
	if cwd := s.cwd(); cwd != "" {
		return execution.Links{Command: "cd " + shellQuote(cwd) + " && claude --resume " + s.id}
	}
	return execution.Links{Command: "claude --resume " + s.id}
}

// ResolveLink takes a session UUID or the 8-character id `claude agents`
// shows (also inside a pasted `claude attach` or `--resume` command).
func (a *Adapter) ResolveLink(ctx context.Context, raw string) (execution.SessionRef, error) {
	id := ""
	for _, f := range strings.Fields(strings.ToLower(raw)) {
		f = strings.Trim(f, "'\"")
		switch {
		case uuidRe.MatchString(f):
			id = f
		case shortRe.MatchString(f) && id == "":
			es, err := a.roster.list(ctx, true)
			if err != nil {
				return execution.SessionRef{}, err
			}
			e, ok := findShort(es, f)
			if !ok {
				return execution.SessionRef{}, fmt.Errorf("Claude Code lists no session %s. Run claude agents to see its sessions.", f)
			}
			id = e.SessionID
		}
	}
	if id == "" {
		return execution.SessionRef{}, errors.New("paste a Claude session ID: a UUID, or the 8-character ID claude agents shows")
	}
	if _, err := a.read(ctx, id, false); err != nil && !errors.Is(err, execution.ErrNotFound) {
		return execution.SessionRef{}, err
	}
	return execution.SessionRef{ConnectionID: a.conn, ExternalSessionID: id}, nil
}

func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+=:@") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
