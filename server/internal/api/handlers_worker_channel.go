package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// The worker channel (docs/api.md "Worker channel", docs/runtime.md
// "Worker channel"): GET streams calls down to a machine's worker (SSE),
// POST carries its results, heartbeats and its own calls up.

// newChannelHub is the router's own hub when Deps.Channels is nil (tests):
// presence is persisted through the store.
func newChannelHub(ctx context.Context, store *db.Store) *workerchan.Hub {
	return workerchan.NewHub(ctx, workerchan.Options{Version: buildinfo.Version, Seen: TouchMachineFunc(store, nil)})
}

// TouchMachineFunc persists channel presence (workerchan.Options.Seen).
func TouchMachineFunc(store *db.Store, onErr func(error)) func(int64, time.Time, string) {
	return func(id int64, at time.Time, version string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.TouchMachine(ctx, id, at, version); err != nil && onErr != nil {
			onErr(err)
		}
	}
}

// workerChannelStream is GET /api/worker/channel (workerchan.Hub.ServeStream).
func (s *server) workerChannelStream(w http.ResponseWriter, r *http.Request) error {
	id, ok := workerMachineID(r.Context())
	if !ok {
		return db.ErrInvalidMachineToken
	}
	s.Channels.ServeStream(w, r, id, buildinfo.Version, s.log)
	return nil
}

// workerChannelPost is POST /api/worker/channel (workerchan.Hub.ServePost).
func (s *server) workerChannelPost(w http.ResponseWriter, r *http.Request) error {
	id, ok := workerMachineID(r.Context())
	if !ok {
		return db.ErrInvalidMachineToken
	}
	s.Channels.ServePost(w, r, id)
	return nil
}

// workerLeave is POST /api/worker/leave: the token's machine removes
// itself (same rules as DELETE /api/machines/{id}). 204.
func (s *server) workerLeave(w http.ResponseWriter, r *http.Request) error {
	id, ok := workerMachineID(r.Context())
	if !ok {
		return db.ErrInvalidMachineToken
	}
	if err := s.Store.RemoveMachine(r.Context(), id); err != nil {
		return err
	}
	s.Channels.Close(id)
	if s.Machines != nil {
		s.Machines.Leave(id)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// pingJSON is POST /api/machines/{id}/ping.
type pingJSON struct {
	MachineID int64 `json:"machineId"`
	// LatencyMs is the round trip through the worker channel (0 for the
	// local machine).
	LatencyMs int64  `json:"latencyMs"`
	Version   string `json:"version"`
}

// pingTimeout bounds a ping round trip.
const pingTimeout = 10 * time.Second

// pingMachine is POST /api/machines/{id}/ping: a round trip through the
// machine's worker channel. 409 machine_offline without a live channel;
// 504 lost_reply when no reply came in time.
func (s *server) pingMachine(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if _, err := s.Store.GetMachineInfo(r.Context(), id); err != nil {
		return err
	}
	if id == db.LocalMachine {
		writeJSON(w, http.StatusOK, pingJSON{MachineID: id, Version: buildinfo.Version})
		return nil
	}
	start := time.Now()
	raw, err := s.Channels.Call(r.Context(), id, workerchan.KindPing, 0, nil, pingTimeout)
	var re *workerchan.RemoteError
	switch {
	case errors.Is(err, workerchan.ErrOffline):
		return &db.ConflictError{Code: "machine_offline", Msg: "The machine is offline: its worker is not connected."}
	case errors.Is(err, workerchan.ErrLostReply):
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": err.Error(), "code": "lost_reply"})
		return nil
	case errors.As(err, &re):
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": re.Error(), "code": re.Code})
		return nil
	case err != nil:
		return err
	}
	var pong workerchan.Pong
	if err := json.Unmarshal(raw, &pong); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "malformed ping reply", "code": "bad_reply"})
		return nil
	}
	writeJSON(w, http.StatusOK, pingJSON{MachineID: id, LatencyMs: time.Since(start).Milliseconds(), Version: pong.Version})
	return nil
}
