package client_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
)

func TestWorkspaceClientRoutes(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readBody := func() map[string]json.RawMessage {
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode %s: %v", r.URL.Path, err)
			}
			return body
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/connections/7/workspaces":
			io.WriteString(w, `{"workspaces":[{"connectionId":7,"externalId":"pathless","workspace":"","available":true}]}`)
		case "PUT /api/projects/1/binding":
			in := readBody()
			if string(in["connectionId"]) != "7" || string(in["externalProjectId"]) != `"pathless"` ||
				string(in["workspace"]) != `""` || string(in["primary"]) != "true" || string(in["replaces"]) != "20" {
				t.Errorf("bind body: %s", in)
			}
			io.WriteString(w, `{"id":1,"bindings":[{"id":21,"primary":true}]}`)
		case "DELETE /api/projects/1/bindings/21":
			io.WriteString(w, `{"id":1,"bindings":[]}`)
		case "POST /api/connections/7/resolve":
			in := readBody()
			switch string(in["apply"]) {
			case "false":
				io.WriteString(w, `{"previewToken":"plan"}`)
			case "true":
				if string(in["previewToken"]) != `"plan"` {
					t.Errorf("apply body: %s", in)
				}
				w.WriteHeader(http.StatusConflict)
				io.WriteString(w, `{"error":"The preview changed.","code":"preview_changed","preview":{"previewToken":"changed"}}`)
			default:
				t.Errorf("resolve body: %s", in)
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	c := client.NewInProcess(handler, "https://stage.test")
	ctx := t.Context()
	ws, err := c.Workspaces(ctx, 7)
	if err != nil || len(ws) != 1 || ws[0].ExternalID != "pathless" || !ws[0].Available {
		t.Fatalf("inventory = %+v, %v", ws, err)
	}
	replaces := int64(20)
	p, err := c.BindProject(ctx, 1, client.BindProjectInput{
		ConnectionID: 7, ExternalProjectID: "pathless", Primary: true, Replaces: &replaces,
	})
	if err != nil || len(p.Bindings) != 1 || p.Bindings[0].ID != 21 || !p.Bindings[0].Primary {
		t.Fatalf("bind project = %+v, %v", p, err)
	}
	p, err = c.UnbindProject(ctx, p.ID, p.Bindings[0].ID)
	if err != nil || p.ID != 1 || len(p.Bindings) != 0 {
		t.Fatalf("unbind project = %+v, %v", p, err)
	}
	plan, err := c.ResolveWorkspaces(ctx, 7, false, "")
	if err != nil || plan.PreviewToken != "plan" {
		t.Fatalf("resolve preview = %+v, %v", plan, err)
	}
	_, err = c.ResolveWorkspaces(ctx, 7, true, plan.PreviewToken)
	var changed *client.Error
	if !errors.As(err, &changed) || changed.Status != http.StatusConflict || changed.Code != "preview_changed" {
		t.Fatalf("resolve apply error = %v", err)
	}
	var fresh db.ResolveResult
	if err := json.Unmarshal(changed.Preview, &fresh); err != nil || fresh.PreviewToken != "changed" {
		t.Fatalf("fresh preview = %+v, %v", fresh, err)
	}
}
