// Package api is the REST API under /api (contract: docs/api.md).
package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/notify"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// Deps are the API's collaborators.
type Deps struct {
	Config config.Config
	Store  *db.Store
	Engine *engine.Engine
	Push   *notify.Notifier
	// Holds releases held notifications; escalate_to_user goes through it
	// (nil: escalate is 503).
	Holds  *notify.Holds
	Logger *slog.Logger
	// Inbox wakes GET /api/notifications/stream (nil = a private hub).
	Inbox *notify.Hub
	// Instance identifies the running server in GET /api/health (zero in
	// tests).
	Instance instance.Meta
	// OnTreeChange is called after a tree mutation (needs, start mode,
	// membership) so the release runner reconciles now; nil = no-op. Must
	// not block.
	OnTreeChange func()
	// Connectors is the connection setup per machine and connected harness
	// kind (fixed; used when LiveConnectors is nil).
	Connectors harness.Connectors
	// LiveConnectors indexes the connection setups of the machines hosted
	// now (remote machines join and leave while the server runs).
	LiveConnectors func() harness.Connectors
	// Harnesses is the server's fixed harness composition (nil: none).
	Harnesses *harness.Registry
	// Channels is the worker channel hub (nil: a private hub that
	// persists presence through Store).
	Channels *workerchan.Hub
	// MCP is the streamable HTTP MCP handler (no origin policy of its own)
	// that worker agent gateways reach through GatewayPrefix/mcp (nil: 404).
	MCP http.Handler
	// Access is nil in personal mode. Shared routes and in-process MCP
	// dispatch use this same policy.
	Access *Access
	// InstallationID is the installation discovered at runtime startup.
	InstallationID string
	// Machines hosts remote machines as they join and are removed (nil:
	// none are hosted).
	Machines MachineHosts
}

// MachineHosts follows the remote machines the server hosts
// (remote.Hosts).
type MachineHosts interface {
	Join(machineID int64)
	Leave(machineID int64)
}

// connectors is the connection setup per machine and kind now.
func (s *server) connectors() harness.Connectors {
	if s.LiveConnectors != nil {
		return s.LiveConnectors()
	}
	return s.Connectors
}

// registry is Harnesses, or an empty registry.
func (s *server) registry() *harness.Registry {
	if s.Harnesses == nil {
		return harness.MustRegistry()
	}
	return s.Harnesses
}

type server struct {
	Deps
	log *slog.Logger
	// mux is the /api route table (what the agent gateway route serves).
	mux                    *http.ServeMux
	setupMutations         sync.Map
	sessionSearchCursorKey []byte
}

