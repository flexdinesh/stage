package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/db"
)

// Machines and worker auth (docs/api.md "Machines", docs/runtime.md
// "Worker routes"). /api/machines is ordinary API, behind the deployment's
// authenticating proxy. /api/worker/* is called by `stage worker` on a
// remote machine: the proxy lets it through, and the pairing code (join) or
// the machine token (every other route) is its auth.

// machineInfoJSON is a machine in /api/machines.
type machineInfoJSON struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
	// Kind is local (this server's machine, id 1) or remote (a worker).
	Kind string `json:"kind"`
	// Online: local always; remote while its worker channel is live.
	Online bool `json:"online"`
	// Status reflects the worker channel handshake and presence.
	Status       string     `json:"status"`
	StatusReason string     `json:"statusReason,omitempty"`
	LastSeenAt   *time.Time `json:"lastSeenAt"`
	// Version is the build the machine runs (null when unknown).
	Version     *string                `json:"version"`
	CreatedBy   int64                  `json:"createdBy"`
	CreatedAt   time.Time              `json:"createdAt"`
	Connections []db.MachineConnection `json:"connections"`
}

// Machine statuses.
const (
	machineOnline      = "online"
	machineOffline     = "offline"
	machineNegotiating = "negotiating"
	machineNeedsUpdate = "needs_update"
)

func (s *server) toMachineJSON(m db.MachineInfo) machineInfoJSON {
	out := machineInfoJSON{
		ID: m.ID, Label: m.Label, Kind: "remote", Status: machineOffline, LastSeenAt: m.LastSeenAt,
		CreatedBy: m.CreatedBy, CreatedAt: m.CreatedAt, Connections: m.Connections,
	}
	version := m.Version
	if m.ID == s.Engine.Connections.Machines().DefaultID() {
		out.Kind, out.Online, out.Status, version = "local", true, machineOnline, buildinfo.Version
	} else {
		p := s.Channels.Presence(m.ID)
		if !p.LastSeen.IsZero() && (out.LastSeenAt == nil || p.LastSeen.After(*out.LastSeenAt)) {
			t := p.LastSeen.UTC()
			out.LastSeenAt = &t
		}
		switch {
		case p.Online:
			out.Online, out.Status = true, machineOnline
			if p.Version != "" {
				version = p.Version
			}
		case p.NeedsUpdate != nil:
			out.Status = machineNeedsUpdate
			out.StatusReason = p.NeedsUpdate.Reason
			if !p.NeedsUpdate.ServerOlder {
				out.StatusReason = fmt.Sprintf("Update Stage on %s. Its worker channel protocol is older than the main machine's.", m.Label)
			}
			if p.NeedsUpdate.Version != "" {
				version = p.NeedsUpdate.Version
			}
		case p.Negotiating:
			out.Status, out.StatusReason = machineNegotiating, "The Stage worker is negotiating capabilities and preparing harness setup."
			if p.Version != "" {
				version = p.Version
			}
		}
	}
	if version != "" {
		out.Version = &version
	}
	return out
}

func (s *server) listMachines(w http.ResponseWriter, r *http.Request) error {
	ms, err := s.Store.ListMachines(r.Context())
	if err != nil {
		return err
	}
	out := make([]machineInfoJSON, len(ms))
	for i, m := range ms {
		out[i] = s.toMachineJSON(m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"machines": out})
	return nil
}

func (s *server) writeMachine(w http.ResponseWriter, r *http.Request, id int64) error {
	m, err := s.Store.GetMachineInfo(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, s.toMachineJSON(m))
	return nil
}

func (s *server) getMachine(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	return s.writeMachine(w, r, id)
}

func (s *server) renameMachine(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var body struct {
		Label string `json:"label"`
	}
	if err := decodeStrict(r, &body); err != nil {
		return err
	}
	if err := s.Store.RenameMachine(r.Context(), id, body.Label); err != nil {
		return err
	}
	return s.writeMachine(w, r, id)
}

