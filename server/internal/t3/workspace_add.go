package t3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Workspace sources (ADR 0012), each a T3 MCP call with Stage's mcp-client
// token. t3_project_create has no request id: every source lists projects
// first and reconciles a lost reply by listing again.
const (
	SourceFolder = "folder"
	SourceClone  = "clone"
	SourceNew    = "new"
)

func workspaceSources() []execution.WorkspaceSource {
	yes := execution.Availability{Available: true}
	title := execution.Field{ID: "title", Label: "Title", Kind: execution.FieldText, Help: "Defaults to the folder name."}
	return []execution.WorkspaceSource{
		{
			ID: SourceFolder, Label: "Existing folder",
			Help: "Register a folder on the T3 host as a T3 project.",
			Input: []execution.Field{
				{ID: "path", Label: "Folder", Kind: execution.FieldPath, Required: true, Help: "Absolute path on the T3 host"},
				title,
				{ID: "createIfMissing", Label: "Create folder if missing", Kind: execution.FieldBool},
			},
			Availability: yes,
		},
		{
			ID: SourceClone, Label: "Clone repository",
			Help: "T3 clones the repository, then registers the checkout as a T3 project.",
			Input: []execution.Field{
				{ID: "remoteUrl", Label: "Repository URL", Kind: execution.FieldText, Required: true, Help: "Git clone URL"},
				{ID: "destination", Label: "Destination", Kind: execution.FieldPath, Required: true, Help: "Absolute path on the T3 host. The folder must be new or empty."},
				title,
			},
			Availability: yes,
		},
		{
			ID: SourceNew, Label: "New project",
			Help: "T3 creates the folder in its projects directory and initializes a Git repository.",
			Input: []execution.Field{
				{ID: "title", Label: "Title", Kind: execution.FieldText, Required: true},
			},
			Availability: yes,
		},
	}
}

type workspaceInput struct {
	Path            string `json:"path"`
	Title           string `json:"title"`
	CreateIfMissing bool   `json:"createIfMissing"`
	RemoteURL       string `json:"remoteUrl"`
	Destination     string `json:"destination"`
}

// AddWorkspace runs one declared source. Stage has validated the input
// against the source's fields.
func (a *Adapter) AddWorkspace(ctx context.Context, r execution.WorkspaceAddRequest) (execution.WorkspaceAdded, error) {
	var in workspaceInput
	if err := json.Unmarshal(r.Input, &in); err != nil {
		return execution.WorkspaceAdded{}, &execution.Invalid{Reason: "Workspace source input must be a JSON object."}
	}
	switch r.Source {
	case SourceFolder:
		return a.addFolder(ctx, in)
	case SourceClone:
		return a.addClone(ctx, in)
	case SourceNew:
		return a.addNew(ctx, strings.TrimSpace(in.Title))
	}
	return execution.WorkspaceAdded{}, &execution.Invalid{Reason: fmt.Sprintf("T3 has no workspace source %q.", r.Source)}
}

// absolutePath is p cleaned as T3 resolves it; "" when p is not absolute.
func absolutePath(p string) string {
	p = strings.TrimSpace(p)
	if !path.IsAbs(p) {
		return ""
	}
	return path.Clean(p)
}

func titleOr(title, root string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	return path.Base(root)
}

// registered finds the project T3 holds at root (deleted ones ignored).
// An archived one is refused: Stage does not list archived T3 projects.
func registered(projects []Project, root string) (Project, bool, error) {
	for _, p := range projects {
		if p.WorkspaceRoot != root || p.DeletedAt != nil {
			continue
		}
		if p.ArchivedAt != nil {
			return p, true, &execution.Invalid{Reason: fmt.Sprintf("T3 has %s in the archived project %q. Restore it in T3, then add it again.", root, p.Title)}
		}
		return p, true, nil
	}
	return Project{}, false, nil
}

