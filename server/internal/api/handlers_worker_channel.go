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

// workerChannelStream is GET /api/worker/channel: hello, then one rpc
// event per request or cancel, and a comment every keepalive. A newer
// stream of the same machine ends this one.
func (s *server) workerChannelStream(w http.ResponseWriter, r *http.Request) error {
	id, ok := workerMachineID(r.Context())
	if !ok {
		return db.ErrInvalidMachineToken
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return nil
	}
	sess := s.Channels.Attach(id, r.Header.Get(workerchan.VersionHeader))
	defer s.Channels.Detach(sess)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	h.Set(workerchan.VersionHeader, buildinfo.Version)
	w.WriteHeader(http.StatusOK)
	hello := workerchan.Hello{Session: sess.ID, MachineID: id,
		HeartbeatSec: int(s.Channels.Keepalive() / time.Second), TimeoutSec: int(s.Channels.Timeout() / time.Second)}
	if err := workerchan.WriteEvent(w, workerchan.EventHello, hello); err != nil {
		return nil
	}
	flusher.Flush()
	s.log.Info("worker channel open", "machine", id, "session", sess.ID, "version", sess.Version)
	defer s.log.Info("worker channel closed", "machine", id, "session", sess.ID)
	t := time.NewTicker(s.Channels.Keepalive())
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-sess.Done():
			return nil
		case env := <-sess.Outbound():
			if err := workerchan.WriteEvent(w, workerchan.EventRPC, env); err != nil {
				return nil
			}
		case <-t.C:
			if err := workerchan.WriteComment(w, "keepalive"); err != nil {
				return nil
			}
		}
		flusher.Flush()
	}
}

// workerChannelPost is POST /api/worker/channel: one up message of the
// session named by Stage-Channel-Session. 204 for a result or heartbeat;
// 200 with the reply envelope for a worker-initiated call; 409
// channel_replaced when the session is not the machine's live one.
func (s *server) workerChannelPost(w http.ResponseWriter, r *http.Request) error {
	id, ok := workerMachineID(r.Context())
	if !ok {
		return db.ErrInvalidMachineToken
	}
	session := r.Header.Get(workerchan.SessionHeader)
	if session == "" {
		return db.Invalid("missing %s header", workerchan.SessionHeader)
	}
	var env workerchan.Envelope
	if err := decodeStrict(r, &env); err != nil {
		return err
	}
	if env.Kind == "" {
		return db.Invalid("kind is required")
	}
	reply, err := s.Channels.Deliver(id, session, env)
	if errors.Is(err, workerchan.ErrReplaced) {
		return &db.ConflictError{Code: "channel_replaced", Msg: "this channel session was replaced or ended; reconnect the stream"}
	}
	if err != nil {
		return err
	}
	if reply == nil {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	writeJSON(w, http.StatusOK, reply)
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
