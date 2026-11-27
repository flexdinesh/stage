package client

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/principal"
)

func TestBoundCredentialUsesVerifiedInstallationAndRejectsRedirect(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	defer other.Close()
	token := principal.AgentTokenPrefix + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, tc := range []struct {
		name, status, changeTarget string
		closeProbe, redirect, h2   bool
		want                       string
		wantRequests               int32
	}{
		{name: "verified", status: "original", wantRequests: 1},
		{name: "verified over HTTP/2", status: "original", h2: true, wantRequests: 1},
		{name: "replacement installation", status: "replacement", want: "installation changed"},
		{name: "replacement address", status: "original", changeTarget: other.URL, want: "different address"},
		{name: "closed probe connection", status: "original", closeProbe: true, want: "closed the verified connection"},
		{name: "cross-origin redirect", status: "original", redirect: true, want: "redirected the request", wantRequests: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.h2 && r.ProtoMajor != 2 {
					t.Error("TLS connection did not negotiate HTTP/2")
				}
				if r.URL.Path == "/api/auth/status" {
					if r.Header.Get("Authorization") != "" {
						t.Error("sent credential before verifying installation")
					}
					if tc.closeProbe {
						w.Header().Set("Connection", "close")
					}
					fmt.Fprintf(w, `{"mode":"claimed","installationId":%q}`, tc.status)
					return
				}
				requests.Add(1)
				if r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("verified request lacks the agent credential")
				}
				if tc.redirect {
					http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
					return
				}
				fmt.Fprint(w, `{"projects":[]}`)
			}))
			s.EnableHTTP2 = tc.h2
			s.StartTLS()
			defer s.Close()
			cl := New(s.URL)
			cl.HTTP = s.Client()
			target := s.URL
			if tc.changeTarget != "" {
				target = tc.changeTarget
			}
			cl = cl.WithCredential(func() (Credential, error) {
				return Credential{target, "original", store.SessionAgent, time.Now().Add(time.Hour), token}, nil
			})
			_, err := cl.Projects(t.Context())
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("projects error %v, expected %q", err, tc.want)
			}
			if requests.Load() != tc.wantRequests || redirected.Load() != 0 {
				t.Fatalf("authenticated requests %d, redirects %d", requests.Load(), redirected.Load())
			}
		})
	}
}

func TestLoginNeverForwardsPasswordThroughRedirect(t *testing.T) {
	var redirected atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Store(true)
	}))
	defer other.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/status" {
			fmt.Fprint(w, `{"mode":"claimed","installationId":"original"}`)
			return
		}
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	_, err := New(s.URL).Login(t.Context(), "original", "example@example.test", "test password")
	if !IsStatus(err, http.StatusTemporaryRedirect) || redirected.Load() {
		t.Fatalf("login error %v, redirected %v", err, redirected.Load())
	}
}
