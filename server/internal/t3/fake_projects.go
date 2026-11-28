package t3

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
)

// The fake's model of t3_project_create and t3_project_clone (t3code
// apps/server/src/mcp/toolkits/project/handlers.ts, ProjectService,
// WorkspacePaths, ManagedProjectFolders, SourceControlRepositoryService).
// Every folder exists and is empty unless a test says otherwise.

// FakeHome is the fake T3 host's home folder (~ expansion).
const FakeHome = "/home/fake"

// errFakeLostReply is a call that ran in T3 but whose reply never arrived.
var errFakeLostReply = errors.New("fake: connection closed before the reply")

// T3's messages for project create, clone, update and delete failures.
const (
	msgProjectNotFound  = "The project was not found."
	msgProjectNotEmpty  = "The project is not empty; force=true is required to delete it."
	msgOperationFailed  = "The operation could not be completed."
	msgAlreadyProject   = "The workspace is already registered to a project."
	msgDestinationInUse = "Destination path already exists and is not empty."
)

type fakeFolders struct {
	missing    map[string]bool // absent on the T3 host
	containers map[string]bool // hold worktrees but are not a checkout
	occupied   map[string]bool // exist and are not empty
}

func mark(m *map[string]bool, p string, on bool) {
	if *m == nil {
		*m = map[string]bool{}
	}
	if on {
		(*m)[p] = true
	} else {
		delete(*m, p)
	}
}

// MissFolder makes p absent on the fake T3 host.
func (f *Fake) MissFolder(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mark(&f.folders.missing, fakeNormalize(p), true)
}

// AddWorktreeContainer makes p a folder of worktrees that is not a checkout.
func (f *Fake) AddWorktreeContainer(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mark(&f.folders.containers, fakeNormalize(p), true)
}

// OccupyFolder makes p an existing, non-empty folder.
func (f *Fake) OccupyFolder(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p = fakeNormalize(p)
	mark(&f.folders.missing, p, false)
	mark(&f.folders.occupied, p, true)
}

// fakeNormalize is T3's path.resolve(expandHome(p.trim())) for absolute and
// ~ paths; other paths stay as given.
func fakeNormalize(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = FakeHome + p[1:]
	}
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return p
}

func (f *Fake) projectsDir() string {
	if f.ProjectsDir != "" {
		return f.ProjectsDir
	}
	return FakeHome + "/.t3/projects"
}

func createError(message string) error {
	return &ToolError{Tool: "t3_project_create", Code: "invalid_request", Message: message}
}

func (f *Fake) CreateProject(_ context.Context, in CreateProjectInput) (CreatedProject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Created = append(f.Created, in)
	if f.CreateErr != nil {
		return CreatedProject{}, f.CreateErr
	}
	p, err := f.createLocked(in)
	if err == nil && f.LoseCreate > 0 {
		f.LoseCreate--
		return CreatedProject{}, fmt.Errorf("t3 t3_project_create: %w", errFakeLostReply)
	}
	return p, err
}

func (f *Fake) createLocked(in CreateProjectInput) (CreatedProject, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return CreatedProject{}, createError("title must be a non-empty string.")
	}
	if strings.TrimSpace(in.WorkspaceRoot) == "" {
		return f.createNamedLocked(title)
	}
	root := fakeNormalize(in.WorkspaceRoot)
	if f.folders.missing[root] {
		if !in.CreateIfMissing {
			// T3 reports a missing folder as a failed operation.
			return CreatedProject{}, &ToolError{Tool: "t3_project_create", Code: "orchestration_error", Message: msgOperationFailed}
		}
		mark(&f.folders.missing, root, false)
	}
	if f.folders.containers[root] {
		return CreatedProject{}, createError(root + " holds a repository's worktrees but is not a checkout itself. Add one of its worktrees as the project instead.")
	}
	if f.registeredLocked(root) {
		return CreatedProject{}, createError(msgAlreadyProject)
	}
	return CreatedProject{Project: f.addLocked(title, root)}, nil
}

// createNamedLocked makes <ProjectsDir>/<slug>, suffixed -2, -3... when
// taken, as ManagedProjectFolders.createNamedProject does.
func (f *Fake) createNamedLocked(title string) (CreatedProject, error) {
	slug := fakeSlug(title)
	for n := 1; n <= 100; n++ {
		root := path.Join(f.projectsDir(), slug)
		if n > 1 {
			root = fmt.Sprintf("%s-%d", root, n)
		}
		if f.folders.occupied[root] || f.registeredLocked(root) {
			continue
		}
		mark(&f.folders.missing, root, false)
		mark(&f.folders.occupied, root, true)
		return CreatedProject{Project: f.addLocked(title, root), CommitError: f.CommitError}, nil
	}
	return CreatedProject{}, &ToolError{Tool: "t3_project_create", Code: "orchestration_error", Message: "No free project folder for " + title + "."}
}

