package codex

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

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/gitmeta"
)

// Adapter is one Codex connection: the daemon a Client reaches, for the
// connection Stage recorded for its Codex home.
type Adapter struct {
	client     *Client
	connection int64
	// command is the CLI the resume command names ("codex" by default).
	command string
	now     func() time.Time

	mu        sync.Mutex
	health    execution.Health
	healthAt  time.Time
	models    []model
	modelsAt  time.Time
	modelsErr error
	// dirs are threads' directories seen by Observe and Launch.
	dirs map[string]string
}

// Cache windows: health is read on every sync tick; the model catalog
// changes with Codex releases.
const (
	healthTTL = 5 * time.Second
	modelsTTL = time.Minute
)

// New adapts client for connection. command is the configured CLI ("" =
// codex), named by resume commands.
func New(client *Client, connection int64, command string) *Adapter {
	if strings.TrimSpace(command) == "" {
		command = "codex"
	}
	return &Adapter{client: client, connection: connection, command: command, now: time.Now}
}

var _ execution.Adapter = (*Adapter)(nil)
var _ execution.LinkResolver = (*Adapter)(nil)
var _ execution.EventSource = (*Adapter)(nil)

// threadID: Codex thread ids are UUIDs (v7 for new threads).
var threadID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (a *Adapter) valid(ref execution.SessionRef) bool {
	return ref.ConnectionID == a.connection && threadID.MatchString(ref.ExternalSessionID)
}

func (a *Adapter) ref(id string) execution.SessionRef {
	return execution.SessionRef{ConnectionID: a.connection, ExternalSessionID: id}
}

// Health connects to the daemon (starting it when needed) and checks the
// login. Identity is the Codex home the daemon serves.
func (a *Adapter) Health(ctx context.Context) execution.Health {
	a.mu.Lock()
	if !a.healthAt.IsZero() && a.now().Sub(a.healthAt) < healthTTL {
		h := a.health
		a.mu.Unlock()
		return h
	}
	a.mu.Unlock()
	h := execution.Health{CheckedAt: a.now().UTC()}
	p, err := a.client.Probe(ctx)
	switch {
	case err != nil:
		h.Reason = Problem(err)
	case !p.SignedIn():
		h.Identity = p.CodexHome
		h.Reason = signedOut
	default:
		h.Available, h.Identity = true, p.CodexHome
	}
	a.mu.Lock()
	a.health, a.healthAt = h, a.now()
	a.mu.Unlock()
	return h
}

const signedOut = "Codex isn't signed in. Run codex login, then connect again."

// Problem is the user-facing sentence for a failure to reach Codex.
func Problem(err error) string {
	var down *DaemonDownError
	switch {
	case errors.Is(err, ErrNotInstalled):
		return "Codex CLI not found (" + strings.TrimPrefix(err.Error(), ErrNotInstalled.Error()+": ") + "). Install Codex or set its path."
	case errors.As(err, &down):
		return down.Reason
	case unsupported(err):
		return "This Codex version lacks an app-server method Stage uses (" + err.Error() + "). Update Codex."
	}
	return "Codex is unreachable: " + err.Error()
}

// Permission presets: Codex's approval policy and sandbox, as its own
// /approvals menu pairs them.
type permission struct {
	ID, Label, Approval, Sandbox string
}

var permissions = []permission{
	{ID: "read-only", Label: "Read only", Approval: "on-request", Sandbox: "read-only"},
	{ID: "auto", Label: "Auto", Approval: "on-request", Sandbox: "workspace-write"},
	{ID: "full-access", Label: "Full access", Approval: "never", Sandbox: "danger-full-access"},
}

func permissionFor(id string) (permission, bool) {
	if id == "" {
		id = "auto"
	}
	i := slices.IndexFunc(permissions, func(p permission) bool { return p.ID == id })
	if i < 0 {
		return permission{}, false
	}
	return permissions[i], true
}

// effortOption is the per-model reasoning effort option.
const effortOption = "effort"

var available = execution.Availability{Available: true}

const answerInCodex = "Answer in Codex: copy the resume command and run it in a terminal."

// Features reads Codex's model catalog (cached a minute).
func (a *Adapter) Features(ctx context.Context) (execution.Capabilities, error) {
	ms, err := a.catalog(ctx)
	if err != nil {
		return execution.Capabilities{}, err
	}
	c := execution.Capabilities{
		Operations: map[execution.Op]execution.Availability{
			execution.OpLaunch: available, execution.OpStop: available, execution.OpAdopt: available, execution.OpReply: available, execution.OpOpening: available, execution.OpAttention: available,
			execution.OpImages:          available,
			execution.OpPredecessorBase: {Reason: "Codex runs in the project checkout; Stage can't start it on another task's branch."},
			execution.OpWorktreeBase:    {Reason: "Codex runs in the project checkout."},
			execution.OpRespond:         {Reason: answerInCodex},
			execution.OpSend:            {Reason: answerInCodex},
		},
		Workspaces: []execution.Choice{{ID: "root", Label: "Project checkout", Availability: available}},
	}
	for _, p := range permissions {
		c.Permissions = append(c.Permissions, execution.Choice{ID: p.ID, Label: p.Label, Availability: available})
	}
	for _, m := range ms {
		if m.Hidden || m.ID == "" {
			continue
		}
		label := m.DisplayName
		if label == "" {
			label = m.ID
		}
		em := execution.Model{Choice: execution.Choice{ID: m.ID, Label: label, Availability: available}, Group: "Codex"}
		if len(m.SupportedReasoningEfforts) > 0 {
			opt := execution.Option{ID: effortOption}
			for _, e := range m.SupportedReasoningEfforts {
				opt.Values = append(opt.Values, execution.StringValue(e.ReasoningEffort))
			}
			if m.DefaultReasoningEffort != "" {
				d := execution.StringValue(m.DefaultReasoningEffort)
				opt.Default = &d
			}
			em.Options = []execution.Option{opt}
		}
		c.Models = append(c.Models, em)
	}
	return c, nil
}

