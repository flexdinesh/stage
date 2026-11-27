package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOwnerCommandsUseOnlyLocalControl(t *testing.T) {
	var requests atomic.Int32
	product := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer product.Close()
	short, environ := env(t)
	environ = append(environ, "STAGE_URL="+product.URL, "STAGE_DATA_DIR="+filepath.Join(short, "data"))
	for _, args := range [][]string{{"owner", "reissue-setup"}, {"owner", "recover-admin", "--email", "owner@example.test"}} {
		code, stderr := runMain(t, environ, args...)
		if code != 1 || !strings.Contains(stderr, "Stage installation owner") || strings.Contains(stderr, "owner@example.test") {
			t.Fatalf("owner command exit %d, stderr %q", code, stderr)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("owner commands used a product endpoint")
	}
}
