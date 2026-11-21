package workerchan

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// MaxPostBytes bounds one up message (the API's upload cap): results and
// pushed observations can be large, a launch image travels down in a
// callback reply instead.
const MaxPostBytes = 32 << 20

// ServeStream is GET /api/worker/channel for an authenticated machine:
// hello, then one rpc event per request or cancel, and a comment every
// keepalive. A newer stream of the same machine ends this one. The caller
// authenticates the worker; the request carries its build (VersionHeader)
// and agent gateway URLs (GatewayHeader); serverVersion is this server's.
func (h *Hub) ServeStream(w http.ResponseWriter, r *http.Request, machineID int64, serverVersion string, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "", "streaming unsupported")
		return
	}
	sess := h.AttachGateway(machineID, r.Header.Get(VersionHeader), ParseGateway(r.Header.Get(GatewayHeader)))
	defer h.Detach(sess)
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Accel-Buffering", "no")
	hd.Set(VersionHeader, serverVersion)
	w.WriteHeader(http.StatusOK)
	hello := Hello{Session: sess.ID, MachineID: machineID,
		HeartbeatSec: int(h.Keepalive() / time.Second), TimeoutSec: int(h.Timeout() / time.Second)}
	if err := WriteEvent(w, EventHello, hello); err != nil {
		return
	}
	flusher.Flush()
	log.Info("worker channel open", "machine", machineID, "session", sess.ID, "version", sess.Version)
	defer log.Info("worker channel closed", "machine", machineID, "session", sess.ID)
	t := time.NewTicker(h.Keepalive())
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sess.Done():
			return
		case env := <-sess.Outbound():
			if err := WriteEvent(w, EventRPC, env); err != nil {
				return
			}
		case <-t.C:
			if err := WriteComment(w, "keepalive"); err != nil {
				return
			}
		}
		flusher.Flush()
	}
}

// ServePost is POST /api/worker/channel for an authenticated machine: one
// up message of the session named by SessionHeader. 204 for a result or
// heartbeat; 200 with the reply envelope for a worker-initiated call; 400
// for a malformed message; 409 channel_replaced when the session is not
// the machine's live one. Errors use the API's {error, code} shape.
func (h *Hub) ServePost(w http.ResponseWriter, r *http.Request, machineID int64) {
	session := r.Header.Get(SessionHeader)
	if session == "" {
		writeError(w, http.StatusBadRequest, "", "missing "+SessionHeader+" header")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, MaxPostBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "", "read body: "+err.Error())
		return
	}
	if len(raw) > MaxPostBytes {
		writeError(w, http.StatusBadRequest, "", "body exceeds the channel message limit")
		return
	}
	var env Envelope
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid JSON body: "+err.Error())
		return
	}
	if err := d.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "", "body must contain one JSON object")
		return
	}
	if env.Kind == "" {
		writeError(w, http.StatusBadRequest, "", "kind is required")
		return
	}
	reply, err := h.Deliver(r.Context(), machineID, session, env)
	if errors.Is(err, ErrReplaced) {
		writeError(w, http.StatusConflict, "channel_replaced", "this channel session was replaced or ended; reconnect the stream")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "", "internal error")
		return
	}
	if reply == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(reply)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	body := map[string]string{"error": msg}
	if code != "" {
		body["code"] = code
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