func fakeSlug(title string) string {
	words := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	if len(words) > 5 {
		words = words[:5]
	}
	slug := strings.Join(words, "-")
	if len(slug) > 48 {
		slug = strings.TrimRight(slug[:48], "-")
	}
	if slug == "" {
		return "project"
	}
	return slug
}

// registeredLocked: a project that is not deleted (archived included) has
// root, as T3's findActiveByWorkspaceRoot.
func (f *Fake) registeredLocked(root string) bool {
	for _, p := range f.projects {
		if p.WorkspaceRoot == root && p.DeletedAt == nil {
			return true
		}
	}
	return false
}

func (f *Fake) addLocked(title, root string) Project {
	now := f.now()
	p := Project{ID: f.nextID("project"), Title: title, WorkspaceRoot: root, CreatedAt: now, UpdatedAt: now}
	f.projects = append(f.projects, p)
	return p
}

func (f *Fake) CloneProject(_ context.Context, in CloneInput) (CloneResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Cloned = append(f.Cloned, in)
	if f.CloneErr != nil {
		return CloneResult{}, f.CloneErr
	}
	dest := fakeNormalize(in.DestinationPath)
	if strings.TrimSpace(in.RemoteURL) == "" || dest == "" {
		return CloneResult{}, &ToolError{Tool: "t3_project_clone", Code: "orchestration_error", Message: "Enter a repository path or clone URL before cloning."}
	}
	if f.folders.occupied[dest] || f.registeredLocked(dest) {
		return CloneResult{}, &ToolError{Tool: "t3_project_clone", Code: "orchestration_error", Message: msgDestinationInUse}
	}
	mark(&f.folders.missing, dest, false)
	mark(&f.folders.occupied, dest, true)
	if f.LoseClone > 0 {
		f.LoseClone--
		return CloneResult{}, fmt.Errorf("t3 t3_project_clone: %w", errFakeLostReply)
	}
	return CloneResult{Cwd: dest}, nil
}

// FakeProjectUpdate is one UpdateProject call, its fields copied.
type FakeProjectUpdate struct {
	ProjectID string
	Title     string // "" when unchanged
	Archived  *bool
}

// projectIndexLocked finds a live project, as T3's readRow.
func (f *Fake) projectIndexLocked(id string) int {
	for i, p := range f.projects {
		if p.ID == id && p.DeletedAt == nil {
			return i
		}
	}
	return -1
}

// UpdateProject models t3_project_update's title and archived fields.
func (f *Fake) UpdateProject(_ context.Context, id string, in ProjectUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := FakeProjectUpdate{ProjectID: id}
	if in.Title != nil {
		call.Title = *in.Title
	}
	if in.Archived != nil {
		archived := *in.Archived
		call.Archived = &archived
	}
	f.Updated = append(f.Updated, call)
	if f.UpdateErr != nil {
		return f.UpdateErr
	}
	i := f.projectIndexLocked(id)
	if i < 0 {
		return &ToolError{Tool: "t3_project_update", Code: "invalid_request", Message: msgProjectNotFound}
	}
	p := &f.projects[i]
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if title == "" {
			return &ToolError{Tool: "t3_project_update", Code: "invalid_request", Message: "title must be a non-empty string."}
		}
		p.Title = title
	}
	now := f.now()
	if in.Archived != nil {
		switch {
		case !*in.Archived:
			p.ArchivedAt = nil
		case p.ArchivedAt == nil:
			p.ArchivedAt = &now
		}
	}
	p.UpdatedAt = now
	if f.LoseUpdate > 0 {
		f.LoseUpdate--
		return fmt.Errorf("t3 t3_project_update: %w", errFakeLostReply)
	}
	return nil
}

// DeleteProject models t3_project_delete without force: a project with
// threads (archived ones included) is refused; a deleted one leaves the
// listing, as t3_project_list skips deleted projects.
func (f *Fake) DeleteProject(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Deleted = append(f.Deleted, id)
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	i := f.projectIndexLocked(id)
	if i < 0 {
		return &ToolError{Tool: "t3_project_delete", Code: "invalid_request", Message: msgProjectNotFound}
	}
	for _, t := range f.threads {
		if t.ProjectID == id {
			return &ToolError{Tool: "t3_project_delete", Code: "invalid_request", Message: msgProjectNotEmpty}
		}
	}
	f.projects = append(f.projects[:i], f.projects[i+1:]...)
	if f.LoseDelete > 0 {
		f.LoseDelete--
		return fmt.Errorf("t3 t3_project_delete: %w", errFakeLostReply)
	}
	return nil
}
