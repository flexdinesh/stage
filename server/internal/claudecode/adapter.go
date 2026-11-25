package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/gitmeta"
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
	Now        func() time.Time
}

// Adapter is the Claude Code execution.Adapter for one connection.
type Adapter struct {
	conn       int64
	cli        *CLI
	identity   string
	roster     *roster
	launchWait time.Duration
	now        func() time.Time

	authMu sync.Mutex
	auth   authCache

	invMu  sync.Mutex
	invAt  time.Time // ~/.claude.json mtime of inv
	invKey string
	inv    []execution.WorkspaceInfo
}

var (
	_ execution.Adapter      = (*Adapter)(nil)
	_ execution.Correlator   = (*Adapter)(nil)
	_ execution.LinkResolver = (*Adapter)(nil)
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
	return &Adapter{
		conn: o.Connection, cli: o.CLI, identity: o.Identity, launchWait: o.LaunchWait, now: o.Now,
		roster: &roster{cli: o.CLI, ttl: o.RosterTTL, now: o.Now},
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

var efforts = []string{"low", "medium", "high", "xhigh", "max"}

// Permission modes (--permission-mode).
var permissions = []execution.Choice{
	choice("manual", "Ask before acting"),
	choice("acceptEdits", "Accept edits"),
	choice("auto", "Auto"),
	choice("plan", "Plan only"),
	choice("dontAsk", "Don't ask"),
	choice("bypassPermissions", "Bypass permissions"),
}

// Workspace kinds.
const (
	workspaceRoot     = "root"
	workspaceWorktree = "worktree"
)

func choice(id, label string) execution.Choice {
	return execution.Choice{ID: id, Label: label, Availability: execution.Availability{Available: true}}
}

var available = execution.Availability{Available: true}

// Unavailable operation reasons.
const (
	reasonAnswer = "Answer in Claude Code: copy the resume command and run it in a terminal."
	reasonBase   = "Claude Code creates worktrees from the checkout's current HEAD and names the branch itself."
	reasonImages = "Claude Code background sessions can't take image attachments from Stage yet."
	reasonFolder = "Open Claude Code in the folder once; Stage lists the folders Claude Code knows."
)

// Features is static: the catalog is Claude's aliases, not a probe.
func (a *Adapter) Features(context.Context) (execution.Capabilities, error) {
	effort := execution.Option{ID: optionEffort}
	for _, e := range efforts {
		effort.Values = append(effort.Values, execution.StringValue(e))
	}
	return execution.Capabilities{
		Operations: map[string]execution.Availability{
			"launch": available, "stop": available, "adopt": available, "reply": available, "opening": available,
			"images":            {Reason: reasonImages},
			"predecessorBase":   {Reason: reasonBase},
			"worktreeBase":      {Reason: reasonBase},
			"registerWorkspace": {Reason: reasonFolder},
			execution.OpRespond: {Reason: reasonAnswer},
			execution.OpSend:    {Reason: reasonAnswer},
		},
		Models:      slices.Clone(models),
		Options:     []execution.Option{effort},
		Permissions: slices.Clone(permissions),
		Modes:       []execution.Choice{},
		Workspaces:  []execution.Choice{choice(workspaceRoot, "Project folder"), choice(workspaceWorktree, "Claude Code worktree")},
	}, nil
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
// file); a Git checkout's top is importable. No file: none. An unreadable
// or malformed file is an error, so discovery keeps its last inventory.
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
		w := execution.WorkspaceInfo{ID: dir, Title: filepath.Base(dir), WorkspaceRoot: dir, CreatedAt: st.ModTime().UTC(), UpdatedAt: st.ModTime().UTC()}
		if fi, err := os.Stat(filepath.Join(dir, ".git")); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
			w.Importable = true
			w.Repo, w.RepoPath = gitmeta.Checkout(dir, dir)
			w.OriginRemote = ""
		}
		out = append(out, w)
	}
	slices.SortFunc(out, func(x, y execution.WorkspaceInfo) int { return strings.Compare(x.ID, y.ID) })
	a.inv, a.invAt, a.invKey = out, st.ModTime(), path
	return slices.Clone(out), nil
}

