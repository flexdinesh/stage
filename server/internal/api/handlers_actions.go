package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/principal"
	"github.com/flexdinesh/stage/server/internal/remote"
)

func actionCaller(r *http.Request) execution.Caller {
	if a, ok := principal.From(r.Context()); ok && a.Kind != principal.Personal {
		if a.IsHuman() {
			return execution.CallerUser
		}
		return execution.CallerAgent
	}
	if db.ActingMachine(r.Context()) != nil || r.Header.Get("Stage-Action-Agent") == "1" || r.Header.Get(agentctx.CallerHeader) != "" {
		return execution.CallerAgent
	}
	return execution.CallerUser
}

type actionForbidden struct{}

func (actionForbidden) Error() string { return "This action is available to users only." }

type actionSite struct {
	descriptor   harness.Descriptor
	machineID    int64
	connectionID int64
	taskID       int64
	request      execution.ActionRequest
	connector    harness.Connector
	adapter      execution.Adapter
	binding      *db.ProjectBinding
}

func (s *server) resolveActionSite(r *http.Request, target execution.ActionTarget) (actionSite, error) {
	var site actionSite
	id, err := pathID(r)
	if err != nil {
		return site, err
	}
	ctx := r.Context()
	var bindingID *int64
	site.request.Target = target
	switch target {
	case execution.TargetConnection:
		site.connectionID = id
	case execution.TargetSession:
		task, err := s.Store.GetTask(ctx, id)
		if err != nil {
			return site, err
		}
		if task.Session == nil || task.Session.ConnectionID != task.ConnectionID {
			return site, fmt.Errorf("%w: the task has no linked session", db.ErrNotFound)
		}
		site.connectionID, site.taskID, site.request.Session = task.ConnectionID, task.ID, task.Session
		bindingID = task.BindingID
		if task.Workspace != nil && task.Workspace.Source == "pinned" {
			bindingID = &task.Workspace.BindingID
		}
		site.binding, err = s.actionBinding(ctx, task.ProjectID, task.ConnectionID, bindingID)
		if err != nil {
			return site, err
		}
	case execution.TargetProject:
		site.connectionID, err = strconv.ParseInt(r.URL.Query().Get("connectionId"), 10, 64)
		if err != nil || site.connectionID <= 0 {
			return site, db.Invalid("A positive connectionId is required for project actions.")
		}
		if q := r.URL.Query(); q.Has("bindingId") {
			bindingID, err = queryInt64(q, "bindingId")
			if err != nil || bindingID == nil || *bindingID <= 0 {
				return site, db.Invalid("A positive bindingId is required.")
			}
		}
	}
	conn, err := s.Store.GetHarnessConnection(ctx, site.connectionID)
	if err != nil {
		return site, err
	}
	if !conn.Active {
		return site, db.ConnectionRetired()
	}
	site.descriptor, _ = s.registry().Lookup(conn.Harness)
	site.machineID = conn.MachineID
	if target == execution.TargetConnection {
		site.connector, _ = s.connectors().Get(conn.MachineID, conn.Harness)
		return site, nil
	}
	if target == execution.TargetProject {
		site.binding, err = s.actionBinding(ctx, id, conn.ID, bindingID)
		if err != nil {
			return site, err
		}
		if site.binding == nil {
			return site, fmt.Errorf("%w: no available workspace binding for this project on the selected connection", db.ErrNotFound)
		}
		site.request.Project = &execution.ActionWorkspace{
			ExternalProjectID: site.binding.ExternalProjectID,
			Directory:         site.binding.Workspace,
		}
	}
	site.adapter, err = s.Engine.Connections.Adapter(conn.ID)
	return site, actionError(err)
}

