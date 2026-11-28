package api

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/remote"
)

// workspaceAddInput is POST /api/connections/{connectionId}/workspaces.
type workspaceAddInput struct {
	SourceID  string          `json:"sourceId"`
	Input     json.RawMessage `json:"input"`
	RequestID string          `json:"requestId"`
	ProjectID *int64          `json:"projectId"`
	Confirmed bool            `json:"confirmed"`
}

// addedWorkspace is the destination projection of the added row. Path is
// the raw root, on human responses only (ADR 0010).
type addedWorkspace struct {
	db.Destination
	Path string `json:"path,omitempty"`
}

type workspaceAddResult struct {
	Workspace addedWorkspace `json:"workspace"`
	Group     db.MergeSource `json:"group"`
	Existing  bool           `json:"existing"`
	// Message and Facts are the harness's words, on human responses only:
	// an adapter may name paths in them.
	Message string           `json:"message,omitempty"`
	Facts   []execution.Fact `json:"facts,omitempty"`
}

// workspaceNotListed: the harness answered, but its inventory does not
// show the workspace (502 workspace_not_listed).
type workspaceNotListed struct{ msg string }

func (e *workspaceNotListed) Error() string { return e.msg }

// checkWorkspaceAdd applies the add-workspace declaration rules to caps: a
// malformed declaration or an adapter without WorkspaceAdder leaves the
// operation unavailable.
func checkWorkspaceAdd(adapter execution.Catalog, caps execution.Capabilities) (execution.Capabilities, error) {
	op := caps.Operations[execution.OpWorkspaceAdd]
	if err := caps.ValidateWorkspaceSources(); err != nil {
		return caps, &db.HarnessError{Err: fmt.Errorf("malformed workspace source declarations: %w", err)}
	}
	if _, ok := execution.As[execution.WorkspaceAdder](adapter); op.Available && !ok {
		caps.Operations = cloneOperations(caps.Operations)
		caps.Operations[execution.OpWorkspaceAdd] = execution.Availability{Reason: "This connection does not support adding workspaces."}
	}
	return caps, nil
}

func cloneOperations(ops map[execution.Op]execution.Availability) map[execution.Op]execution.Availability {
	out := make(map[execution.Op]execution.Availability, len(ops)+1)
	for op, a := range ops {
		out[op] = a
	}
	return out
}