// RegisterWorkspace: Claude Code learns folders when it runs in them.
func (a *Adapter) RegisterWorkspace(context.Context, string, string) (execution.WorkspaceInfo, error) {
	return execution.WorkspaceInfo{}, fmt.Errorf("%w: %s", execution.ErrUnavailable, reasonFolder)
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
}

func (s session) cwd() string {
	if s.hasLog && s.transcript.Cwd != "" {
		return s.transcript.Cwd
	}
	return s.entry.Cwd
}

// read gathers a session from the session list (fresh: read the CLI again)
// and its transcript. ErrNotFound: neither exists. A T3 session is refused.
func (a *Adapter) read(ctx context.Context, id string, fresh bool) (session, error) {
	if !validUUID(id) {
		return session{}, execution.ErrNotFound
	}
	es, err := a.roster.list(ctx, fresh)
	if err != nil {
		return session{}, err
	}
	s := session{id: id}
	s.entry, s.listed = findSession(es, id)
	if p, ok := transcriptPath(a.projects(ctx), s.entry.Cwd, id); ok {
		if t, err := ReadTranscript(p); err == nil {
			s.transcript, s.hasLog = t, true
		}
	}
	if !s.listed && !s.hasLog {
		return s, execution.ErrNotFound
	}
	if s.hasLog && s.transcript.Entrypoint == entrypointT3 {
		return s, &execution.Refused{Reason: refusedT3}
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
// the reply (the transcript exists), closed (archived) when removed.
func (a *Adapter) observation(s session) (execution.Observation, error) {
	state := "idle"
	if s.listed {
		var err error
		if state, err = MapState(s.entry); err != nil {
			return execution.Observation{}, err
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
		ModelLabel:  t.Model,
		ExecutionID: t.LastPromptID,
		Links:       a.links(s),
		Artifacts:   []execution.Artifact{},
		Base:        execution.Availability{Reason: reasonBase},
		Config:      execution.Config{Options: []execution.ModelOption{}},
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
		o.Reply = &execution.Reply{ItemID: t.ReplyID, RunID: t.LastPromptID, MessageID: t.ReplyID, Text: text, Truncated: cut, UpdatedAt: t.ReplyAt.UTC()}
	}
	if o.Project == "" {
		return execution.Observation{}, errors.New("claude session has no working directory")
	}
	return o, nil
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

// Attention is empty: Stage watches the sessions it launched or adopted
// from its own records, and Claude sessions it does not own are not its to
// discover (ADR 0005 decision 9).
func (a *Adapter) Attention(context.Context) ([]execution.SessionRef, error) {
	return []execution.SessionRef{}, nil
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
// its folder.
func (a *Adapter) links(s session) execution.Links {
	if s.listed && s.entry.Background() && s.entry.ID != "" {
		return execution.Links{Command: "claude attach " + s.entry.ID}
	}
	if cwd := s.cwd(); cwd != "" {
		return execution.Links{Command: "cd " + shellQuote(cwd) + " && claude --resume " + s.id}
	}
	return execution.Links{Command: "claude --resume " + s.id}
}

// Destinations reads only the cached session list and the transcript.
func (a *Adapter) Destinations(ctx context.Context, ref execution.SessionRef) execution.Links {
	if ref.ConnectionID != a.conn || !validUUID(ref.ExternalSessionID) {
		return execution.Links{}
	}
	s := session{id: ref.ExternalSessionID}
	if es, ok := a.roster.cached(); ok {
		s.entry, s.listed = findSession(es, s.id)
	}
	a.authMu.Lock()
	root := a.auth.auth.ProjectsDirectory
	a.authMu.Unlock()
	if p, ok := transcriptPath(root, s.entry.Cwd, s.id); ok {
		if t, err := ReadHead(p); err == nil {
			s.transcript, s.hasLog = t, true
		}
	}
	return a.links(s)
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
