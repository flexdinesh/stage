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

// authStatus reuses the cached status within authTTL unless fresh is set.
func (a *Adapter) authStatus(ctx context.Context, fresh bool) (Auth, error) {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if !fresh && !a.auth.at.IsZero() && a.now().Sub(a.auth.at) < authTTL {
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
	au, err := a.authStatus(ctx, false)
	if err != nil {
		return ""
	}
	return au.ProjectsDirectory
}

// Health: Claude Code answers its auth status, is logged in, and still
// uses the connection's config directory.
func (a *Adapter) Health(ctx context.Context) execution.Health {
	h := execution.Health{Identity: a.identity, CheckedAt: a.now().UTC()}
	if _, err := a.checkedAuth(ctx, false); err != nil {
		h.Reason = err.Error()
	} else {
		h.Available = true
	}
	return h
}

func (a *Adapter) checkedAuth(ctx context.Context, fresh bool) (Auth, error) {
	au, err := a.authStatus(ctx, fresh)
	switch {
	case err != nil:
		return au, fmt.Errorf("Stage can't run Claude Code: %w", err)
	case !au.LoggedIn:
		return au, errors.New("Claude Code isn't logged in. Run `claude auth login` in a terminal.")
	case a.identity != "" && au.ConfigDirectory != a.identity:
		return au, fmt.Errorf("Claude Code now uses %s instead of %s. Connect Claude Code again in Settings.", au.ConfigDirectory, a.identity)
	default:
		return au, nil
	}
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
			execution.OpAttention:     available,
			execution.OpSessionSearch: available,
			execution.OpRespond:       a.hooksRunning(),
			execution.OpSend:          available,
			execution.OpSessionRename: {Reason: "Claude Code has no rename interface."},
			execution.OpWorkspaceAdd:  execution.WorkspaceAddNotSupported(Descriptor.Name),
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

// getenv is key in the environment Claude runs with (the last value wins).
func (c *CLI) getenv(key string) string {
	value := ""
	for _, kv := range c.env() {
		if k, v, _ := strings.Cut(kv, "="); k == key {
			value = v
		}
	}
	return value
}

// claudeHome is the home directory Claude runs with.
func (c *CLI) claudeHome() string {
	if home := c.getenv("HOME"); home != "" {
		return home
	}
	return c.home()
}

// claudeJSON is the state file. An explicit CLAUDE_CONFIG_DIR puts the file
// inside that directory even when it points to ~/.claude.
func (c *CLI) claudeJSON(configDir string) string {
	home := c.claudeHome()
	custom := c.getenv("CLAUDE_CONFIG_DIR") != ""
	if !custom && home != "" && filepath.Clean(configDir) == filepath.Join(home, ".claude") {
		return filepath.Join(home, ".claude.json")
	}
	return filepath.Join(configDir, ".claude.json")
}

// Claude Code starts background sessions (`claude --bg`) only in a trusted
// workspace (hasTrustDialogAccepted on the checkout's main root, or on the
// folder or an ancestor up to the checkout top; outside a checkout, up to the
// filesystem root). Its home folder is trusted one interactive session at a
// time, so a background session there needs a saved record above it.
const (
	untrustedReason = "Not trusted in Claude Code. Run claude in this folder once and accept the trust prompt, then refresh."
	homeReason      = "Claude Code trusts the home folder one session at a time, so Stage can't start sessions here. Use a project folder."
)

// sandboxed is Claude's CLAUDE_CODE_SANDBOXED switch, which trusts every
// workspace (Claude's boolean environment values).
func sandboxed(v string) bool {
	return slices.Contains([]string{"1", "true", "yes", "on"}, strings.ToLower(strings.TrimSpace(v)))
}

// worktreeMarker is the path segment of a Claude-managed worktree.
const worktreeMarker = "/.claude/worktrees/"

// Workspaces combines Claude's state projects with cwd evidence from its
// native session list. Machine ops decide whether a folder is a Git checkout
// top (ImportCheckoutTop) and whether a trust record covers it (Trust).
func (a *Adapter) Workspaces(ctx context.Context) ([]execution.WorkspaceInfo, error) {
	au, err := a.checkedAuth(ctx, true)
	if err != nil {
		return nil, err
	}
	path := a.cli.claudeJSON(au.ConfigDirectory)
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("run claude once to initialize its state: %w", err)
	}
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state *struct {
		Projects json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if state == nil {
		return nil, fmt.Errorf("read %s: expected a state object", path)
	}

	// A fresh Claude state object can omit projects before it records a folder.
	var projects map[string]json.RawMessage
	if state.Projects != nil {
		if err := json.Unmarshal(state.Projects, &projects); err != nil {
			return nil, fmt.Errorf("read %s projects: %w", path, err)
		}
		if projects == nil {
			return nil, fmt.Errorf("read %s: expected a projects object", path)
		}
	}
	entries, err := a.roster.list(ctx, true)
	if err != nil {
		return nil, err
	}

	paths := make(map[string]time.Time, len(projects)+len(entries))
	add := func(dir string, at time.Time) {
		if !filepath.IsAbs(dir) {
			return
		}
		dir = filepath.Clean(dir)
		if strings.Contains(filepath.ToSlash(dir)+"/", worktreeMarker) {
			return
		}
		if _, ok := paths[dir]; !ok {
			paths[dir] = at
		}
	}
	var records []string
	for dir, raw := range projects {
		add(dir, st.ModTime().UTC())
		var project struct {
			Trusted json.RawMessage `json:"hasTrustDialogAccepted"`
		}
		// Claude looks keys up verbatim, so a record is the key as written.
		if json.Unmarshal(raw, &project) == nil && string(project.Trusted) == "true" && filepath.IsAbs(dir) {
			records = append(records, dir)
		}
	}
	for _, entry := range entries {
		add(entry.Cwd, time.Time{})
	}
	slices.Sort(records)
	trust := &execution.TrustRule{Records: records, Reason: untrustedReason}
	home := filepath.Clean(a.cli.claudeHome())
	everyFolder := sandboxed(a.cli.getenv("CLAUDE_CODE_SANDBOXED"))
	out := make([]execution.WorkspaceInfo, 0, len(paths))
	for dir, at := range paths {
		w := execution.WorkspaceInfo{ID: dir, Title: filepath.Base(dir), WorkspaceRoot: dir, CreatedAt: at, UpdatedAt: at, Local: true, ImportIf: execution.ImportCheckoutTop}
		switch {
		case everyFolder:
		case dir == home:
			w.Trust = &execution.TrustRule{Records: records, Reason: homeReason}
		default:
			w.Trust = trust
		}
		out = append(out, w)
	}
	slices.SortFunc(out, func(x, y execution.WorkspaceInfo) int { return strings.Compare(x.ID, y.ID) })
	return out, nil
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

// Observe reads the session list and the transcript. Named tracking reads
// refresh the roster to recheck archive and live state.
func (a *Adapter) Observe(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	if ref.ConnectionID != a.conn {
		return execution.Observation{}, execution.ErrNotFound
	}
	mode := execution.SessionReadModeFrom(ctx)
	s, err := a.read(ctx, ref.ExternalSessionID, mode != "")
	if err != nil {
		return execution.Observation{}, err
	}
	o, err := a.observation(s)
	if mode == execution.SessionReadPreview {
		o.Reply = nil
	}
	return o, err
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