// addWorkspace relays an explicit request to the connection's harness,
// then refreshes the connection's inventory and answers with the row it
// lists (ADR 0012). Stage never creates the workspace itself.
func (s *server) addWorkspace(w http.ResponseWriter, r *http.Request) error {
	id, err := s.connectionPath(r)
	if err != nil {
		return err
	}
	var in workspaceAddInput
	if err := decodeStrict(r, &in); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(in.RequestID) == "":
		return db.Invalid("requestId is required")
	case len(in.RequestID) > execution.WorkspaceAddMaxRequestID:
		return db.Invalid("requestId must be at most %d bytes", execution.WorkspaceAddMaxRequestID)
	case strings.TrimSpace(in.SourceID) == "":
		return db.Invalid("sourceId is required: choose a source from the connection's workspaceSources")
	case in.ProjectID != nil && *in.ProjectID <= 0:
		return db.Invalid("projectId must be a positive project id")
	}
	if len(bytes.TrimSpace(in.Input)) == 0 || bytes.Equal(bytes.TrimSpace(in.Input), []byte("null")) {
		in.Input = json.RawMessage("{}")
	}
	ctx := r.Context()
	conn, err := s.Store.GetHarnessConnection(ctx, id)
	if err != nil {
		return err
	}
	if conn.ForgottenAt != nil {
		return db.ErrNotFound
	}
	if !conn.Active {
		return db.ConnectionRetired()
	}
	descriptor, _ := s.registry().Lookup(conn.Harness)
	name := cmp.Or(descriptor.Name, conn.Harness)
	var target *db.Project
	if in.ProjectID != nil {
		p, err := s.Store.GetProject(ctx, *in.ProjectID)
		if err != nil {
			return err
		}
		target = &p
	}
	adapter, err := s.Engine.Connections.Adapter(id)
	if err != nil {
		return err
	}
	caps, err := adapter.Features(ctx)
	if err != nil {
		return &db.HarnessError{Err: err}
	}
	if caps, err = checkWorkspaceAdd(adapter, caps); err != nil {
		return err
	}
	if op := caps.Operations[execution.OpWorkspaceAdd]; !op.Available {
		return &db.ConflictError{Code: "operation_unavailable", Msg: cmp.Or(op.Reason, "This connection does not support adding workspaces.")}
	}
	index := slices.IndexFunc(caps.WorkspaceSources, func(src execution.WorkspaceSource) bool { return src.ID == in.SourceID })
	if index < 0 {
		return fmt.Errorf("%w: unknown workspace source %q", db.ErrNotFound, in.SourceID)
	}
	source := caps.WorkspaceSources[index]
	if !source.Availability.Available {
		return &db.ConflictError{Code: "operation_unavailable", Msg: source.Availability.Reason}
	}
	if err := source.ValidateInput(in.Input); err != nil {
		return workspaceAddError(name, err)
	}
	if source.Confirm != "" && !in.Confirmed {
		return db.Invalid("Confirm before running %s: %s Send confirmed: true.", source.Label, source.Confirm)
	}
	adder, ok := execution.As[execution.WorkspaceAdder](adapter)
	if !ok {
		return &db.ConflictError{Code: "operation_unavailable", Msg: "This connection does not support adding workspaces."}
	}
	if !s.Store.ConnectionActive(id) {
		return db.ConnectionRetired()
	}

	// The harness may finish after the client stops waiting: complete the
	// add, refresh and record under the server's own deadline.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), execution.WorkspaceAddTimeout)
	defer cancel()
	added, err := adder.AddWorkspace(ctx, execution.WorkspaceAddRequest{
		Source: source.ID, Input: in.Input, RequestID: in.RequestID, Confirmed: in.Confirmed,
	})
	if err != nil {
		return workspaceAddError(name, err)
	}
	if added.Workspace.ID == "" {
		return &db.HarnessError{Err: fmt.Errorf("%s did not name the workspace it added. Check it in %s, then retry with the same requestId.", name, name)}
	}
	retry := fmt.Sprintf("Check it in %s, then retry with the same requestId.", name)
	if _, err := s.refreshWorkspaces(ctx, id); err != nil {
		return &workspaceNotListed{fmt.Sprintf("%s answered, but Stage could not refresh its workspaces (%v). %s", name, err, retry)}
	}
	row, err := s.addedRow(ctx, id, added.Workspace)
	if err != nil {
		return err
	}
	if row == nil || row.GroupProjectID == nil {
		return &workspaceNotListed{fmt.Sprintf("%s does not list the added workspace. %s", name, retry)}
	}
	group := *row.GroupProjectID
	if target != nil && target.ID != group {
		if err := s.joinProject(ctx, id, row.WorkspaceID, target.ID); err != nil {
			return &db.ConflictError{Code: "membership_failed", Msg: fmt.Sprintf(
				"%s added the workspace, but Stage could not move it into project #%d: %v Retrying with the same requestId is safe.", name, target.ID, err)}
		}
		group = target.ID
	}
	project, err := s.Store.GetProject(ctx, group)
	if err != nil {
		return err
	}
	result, err := s.addedResult(ctx, conn, descriptor, *row, project)
	if err != nil {
		return err
	}
	result.Existing = added.Existing
	s.recordWorkspaceAdded(ctx, r, result, source, in.RequestID)
	if projectSummaryRequest(r) {
		result.Workspace.Path = ""
	} else {
		result.Message, result.Facts = added.Message, descriptor.DisplayFacts(added.Facts)
	}
	writeJSON(w, http.StatusCreated, result)
	return nil
}

// addedRow is the listed catalog row with the harness's native ID and root.
func (s *server) addedRow(ctx context.Context, connection int64, info execution.WorkspaceInfo) (*db.Workspace, error) {
	rows, _, err := s.Store.WorkspaceCatalog(ctx, connection)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(rows, func(w db.Workspace) bool {
		return w.ExternalID == info.ID && w.Workspace == info.WorkspaceRoot && w.Available && !w.HarnessArchived
	})
	if i < 0 {
		return nil, nil
	}
	return &rows[i], nil
}

