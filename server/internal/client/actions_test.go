package client_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
)

func TestActionTransportPreservesInputAndErrorCode(t *testing.T) {
	const input = `{"text":"<raw>", "replace":true}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.RequestURI() != "/api/projects/4/actions/pin.workspace?connectionId=7" {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Stage-Caller-Session") != "2:session:1" ||
			r.Header.Get("Stage-Action-Agent") != "1" || r.Header.Get("Stage-Action-Confirmed") != "1" {
			t.Errorf("headers %v", r.Header)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != input {
			t.Errorf("body %q, %v", body, err)
		}
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"error":"Check the harness before retrying.","code":"action_uncertain"}`)
	}))
	defer srv.Close()

	project, onConnection := int64(4), int64(7)
	_, err := client.New(srv.URL).RunAction(t.Context(), client.ActionScope{ProjectID: &project, OnConnectionID: &onConnection},
		"pin.workspace", json.RawMessage(input), client.ActionCaller{
			Session: &db.SessionRef{ConnectionID: 2, ExternalSessionID: "session:1"}, Confirmed: true,
		})
	var apiErr *client.Error
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict ||
		apiErr.Code != "action_uncertain" || apiErr.Msg != "Check the harness before retrying." {
		t.Fatalf("error %#v (%v)", apiErr, err)
	}
}
