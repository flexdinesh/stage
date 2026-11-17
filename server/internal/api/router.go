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
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/notify"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/t3setup"
)

// Deps are the API's collaborators.
type Deps struct {
	Config config.Config
	Store  *db.Store
	T3     t3.Client
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
	// Setup is the T3 URL, binding and pairing (nil = one built from
	// Config, Store and T3 with the CLI runner disabled for the fake).
	Setup *t3setup.Service
}

type server struct {
	Deps
	log *slog.Logger
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
	if s.Setup == nil {
		s.Setup = t3setup.New(t3setup.Options{
			Config: s.Config, Store: s.Store, Client: s.T3, Fake: t3setup.IsFake(s.T3), Logger: s.log,
		})
	}
	mux := http.NewServeMux()
	h := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if err := fn(w, r); err != nil {
				s.writeErr(w, r, err)
			}
		})
	}

	mux.HandleFunc("GET /api/health", s.health)

	h("GET /api/t3/status", s.t3Status)
	h("POST /api/t3/pair", s.t3Pair)
	h("POST /api/t3/pair/auto", s.t3PairAuto)
	h("PUT /api/t3/url", s.t3SetURL)
	h("POST /api/sync", s.runSync)
	h("GET /api/catalog", s.catalog)

	h("GET /api/projects", s.listProjects)
	h("POST /api/projects", s.createProject)
	h("GET /api/projects/{id}", s.getProject)

	h("GET /api/tasks", s.listTasks)
	h("POST /api/tasks", s.createTask)
	h("GET /api/tasks/{id}", s.getTask)
	h("GET /api/tasks/{id}/opening-prompt", s.getOpeningPrompt)
	h("PATCH /api/tasks/{id}", s.updateTask)
	h("DELETE /api/tasks/{id}", s.deleteTask)
	h("POST /api/tasks/{id}/start", s.startTask)
	h("POST /api/tasks/{id}/stop", s.stopTask)
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

	h("GET /api/threads/{id}", s.getThread)
	h("POST /api/threads/{id}/adopt", s.adoptThread)

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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(buildinfo.APIVersionHeader, apiVersion)
		mux.ServeHTTP(w, r)
	})
}

// treeChanged notifies OnTreeChange.
func (s *server) treeChanged() {
	if s.OnTreeChange != nil {
		s.OnTreeChange()
	}
}

// Health is GET /api/health: whether this is a live Stage server with a
// working database, which build it is, and (separately) whether T3 is
// connected. T3 being offline does not make the server unhealthy.
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
	PublicURL *string  `json:"publicUrl"`
	T3        HealthT3 `json:"t3"`
}

// HealthT3 is the T3 connection state in Health.
type HealthT3 struct {
	Connected bool    `json:"connected"`
	LastError *string `json:"lastError"`
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
	if s.T3 != nil {
		st := s.T3.Status(r.Context())
		h.T3.Connected = st.Connected
		if st.LastError != "" {
			h.T3.LastError = &st.LastError
		}
	}
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
		t3Err    *db.T3Error
		pushErr  *notify.PushError
	)
	switch {
	case errors.Is(err, t3.ErrNotConnected):
		writeError(w, http.StatusServiceUnavailable, "T3 not connected")
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