// NewRouter returns the /api handler.
func NewRouter(deps Deps) http.Handler {
	s := &server{Deps: deps, log: deps.Logger, sessionSearchCursorKey: []byte(rand.Text())}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.Inbox == nil {
		s.Inbox = notify.NewHub(context.Background())
	}
	if s.Channels == nil {
		s.Channels = newChannelHub(context.Background(), s.Store)
	}
	s.Channels.SetHarnessKinds(s.registry().Kinds())
	mux := http.NewServeMux()
	s.mux = mux
	handle := func(scope routeScope, pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, s.authorize(scope, func(w http.ResponseWriter, r *http.Request) {
			if err := fn(w, r); err != nil {
				s.writeErr(w, r, err)
			}
		}))
	}
	h := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		handle(humanRoute, pattern, fn)
	}
	agent := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		handle(agentRoute, pattern, func(w http.ResponseWriter, r *http.Request) error {
			return fn(w, s.withCaller(r))
		})
	}
	passive := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		handle(passiveHumanRoute, pattern, fn)
	}

	s.registerAuth(mux)
	mux.HandleFunc("GET /api/health", s.accessHealth)
	h("GET /api/me", s.me)

	h("POST /api/sync", s.runSync)

	agent("GET /api/projects", s.listProjects)
	agent("GET /api/project-groups", s.listProjectGroups)
	agent("GET /api/project-groups/{id}", s.getProjectGroup)
	agent("POST /api/project-groups/membership", s.editProjectGroup)
	h("GET /api/shared-paths", removedRoute("GET /api/connections/{connectionId}/workspaces"))
	h("PUT /api/shared-paths", removedRoute("POST /api/project-groups/membership"))
	h("POST /api/projects/{id}/bindings/{connectionId}/match", removedRoute("GET /api/connections/{connectionId}/workspaces"))
	agent("POST /api/projects", s.createProject)
	agent("GET /api/projects/{id}", s.getProject)
	agent("PATCH /api/projects/{id}", s.updateProject)
	h("PUT /api/projects/{id}/binding", removedRoute("POST /api/project-groups/membership"))
	h("DELETE /api/projects/{id}/binding", removedRoute("POST /api/project-groups/membership"))
	h("DELETE /api/projects/{id}/bindings/{bindingId}", removedRoute("POST /api/project-groups/membership"))
	h("PUT /api/projects/{id}/bindings/{bindingId}/primary", removedRoute("an explicit connectionId and workspaceId on each task or schedule"))
	h("POST /api/projects/{id}/merge", removedRoute("POST /api/project-groups/membership"))
	h("POST /api/projects/{id}/merge-dismissals", removedRoute("GET /api/project-groups"))
	h("POST /api/projects/{id}/split", removedRoute("POST /api/project-groups/membership"))
	agent("GET /api/projects/{id}/events", s.projectEvents)
	h("GET /api/workspaces/review", removedRoute("GET /api/project-groups"))
	agent("GET /api/harnesses", s.listHarnesses)
	agent("GET /api/harnesses/{kind}/status", s.harnessStatus)
	h("PUT /api/harnesses/{kind}/settings", s.harnessSettings)
	h("POST /api/harnesses/{kind}/actions/{action}", s.harnessAction)
	h("DELETE /api/harnesses/{kind}/connection", s.harnessDisconnect)
	// The same per machine (/api/harnesses is machine 1's).
	agent("GET /api/machines/{id}/harnesses", s.listHarnesses)
	agent("GET /api/machines/{id}/harnesses/{kind}/status", s.harnessStatus)
	h("PUT /api/machines/{id}/harnesses/{kind}/settings", s.harnessSettings)
	h("POST /api/machines/{id}/harnesses/{kind}/actions/{action}", s.harnessAction)
	h("DELETE /api/machines/{id}/harnesses/{kind}/connection", s.harnessDisconnect)
	agent("GET /api/machines", s.listMachines)
	h("POST /api/machines/pairings", s.createPairing)
	// GET /api/machines/pairings/{id}, written so the mux can tell it from
	// GET /api/machines/{id}/harnesses (the more specific pattern below).
	h("GET /api/machines/{scope}/{id}", func(w http.ResponseWriter, r *http.Request) error {
		if r.PathValue("scope") != "pairings" {
			return fmt.Errorf("%w: %s", db.ErrNotFound, r.URL.Path)
		}
		return s.pairingStatus(w, r)
	})
	h("DELETE /api/machines/pairings/{id}", s.revokePairing)
	agent("GET /api/machines/{id}", s.getMachine)
	h("PATCH /api/machines/{id}", s.renameMachine)
	h("DELETE /api/machines/{id}", s.removeMachine)
	h("POST /api/machines/{id}/ping", s.pingMachine)
	mux.Handle("POST /api/worker/join", s.worker(false, true, false, s.joinWorker))
	mux.Handle("GET /api/worker/machine", s.worker(true, true, true, s.workerMachine))
	mux.Handle("GET /api/worker/channel", s.worker(true, false, true, s.workerChannelStream))
	mux.Handle("POST /api/worker/channel", s.worker(true, false, true, s.workerChannelPost))
	mux.Handle("POST /api/worker/leave", s.worker(true, true, false, s.workerLeave))
	mux.Handle(GatewayPrefix+"/", s.worker(true, true, false, s.workerGateway))
	mux.Handle(WorkerPrefix, s.worker(true, true, false, func(w http.ResponseWriter, _ *http.Request) error {
		writeError(w, http.StatusNotFound, "not found")
		return nil
	}))
	agent("GET /api/connections", s.connections)
	agent("GET /api/connections/resolve", s.resolveConnection)
	agent("GET /api/connections/{connectionId}/capabilities", s.executionCapabilities)
	h("PUT /api/connections/{connectionId}/auto-complete", s.setAutoComplete)
	h("PUT /api/connections/{connectionId}/new-task-config", s.setNewTaskConfig)
	h("GET /api/connections/{connectionId}/facts", s.connectionFacts)
	h("DELETE /api/connections/{connectionId}", s.forgetConnection)
	agent("POST /api/connections/{connectionId}/observations", s.pushObservations)
	h("POST /api/tasks/{id}/launch-link/preview", s.previewLaunchLink)
	h("POST /api/tasks/{id}/launch-link", s.attachLaunchLink)
	h("GET /api/connections/{connectionId}/projects", removedRoute("GET /api/connections/{connectionId}/workspaces"))
	h("GET /api/connections/{connectionId}/workspaces", s.workspaces)
	agent("POST /api/connections/{connectionId}/workspaces", s.addWorkspace)
	h("GET /api/connections/{connectionId}/directories", s.browseDirectories)
	h("POST /api/connections/{connectionId}/resolve", removedRoute("POST /api/sync and GET /api/connections/{connectionId}/workspaces"))
	h("POST /api/connections/{connectionId}/workspaces/decline", removedRoute("POST /api/project-groups/membership"))
	h("POST /api/connections/{connectionId}/workspaces/undecline", removedRoute("POST /api/project-groups/membership"))

	for target, path := range map[execution.ActionTarget]string{
		execution.TargetConnection: "/api/connections/{id}/actions",
		execution.TargetSession:    "/api/tasks/{id}/actions",
		execution.TargetProject:    "/api/projects/{id}/actions",
	} {
		handle := func(w http.ResponseWriter, r *http.Request) error {
			return s.actions(w, r, target)
		}
		agent("GET "+path, handle)
		agent("POST "+path+"/{action}", handle)
	}

	agent("GET /api/tasks", s.listTasks)
	agent("POST /api/tasks", s.createTask)
	agent("GET /api/tasks/{id}", s.getTask)
	agent("GET /api/tasks/{id}/completion-preview", s.completionPreview)
	agent("POST /api/tasks/{id}/complete", s.completeTask)
	agent("POST /api/tasks/{id}/reopen", s.reopenTask)
	agent("GET /api/tasks/{id}/opening-prompt", s.getOpeningPrompt)
	agent("PATCH /api/tasks/{id}", s.updateTask)
	agent("DELETE /api/tasks/{id}", s.deleteTask)
	agent("POST /api/tasks/{id}/start", s.startTask)
	agent("POST /api/tasks/{id}/stop", s.stopTask)
	agent("GET /api/tasks/{id}/requests", s.taskRequests)
	agent("GET /api/tasks/{id}/queue", s.taskQueue)
	agent("DELETE /api/tasks/{id}/queue/{qid}", s.changeTaskQueue(false))
	agent("POST /api/tasks/{id}/queue/{qid}/steer", s.changeTaskQueue(true))
	agent("GET /api/tasks/{id}/activity", s.taskActivity)
	agent("GET /api/tasks/{id}/sessions/{attempt}", s.getTaskSession)
	agent("POST /api/tasks/{id}/requests/{rid}", s.respondRequest)
	agent("POST /api/tasks/{id}/messages", s.sendMessage)
	agent("POST /api/tasks/{id}/escalate", s.escalateTask)
	agent("POST /api/tasks/{id}/duplicate", s.duplicateTask)
	agent("POST /api/tasks/{id}/archive", s.archiveTask)
	agent("POST /api/tasks/{id}/unarchive", s.unarchiveTask)
	agent("PUT /api/tasks/{id}/needs", s.setNeeds)
	agent("GET /api/tasks/{id}/journal", s.taskJournal)
	agent("POST /api/tasks/{id}/journal", s.taskJournal)

	agent("GET /api/trees", s.listTrees)
	agent("POST /api/trees", s.createTree)
	agent("GET /api/trees/{id}", s.getTree)
	agent("PATCH /api/trees/{id}", s.updateTree)
	agent("DELETE /api/trees/{id}", s.deleteTree)
	agent("POST /api/trees/{id}/dissolve", s.dissolveTree)
	agent("POST /api/trees/{id}/tasks", s.addToTree)
	agent("DELETE /api/trees/{id}/tasks/{taskId}", s.removeFromTree)
	agent("POST /api/trees/{id}/start", s.startTree)
	agent("POST /api/trees/{id}/steerer", s.setSteerer)
	agent("DELETE /api/trees/{id}/steerer", s.endSteering)
	agent("GET /api/trees/{id}/steering/events", s.steeringEvents)
	agent("GET /api/trees/{id}/journal", s.treeJournal)
	agent("POST /api/trees/{id}/journal", s.treeJournal)
	agent("GET /api/journal/{id}", s.getJournalEntry)

	agent("GET /api/schedules", s.listSchedules)
	agent("POST /api/schedules", s.createSchedule)
	agent("POST /api/schedules/preview", s.previewSchedule)
	agent("GET /api/schedules/{id}", s.getSchedule)
	agent("PATCH /api/schedules/{id}", s.updateSchedule)
	agent("DELETE /api/schedules/{id}", s.deleteSchedule)
	agent("POST /api/schedules/{id}/run", s.runSchedule)

	agent("POST /api/uploads", s.uploadImage)
	agent("GET /api/attachments/{id}", s.getAttachment)
	agent("DELETE /api/attachments/{id}", s.deleteAttachment)

	h("GET /api/connections/{connectionId}/sessions", s.searchSessions)
	agent("GET /api/connections/{connectionId}/sessions/{id}/context", s.sessionContext)
	agent("POST /api/connections/{connectionId}/sessions/{id}/adopt", s.adoptSession)

	agent("GET /api/snippets", s.listSnippets)
	agent("POST /api/snippets", s.createSnippet)
	agent("GET /api/snippets/{id}", s.getSnippet)
	agent("PATCH /api/snippets/{id}", s.updateSnippet)
	agent("DELETE /api/snippets/{id}", s.deleteSnippet)

	agent("GET /api/refs/resolve", s.resolveRef)

	agent("GET /api/preferences", s.getPreferences)
	h("PUT /api/preferences/default-connection", s.putDefaultConnection)
	h("PUT /api/preferences/notifications", s.putNotifyEvents)

	h("GET /api/push/config", s.pushConfig)
	h("POST /api/push/subscriptions", s.subscribePush)
	h("DELETE /api/push/subscriptions", s.unsubscribePush)
	passive("POST /api/push/subscriptions/check", s.checkPush)
	passive("POST /api/push/delivery", s.pushDelivery)
	h("POST /api/push/test", s.testPush)
	h("GET /api/badge", s.badge)
	h("GET /api/notifications", s.listNotifications)
	h("GET /api/notifications/groups", s.listNotificationGroups)
	h("POST /api/notifications/read", s.readNotifications)
	passive("GET /api/notifications/unread-tags", s.unreadNotificationTags)
	passive("GET /api/notifications/stream", s.streamNotifications)

	mux.HandleFunc("/api/", s.authorize(humanRoute, func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	}))
	// Every response names the API contract version; clients refuse a
	// server whose version differs from theirs.
	apiVersion := strconv.Itoa(buildinfo.APIVersion)
	return RejectMachineTokens(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(buildinfo.APIVersionHeader, apiVersion)
		mux.ServeHTTP(w, r)
	}))
}