// joinProject applies the membership edit set_membership uses: preview,
// then set with its token. A changed preview is previewed once more.
func (s *server) joinProject(ctx context.Context, connection int64, workspaceID string, project int64) error {
	in := db.GroupEditInput{ConnectionID: connection, WorkspaceID: workspaceID, TargetProjectID: &project}
	for attempt := 0; ; attempt++ {
		preview, err := s.Store.EditProjectGroup(ctx, in)
		if err != nil {
			return err
		}
		if preview.Blocked != nil {
			if preview.Blocked.Code == "same_project" {
				return nil
			}
			return errors.New(preview.Blocked.Reason)
		}
		set := in
		set.PreviewToken = preview.PreviewToken
		_, err = s.Store.EditProjectGroup(ctx, set)
		var changed *db.PreviewChangedError
		if errors.As(err, &changed) && attempt == 0 {
			continue
		}
		return err
	}
}

func (s *server) addedResult(ctx context.Context, conn db.HarnessConnection, d harness.Descriptor, row db.Workspace, project db.Project) (workspaceAddResult, error) {
	m, err := s.Store.GetMachineInfo(ctx, conn.MachineID)
	if err != nil {
		return workspaceAddResult{}, err
	}
	return workspaceAddResult{
		Workspace: addedWorkspace{
			Destination: db.Destination{
				ConnectionID: conn.ID, WorkspaceID: row.WorkspaceID, ProjectID: row.ProjectID, GroupProjectID: &project.ID,
				Title:           db.WorkspaceTitle(row.Title, row.Workspace),
				ConnectionLabel: db.SummarizeConnection(conn, d.Name).Label,
				MachineLabel:    cmp.Or(m.Label, "This computer"), Available: true,
			},
			Path: row.Workspace,
		},
		Group: db.MergeSource{ID: project.ID, Title: project.Title},
	}, nil
}

// recordWorkspaceAdded writes the project event. The add already happened,
// so a failure is logged, not returned.
func (s *server) recordWorkspaceAdded(ctx context.Context, r *http.Request, result workspaceAddResult, source execution.WorkspaceSource, requestID string) {
	var caller *db.SessionRef
	if actionCaller(r) == execution.CallerAgent {
		caller = db.CallerRef(r.Context())
	}
	ws := result.Workspace
	text := fmt.Sprintf("Added workspace %s on %s (%s).", ws.Title, ws.ConnectionLabel, source.Label)
	if result.Existing {
		text = fmt.Sprintf("Workspace %s was already on %s; requested again (%s).", ws.Title, ws.ConnectionLabel, source.Label)
	}
	if err := s.Store.RecordWorkspaceAdded(ctx, result.Group.ID, text, requestID, caller); err != nil {
		s.log.Error("record workspace added", "connection", ws.ConnectionID, "project", result.Group.ID, "err", err)
	}
}

func workspaceAddError(name string, err error) error {
	var (
		invalid     *execution.Invalid
		busy        *execution.Busy
		uncertain   *execution.OutcomeUncertain
		unavailable *remote.UnavailableError
		conflict    *db.ConflictError
		harnessErr  *db.HarnessError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &uncertain), errors.As(err, &unavailable), errors.As(err, &conflict), errors.As(err, &harnessErr):
		return err
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return &execution.OutcomeUncertain{Cause: err}
	case errors.Is(err, remote.ErrOffline):
		return &remote.UnavailableError{Code: remote.CodeMachineOffline, Message: "The Stage worker is offline. The request was not sent."}
	case errors.As(err, &invalid):
		return db.Invalid("%s", invalid.Reason)
	case errors.As(err, &busy):
		// The harness changed nothing; a retry is safe.
		return &db.ConflictError{Code: "harness_busy", Msg: busy.Reason}
	case errors.Is(err, execution.ErrUnavailable):
		return &db.ConflictError{Code: "operation_unavailable", Msg: name + " cannot add workspaces right now."}
	default:
		return &db.HarnessError{Err: fmt.Errorf("%s could not add the workspace. Check it in %s before retrying with the same requestId.", name, name)}
	}
}
