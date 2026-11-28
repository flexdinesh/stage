package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

type wireTime struct{ Created, Updated, Busy, Idle, Archived, Completed int64 }
type location struct {
	Directory string `json:"directory"`
}
type session struct {
	ID, ProjectID, ParentID, Title, Agent, Outcome string
	Model                                          *wireModel
	Cost                                           *float64
	Tokens                                         *sessionTokens
	Location                                       location
	Time                                           wireTime
	Metadata                                       map[string]interface{}
}

type sessionTokens struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
	Cache     struct {
		Read  int64 `json:"read"`
		Write int64 `json:"write"`
	} `json:"cache"`
}

func (s session) usage() *execution.Usage {
	if s.Cost == nil && s.Tokens == nil {
		return nil
	}
	u := &execution.Usage{CostUSD: s.Cost}
	if v := s.Tokens; v != nil {
		u.InputTokens, u.OutputTokens = v.Input, v.Output
		u.CacheReadTokens, u.CacheWriteTokens = v.Cache.Read, v.Cache.Write
	}
	return u
}

func (a *Adapter) readSession(ctx context.Context, ref execution.SessionRef) (session, error) {
	var s envelope[session]
	if !a.valid(ref) {
		return s.Data, errors.New("session belongs to another OpenCode connection or has an invalid ID")
	}
	e := a.get(ctx, "/api/session/"+url.PathEscape(ref.ExternalSessionID), &s)
	if e != nil {
		return s.Data, sessionReadErr(e)
	}
	v := s.Data
	if v.ID != ref.ExternalSessionID || v.ProjectID == "" || v.Location.Directory == "" || v.Time.Created <= 0 || v.Time.Updated <= 0 {
		return v, errors.New("incomplete or mismatched OpenCode session snapshot")
	}
	return v, nil
}
func (a *Adapter) active(ctx context.Context) (map[string]struct{ Type string }, error) {
	var list envelope[map[string]struct{ Type string }]
	if e := a.get(ctx, "/api/session/active", &list); e != nil {
		return nil, e
	}
	if list.Data == nil {
		return nil, errors.New("missing OpenCode active session list")
	}
	for id, v := range list.Data {
		if !sessionID.MatchString(id) || v.Type != "running" {
			return nil, errors.New("invalid OpenCode active session")
		}
	}
	return list.Data, nil
}
func (a *Adapter) Attention(ctx context.Context) ([]execution.SessionRef, error) {
	active, e := a.active(ctx)
	if e != nil {
		return nil, e
	}
	// Forms can remain pending on an idle session. Query only loaded
	// locations, avoiding a session-history scan or booting every project.
	var locations []location
	if e := a.get(ctx, "/api/debug/location", &locations); e != nil {
		return nil, e
	}
	for _, loc := range locations {
		if loc.Directory == "" {
			return nil, errors.New("OpenCode returned an empty loaded location")
		}
		for _, path := range []string{"/api/form", "/api/permission/request"} {
			var pending envelope[[]struct{ SessionID string }]
			if e := a.get(ctx, path+queryDirectory(loc.Directory), &pending); e != nil {
				return nil, e
			}
			if pending.Location.Directory != loc.Directory || pending.Data == nil {
				return nil, errors.New("invalid OpenCode attention inventory")
			}
			for _, item := range pending.Data {
				if sessionID.MatchString(item.SessionID) {
					active[item.SessionID] = struct{ Type string }{}
				}
			}
		}
	}
	refs := make([]execution.SessionRef, 0, len(active))
	for id := range active {
		refs = append(refs, execution.SessionRef{ConnectionID: a.connection, ExternalSessionID: id})
	}
	return refs, nil
}

type message struct {
	ID, Type, Text, Finish string
	Time                   wireTime
	Content                []messagePart
	Metadata               map[string]interface{}
}

type messagePart struct {
	Type, Text, Name string
	Time             wireTime
	State            struct {
		Status string
		Input  json.RawMessage // streaming stores incomplete JSON as a string
	}
}