func (a *Adapter) catalog(ctx context.Context) ([]model, error) {
	a.mu.Lock()
	if !a.modelsAt.IsZero() && a.now().Sub(a.modelsAt) < modelsTTL {
		ms, err := a.models, a.modelsErr
		a.mu.Unlock()
		return ms, err
	}
	a.mu.Unlock()
	var all []model
	var cursor *string
	for range 20 {
		var page modelsPage
		params := map[string]any{"limit": 100}
		if cursor != nil {
			params["cursor"] = *cursor
		}
		if err := a.client.call(ctx, "model/list", params, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	a.mu.Lock()
	a.models, a.modelsAt, a.modelsErr = all, a.now(), nil
	a.mu.Unlock()
	return all, nil
}

// Workspaces are the directories trusted in Codex (config projects). One
// inside a Git checkout is importable; others stay manual-bind only.
// Codex reports no archive or activity for them.
func (a *Adapter) Workspaces(ctx context.Context) ([]execution.WorkspaceInfo, error) {
	var res configResponse
	if err := a.client.call(ctx, "config/read", map[string]any{}, &res); err != nil {
		return nil, err
	}
	if res.Config == nil {
		return nil, errors.New("codex config/read: missing config")
	}
	var projects map[string]struct {
		TrustLevel string `json:"trust_level"`
	}
	if raw, ok := res.Config["projects"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &projects); err != nil {
			return nil, fmt.Errorf("codex config projects: %w", err)
		}
	}
	paths := make([]string, 0, len(projects))
	for p, v := range projects {
		if v.TrustLevel == "trusted" && filepath.IsAbs(p) {
			paths = append(paths, filepath.Clean(p))
		}
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	out := make([]execution.WorkspaceInfo, 0, len(paths))
	for _, p := range paths {
		out = append(out, workspace(p))
	}
	return out, nil
}

// workspace describes a trusted directory: its Git checkout (read from
// .git on this machine) decides import, its origin the match key.
func workspace(dir string) execution.WorkspaceInfo {
	w := execution.WorkspaceInfo{ID: dir, Title: title(dir), WorkspaceRoot: dir}
	if info, err := os.Stat(dir); err == nil {
		t := info.ModTime().UTC()
		w.CreatedAt, w.UpdatedAt = t, t
	}
	if gitTop(dir) != "" {
		w.Importable = true
		w.Repo, w.RepoPath = gitmeta.Checkout(dir, string(filepath.Separator))
	}
	return w
}

// gitTop is the nearest directory at or above dir holding .git ("" none).
func gitTop(dir string) string {
	for top := filepath.Clean(dir); ; top = filepath.Dir(top) {
		if _, err := os.Stat(filepath.Join(top, ".git")); err == nil {
			return top
		}
		if top == filepath.Dir(top) {
			return ""
		}
	}
}

// title is the directory's name, or parent/name for main, master or trunk.
func title(dir string) string {
	base := filepath.Base(dir)
	if base == string(filepath.Separator) || base == "." {
		return dir
	}
	switch base {
	case "main", "master", "trunk":
		if parent := filepath.Base(filepath.Dir(dir)); parent != string(filepath.Separator) && parent != "." {
			return parent + "/" + base
		}
	}
	return base
}

// Destinations: Codex has no web or app destination; the resume command
// attaches the user's terminal to the thread (live, through the daemon).
func (a *Adapter) Destinations(_ context.Context, ref execution.SessionRef) execution.Links {
	if !a.valid(ref) {
		return execution.Links{}
	}
	a.mu.Lock()
	dir := a.dirs[ref.ExternalSessionID]
	a.mu.Unlock()
	return execution.Links{Command: a.resumeIn(dir, ref.ExternalSessionID)}
}

// resumeIn is the resume command run from the thread's directory.
func (a *Adapter) resumeIn(dir, id string) string {
	cmd := shellQuote(a.command) + " resume " + id
	if dir == "" {
		return cmd
	}
	return "cd " + shellQuote(dir) + " && " + cmd
}

// shellQuote quotes s for a POSIX shell unless it is plainly safe.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:@%+=,") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ResolveLink accepts a thread id, or a `codex resume <id>` command. An
// ephemeral thread has no history. A thread another harness started is
// refused by the coordinator (its observed origin).
func (a *Adapter) ResolveLink(ctx context.Context, s string) (execution.SessionRef, error) {
	fields := strings.Fields(s)
	id := ""
	if len(fields) > 0 {
		id = strings.ToLower(fields[len(fields)-1])
	}
	ref := a.ref(id)
	if !a.valid(ref) {
		return execution.SessionRef{}, errors.New("paste a Codex thread id (codex resume <id>)")
	}
	var res threadResponse
	if err := a.client.call(ctx, "thread/read", map[string]any{"threadId": id}, &res); err != nil {
		if notLoaded(err) {
			return execution.SessionRef{}, execution.ErrNotFound
		}
		return execution.SessionRef{}, err
	}
	switch {
	case res.Thread.ID != id:
		return execution.SessionRef{}, errors.New("codex returned a different thread")
	case res.Thread.Ephemeral:
		return execution.SessionRef{}, errors.New("this Codex thread is ephemeral: it keeps no history Stage can follow")
	case res.Thread.ParentThreadID != nil && *res.Thread.ParentThreadID != "":
		return execution.SessionRef{}, errors.New("this is a Codex subagent thread; adopt its parent thread")
	}
	return ref, nil
}