// treeChanged notifies OnTreeChange.
func (s *server) treeChanged() {
	if s.OnTreeChange != nil {
		s.OnTreeChange()
	}
}

// Health is GET /api/health: whether this is a live Stage server with a
// working database, which build it is, and (separately) each connected
// harness's last known state. A harness being offline does not make the
// server unhealthy.
type Health struct {
	Status     string     `json:"status"` // ok | error (database unreachable)
	DB         string     `json:"db"`     // ok | error
	Service    string     `json:"service"`
	Identity   string     `json:"identity"`
	Version    string     `json:"version"`
	Commit     string     `json:"commit"`
	APIVersion int        `json:"apiVersion"`
	InstanceID string     `json:"instanceId"`
	StartedAt  *time.Time `json:"startedAt"`
	// Schema is the database's migration version (0 when unreachable).
	Schema int64 `json:"schema"`
	// Engine is the storage engine: sqlite | postgres (never its URL).
	Engine string `json:"engine"`
	// PublicURL is STAGE_PUBLIC_URL, null when unset: the address copied
	// Stage links use on hosts whose own origin is not shareable (desktop).
	PublicURL *string `json:"publicUrl"`
	// Harnesses is each connector's memory-only summary, in registry order.
	Harnesses    []HealthHarness `json:"harnesses"`
	SharedAccess bool            `json:"sharedAccess,omitempty"`
}

