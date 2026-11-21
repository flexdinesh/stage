package t3

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/flexdinesh/stage/server/internal/gitmeta"
)

// Wire shapes of T3 MCP tool results (t3code packages/contracts/src/
// orchestratorMcp.ts, project.ts, apps/server/src/mcp/toolkits/project/tools.ts).
// Decoding is tolerant: unknown fields are ignored, JSON null and missing
// fields decode to zero values, and unparseable timestamps decode to zero.

// ToolError is a T3 MCP tool call that returned isError. T3 encodes its
// failure as OrchestratorMcpFailure {_tag, code, message} JSON text.
type ToolError struct {
	Tool    string
	Code    string // e.g. thread_not_found, invalid_request; "" if not reported
	Message string
}

func (e *ToolError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("t3 %s: %s: %s", e.Tool, e.Code, e.Message)
	}
	return fmt.Sprintf("t3 %s: %s", e.Tool, e.Message)
}

// Is maps T3 not-found codes to ErrNotFound and pending-request failures to
// ErrRequestAnswered / ErrRequestStale.
func (e *ToolError) Is(target error) bool {
	if target == ErrRequestAnswered || target == ErrRequestStale {
		return requestError(e, target)
	}
	return target == ErrNotFound && (e.Code == "thread_not_found" || e.Code == "run_not_found")
}

// decodeToolResult decodes structuredContent (or JSON text content) into out.
func decodeToolResult(tool string, res *mcp.CallToolResult, out any) error {
	if res == nil {
		return &ToolError{Tool: tool, Message: "empty result"}
	}
	text := resultText(res)
	if res.IsError {
		return toolFailure(tool, text, res.StructuredContent)
	}
	if out == nil {
		return nil
	}
	var raw []byte
	if res.StructuredContent != nil {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			return fmt.Errorf("t3 %s: re-encode structured content: %w", tool, err)
		}
		raw = b
	} else {
		raw = []byte(strings.TrimSpace(text))
	}
	if len(raw) == 0 {
		return fmt.Errorf("t3 %s: result has no content", tool)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("t3 %s: decode result: %w", tool, err)
	}
	return nil
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func toolFailure(tool, text string, structured any) error {
	var f struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(text)), &f) == nil && (f.Code != "" || f.Message != "") {
		return &ToolError{Tool: tool, Code: f.Code, Message: f.Message}
	}
	if structured != nil {
		if b, err := json.Marshal(structured); err == nil && json.Unmarshal(b, &f) == nil {
			if f.Code != "" || f.Message != "" {
				return &ToolError{Tool: tool, Code: f.Code, Message: f.Message}
			}
			if f.Error != nil {
				return &ToolError{Tool: tool, Code: f.Error.Code, Message: f.Error.Message}
			}
		}
	}
	msg := strings.TrimSpace(text)
	if msg == "" {
		msg = "tool call failed"
	}
	return &ToolError{Tool: tool, Message: msg}
}

// isoTime decodes an ISO 8601 string, null, or garbage (-> zero) without error.
type isoTime struct{ time.Time }

func (t *isoTime) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) != nil || s == "" {
		t.Time = time.Time{}
		return nil
	}
	if p, err := time.Parse(time.RFC3339Nano, s); err == nil {
		t.Time = p.UTC()
	}
	return nil
}

func (t isoTime) ptr() *time.Time {
	if t.IsZero() {
		return nil
	}
	v := t.Time
	return &v
}

// wireRepositoryIdentity is T3's resolved repository: the remote it chose
// (locator) and the checkout top (rootPath). Its "origin" field names a
// fork's parent, never the origin remote, so Stage ignores it.
type wireRepositoryIdentity struct {
	Locator struct {
		RemoteName string `json:"remoteName"`
		RemoteURL  string `json:"remoteUrl"`
	} `json:"locator"`
	RootPath string `json:"rootPath"`
}

// originRemote is the origin remote's raw URL, "" when T3 resolved another
// remote.
func (r wireRepositoryIdentity) originRemote() string {
	if r.Locator.RemoteName != "origin" {
		return ""
	}
	return r.Locator.RemoteURL
}