// existing answers a target T3 already registered; ok is false when
// nothing matched and err is nil.
func existing(p Project, ok bool, err error) (execution.WorkspaceAdded, bool, error) {
	if err != nil || !ok {
		return execution.WorkspaceAdded{}, err != nil, err
	}
	return execution.WorkspaceAdded{Workspace: p, Existing: true, Message: fmt.Sprintf("T3 already has %s as project %q.", p.WorkspaceRoot, p.Title)}, true, nil
}

// relist reads the projects again after an unconfirmed call; root names
// the workspace to find.
func (a *Adapter) relist(ctx context.Context, root string, cause error) (Project, bool, error) {
	projects, err := a.Client.ListProjects(ctx)
	if err != nil {
		return Project{}, false, &execution.OutcomeUncertain{Cause: errors.Join(cause, err)}
	}
	return registered(projects, root)
}

func isDuplicate(te *ToolError) bool { return te.Message == msgAlreadyProject }

// invalid is T3's refusal as a 400, with hint appended when non-empty.
func invalid(te *ToolError, hint string) error {
	reason := strings.TrimSpace(te.Message)
	if reason == "" {
		reason = "T3 refused the request."
	}
	if hint != "" {
		reason += " " + hint
	}
	return &execution.Invalid{Reason: reason}
}

const pathHint = "Enter an absolute path on the T3 host."

func (a *Adapter) addFolder(ctx context.Context, in workspaceInput) (execution.WorkspaceAdded, error) {
	root := absolutePath(in.Path)
	if root == "" {
		return execution.WorkspaceAdded{}, &execution.Invalid{Reason: pathHint}
	}
	projects, err := a.Client.ListProjects(ctx)
	if err != nil {
		return execution.WorkspaceAdded{}, err
	}
	if added, done, err := existing(registered(projects, root)); done {
		return added, err
	}
	created, err := a.Client.CreateProject(ctx, CreateProjectInput{Title: titleOr(in.Title, root), WorkspaceRoot: root, CreateIfMissing: in.CreateIfMissing})
	if err == nil {
		return execution.WorkspaceAdded{Workspace: created.Project, Message: fmt.Sprintf("Registered %s as T3 project %q.", created.WorkspaceRoot, created.Title)}, nil
	}
	var te *ToolError
	switch {
	case errors.As(err, &te) && isDuplicate(te):
		// Registered since the list: return it.
		if added, done, lerr := existing(a.relist(ctx, root, err)); done {
			return added, lerr
		}
		return execution.WorkspaceAdded{}, invalid(te, "")
	case errors.As(err, &te):
		hint := ""
		if te.Message == msgOperationFailed {
			hint = "Check that the folder exists on the T3 host, or turn on Create folder if missing."
		}
		return execution.WorkspaceAdded{}, invalid(te, hint)
	case IsTransportError(err):
		p, ok, lerr := a.relist(ctx, root, err)
		if lerr != nil {
			return execution.WorkspaceAdded{}, lerr
		}
		if ok { // absent before the call: this request registered it
			return execution.WorkspaceAdded{Workspace: p, Message: fmt.Sprintf("Registered %s as T3 project %q.", p.WorkspaceRoot, p.Title)}, nil
		}
		return execution.WorkspaceAdded{}, &execution.OutcomeUncertain{Cause: err}
	}
	return execution.WorkspaceAdded{}, err
}