// HealthHarness is one connected harness in Health.
type HealthHarness struct {
	Kind         string  `json:"kind"`
	ConnectionID int64   `json:"connectionId"`
	Configured   bool    `json:"configured"`
	Connected    bool    `json:"connected"`
	LastError    *string `json:"lastError"`
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	h := Health{
		Status: "ok", DB: "ok", Service: buildinfo.Service, Identity: buildinfo.Identity,
		Version: buildinfo.Version, Commit: buildinfo.Revision(), APIVersion: buildinfo.APIVersion,
		InstanceID: s.Instance.InstanceID, Engine: s.Config.Database.Engine(), SharedAccess: s.Access != nil,
	}
	if !s.Instance.StartedAt.IsZero() {
		t := s.Instance.StartedAt
		h.StartedAt = &t
	}
	if u := strings.TrimRight(s.Config.PublicURL, "/"); u != "" {
		h.PublicURL = &u
	}
	h.Harnesses = []HealthHarness{}
	// The harness setup API serves the local machine's connectors.
	s.connectors().Each(func(machine int64, d harness.Descriptor, c harness.Connector) {
		if machine != s.Engine.Connections.Machines().DefaultID() {
			return
		}
		sum := c.Summary(r.Context())
		if sum.LastError == "" {
			if saved, err := s.Store.HarnessSyncStatus(r.Context(), machine, d.Kind); err == nil && saved.LastError != nil {
				sum.LastError = *saved.LastError
			}
		}
		sum = s.harnessSummary(sum)
		hh := HealthHarness{Kind: d.Kind, ConnectionID: sum.ConnectionID, Configured: sum.Configured, Connected: sum.Connected}
		if sum.LastError != "" {
			hh.LastError = &sum.LastError
		}
		h.Harnesses = append(h.Harnesses, hh)
	})
	status := http.StatusOK
	if v, err := s.Store.SchemaVersion(r.Context()); err != nil {
		h.Status, h.DB, status = "error", "error", http.StatusServiceUnavailable
	} else {
		h.Schema = v
	}
	writeJSON(w, status, h)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json", "err", err)
	}
}

