package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// GatewayPrefix is the worker route a worker's agent gateway forwards its
// local /api and /mcp requests to: <GatewayPrefix>/api/... and
// <GatewayPrefix>/mcp (docs/runtime.md "Agent gateway").
const GatewayPrefix = workerchan.GatewayPath

// gatewayForbidden are the routes a machine token never reaches through
// the gateway: machine management. A token that could mint pairing codes
// could outlive its own revocation.
var gatewayForbidden = func() *http.ServeMux {
	m := http.NewServeMux()
	for _, p := range []string{
		"POST /api/machines/pairings",
		"DELETE /api/machines/pairings/{id}",
		"PATCH /api/machines/{id}",
		"DELETE /api/machines/{id}",
		"POST /api/machines/{id}/ping",
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
	rest := strings.TrimPrefix(r.URL.Path, GatewayPrefix)
	inner := r.Clone(db.WithMachine(r.Context(), id))
	inner.Header.Del("Authorization")
	inner.URL.Path, inner.URL.RawPath = rest, ""
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
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "machine management is not available through a worker's agent gateway; use the Stage UI or a CLI on the main server", "code": "gateway_forbidden"})
			return nil
		}
		s.mux.ServeHTTP(w, inner)
	}
	return nil
}
