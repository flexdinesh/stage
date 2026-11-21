// Package api is the REST API under /api (contract: docs/api.md).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/notify"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3setup"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// Deps are the API's collaborators.
type Deps struct {
	Config config.Config
	Store  *db.Store
	Sync   *t3sync.Service
	Push   *notify.Notifier
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
	// Setup is the T3 endpoint, read only for the legacy T3 link fields of
	// task and thread responses and T3 thread refs (nil: none).
	Setup *t3setup.Service
	// Connectors is the connection setup per connected harness kind.
	Connectors harness.Connectors
	// Harnesses is the server's fixed harness composition (nil: none).
	Harnesses *harness.Registry
	// Channels is the worker channel hub (nil: a private hub that
	// persists presence through Store).
	Channels *workerchan.Hub
	// MCP is the streamable HTTP MCP handler (no origin policy of its own)
	// that worker agent gateways reach through GatewayPrefix/mcp (nil: 404).
	MCP http.Handler
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
	mux *http.ServeMux
}

// NewRouter returns the /api handler.
func NewRouter(deps Deps) http.Handler {
	s := &server{Deps: deps, log: deps.Logger}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.Inbox == nil {
		s.Inbox = notify.NewHub(context.Background())
	}
	if s.Channels == nil {
		s.Channels = newChannelHub(context.Background(), s.Store)
	}
	mux := http.NewServeMux()
	s.mux = mux
	h := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if err := fn(w, r); err != nil {
				s.writeErr(w, r, err)
			}
		})
	}

	mux.HandleFunc("GET /api/health", s.health)
	h("GET /api/me", s.me)

	h("POST /api/sync", s.runSync)
	h("GET /api/catalog", s.catalog)

	h("GET /api/projects", s.listProjects)
	h("GET /api/shared-paths", s.sharedPaths)
	h("PUT /api/shared-paths", s.sharedPaths)
	h("POST /api/projects/{id}/bindings/{connectionId}/match", s.matchWorkspace)
	h("POST /api/projects", s.createProject)
	h("GET /api/projects/{id}", s.getProject)
	h("PATCH /api/projects/{id}", s.updateProject)
	h("PUT /api/projects/{id}/binding", s.bindProject)
	h("DELETE /api/projects/{id}/binding", s.unbindProject)
	h("DELETE /api/projects/{id}/bindings/{connectionId}", s.unbindProject)
	h("GET /api/harnesses", s.listHarnesses)
	h("GET /api/harnesses/{kind}/status", s.harnessStatus)
	h("PUT /api/harnesses/{kind}/settings", s.harnessSettings)
	h("POST /api/harnesses/{kind}/actions/{action}", s.harnessAction)
	h("DELETE /api/harnesses/{kind}/connection", s.harnessDisconnect)
	h("GET /api/machines", s.listMachines)
	h("POST /api/machines/pairings", s.createPairing)
	h("GET /api/machines/pairings/{id}", s.pairingStatus)
	h("DELETE /api/machines/pairings/{id}", s.revokePairing)
	h("GET /api/machines/{id}", s.getMachine)
	h("PATCH /api/machines/{id}", s.renameMachine)
	h("DELETE /api/machines/{id}", s.removeMachine)
	h("POST /api/machines/{id}/ping", s.pingMachine)
	mux.Handle("POST /api/worker/join", s.worker(false, s.joinWorker))
	mux.Handle("GET /api/worker/machine", s.worker(true, s.workerMachine))
	mux.Handle("GET /api/worker/channel", s.worker(true, s.workerChannelStream))
	mux.Handle("POST /api/worker/channel", s.worker(true, s.workerChannelPost))
	mux.Handle("POST /api/worker/leave", s.worker(true, s.workerLeave))
	mux.Handle(GatewayPrefix+"/", s.worker(true, s.workerGateway))
	mux.Handle(WorkerPrefix, s.worker(true, func(w http.ResponseWriter, _ *http.Request) error {
		writeError(w, http.StatusNotFound, "not found")
		return nil
	}))
	h("GET /api/connections", s.connections)
	h("GET /api/connections/{connectionId}/capabilities", s.executionCapabilities)
	h("PUT /api/connections/{connectionId}/auto-complete", s.setAutoComplete)
	h("DELETE /api/connections/{connectionId}", s.forgetConnection)
	h("POST /api/tasks/{id}/launch-link/preview", s.previewLaunchLink)
	h("POST /api/tasks/{id}/launch-link", s.attachLaunchLink)
	h("GET /api/connections/t3/projects", s.externalProjects)
	h("GET /api/connections/{connectionId}/projects", s.externalProjects)
	h("POST /api/connections/t3/projects", s.registerWorkspace)

	h("GET /api/tasks", s.listTasks)
	h("POST /api/tasks", s.createTask)
	h("GET /api/tasks/{id}", s.getTask)
	h("GET /api/tasks/{id}/completion-preview", s.completionPreview)
	h("POST /api/tasks/{id}/complete", s.completeTask)
	h("POST /api/tasks/{id}/reopen", s.reopenTask)
	h("GET /api/tasks/{id}/opening-prompt", s.getOpeningPrompt)
	h("PATCH /api/tasks/{id}", s.updateTask)
	h("DELETE /api/tasks/{id}", s.deleteTask)
	h("POST /api/tasks/{id}/start", s.startTask)
	h("POST /api/tasks/{id}/stop", s.stopTask)
	h("GET /api/tasks/{id}/requests", s.taskRequests)
	h("POST /api/tasks/{id}/requests/{rid}", s.respondRequest)
	h("POST /api/tasks/{id}/messages", s.sendMessage)
	h("POST /api/tasks/{id}/duplicate", s.duplicateTask)
	h("POST /api/tasks/{id}/archive", s.archiveTask)
	h("POST /api/tasks/{id}/unarchive", s.unarchiveTask)
	h("PUT /api/tasks/{id}/needs", s.setNeeds)
	h("GET /api/tasks/{id}/journal", s.taskJournal)
	h("POST /api/tasks/{id}/journal", s.taskJournal)

	h("GET /api/trees", s.listTrees)
	h("POST /api/trees", s.createTree)
	h("GET /api/trees/{id}", s.getTree)
	h("PATCH /api/trees/{id}", s.updateTree)
	h("DELETE /api/trees/{id}", s.deleteTree)
	h("POST /api/trees/{id}/dissolve", s.dissolveTree)
	h("POST /api/trees/{id}/tasks", s.addToTree)
	h("DELETE /api/trees/{id}/tasks/{taskId}", s.removeFromTree)
	h("POST /api/trees/{id}/start", s.startTree)
	h("GET /api/trees/{id}/journal", s.treeJournal)
	h("POST /api/trees/{id}/journal", s.treeJournal)
	h("GET /api/journal/{id}", s.getJournalEntry)

	h("GET /api/schedules", s.listSchedules)
	h("POST /api/schedules", s.createSchedule)
	h("POST /api/schedules/preview", s.previewSchedule)
	h("GET /api/schedules/{id}", s.getSchedule)
	h("PATCH /api/schedules/{id}", s.updateSchedule)
	h("DELETE /api/schedules/{id}", s.deleteSchedule)
	h("POST /api/schedules/{id}/run", s.runSchedule)

	h("POST /api/uploads", s.uploadImage)
	h("GET /api/attachments/{id}", s.getAttachment)
	h("DELETE /api/attachments/{id}", s.deleteAttachment)

	h("GET /api/connections/{connectionId}/sessions/{id}/context", s.sessionContext)
	h("GET /api/connections/{connectionId}/sessions/{id}", s.getThread)
	h("POST /api/connections/{connectionId}/sessions/{id}/adopt", s.adoptThread)

	h("GET /api/snippets", s.listSnippets)
	h("POST /api/snippets", s.createSnippet)
	h("GET /api/snippets/{id}", s.getSnippet)
	h("PATCH /api/snippets/{id}", s.updateSnippet)
	h("DELETE /api/snippets/{id}", s.deleteSnippet)

	h("GET /api/refs/resolve", s.resolveRef)

	h("GET /api/preferences", s.getPreferences)
	h("PUT /api/preferences/pinned", s.putPinned)
	h("PUT /api/preferences/notifications", s.putNotifyEvents)

	h("GET /api/push/config", s.pushConfig)
	h("POST /api/push/subscriptions", s.subscribePush)
	h("DELETE /api/push/subscriptions", s.unsubscribePush)
	h("POST /api/push/test", s.testPush)
	h("GET /api/badge", s.badge)
	h("GET /api/notifications", s.listNotifications)
	h("GET /api/notifications/groups", s.listNotificationGroups)
	h("POST /api/notifications/read", s.readNotifications)
	h("GET /api/notifications/unread-tags", s.unreadNotificationTags)
	h("GET /api/notifications/stream", s.streamNotifications)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
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
	// PublicURL is STAGE_PUBLIC_URL, null when unset: the address copied
	// Stage links use on hosts whose own origin is not shareable (desktop).
	PublicURL *string `json:"publicUrl"`
	// Harnesses is each connector's memory-only summary, in registry order.
	Harnesses []HealthHarness `json:"harnesses"`
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
		InstanceID: s.Instance.InstanceID,
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
	s.Connectors.Each(func(machine int64, d harness.Descriptor, c harness.Connector) {
		if machine != db.LocalMachine {
			return
		}
		sum := c.Summary(r.Context())
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

// writeErr maps app errors to contract statuses.
func (s *server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var (
		invalid  *db.InvalidError
		conflict *db.ConflictError
		t3Err    *db.HarnessError
		pushErr  *notify.PushError
	)
	if writeAuthErr(w, err) {
		return
	}
	switch {
	case errors.Is(err, execution.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "Harness unavailable")
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Msg)
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.As(err, &conflict) && conflict.Code != "":
		writeJSON(w, http.StatusConflict, map[string]string{"error": conflict.Msg, "code": conflict.Code})
	case errors.As(err, &conflict):
		writeError(w, http.StatusConflict, conflict.Msg)
	case errors.As(err, &t3Err):
		writeError(w, http.StatusBadGateway, t3Err.Error())
	case errors.As(err, &pushErr):
		writeError(w, http.StatusBadGateway, pushErr.Msg)
	default:
		s.log.Error("api error", "method", r.Method, "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
