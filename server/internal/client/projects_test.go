package client_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/flexdinesh/stage/server/internal/client"
)

func TestWorkspaceClientRoutes(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.RequestURI() {
		case "GET /api/connections/7/workspaces":
			io.WriteString(w, `{"workspaces":[{"connectionId":7,"externalId":"pathless","workspace":"","available":true}]}`)
		case "GET /api/projects?view=destinations":
			io.WriteString(w, `{"groups":[{"id":1,"title":"Grouped"}],"destinations":[{"connectionId":7,"workspaceId":"ws_exact","groupProjectId":1,"available":true}]}`)
		case "GET /api/connections/7/capabilities":
			io.WriteString(w, `{"models":[{"id":"connection-model","available":true}]}`)
		case "GET /api/connections/7/capabilities?workspaceId=ws_exact":
			io.WriteString(w, `{"models":[{"id":"workspace-model","available":true}]}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	c := client.NewInProcess(handler, "https://stage.test")
	ctx := t.Context()
	ws, err := c.Workspaces(ctx, 7)
	if err != nil || len(ws) != 1 || ws[0].ConnectionID != 7 || ws[0].ExternalID != "pathless" || !ws[0].Available {
		t.Fatalf("inventory = %+v, %v", ws, err)
	}
	summaries, err := c.ProjectSummaries(ctx)
	if err != nil || len(summaries.Groups) != 1 || len(summaries.Destinations) != 1 {
		t.Fatalf("destinations = %+v, %v", summaries, err)
	}
	dest := summaries.Destinations[0]
	if dest.WorkspaceID != "ws_exact" || dest.ConnectionID != 7 || dest.GroupProjectID == nil || *dest.GroupProjectID != summaries.Groups[0].ID {
		t.Fatalf("exact destination = %+v", dest)
	}
	connection, err := c.Capabilities(ctx, dest.ConnectionID)
	if err != nil || len(connection.Models) != 1 || connection.Models[0].ID != "connection-model" {
		t.Fatalf("connection capabilities = %+v, %v", connection, err)
	}
	exact, err := c.DestinationCapabilities(ctx, dest.ConnectionID, dest.WorkspaceID)
	if err != nil || len(exact.Models) != 1 || exact.Models[0].ID != "workspace-model" {
		t.Fatalf("workspace capabilities = %+v, %v", exact, err)
	}
}
