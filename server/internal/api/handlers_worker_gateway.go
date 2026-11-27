package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/principal"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// GatewayPrefix is the worker route a worker's agent gateway forwards its
// local /api and /mcp requests to: <GatewayPrefix>/api/... and
// <GatewayPrefix>/mcp (docs/runtime.md "Agent gateway").
const GatewayPrefix = workerchan.GatewayPath

// gatewayForbidden are the routes a machine token never reaches through
// the gateway: machine management (a token that could mint pairing codes
// could outlive its own revocation) and changes to a machine's harness
// setup (pairing and credentials belong to the user), and human workspace
// inventory. Reading setup status and destination summaries stays allowed.
var gatewayForbidden = func() *http.ServeMux {
	m := http.NewServeMux()
	for _, p := range []string{
		"POST /api/machines/pairings",
		"DELETE /api/machines/pairings/{id}",
		"PATCH /api/machines/{id}",
		"DELETE /api/machines/{id}",
		"POST /api/machines/{id}/ping",
		"PUT /api/machines/{id}/harnesses/{kind}/settings",
		"POST /api/machines/{id}/harnesses/{kind}/actions/{action}",
		"DELETE /api/machines/{id}/harnesses/{kind}/connection",
		"PUT /api/harnesses/{kind}/settings",
		"POST /api/harnesses/{kind}/actions/{action}",
		"DELETE /api/harnesses/{kind}/connection",
		"GET /api/connections/{connectionId}/workspaces",
		"POST /api/connections/{connectionId}/workspaces/decline",
		"POST /api/connections/{connectionId}/workspaces/undecline",
		"POST /api/connections/{connectionId}/resolve",
		"GET /api/workspaces/review",
		"POST /api/sync",
	} {
		m.HandleFunc(p, func(http.ResponseWriter, *http.Request) {})
	}
	return m
}()

// workerGateway is <GatewayPrefix>/{rest...}: serves a request a worker's
// agent gateway forwarded, as the same request on this server's /api or
// /mcp, with the token's machine as the forwarding machine on ctx
// (db.WithMachine: writes record it as source_machine_id). The worker
// route already checked the API version and the token; the origin policy
// ran on the outer request.
func (s *server) workerGateway(w http.ResponseWriter, r *http.Request) error {
	id, ok := workerMachineID(r.Context())
	if !ok {
		return db.ErrInvalidMachineToken
	}
	ctx := db.WithMachine(r.Context(), id)
	ctx = principal.With(ctx, principal.Actor{Kind: principal.Agent, MachineID: id})
	if s.Access != nil {
		token := bearer(r)
		proof := r.Header.Values(workerchan.PrivateGatewayHeader)
		if len(proof) != 1 || !workerchan.ValidPrivateGatewayProof(token, proof[0]) {
			accessError(w, http.StatusForbidden, "private_gateway_required", "Update the worker and connect through its private Stage socket.")
			return nil
		}
		ctx = withGatewayCheck(ctx, func(ctx context.Context) error {
			_, err := s.Store.AuthenticateMachine(ctx, token)
			return err
		})
	}
	rest := strings.TrimPrefix(r.URL.Path, GatewayPrefix)
	inner := r.Clone(ctx)
	inner.Header.Del("Authorization")
	inner.Header.Del(workerchan.PrivateGatewayHeader)
	inner.URL.Path = rest
	if inner.URL.RawPath != "" {
		if raw, ok := strings.CutPrefix(inner.URL.RawPath, GatewayPrefix); ok {
			inner.URL.RawPath = raw
		} else {
			writeError(w, http.StatusNotFound, "not found")
			return nil
		}
	}
	inner.RequestURI = inner.URL.RequestURI()
	switch {
	case rest == "/mcp" && s.MCP != nil:
		// The MCP SDK refuses a non-loopback Host on a loopback listener
		// (DNS rebinding); this request already passed the origin policy
		// and bearer auth, and may arrive through a local reverse proxy.
		inner = inner.WithContext(context.WithValue(inner.Context(), http.LocalAddrContextKey, nil))
		s.MCP.ServeHTTP(w, inner)
	case strings.HasPrefix(rest, WorkerPrefix) || !strings.HasPrefix(rest, "/api/"):
		writeError(w, http.StatusNotFound, "not found")
	default:
		if _, pattern := gatewayForbidden.Handler(inner); pattern != "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "This operation requires the Stage UI or a human CLI session on the main server.", "code": "gateway_forbidden"})
			return nil
		}
		s.mux.ServeHTTP(w, inner)
	}
	return nil
}