func (s *server) removeMachine(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := s.Store.RemoveMachine(r.Context(), id); err != nil {
		return err
	}
	// Revocation is immediate: its open channel ends now.
	s.Channels.Close(id)
	if s.Machines != nil {
		s.Machines.Leave(id)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// pairingJSON is POST /api/machines/pairings. Code is shown only here.
type pairingJSON struct {
	// ID names the code in GET/DELETE /api/machines/pairings/{id}.
	ID        int64     `json:"id"`
	Code      string    `json:"code"`
	Label     string    `json:"label"`
	ExpiresAt time.Time `json:"expiresAt"`
	// Link is STAGE_PUBLIC_URL/join#<code>: the server and the code in one
	// value (the fragment never reaches a server or proxy log).
	Link string `json:"link"`
	// Command joins a machine as a worker with Link.
	Command string `json:"command"`
}

// PairingLink is the join link for code on the server at publicURL.
func PairingLink(publicURL, code string) string {
	return strings.TrimRight(publicURL, "/") + "/join#" + code
}

func (s *server) createPairing(w http.ResponseWriter, r *http.Request) error {
	public := strings.TrimRight(s.Config.PublicURL, "/")
	if public == "" {
		return &db.ConflictError{Code: "public_url_required", Msg: "Set STAGE_PUBLIC_URL to the address workers use to reach this server, then add the machine again."}
	}
	var body struct {
		Label string `json:"label"`
	}
	if err := decodeStrict(r, &body); err != nil {
		return err
	}
	p, err := s.Store.CreatePairing(r.Context(), body.Label)
	if err != nil {
		return err
	}
	link := PairingLink(public, p.Code)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, pairingJSON{ID: p.ID, Code: p.Code, Label: p.Label, ExpiresAt: p.ExpiresAt, Link: link, Command: "stage worker join " + link})
	return nil
}

// pairingStatusJSON is GET /api/machines/pairings/{id}: never the code.
type pairingStatusJSON struct {
	ID int64 `json:"id"`
	// State is waiting (redeemable), joined or expired.
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expiresAt"`
	// MachineID is the machine a joined code added (null otherwise).
	MachineID *int64 `json:"machineId"`
}

func (s *server) pairingStatus(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	p, err := s.Store.PairingStatus(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, pairingStatusJSON{ID: p.ID, State: p.State, ExpiresAt: p.ExpiresAt, MachineID: p.MachineID})
	return nil
}

func (s *server) revokePairing(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := s.Store.RevokePairing(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// joinJSON is POST /api/worker/join. Token is shown only here.
type joinJSON struct {
	MachineID int64  `json:"machineId"`
	Label     string `json:"label"`
	Token     string `json:"token"`
}

// joinWorker redeems a pairing code for a new machine and its token.
func (s *server) joinWorker(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Code    string `json:"code"`
		Label   string `json:"label"`
		Version string `json:"version"`
	}
	if err := decodeStrict(r, &body); err != nil {
		return err
	}
	g, err := s.Store.RedeemPairing(r.Context(), body.Code, body.Label, body.Version)
	if err != nil {
		return err
	}
	if s.Machines != nil {
		s.Machines.Join(g.MachineID)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, joinJSON{MachineID: g.MachineID, Label: g.Label, Token: g.Token})
	return nil
}

// workerMachine is GET /api/worker/machine: the machine the token names.
func (s *server) workerMachine(w http.ResponseWriter, r *http.Request) error {
	id, ok := workerMachineID(r.Context())
	if !ok {
		return db.ErrInvalidMachineToken
	}
	return s.writeMachine(w, r, id)
}

type workerMachineKey struct{}

// workerMachineID is the machine principal bearer auth put on ctx.
func workerMachineID(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(workerMachineKey{}).(int64)
	return id, ok
}

// WorkerPrefix is the path prefix of worker routes: the only routes a
// machine token is accepted on, and the only ones an authenticating proxy
// passes through without user auth.
const WorkerPrefix = "/api/worker/"

// bearer is the Authorization Bearer credential ("" when none).
func bearer(r *http.Request) string {
	scheme, cred, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(cred)
}

// RejectMachineTokens refuses a machine token on any route but worker
// routes (401). Other Authorization values (an authenticating proxy's own)
// pass untouched.
func RejectMachineTokens(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, WorkerPrefix) && db.IsMachineToken(bearer(r)) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "machine tokens are accepted only on " + WorkerPrefix + "*", "code": "machine_token_not_accepted"})
			return
		}
		h.ServeHTTP(w, r)
	})
}

// worker guards a worker route. Channel routes negotiate their own protocol.
// Other worker routes check the product API version. Authenticated routes
// require a machine token; join accepts only a pairing code.
func (s *server) worker(authed, checkAPI bool, fn func(http.ResponseWriter, *http.Request) error) http.Handler {
	want := strconv.Itoa(buildinfo.APIVersion)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(buildinfo.APIVersionHeader); checkAPI && got != want {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": fmt.Sprintf("API version mismatch: this server speaks %s, the worker sent %q; install the same Stage version on both", want, got),
				"code":  "api_version_mismatch",
			})
			return
		}
		ctx := r.Context()
		switch {
		case !authed && r.Header.Get("Authorization") != "":
			writeError(w, http.StatusBadRequest, "join takes only a pairing code; send no Authorization header")
			return
		case authed:
			tok := bearer(r)
			if tok == "" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="stage-worker"`)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "a machine token is required", "code": "machine_token_required"})
				return
			}
			id, err := s.Store.AuthenticateMachine(ctx, tok)
			if err != nil {
				s.writeErr(w, r, err)
				return
			}
			ctx = context.WithValue(ctx, workerMachineKey{}, id)
		}
		if err := fn(w, r.WithContext(ctx)); err != nil {
			s.writeErr(w, r, err)
		}
	})
}

// writeAuthErr maps credential errors to 401 (false when err is another
// error).
func writeAuthErr(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, db.ErrInvalidMachineToken):
		w.Header().Set("WWW-Authenticate", `Bearer realm="stage-worker", error="invalid_token"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error(), "code": "machine_token_invalid"})
	case errors.Is(err, db.ErrInvalidPairing):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error(), "code": "pairing_code_invalid"})
	default:
		return false
	}
	return true
}

// machineReachable reports whether the local host or a ready worker can answer.
func (s *server) machineReachable(machine int64) bool {
	m, ok := s.Engine.Connections.Machines().Get(machine)
	return ok && m.Online()
}
