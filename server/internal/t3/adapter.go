package t3

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Adapter exposes retained Stage operations, not the T3 protocol surface.
type Adapter struct {
	Client       Client
	ConnectionID int64
	replies      sync.Map
}

// NewAdapter is the adapter of the original T3 connection (tests).
func NewAdapter(c Client) *Adapter { return &Adapter{Client: c, ConnectionID: 1} }

// NewConnectionAdapter scopes c to one T3 connection: every session it
// observes or launches belongs to that connection.
func NewConnectionAdapter(c Client, id int64) *Adapter { return &Adapter{Client: c, ConnectionID: id} }
func (a *Adapter) Health(ctx context.Context) execution.Health {
	st := a.Client.Status(ctx)
	if !st.HasToken {
		// Not paired: never contacted in the background.
		return execution.Health{Availability: execution.Availability{Reason: "T3 is not connected. Connect it in Settings."}, Identity: st.EnvironmentID}
	}
	p := a.Client.Probe(ctx)
	reason := ""
	ok := st.Connected && p.Reachable
	if !ok {
		reason = "T3 is unavailable. Check its connection in Settings."
	}
	return execution.Health{Availability: execution.Availability{Available: ok, Reason: reason}, Identity: st.EnvironmentID, CheckedAt: p.CheckedAt}
}
func (a *Adapter) Workspaces(ctx context.Context) ([]execution.WorkspaceInfo, error) {
	return a.Client.ListProjects(ctx)
}
func ModelKey(provider, model string) string {
	b, _ := json.Marshal([2]string{provider, model})
	return string(b)
}
func LegacyConfig(provider, model string, opts []execution.ModelOption, permission, mode string) execution.Config {
	key := ""
	if provider != "" && model != "" {
		key = ModelKey(provider, model)
	}
	return execution.Config{Model: key, Options: opts, Permission: permission, Mode: mode}
}
func DecodeModelKey(key string) (string, string, error) {
	var pair []string
	if err := json.Unmarshal([]byte(key), &pair); err != nil || len(pair) != 2 || pair[0] == "" || pair[1] == "" {
		return "", "", errors.New("invalid T3 model selection; choose a model from the catalog")
	}
	return pair[0], pair[1], nil
}
func (a *Adapter) Features(ctx context.Context) (execution.Capabilities, error) {
	c, err := a.Client.Capabilities(ctx)
	if err != nil {
		return execution.Capabilities{}, err
	}
	yes := execution.Availability{Available: true}
	out := execution.Capabilities{Operations: map[execution.Op]execution.Availability{}, Models: []execution.Model{}}
	for _, op := range []execution.Op{execution.OpLaunch, execution.OpStop, execution.OpAdopt, execution.OpOpening, execution.OpReply, execution.OpAttention, execution.OpImages, execution.OpPredecessorBase, execution.OpWorktreeBase} {
		out.Operations[op] = yes
	}
	for _, op := range []execution.Op{execution.OpRespond, execution.OpSend} {
		out.Operations[op] = execution.Availability{Reason: "Answer in T3: open the thread there."}
	}
	for _, mode := range []string{"approval-required", "auto-accept-edits", "auto", "full-access"} {
		out.Permissions = append(out.Permissions, execution.Choice{ID: mode, Label: mode, Availability: yes})
	}
	for _, mode := range []string{"default", "plan"} {
		out.Modes = append(out.Modes, execution.Choice{ID: mode, Label: mode, Availability: yes})
	}
	for _, mode := range []string{"root", "worktree"} {
		out.Workspaces = append(out.Workspaces, execution.Choice{ID: mode, Label: mode, Availability: yes})
	}
	for _, p := range c.Providers {
		av := yes
		if len(p.Constraints) > 0 {
			av = execution.Availability{Reason: strings.Join(p.Constraints, "; ")}
		}
		for _, m := range p.Models {
			label := m.Label
			if label == "" {
				label = m.ID
			}
			group := p.DisplayName
			if group == "" {
				group = p.ProviderInstanceID
			}
			model := execution.Model{Choice: execution.Choice{ID: ModelKey(p.ProviderInstanceID, m.ID), Label: label, Availability: av}, Group: group, Options: []execution.Option{}}
			for _, o := range m.Options {
				model.Options = append(model.Options, execution.Option{ID: o.ID, Values: o.Values, Default: o.Default})
			}
			// Unreported runtime modes leave Permissions nil: the model inherits the harness list.
			if p.RuntimeModes != nil {
				model.Permissions = []execution.Choice{}
			}
			for _, mode := range p.RuntimeModes {
				model.Permissions = append(model.Permissions, execution.Choice{ID: string(mode), Label: string(mode), Availability: av})
			}
			out.Models = append(out.Models, model)
		}
	}
	return out, nil
}
func (a *Adapter) validate(ctx context.Context, in execution.Intent) error {
	c, err := a.Features(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(c.Workspaces, func(w execution.Choice) bool { return w.ID == in.Workspace.Kind && w.Available }) {
		return &execution.Rejected{Reason: "Workspace mode is unavailable. Choose a supported mode."}
	}
	if in.Config.Model == "" && len(in.Config.Options) > 0 {
		return &execution.Rejected{Reason: "Choose a model before setting model options."}
	}
	if in.Config.Model == "" && in.Config.Permission != "" && !slices.Contains([]string{"approval-required", "auto-accept-edits", "auto", "full-access"}, in.Config.Permission) {
		return &execution.Rejected{Reason: "Permission mode is unavailable. Choose a supported mode."}
	}
	if in.Config.Model != "" {
		idx := slices.IndexFunc(c.Models, func(m execution.Model) bool { return m.ID == in.Config.Model })
		if idx < 0 {
			return &execution.Rejected{Reason: "Selected model is unavailable. Choose another model."}
		}
		m := c.Models[idx]
		if !m.Available {
			return &execution.Rejected{Reason: m.Reason}
		}
		if in.Config.Permission != "" && !slices.ContainsFunc(c.PermissionsFor(m), func(p execution.Choice) bool { return p.ID == in.Config.Permission && p.Available }) {
			return &execution.Rejected{Reason: "Selected permission mode is unavailable for this model. Choose a supported mode."}
		}
		for _, opt := range in.Config.Options {
			if !slices.ContainsFunc(m.Options, func(o execution.Option) bool { return o.ID == opt.ID && slices.Contains(o.Values, opt.Value) }) {
				return &execution.Rejected{Reason: "Model option " + opt.ID + " is unavailable. Choose a supported value."}
			}
		}
	}
	if in.Config.Mode != "" && !slices.ContainsFunc(c.Modes, func(m execution.Choice) bool { return m.ID == in.Config.Mode }) {
		return &execution.Rejected{Reason: "Interaction mode is unavailable. Choose a supported mode."}
	}
	return nil
}

func (d ThreadDetail) Observation() execution.Observation {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = d.CreatedAt
	}
	if d.SettledAt != nil && d.SettledAt.After(d.UpdatedAt) {
		d.UpdatedAt = *d.SettledAt
	}
	state := "idle"
	status := d.Status
	// T3 reports the newest run, even a queued follow-up cancelled before it
	// started. Such a run did no work, so the newest other run decides.
	last := max(0, slices.IndexFunc(d.RecentRuns, func(r Run) bool {
		return r.StartedAt != nil || (r.Status != StatusCancelled && r.Status != StatusRolledBack)
	}))
	switch {
	case len(d.RecentRuns) == 0:
	case status == StatusIdle, last > 0 && (status == StatusCancelled || status == StatusRolledBack):
		status = d.RecentRuns[last].Status
	default:
		last = 0
	}
	switch status {
	case StatusPreparing, StatusQueued, StatusStarting, StatusRunning, StatusWaiting:
		state = "running"
	case StatusCompleted:
		state = "done"
	case StatusFailed:
		state = "failed"
	case StatusInterrupted:
		state = "interrupted"
	case StatusCancelled, StatusRolledBack:
		state = "cancelled"
	case StatusIdle:
		if d.Settled {
			state = "done"
		}
	default:
		state = "unknown"
	}
	if d.ActiveRunID != "" {
		state = "running"
	}
	if d.PendingRequestCount > 0 {
		state = "needs_input"
	}
	o := execution.Observation{ModelLabel: d.Model, Session: execution.SessionRef{ConnectionID: 1, ExternalSessionID: d.ThreadID}, Project: d.ProjectID, Title: d.Title, State: state, Active: state == "running" || state == "needs_input", Archived: d.Archived, Child: d.RelationshipToParent == "subagent", Branch: strings.TrimSpace(d.Branch), Directory: d.WorktreePath, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, ExecutionID: d.LatestRunID, Artifacts: d.PullRequests, Config: LegacyConfig(d.ProviderInstanceID, d.Model, nil, string(d.RuntimeMode), string(d.InteractionMode)), Links: execution.Links{App: d.Link}}
	if d.ParentThreadID != "" {
		o.Parent = &execution.SessionRef{ConnectionID: 1, ExternalSessionID: d.ParentThreadID}
	}
	if d.Settled {
		o.Closed = &execution.Closure{Signal: execution.ClosedSettled}
	}
	if len(d.RecentRuns) > 0 {
		o.EndedAt = d.RecentRuns[last].CompletedAt
		old := d.RecentRuns[len(d.RecentRuns)-1]
		switch {
		case d.RunCount > len(d.RecentRuns) && !d.CreatedAt.IsZero():
			o.StartedAt = &d.CreatedAt
		case old.StartedAt != nil:
			o.StartedAt = old.StartedAt
		case !old.RequestedAt.IsZero():
			o.StartedAt = &old.RequestedAt
		}
	}
	switch {
	case o.Directory == "":
		o.Base.Reason = "Session ran at project root."
	case o.Branch == "":
		o.Base.Reason = "Session has no branch."
	case len(d.PullRequests) > 0 && !slices.ContainsFunc(d.PullRequests, func(p PullRequest) bool { return !strings.EqualFold(p.State, "merged") }):
		o.Base.Reason = "The predecessor's pull requests are merged."
	default:
		o.Base.Available = true
	}
	return o
}
func (t Thread) Observation() execution.Observation { return (ThreadDetail{Thread: t}).Observation() }
func Observations(ts []Thread) []execution.Observation {
	out := make([]execution.Observation, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Observation())
	}
	return out
}
func (a *Adapter) Observe(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	if ref.ConnectionID != a.ConnectionID {
		return execution.Observation{}, execution.ErrUnavailable
	}
	d, err := a.Client.ReadThread(ctx, ref.ExternalSessionID)
	if errors.Is(err, ErrNotFound) {
		return execution.Observation{}, execution.ErrNotFound
	}
	if err != nil {
		return execution.Observation{}, err
	}
	if d.ThreadID != ref.ExternalSessionID {
		return execution.Observation{}, errors.New("T3 returned a different session")
	}
	o := d.Observation()
	o.Session = ref
	if o.Parent != nil {
		o.Parent.ConnectionID = ref.ConnectionID
	}
	o.Links = a.Destinations(ctx, ref)
	if o.Links.App == "" {
		o.Links.App = d.Link
	}
	if err = o.Valid(); err != nil {
		return o, err
	}
	cached, found := a.replies.Load(d.ThreadID)
	if (!o.Active || o.State == "needs_input") && (!found || cached != d.UpdatedAt) {
		r, e := a.Client.LatestReply(ctx, LatestReplyInput{ThreadID: d.ThreadID, MaxChars: 4000, ItemCount: d.ItemCount})
		if e == nil {
			o.Reply = &r
			a.replies.Store(d.ThreadID, d.UpdatedAt)
		} else if errors.Is(e, ErrNoReply) {
			a.replies.Store(d.ThreadID, d.UpdatedAt)
		}
	}
	return o, nil
}
func (a *Adapter) Stop(ctx context.Context, ref execution.SessionRef) error {
	if ref.ConnectionID != a.ConnectionID {
		return execution.ErrUnavailable
	}
	return a.Client.Interrupt(ctx, ref.ExternalSessionID)
}
func (a *Adapter) Attention(ctx context.Context) ([]execution.SessionRef, error) {
	out := []execution.SessionRef{}
	var cursor *int
	settled := false
	for range 10000 {
		p, err := a.Client.ListThreads(ctx, ListThreadsInput{AllProjects: true, Statuses: []ThreadStatus{StatusPreparing, StatusQueued, StatusStarting, StatusRunning, StatusWaiting}, Settled: &settled, Limit: 100, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, t := range p.Threads {
			if t.RelationshipToParent != "subagent" {
				out = append(out, execution.SessionRef{ConnectionID: a.ConnectionID, ExternalSessionID: t.ThreadID})
			}
		}
		if p.NextCursor == nil {
			return out, nil
		}
		if cursor != nil && *p.NextCursor <= *cursor {
			return nil, errors.New("T3 attention cursor did not advance")
		}
		cursor = p.NextCursor
	}
	return nil, errors.New("T3 attention limit exceeded")
}
func (a *Adapter) Destinations(ctx context.Context, ref execution.SessionRef) execution.Links {
	if ref.ConnectionID != a.ConnectionID {
		return execution.Links{}
	}
	env := a.Client.Status(ctx).EnvironmentID
	web := ""
	switch c := a.Client.(type) {
	case interface{ PublicURL() string }:
		web = WebThreadURL(c.PublicURL(), env, ref.ExternalSessionID)
	case interface{ WebBase() string }: // the fake (T3_FAKE)
		web = WebThreadURL(c.WebBase(), env, ref.ExternalSessionID)
	}
	return execution.Links{Web: web, Native: AppThreadURL(env, ref.ExternalSessionID)}
}
func (a *Adapter) Opening(ctx context.Context, ref execution.SessionRef, full bool) (*execution.Reply, error) {
	r := OpeningReader{T3: a.Client}
	it, err := r.Opening(ctx, ref.ExternalSessionID)
	if errors.Is(err, ErrNotFound) {
		return nil, execution.ErrNotFound
	}
	if err != nil || it == nil {
		return nil, err
	}
	text, truncated := it.Text, it.Truncated
	if full && truncated {
		text, truncated, err = r.FullText(ctx, ref.ExternalSessionID, it.ItemID)
	}
	return &execution.Reply{ItemID: it.ItemID, Text: text, Truncated: truncated, UpdatedAt: it.UpdatedAt}, err
}

type launchCheckpoint struct {
	Hold        string       `json:"hold,omitempty"`
	Scope       string       `json:"scope"`
	Environment string       `json:"environment"`
	Expected    string       `json:"expected"`
	Sent        bool         `json:"sent"`
	Uploads     []Attachment `json:"uploads"`
}
type launchScopeClient interface {
	LaunchScope(context.Context, string) (scope, environment, expected string, err error)
	LaunchPinned(context.Context, LaunchInput, string, string) (LaunchResult, error)
}

func (a *Adapter) Launch(ctx context.Context, in execution.Intent, checkpoint execution.Checkpoint, save execution.SaveCheckpoint) (execution.Observation, error) {
	var cp launchCheckpoint
	if checkpoint.Version != 0 {
		if checkpoint.Version != 1 {
			return execution.Observation{}, errors.New("unsupported launch checkpoint")
		}
		if err := json.Unmarshal(checkpoint.Data, &cp); err != nil {
			return execution.Observation{}, err
		}
	}
	if checkpoint.Version != 0 && (cp.Scope == "" || cp.Environment == "") {
		return execution.Observation{}, errors.New("Launch identity is incomplete. Link the existing session.")
	}
	if cp.Hold != "" {
		return execution.Observation{}, errors.New(cp.Hold)
	}
	scoped, ok := a.Client.(launchScopeClient)
	if !ok {
		return execution.Observation{}, errors.New("T3 client cannot guarantee launch identity")
	}
	scope, environment, expected, err := scoped.LaunchScope(ctx, in.RequestID)
	if err != nil {
		return execution.Observation{}, err
	}
	if cp.Environment != "" && cp.Environment != environment {
		return execution.Observation{}, errors.New("T3 environment changed. Link the existing session; this launch will not be replayed.")
	}
	if cp.Sent && cp.Expected != "" {
		o, e := a.Observe(ctx, execution.SessionRef{ConnectionID: in.ConnectionID, ExternalSessionID: cp.Expected})
		if e == nil {
			if o.Project != in.Project {
				return o, errors.New("recovered session has a different project")
			}
			return o, nil
		}
		if !errors.Is(e, execution.ErrNotFound) {
			return o, e
		}
	}
	if cp.Scope != "" && (cp.Scope != scope || cp.Environment != environment) {
		return execution.Observation{}, errors.New("T3 identity changed. Link the existing session; this launch will not be replayed.")
	}
	if cp.Scope == "" {
		if err = a.validate(ctx, in); err != nil {
			return execution.Observation{}, err
		}
		cp = launchCheckpoint{Scope: scope, Environment: environment, Expected: expected}
	}
	persist := func() error {
		b, e := json.Marshal(cp)
		if e != nil {
			return e
		}
		return save(ctx, execution.Checkpoint{Version: 1, Data: b})
	}
	if err = persist(); err != nil {
		return execution.Observation{}, err
	}
	// Pending uploads are adapter checkpoints. An upload without a recorded ID
	// expires harmlessly; no execution is dispatched until all IDs are durable.
	for len(cp.Uploads) < len(in.Images) {
		img := in.Images[len(cp.Uploads)]
		att, e := a.Client.UploadAttachment(ctx, UploadInput{Name: img.Name, MimeType: img.MimeType, Data: img.Data})
		if e != nil {
			return execution.Observation{}, e
		}
		cp.Uploads = append(cp.Uploads, att)
		if e = persist(); e != nil {
			return execution.Observation{}, e
		}
	}
	var model *ModelSelection
	if in.Config.Model != "" {
		provider, name, e := DecodeModelKey(in.Config.Model)
		if e != nil {
			return execution.Observation{}, &execution.Rejected{Reason: e.Error()}
		}
		model = &ModelSelection{InstanceID: provider, Model: name, Options: in.Config.Options}
	}
	remote := in.Workspace.FromRemote
	launch := LaunchInput{ProjectID: in.Project, Title: in.Title, Message: in.Prompt, ClientRequestID: in.RequestID, ModelSelection: model, RuntimeMode: RuntimeMode(in.Config.Permission), InteractionMode: InteractionMode(in.Config.Mode), Attachments: cp.Uploads, Workspace: &WorkspaceStrategy{Type: WorkspaceType(in.Workspace.Kind), BaseRef: in.Workspace.Base, Branch: in.Workspace.Branch, StartFromOrigin: &remote}}
	cp.Sent = true
	if err = persist(); err != nil {
		return execution.Observation{}, err
	}
	var out LaunchResult
	if cp.Scope == "fake" {
		out, err = a.Client.LaunchThread(ctx, launch)
	} else {
		out, err = scoped.LaunchPinned(ctx, launch, cp.Scope, cp.Environment)
	}
	if err != nil {
		var te *ToolError
		if errors.As(err, &te) && te.Code == "invalid_request" && strings.Contains(te.Message, "No thread was created") {
			for _, att := range cp.Uploads {
				_ = a.Client.DiscardAttachment(ctx, att.ID)
			}
			return execution.Observation{}, &execution.Rejected{Reason: te.Message}
		}
		return execution.Observation{}, err
	}
	if out.ThreadID == "" || out.ProjectID != in.Project || cp.Expected != "" && out.ThreadID != cp.Expected {
		cp.Hold = "T3 returned a conflicting or malformed launch identity. Link the existing session."
		if e := persist(); e != nil {
			return execution.Observation{}, e
		}
		return execution.Observation{}, errors.New(cp.Hold)
	}
	cp.Expected = out.ThreadID
	if err = persist(); err != nil {
		return execution.Observation{}, err
	}
	o, err := a.Observe(ctx, execution.SessionRef{ConnectionID: in.ConnectionID, ExternalSessionID: out.ThreadID})
	if err == nil {
		return o, nil
	}
	now := time.Now().UTC()
	return execution.Observation{Session: execution.SessionRef{ConnectionID: in.ConnectionID, ExternalSessionID: out.ThreadID}, Project: in.Project, Title: in.Title, State: "running", Active: true, CreatedAt: now, UpdatedAt: now, ExecutionID: out.RunID, Links: a.Destinations(ctx, execution.SessionRef{ConnectionID: in.ConnectionID, ExternalSessionID: out.ThreadID})}, nil
}

var _ execution.Adapter = (*Adapter)(nil)

func (a *Adapter) ResolveLink(ctx context.Context, raw string) (execution.SessionRef, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return execution.SessionRef{}, errors.New("Paste a T3 session link.")
	}
	env := a.Client.Status(ctx).EnvironmentID
	id := ""
	if u.Scheme == "t3code" && u.Host == "threads" {
		parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
		if len(parts) == 2 && parts[0] == url.PathEscape(env) {
			id = parts[1]
		}
	} else {
		base := a.Destinations(ctx, execution.SessionRef{ConnectionID: a.ConnectionID, ExternalSessionID: "stage-link-placeholder"}).Web
		b, e := url.Parse(base)
		if e == nil && base != "" && strings.EqualFold(u.Scheme, b.Scheme) && strings.EqualFold(u.Host, b.Host) {
			prefix := strings.TrimSuffix(b.EscapedPath(), "stage-link-placeholder")
			if strings.HasPrefix(u.EscapedPath(), prefix) {
				id = strings.TrimPrefix(u.EscapedPath(), prefix)
			}
		}
	}
	if id == "" || strings.Contains(id, "/") {
		return execution.SessionRef{}, errors.New("Use a session link from the connected T3 environment.")
	}
	decoded, err := url.PathUnescape(id)
	if err != nil {
		return execution.SessionRef{}, errors.New("Invalid session link.")
	}
	return execution.SessionRef{ConnectionID: a.ConnectionID, ExternalSessionID: decoded}, nil
}