func (a *Adapter) reply(ctx context.Context, s session, kind, order string, limit int) (*execution.Reply, error) {
	var page envelope[[]message]
	if e := a.get(ctx, "/api/session/"+s.ID+"/message?limit=1&type="+kind+"&order="+order, &page); e != nil {
		return nil, e
	}
	if page.Data == nil {
		return nil, errors.New("invalid OpenCode message page")
	}
	if len(page.Data) == 0 {
		return nil, nil
	}
	m := page.Data[0]
	if m.Type != kind || m.ID == "" || m.Time.Created <= 0 {
		return nil, errors.New("invalid OpenCode message identity")
	}
	text := m.Text
	at := m.Time.Created
	run := ""
	if kind == "assistant" {
		if m.Time.Completed == 0 || m.Time.Created < s.Time.Busy || m.Time.Completed > s.Time.Idle {
			return nil, nil
		}
		parts := []string{}
		for _, p := range m.Content {
			if p.Type == "text" {
				parts = append(parts, p.Text)
			}
		}
		text = strings.Join(parts, "\n")
		at = m.Time.Completed
		run = runID(s)
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	chars := []rune(text)
	truncated := len(chars) > limit
	if truncated {
		text = string(chars[:limit])
	}
	return &execution.Reply{ItemID: m.ID, RunID: run, Text: text, Truncated: truncated, UpdatedAt: *millis(at)}, nil
}
func runID(s session) string {
	if s.Time.Busy <= 0 {
		return ""
	}
	return s.ID + "/" + strconv.FormatInt(s.Time.Busy, 10)
}
func (a *Adapter) Opening(ctx context.Context, ref execution.SessionRef, full bool) (*execution.Reply, error) {
	s, e := a.readSession(ctx, ref)
	if e != nil {
		return nil, e
	}
	limit := 2000
	if full {
		limit = 1_000_000
	}
	reply, e := a.reply(ctx, s, "user", "asc", limit)
	if e == nil && reply == nil {
		return nil, execution.ErrNoReply
	}
	return reply, e
}
func (a *Adapter) Stop(ctx context.Context, ref execution.SessionRef) error {
	if !a.valid(ref) {
		return execution.ErrNotFound
	}
	if _, err := a.check(ctx); err != nil {
		return err
	}
	var v struct{ Interrupted *bool }
	if e := a.request(ctx, "POST", "/api/session/"+ref.ExternalSessionID+"/interrupt", nil, &v); e != nil {
		return e
	}
	if v.Interrupted == nil {
		return errors.New("OpenCode returned an incomplete interrupt response")
	}
	return nil
}
func (a *Adapter) pending(ctx context.Context, id, kind string) (bool, error) {
	var requests envelope[[]struct{ ID, SessionID string }]
	if e := a.get(ctx, "/api/session/"+id+"/"+kind, &requests); e != nil {
		return false, e
	}
	if requests.Data == nil {
		return false, errors.New("incomplete OpenCode input-request list")
	}
	for _, v := range requests.Data {
		if v.ID == "" || v.SessionID != id {
			return false, errors.New("OpenCode input request belongs to another session")
		}
	}
	return len(requests.Data) > 0, nil
}
func (a *Adapter) Observe(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	var o execution.Observation
	s, e := a.readSession(ctx, ref)
	if e != nil {
		return o, e
	}
	active, e := a.active(ctx)
	if e != nil {
		return o, e
	}
	permission, e := a.pending(ctx, s.ID, "permission")
	if e != nil {
		return o, e
	}
	form, e := a.pending(ctx, s.ID, "form")
	if e != nil {
		return o, e
	}
	o = execution.Observation{
		Session: ref, Title: s.Title, Project: s.ProjectID, State: "idle",
		Directory: s.Location.Directory, Links: execution.Links{Web: a.sessionWebURL(s.ID)},
		Archived: s.Time.Archived > 0, Child: s.ParentID != "", Config: execution.Config{Mode: s.Agent},
		CreatedAt: *millis(s.Time.Created), UpdatedAt: *millis(s.Time.Updated),
		StartedAt: millis(s.Time.Busy), ExecutionID: runID(s), Usage: s.usage(),
	}
	if o.Child {
		o.Parent = &execution.SessionRef{ConnectionID: a.connection, ExternalSessionID: s.ParentID}
	}
	if s.Time.Archived > 0 {
		o.Closed = &execution.Closure{Signal: execution.ClosedArchived}
	}
	o.Actions = sessionActionStates(s.Time.Archived > 0)
	if s.Model != nil {
		o.Config.Model = s.Model.ProviderID + "/" + s.Model.ID
		if s.Model.Variant != "" {
			o.Config.Options = []execution.ModelOption{{ID: "variant", Value: execution.StringValue(s.Model.Variant)}}
		}
	}
	_, running := active[s.ID]
	switch {
	case permission || form:
		o.State = "needs_input"
		o.Active = true
	case running:
		o.State = "running"
		o.Active = true
	case s.Time.Busy > s.Time.Idle:
		return o, errors.New("OpenCode has no active owner or terminal evidence for the latest execution. Refresh after reconnecting.")
	case s.Time.Busy > 0 && s.Time.Idle >= s.Time.Busy:
		switch s.Outcome {
		case "succeeded":
			o.State = "done"
		case "failed":
			o.State = "failed"
		case "interrupted":
			o.State = "interrupted"
		default:
			return o, fmt.Errorf("unverified OpenCode terminal outcome %q", s.Outcome)
		}
		o.EndedAt = millis(s.Time.Idle)
		o.Reply, e = a.reply(ctx, s, "assistant", "desc", 4000)
		if e != nil {
			return o, e
		}
	case s.Outcome != "" && s.Time.Busy == 0:
		return o, errors.New("OpenCode outcome lacks execution timing")
	}
	// Subrequests can straddle executions. Only publish a coherent snapshot.
	latest, e := a.readSession(ctx, ref)
	if e != nil {
		return o, e
	}
	if !reflect.DeepEqual(s, latest) {
		return o, errors.New("OpenCode session changed during observation; refresh again")
	}
	return o, o.Valid()
}