// repoPath is the slash path from rootPath to workspace ("." at the top),
// "" when unknown or outside rootPath.
func (r wireRepositoryIdentity) repoPath(workspace string) string {
	if r.RootPath == "" || workspace == "" {
		return ""
	}
	rel, err := filepath.Rel(r.RootPath, workspace)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

type wireProject struct {
	RepositoryIdentity wireRepositoryIdentity `json:"repositoryIdentity"`
	ID                 string                 `json:"id"`
	Title              string                 `json:"title"`
	WorkspaceRoot      string                 `json:"workspaceRoot"`
	ArchivedAt         isoTime                `json:"archivedAt"`
	DeletedAt          isoTime                `json:"deletedAt"`
	CreatedAt          isoTime                `json:"createdAt"`
	UpdatedAt          isoTime                `json:"updatedAt"`
	CommitError        string                 `json:"commitError"` // t3_project_create only
}

// toProject: a live T3 project is importable; users add T3 projects
// deliberately.
func (w wireProject) toProject() Project {
	remote := w.RepositoryIdentity.originRemote()
	repo, repoPath := gitmeta.RemoteIdentity(remote), ""
	if repo != "" {
		repoPath = w.RepositoryIdentity.repoPath(w.WorkspaceRoot)
	}
	return Project{
		OriginRemote:  remote,
		Repo:          repo,
		RepoPath:      repoPath,
		Importable:    w.ArchivedAt.ptr() == nil && w.DeletedAt.ptr() == nil,
		ID:            w.ID,
		Title:         w.Title,
		WorkspaceRoot: w.WorkspaceRoot,
		ArchivedAt:    w.ArchivedAt.ptr(),
		DeletedAt:     w.DeletedAt.ptr(),
		CreatedAt:     w.CreatedAt.Time,
		UpdatedAt:     w.UpdatedAt.Time,
	}
}

type wireProjectList struct {
	Projects   []wireProject `json:"projects"`
	NextCursor *int          `json:"nextCursor"`
}

type wireThread struct {
	ThreadID             string  `json:"threadId"`
	ProjectID            string  `json:"projectId"`
	Title                string  `json:"title"`
	Link                 string  `json:"link"`
	CreatedBy            string  `json:"createdBy"`
	CreationSource       string  `json:"creationSource"`
	Status               string  `json:"status"`
	LatestRunID          string  `json:"latestRunId"`
	ProviderInstanceID   string  `json:"providerInstanceId"`
	Model                string  `json:"model"`
	RuntimeMode          string  `json:"runtimeMode"`
	InteractionMode      string  `json:"interactionMode"`
	Settled              bool    `json:"settled"`
	SettledAt            isoTime `json:"settledAt"`
	Snoozed              bool    `json:"snoozed"`
	ParentThreadID       string  `json:"parentThreadId"`
	RelationshipToParent string  `json:"relationshipToParent"`
	ItemCount            int     `json:"itemCount"`
	LinkedPullRequest    *wirePR `json:"linkedPullRequest"`
	PullRequests         wirePRs `json:"pullRequests"`
	CreatedAt            isoTime `json:"createdAt"`
	UpdatedAt            isoTime `json:"updatedAt"`

	// t3_thread_read detail fields
	ActiveRunID         string `json:"activeRunId"`
	Branch              string `json:"branch"`
	WorktreePath        string `json:"worktreePath"`
	RunCount            int    `json:"runCount"`
	PendingRequestCount int    `json:"pendingRequestCount"`
	Archived            bool   `json:"archived"`
}

func (w wireThread) toThread() Thread {
	return Thread{
		ThreadID:             w.ThreadID,
		ProjectID:            w.ProjectID,
		Title:                w.Title,
		Link:                 w.Link,
		CreatedBy:            w.CreatedBy,
		CreationSource:       w.CreationSource,
		Status:               ThreadStatus(w.Status),
		LatestRunID:          w.LatestRunID,
		Archived:             w.Archived,
		ProviderInstanceID:   w.ProviderInstanceID,
		Model:                w.Model,
		RuntimeMode:          RuntimeMode(w.RuntimeMode),
		InteractionMode:      InteractionMode(w.InteractionMode),
		Settled:              w.Settled,
		SettledAt:            w.SettledAt.ptr(),
		Snoozed:              w.Snoozed,
		ParentThreadID:       w.ParentThreadID,
		RelationshipToParent: w.RelationshipToParent,
		ItemCount:            w.ItemCount,
		PullRequests:         pullRequests(w.LinkedPullRequest, w.PullRequests),
		CreatedAt:            w.CreatedAt.Time,
		UpdatedAt:            w.UpdatedAt.Time,
	}
}

// wirePR is a T3 thread pull request {host, repository, number, url,
// source, state}. Malformed entries decode to zero and are dropped.
type wirePR struct {
	Host       string `json:"host"`
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	Source     string `json:"source"`
	State      string `json:"state"`
}

func (w *wirePR) UnmarshalJSON(data []byte) error {
	type plain wirePR
	var p plain
	if json.Unmarshal(data, &p) != nil {
		*w = wirePR{}
		return nil
	}
	*w = wirePR(p)
	return nil
}

// wirePRs tolerates a non-array pullRequests (decodes to empty).
type wirePRs []wirePR

func (w *wirePRs) UnmarshalJSON(data []byte) error {
	var list []wirePR
	if json.Unmarshal(data, &list) != nil {
		list = nil
	}
	*w = list
	return nil
}

// pullRequests merges the linked PR (source "linked" when unreported) ahead
// of pullRequests, dropping entries without a URL and URL duplicates.
func pullRequests(linked *wirePR, prs wirePRs) []PullRequest {
	out := []PullRequest{}
	seen := map[string]bool{}
	add := func(w wirePR, source string) {
		if w.URL == "" || seen[w.URL] {
			return
		}
		seen[w.URL] = true
		if w.Source == "" {
			w.Source = source
		}
		out = append(out, PullRequest(w))
	}
	if linked != nil {
		add(*linked, "linked")
	}
	for _, w := range prs {
		add(w, "")
	}
	return out
}

type wireThreadList struct {
	Threads    []wireThread `json:"threads"`
	NextCursor *int         `json:"nextCursor"`
	Total      int          `json:"total"`
}

func (w wireThreadList) toPage() ThreadPage {
	page := ThreadPage{Threads: make([]Thread, 0, len(w.Threads)), NextCursor: w.NextCursor, Total: w.Total}
	for _, t := range w.Threads {
		page.Threads = append(page.Threads, t.toThread())
	}
	return page
}

type wireThreadSearch struct {
	Matches []struct {
		ThreadID         string  `json:"threadId"`
		ProjectID        string  `json:"projectId"`
		Title            string  `json:"title"`
		Source           string  `json:"source"`
		Snippet          string  `json:"snippet"`
		MessageCreatedAt isoTime `json:"messageCreatedAt"`
	} `json:"matches"`
}

func (w wireThreadSearch) toMatches() []SearchMatch {
	out := make([]SearchMatch, 0, len(w.Matches))
	for _, m := range w.Matches {
		if m.ThreadID == "" {
			continue
		}
		out = append(out, SearchMatch{
			ThreadID:         m.ThreadID,
			ProjectID:        m.ProjectID,
			Title:            m.Title,
			Source:           SearchSource(m.Source),
			Snippet:          m.Snippet,
			MessageCreatedAt: m.MessageCreatedAt.ptr(),
		})
	}
	return out
}

type wireRun struct {
	RunID              string  `json:"runId"`
	Ordinal            int     `json:"ordinal"`
	Status             string  `json:"status"`
	ProviderInstanceID string  `json:"providerInstanceId"`
	Model              string  `json:"model"`
	ExecutedModel      string  `json:"executedModel"`
	RequestedAt        isoTime `json:"requestedAt"`
	StartedAt          isoTime `json:"startedAt"`
	CompletedAt        isoTime `json:"completedAt"`
}

type wireThreadRead struct {
	Thread     wireThread `json:"thread"`
	RecentRuns []wireRun  `json:"recentRuns"`
	// Timeline page (OrchestratorMcpThreadReadResult items/nextPosition/hasMore).
	Items        []wireTimelineItem `json:"items"`
	NextPosition *int               `json:"nextPosition"`
	HasMore      bool               `json:"hasMore"`
}

// wireTimelineItem is OrchestratorMcpThreadTimelineItem. Position is the
// global index into the visible timeline (the messages view leaves gaps).
// NextTextOffset is nil when null or absent (older T3).
type wireTimelineItem struct {
	Position       int     `json:"position"`
	Visibility     string  `json:"visibility"` // local|inherited|synthetic
	SourceThreadID string  `json:"sourceThreadId"`
	ItemID         string  `json:"itemId"`
	RunID          string  `json:"runId"`
	MessageID      string  `json:"messageId"`
	CreatedBy      string  `json:"createdBy"`
	Type           string  `json:"type"`
	Status         string  `json:"status"`
	Title          string  `json:"title"`
	Text           string  `json:"text"`
	TextTruncated  bool    `json:"textTruncated"`
	NextTextOffset *int    `json:"nextTextOffset"`
	UpdatedAt      isoTime `json:"updatedAt"`
}

func (w wireThreadRead) toDetail() ThreadDetail {
	d := ThreadDetail{
		Thread:              w.Thread.toThread(),
		ActiveRunID:         w.Thread.ActiveRunID,
		Branch:              w.Thread.Branch,
		WorktreePath:        w.Thread.WorktreePath,
		RunCount:            w.Thread.RunCount,
		PendingRequestCount: w.Thread.PendingRequestCount,
		RecentRuns:          make([]Run, 0, len(w.RecentRuns)),
	}
	for _, r := range w.RecentRuns {
		d.RecentRuns = append(d.RecentRuns, Run{
			RunID:              r.RunID,
			Ordinal:            r.Ordinal,
			Status:             ThreadStatus(r.Status),
			ProviderInstanceID: r.ProviderInstanceID,
			Model:              r.Model,
			ExecutedModel:      r.ExecutedModel,
			RequestedAt:        r.RequestedAt.Time,
			StartedAt:          r.StartedAt.ptr(),
			CompletedAt:        r.CompletedAt.ptr(),
		})
	}
	return d
}

func (w wireThreadRead) toItemPage() ItemPage {
	page := ItemPage{Thread: w.toDetail(), Items: make([]TimelineItem, 0, len(w.Items)), NextPosition: w.NextPosition, HasMore: w.HasMore}
	for _, it := range w.Items {
		page.Items = append(page.Items, TimelineItem{
			Position:       it.Position,
			Visibility:     it.Visibility,
			SourceThreadID: it.SourceThreadID,
			ItemID:         it.ItemID,
			RunID:          it.RunID,
			MessageID:      it.MessageID,
			CreatedBy:      it.CreatedBy,
			Type:           it.Type,
			Status:         it.Status,
			Title:          it.Title,
			Text:           it.Text,
			TextTruncated:  it.TextTruncated,
			NextTextOffset: it.NextTextOffset,
			UpdatedAt:      it.UpdatedAt.Time,
		})
	}
	return page
}

// wireModelSelection accepts options as [{id,value}] or a legacy record
// {"effort":"low"}, and the legacy "provider" key for instanceId.
type wireModelSelection struct {
	InstanceID string
	Model      string
	Options    []ModelOption
}

func (w *wireModelSelection) UnmarshalJSON(data []byte) error {
	var raw struct {
		InstanceID string          `json:"instanceId"`
		Provider   string          `json:"provider"`
		Model      string          `json:"model"`
		Options    json.RawMessage `json:"options"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil // tolerate: leave zero
	}
	w.InstanceID = firstNonEmpty(raw.InstanceID, raw.Provider)
	w.Model = raw.Model
	w.Options = nil
	var list []struct {
		ID    string          `json:"id"`
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(raw.Options, &list) == nil {
		for _, o := range list {
			var v OptionValue
			if v.UnmarshalJSON(o.Value) == nil {
				w.Options = append(w.Options, ModelOption{ID: o.ID, Value: v})
			}
		}
		return nil
	}
	var record map[string]json.RawMessage
	if json.Unmarshal(raw.Options, &record) == nil {
		for id, rv := range record {
			var v OptionValue
			if v.UnmarshalJSON(rv) == nil {
				w.Options = append(w.Options, ModelOption{ID: id, Value: v})
			}
		}
	}
	return nil
}

type wireLaunch struct {
	ThreadID       string             `json:"threadId"`
	Link           string             `json:"link"`
	ProjectID      string             `json:"projectId"`
	ModelSelection wireModelSelection `json:"modelSelection"`
	RunID          string             `json:"runId"`
	Status         string             `json:"status"`
}

func (w wireLaunch) toResult() LaunchResult {
	return LaunchResult{
		ThreadID:  w.ThreadID,
		Link:      w.Link,
		ProjectID: w.ProjectID,
		ModelSelection: ModelSelection{
			InstanceID: w.ModelSelection.InstanceID,
			Model:      w.ModelSelection.Model,
			Options:    w.ModelSelection.Options,
		},
		RunID:  w.RunID,
		Status: ThreadStatus(w.Status),
	}
}

// tolerantOption decodes an option value, leaving ok=false on bad input.
type tolerantOption struct {
	v  OptionValue
	ok bool
}

func (t *tolerantOption) UnmarshalJSON(data []byte) error {
	t.ok = t.v.UnmarshalJSON(data) == nil
	return nil
}

type wireCapabilities struct {
	Providers []struct {
		ProviderInstanceID string    `json:"providerInstanceId"`
		DriverKind         string    `json:"driverKind"`
		DisplayName        string    `json:"displayName"`
		RuntimeModes       *[]string `json:"runtimeModes"`
		CanRunChildTask    bool      `json:"canRunChildTask"`
		Constraints        []string  `json:"constraints"`
		Models             []struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			Options []struct {
				ID      string           `json:"id"`
				Values  []tolerantOption `json:"values"`
				Default *tolerantOption  `json:"default"`
			} `json:"options"`
		} `json:"models"`
	} `json:"providers"`
}

func (w wireCapabilities) toCatalog() Catalog {
	cat := Catalog{Providers: make([]Provider, 0, len(w.Providers))}
	for _, p := range w.Providers {
		prov := Provider{
			ProviderInstanceID: p.ProviderInstanceID,
			DriverKind:         p.DriverKind,
			DisplayName:        p.DisplayName,
			CanRunChildTask:    p.CanRunChildTask,
			Constraints:        p.Constraints,
			Models:             make([]Model, 0, len(p.Models)),
		}
		if p.RuntimeModes != nil {
			prov.RuntimeModes = make([]RuntimeMode, 0, len(*p.RuntimeModes))
			for _, m := range *p.RuntimeModes {
				prov.RuntimeModes = append(prov.RuntimeModes, RuntimeMode(m))
			}
		}
		for _, m := range p.Models {
			model := Model{ID: m.ID, Label: m.Label}
			for _, o := range m.Options {
				spec := ModelOptionSpec{ID: o.ID}
				for _, v := range o.Values {
					if v.ok {
						spec.Values = append(spec.Values, v.v)
					}
				}
				if o.Default != nil && o.Default.ok {
					d := o.Default.v
					spec.Default = &d
				}
				model.Options = append(model.Options, spec)
			}
			prov.Models = append(prov.Models, model)
		}
		cat.Providers = append(cat.Providers, prov)
	}
	return cat
}

// environmentFromLink extracts the environment id from a T3 thread link:
// t3-thread://v2/<env>/<thread> or <hosted>/threads/<env>/<thread>.
func environmentFromLink(link string) string {
	link = strings.Trim(strings.TrimSpace(link), "<>")
	if rest, ok := strings.CutPrefix(link, "t3-thread://v2/"); ok {
		env, _, found := strings.Cut(rest, "/")
		if found {
			return env
		}
		return ""
	}
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) == 3 && parts[0] == "threads" {
		env, err := url.PathUnescape(parts[1])
		if err == nil {
			return env
		}
	}
	return ""
}

// WebThreadURL is the T3 web UI link for a thread:
// <publicURL>/<environmentId>/<threadId>. "" if any part is missing.
func WebThreadURL(publicURL, environmentID, threadID string) string {
	if publicURL == "" || environmentID == "" || threadID == "" {
		return ""
	}
	return strings.TrimRight(publicURL, "/") + "/" + url.PathEscape(environmentID) + "/" + url.PathEscape(threadID)
}

// AppThreadURL is the T3 desktop/mobile app deep link for a thread:
// t3code://threads/<environmentId>/<threadId>. "" if any part is missing.
func AppThreadURL(environmentID, threadID string) string {
	if environmentID == "" || threadID == "" {
		return ""
	}
	return "t3code://threads/" + url.PathEscape(environmentID) + "/" + url.PathEscape(threadID)
}
