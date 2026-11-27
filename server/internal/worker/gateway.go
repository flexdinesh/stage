package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/guard"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// strippedHeaders never leave the machine: browser context the local
// origin policy already judged, caller credentials and gateway attestation.
var strippedHeaders = []string{"Origin", "Referer", "Cookie", "Authorization", workerchan.PrivateGatewayHeader}

type privateGatewayKey struct{}

// NewGateway is the agent gateway handler (docs/runtime.md "Agent
// gateway"): the request-origin policy of a Stage listener (guard.Policy,
// no extra trusted origins), then /api/ and /mcp forwarded to the main
// server's gateway route (workerchan.GatewayPath) with the machine token.
// The caller's Stage-Caller-Session and body pass unchanged; browser
// headers (Origin, Sec-Fetch-*, Cookie, Referer) and the caller's own
// Authorization are dropped. Only requests from the private default socket
// get the worker's private gateway header. Streams (SSE, streamable MCP)
// flush as they arrive. Other paths are 404.
func NewGateway(cfg Config, transport http.RoundTripper, log *slog.Logger) (http.Handler, error) {
	if log == nil {
		log = slog.Default()
	}
	target, err := url.Parse(cfg.URL)
	if err != nil || target.Host == "" {
		return nil, fmt.Errorf("worker config url %q: invalid", cfg.URL)
	}
	policy, err := guard.New(nil, log)
	if err != nil {
		return nil, err
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	base := strings.TrimRight(target.Path, "/")
	escapedBase := strings.TrimRight(target.EscapedPath(), "/")
	apiVersion := strconv.Itoa(buildinfo.APIVersion)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			out := pr.Out
			out.URL.Scheme, out.URL.Host = target.Scheme, target.Host
			out.URL.Path = base + workerchan.GatewayPath + pr.In.URL.Path
			out.URL.RawPath = escapedBase + workerchan.GatewayPath + pr.In.URL.EscapedPath()
			// The main server's Host (its origin policy allows it), never the
			// local one ("stage" on the socket).
			out.Host = ""
			for _, h := range strippedHeaders {
				out.Header.Del(h)
			}
			for k := range out.Header {
				if strings.HasPrefix(k, "Sec-Fetch-") {
					delete(out.Header, k)
				}
			}
			out.Header.Set("Authorization", "Bearer "+cfg.Token)
			out.Header.Set(buildinfo.APIVersionHeader, apiVersion)
			out.Header.Set(workerchan.VersionHeader, buildinfo.Version)
			if private, _ := pr.In.Context().Value(privateGatewayKey{}).(bool); private {
				out.Header.Set(workerchan.PrivateGatewayHeader, workerchan.PrivateGatewayProof(cfg.Token))
			}
		},
		Transport:     transport,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
				return // the client went away
			}
			log.Warn("agent gateway", "method", r.Method, "path", r.URL.Path, "err", err)
			writeGatewayError(w, http.StatusBadGateway, "gateway_unreachable", "stage worker gateway: the main server at "+cfg.URL+" is unreachable: "+err.Error())
		},
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", policy.Wrap(proxy))
	mux.Handle("/mcp", policy.Wrap(proxy))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeGatewayError(w, http.StatusNotFound, "", "not found: the Stage worker gateway serves /api and /mcp")
	})
	return mux, nil
}

func writeGatewayError(w http.ResponseWriter, status int, code, msg string) {
	body := map[string]string{"error": msg}
	if code != "" {
		body["code"] = code
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// gateway is the running agent gateway: its listeners and server.
type gateway struct {
	srv    *http.Server
	lns    []*listen.Listener
	cancel context.CancelFunc
	// URLs are the listeners' client URLs (listen.Addr.URL).
	URLs []string
}

// openGateway resolves addrs (unix: the default socket of the data dir
// getenv selects, the one client resolution checks), opens every listener
// and serves h on them. A held socket is a *listen.InUseError.
func openGateway(addrs []listen.Addr, getenv func(string) string, h http.Handler, log *slog.Logger) (*gateway, error) {
	resolved := make([]listen.Addr, len(addrs))
	copy(resolved, addrs)
	for i, a := range resolved {
		if a.Network != "unix" || a.Address != "" {
			continue
		}
		dir, err := config.DataDirFrom(getenv)
		if err != nil {
			return nil, config.Invalid(err)
		}
		p, err := config.DefaultSocket(getenv, dir)
		if err != nil {
			return nil, config.Invalid(err)
		}
		resolved[i].Address = p
	}
	lns, err := listen.OpenAll(resolved)
	if err != nil {
		return nil, fmt.Errorf("agent gateway: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	g := &gateway{
		srv: &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second,
			BaseContext: func(ln net.Listener) context.Context {
				// Listen verifies the private directory and sets the socket mode.
				if l, ok := ln.(*listen.Listener); ok && l.Spec.Network == "unix" && l.Spec.Private {
					return context.WithValue(ctx, privateGatewayKey{}, true)
				}
				return ctx
			}},
		lns: lns, cancel: cancel,
	}
	for _, l := range lns {
		g.URLs = append(g.URLs, l.Spec.URL())
		go func() {
			if err := g.srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("agent gateway", "addr", l.Spec.String(), "err", err)
			}
		}()
		log.Info("agent gateway listening", "addr", l.Spec.String())
	}
	return g, nil
}

// close ends open streams, drains briefly, then removes the sockets and
// releases their locks.
func (g *gateway) close() {
	g.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := g.srv.Shutdown(ctx); err != nil {
		_ = g.srv.Close()
	}
	for _, l := range g.lns {
		_ = l.Close()
	}
}