func (a *Adapter) addClone(ctx context.Context, in workspaceInput) (execution.WorkspaceAdded, error) {
	dest := absolutePath(in.Destination)
	if dest == "" {
		return execution.WorkspaceAdded{}, &execution.Invalid{Reason: pathHint}
	}
	projects, err := a.Client.ListProjects(ctx)
	if err != nil {
		return execution.WorkspaceAdded{}, err
	}
	if added, done, err := existing(registered(projects, dest)); done {
		return added, err
	}
	cloned, err := a.Client.CloneProject(ctx, CloneInput{RemoteURL: strings.TrimSpace(in.RemoteURL), DestinationPath: dest})
	var te *ToolError
	switch {
	case err == nil:
	case errors.As(err, &te):
		hint := ""
		if te.Message == msgDestinationInUse {
			hint = "If an earlier request cloned it, add it with Existing folder."
		}
		return execution.WorkspaceAdded{}, invalid(te, hint)
	case IsTransportError(err):
		return execution.WorkspaceAdded{}, &execution.OutcomeUncertain{Cause: err}
	default:
		return execution.WorkspaceAdded{}, err
	}
	root := cloned.Cwd
	in.Title = titleOr(in.Title, root)
	message := fmt.Sprintf("Cloned the repository into %s and registered it as T3 project %q.", root, in.Title)
	// The clone succeeded: register it, retrying once after a lost reply.
	for attempt := 0; ; attempt++ {
		created, err := a.Client.CreateProject(ctx, CreateProjectInput{Title: in.Title, WorkspaceRoot: root})
		if err == nil {
			return execution.WorkspaceAdded{Workspace: created.Project, Message: message}, nil
		}
		if errors.As(err, &te) && !isDuplicate(te) {
			return execution.WorkspaceAdded{}, &execution.Invalid{Reason: fmt.Sprintf(
				"T3 cloned the repository into %s but could not register it: %s Add it with Existing folder.", root, strings.TrimSpace(te.Message))}
		}
		if !errors.As(err, &te) && !IsTransportError(err) {
			return execution.WorkspaceAdded{}, err
		}
		p, ok, lerr := a.relist(ctx, root, err)
		if lerr != nil {
			return execution.WorkspaceAdded{}, lerr
		}
		if ok {
			return execution.WorkspaceAdded{Workspace: p, Message: message}, nil
		}
		if attempt > 0 {
			return execution.WorkspaceAdded{}, &execution.OutcomeUncertain{Cause: err}
		}
	}
}

func (a *Adapter) addNew(ctx context.Context, title string) (execution.WorkspaceAdded, error) {
	if title == "" {
		return execution.WorkspaceAdded{}, &execution.Invalid{Reason: "Enter a title for the new T3 project."}
	}
	before, err := a.Client.ListProjects(ctx)
	if err != nil {
		return execution.WorkspaceAdded{}, err
	}
	created, err := a.Client.CreateProject(ctx, CreateProjectInput{Title: title})
	if err == nil {
		added := execution.WorkspaceAdded{Workspace: created.Project, Message: fmt.Sprintf("Created T3 project %q at %s.", created.Title, created.WorkspaceRoot)}
		if created.CommitError != "" {
			added.Facts = []execution.Fact{{ID: "commit", Label: "First commit", Value: created.CommitError, Tone: execution.ToneWarn}}
		}
		return added, nil
	}
	var te *ToolError
	if errors.As(err, &te) {
		return execution.WorkspaceAdded{}, invalid(te, "")
	}
	if !IsTransportError(err) {
		return execution.WorkspaceAdded{}, err
	}
	// No target to match: the one new project with this title is ours.
	after, lerr := a.Client.ListProjects(ctx)
	if lerr != nil {
		return execution.WorkspaceAdded{}, &execution.OutcomeUncertain{Cause: errors.Join(err, lerr)}
	}
	known := make(map[string]bool, len(before))
	for _, p := range before {
		known[p.ID] = true
	}
	var found []Project
	for _, p := range after {
		if !known[p.ID] && p.Title == title && p.DeletedAt == nil && p.ArchivedAt == nil {
			found = append(found, p)
		}
	}
	if len(found) != 1 {
		return execution.WorkspaceAdded{}, &execution.OutcomeUncertain{Cause: err}
	}
	p := found[0]
	return execution.WorkspaceAdded{Workspace: p, Message: fmt.Sprintf("Created T3 project %q at %s.", p.Title, p.WorkspaceRoot)}, nil
}