// writeError writes the contract error shape {"error": msg}.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func removedRoute(replacement string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusGone, map[string]string{
			"code": "removed", "error": "Removed. Use " + replacement + ".",
		})
		return nil
	}
}

// writeErr maps app errors to contract statuses.
func (s *server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var (
		invalid    *db.InvalidError
		conflict   *db.ConflictError
		harnessErr *db.HarnessError
		pushErr    *notify.PushError
		changed    *db.PreviewChangedError
	)
	if writeAuthErr(w, err) {
		return
	}
	var unavailable *remote.UnavailableError
	var uncertain *execution.ActionUncertain
	var outcome *execution.OutcomeUncertain
	var notListed *workspaceNotListed
	var forbidden actionForbidden
	var self selfForbidden
	var answered *db.AlreadyAnsweredError
	var notSteerer *db.NotSteererError
	switch {
	case errors.As(err, &notSteerer):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": notSteerer.Msg, "code": "not_steerer"})
	case errors.As(err, &answered):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": answered.Error(), "code": "already_answered", "state": answered.State, "stateRevision": answered.StateRevision,
		})
	case errors.As(err, &outcome):
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": outcome.Error(), "code": "outcome_uncertain"})
	case errors.As(err, &notListed):
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": notListed.Error(), "code": "workspace_not_listed"})
	case errors.As(err, &forbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error(), "code": "action_forbidden"})
	case errors.As(err, &self):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": self.msg, "code": "self_action_forbidden"})
	case errors.As(err, &uncertain):
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": uncertain.Error(), "code": "action_outcome_unknown"})
	case errors.As(err, &unavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": unavailable.Message, "code": unavailable.Code})
	case errors.Is(err, execution.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "Harness unavailable")
	case errors.As(err, &invalid) && invalid.Code != "":
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": invalid.Msg, "code": invalid.Code})
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Msg)
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.As(err, &changed):
		writeJSON(w, http.StatusConflict, map[string]any{
			"code": "preview_changed", "error": changed.Error(), "preview": changed.Fresh,
		})
	case errors.As(err, &conflict) && conflict.Code != "":
		writeJSON(w, http.StatusConflict, map[string]string{"error": conflict.Msg, "code": conflict.Code})
	case errors.As(err, &conflict):
		writeError(w, http.StatusConflict, conflict.Msg)
	case errors.As(err, &harnessErr):
		writeError(w, http.StatusBadGateway, harnessErr.Error())
	case errors.As(err, &pushErr):
		writeError(w, http.StatusBadGateway, pushErr.Msg)
	default:
		s.log.Error("api error", "method", r.Method, "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