// actionBinding selects an available binding on the target connection.
func (s *server) actionBinding(ctx context.Context, projectID, connectionID int64, bindingID *int64) (*db.ProjectBinding, error) {
	id, err := s.projectID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	p, err := s.Store.GetProject(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, b := range p.Bindings {
		if b.ConnectionID != connectionID || !b.ConnectionActive || b.Status != "available" {
			continue
		}
		if bindingID == nil && b.Primary || bindingID != nil && b.ID == *bindingID {
			return &b, nil
		}
	}
	return nil, nil
}

// actionCapabilities uses the same workspace scope for discovery and dispatch.
func actionCapabilities(ctx context.Context, adapter execution.Catalog, binding *db.ProjectBinding) (execution.Capabilities, error) {
	var caps execution.Capabilities
	var err error
	if scoped, ok := execution.As[execution.WorkspaceFeatures](adapter); ok && binding != nil {
		caps, err = scoped.FeaturesForWorkspace(ctx, binding.ExternalProjectID, binding.Workspace)
	} else {
		caps, err = adapter.Features(ctx)
	}
	if err != nil {
		return caps, actionError(err)
	}
	return validateActionCapabilities(adapter, caps)
}

func validateActionCapabilities(adapter execution.Catalog, caps execution.Capabilities) (execution.Capabilities, error) {
	if err := execution.ValidateActions(caps.Actions, ""); err != nil {
		return caps, &db.HarnessError{Err: fmt.Errorf("malformed action declarations: %w", err)}
	}
	if _, ok := execution.As[execution.Actor](adapter); !ok {
		caps.Actions = slices.Clone(caps.Actions)
		for i := range caps.Actions {
			if caps.Actions[i].Availability.Available {
				caps.Actions[i].Availability = execution.Availability{Reason: "This connection does not support running actions."}
			}
		}
	}
	return caps, nil
}

func (site actionSite) actionDeclarations(ctx context.Context) ([]execution.Action, error) {
	if site.request.Target == execution.TargetConnection {
		return site.descriptor.Setup.Actions, nil
	}
	caps, err := actionCapabilities(ctx, site.adapter, site.binding)
	return caps.Actions, err
}

// liveSetupActions applies the connector's last status without a forced probe.
func (site actionSite) liveSetupActions(ctx context.Context, declared []execution.Action) ([]execution.Action, map[string]string, error) {
	if len(declared) == 0 {
		return declared, nil, nil
	}
	actions := slices.Clone(declared)
	if site.connector == nil {
		for i := range actions {
			actions[i].Availability = execution.Availability{Reason: "Connection setup is unavailable on this machine."}
		}
		return actions, nil, nil
	}
	st, err := site.connector.Status(ctx, false)
	if err != nil {
		return nil, nil, actionError(err)
	}
	codes := make(map[string]string)
	for i, a := range actions {
		if !a.Availability.Available {
			continue
		}
		index := slices.IndexFunc(st.Actions, func(state harness.ActionState) bool { return state.ID == a.ID })
		if index >= 0 && !st.Actions[index].Available {
			reason := st.Actions[index].Reason
			if strings.TrimSpace(reason) == "" {
				reason = "The connector cannot run this action right now."
			}
			actions[i].Availability = execution.Availability{Reason: reason}
			if st.Actions[index].Code != "" {
				codes[a.ID] = st.Actions[index].Code
			}
		}
	}
	return actions, codes, nil
}

func visibleActions(actions []execution.Action, target execution.ActionTarget, caller execution.Caller) []execution.Action {
	out := make([]execution.Action, 0, len(actions))
	for _, a := range actions {
		if (target == "" || a.Target == target) && a.Allows(caller) {
			out = append(out, a)
		}
	}
	return out
}

func (s *server) actions(w http.ResponseWriter, r *http.Request, target execution.ActionTarget) error {
	site, err := s.resolveActionSite(r, target)
	if err != nil {
		return err
	}
	if r.Method == http.MethodGet {
		actions, err := site.actionDeclarations(r.Context())
		if err != nil {
			return err
		}
		actions = visibleActions(actions, target, actionCaller(r))
		if target == execution.TargetConnection {
			actions, _, err = site.liveSetupActions(r.Context(), actions)
			if err != nil {
				return err
			}
		}
		writeJSON(w, http.StatusOK, actions)
		return nil
	}
	res, err := s.runAction(r, site, false)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

// scopedSetupActor lets a worker fence a request against a replacement at
// the point where the connector executes.
type scopedSetupActor interface {
	RunForConnection(context.Context, int64, string, json.RawMessage) (harness.Result, error)
}

type setupMutationKey struct {
	machineID int64
	kind      string
}

func (s *server) lockSetupMutation(machineID int64, kind string) func() {
	v, _ := s.setupMutations.LoadOrStore(setupMutationKey{machineID, kind}, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *server) runAction(r *http.Request, site actionSite, setupRoute bool) (execution.ActionResult, error) {
	var result execution.ActionResult
	ctx := r.Context()
	actions, err := site.actionDeclarations(ctx)
	if err != nil {
		return result, err
	}
	id := r.PathValue("action")
	index := slices.IndexFunc(actions, func(a execution.Action) bool { return a.ID == id && a.Target == site.request.Target })
	if index < 0 {
		return result, fmt.Errorf("%w: unknown %s action %q", db.ErrNotFound, site.request.Target, id)
	}
	action := actions[index]
	caller := actionCaller(r)
	if !action.Allows(caller) {
		return result, actionForbidden{}
	}
	if action.Destructive && !action.Allows(execution.CallerAgent) && r.Header.Get("Stage-Action-Confirmed") != "1" {
		return result, db.Invalid("Confirm this action before running it.")
	}
	body, err := rawBody(r)
	if err != nil {
		return result, err
	}
	if setupRoute && len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	if err := action.ValidateInput(body); err != nil {
		return result, actionError(err)
	}
	code := "operation_unavailable"
	if site.request.Target == execution.TargetConnection {
		unlock := s.lockSetupMutation(site.machineID, site.descriptor.Kind)
		defer unlock()
		live, codes, err := site.liveSetupActions(ctx, []execution.Action{action})
		if err != nil {
			return result, err
		}
		action = live[0]
		if codes[action.ID] != "" {
			code = codes[action.ID]
		}
	}
	if !action.Availability.Available {
		return result, &db.ConflictError{Code: code, Msg: action.Availability.Reason}
	}
	site.request.ID, site.request.Input = action.ID, body
	if site.connectionID != 0 && !s.Store.ConnectionActive(site.connectionID) {
		return result, db.ConnectionRetired()
	}
	if site.request.Target == execution.TargetConnection {
		if site.connectionID != 0 && site.connector.Summary(ctx).ConnectionID != site.connectionID {
			return result, db.ConnectionRetired()
		}
		if scoped, ok := site.connector.(scopedSetupActor); ok && site.connectionID != 0 {
			result, err = scoped.RunForConnection(ctx, site.connectionID, action.ID, body)
		} else {
			result, err = site.connector.Run(ctx, action.ID, body)
		}
	} else {
		actor, ok := execution.As[execution.Actor](site.adapter)
		if !ok {
			return result, actionError(execution.ErrUnavailable)
		}
		if site.request.Session != nil {
			fresh, err := site.adapter.Observe(ctx, *site.request.Session)
			if err != nil {
				return result, actionError(err)
			}
			if fresh.Session != *site.request.Session {
				return result, fmt.Errorf("%w: the harness no longer reports the task's linked session", db.ErrNotFound)
			}
		} else if site.request.Project != nil {
			workspaces, err := site.adapter.Workspaces(ctx)
			if err != nil {
				return result, actionError(err)
			}
			workspace := site.request.Project
			if !slices.ContainsFunc(workspaces, func(w execution.WorkspaceInfo) bool {
				return w.ID == workspace.ExternalProjectID && w.WorkspaceRoot == workspace.Directory && w.DeletedAt == nil
			}) {
				return result, fmt.Errorf("%w: the harness no longer reports the selected workspace binding", db.ErrNotFound)
			}
			b, err := s.actionBinding(ctx, site.binding.ProjectID, site.connectionID, &site.binding.ID)
			if err != nil {
				return result, err
			}
			if b == nil || b.ExternalProjectID != workspace.ExternalProjectID || b.Workspace != workspace.Directory {
				return result, fmt.Errorf("%w: the selected workspace binding changed", db.ErrNotFound)
			}
		}
		if !s.Store.ConnectionActive(site.connectionID) {
			return result, db.ConnectionRetired()
		}
		result, err = actor.Act(ctx, site.request)
		result.Replaced = 0 // only connection setup can replace an instance
	}
	if err != nil {
		return execution.ActionResult{}, actionError(err)
	}
	result.JournalError = ""
	if result.Message == "" {
		result.Message = action.Label + " completed."
	}
	result.Facts = site.descriptor.DisplayFacts(result.Facts)
	if !site.descriptor.AllowsDisplayLink(result.Link) {
		result.Link = ""
	}
	if site.request.Session != nil {
		s.journalAction(ctx, r, site, caller, &result)
	}
	return result, nil
}

func (s *server) journalAction(ctx context.Context, r *http.Request, site actionSite, caller execution.Caller, result *execution.ActionResult) {
	var source *execution.SessionRef
	if caller == execution.CallerAgent {
		if ref, err := agentctx.ParseSession(r.Header.Get(agentctx.CallerHeader)); err == nil {
			source = &ref
		}
	}
	// Even if the client stopped waiting, record the successful dispatch.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.Store.RecordSessionAction(ctx, site.taskID, *site.request.Session, site.request.ID, caller, source); err != nil {
		s.log.Error("journal session action", "task", site.taskID, "action", site.request.ID, "err", err)
		result.JournalError = "The action ran, but Stage could not record it in the task journal."
	}
}

func actionError(err error) error {
	var (
		invalid     *execution.Invalid
		busy        *execution.Busy
		uncertain   *execution.ActionUncertain
		unavailable *remote.UnavailableError
		apiInvalid  *db.InvalidError
		conflict    *db.ConflictError
		harnessErr  *db.HarnessError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &uncertain), errors.As(err, &unavailable), errors.As(err, &apiInvalid), errors.As(err, &conflict):
		return err
	case errors.Is(err, remote.ErrOffline):
		return &remote.UnavailableError{Code: remote.CodeMachineOffline, Message: "The Stage worker is offline. The action was not sent."}
	case errors.As(err, &invalid):
		return db.Invalid("%s", invalid.Reason)
	case errors.As(err, &busy):
		return &db.ConflictError{Code: "action_busy", Msg: busy.Reason}
	case errors.Is(err, execution.ErrUnavailable):
		reason := strings.TrimSpace(strings.TrimPrefix(err.Error(), execution.ErrUnavailable.Error()+":"))
		if reason == execution.ErrUnavailable.Error() || reason == "" {
			reason = "The connection cannot run this action right now."
		}
		return &db.ConflictError{Code: "operation_unavailable", Msg: reason}
	case errors.Is(err, execution.ErrNotFound):
		return fmt.Errorf("%w: the harness could not find the action target", db.ErrNotFound)
	case errors.Is(err, db.ErrNotFound):
		return err
	case errors.As(err, &harnessErr):
		return err
	default:
		return &db.HarnessError{Err: errors.New("The harness could not finish the action request. Check its state before retrying.")}
	}
}
