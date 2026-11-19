package opencodesetup

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func TestEnvironmentCredentialsStayOnTheirConfiguredService(t *testing.T) {
	var temp dbtest.DB
	store := db.NewStore(temp.Reset(t))
	cfg := config.OpenCodeSettings{Endpoint: "https://user:private@example.com", BrowserEndpoint: "https://example.com?secret=private", Username: "user", Password: "private"}
	s, err := New(t.Context(), store, cfg, func(int64, int64, execution.Adapter) { t.Fatal("invalid connection installed") }, nil)
	if err != nil {
		t.Fatal(err)
	}
	status := s.Status(t.Context())
	body, err := json.Marshal(status)
	if err != nil || status.State != "unavailable" || strings.Contains(string(body), "private") {
		t.Fatalf("public status contains invalid address or credentials: %s", body)
	}
	address := "https://different.example"
	if err := s.Save(t.Context(), config.OpenCodeEdit{Endpoint: &address}); err == nil {
		t.Fatal("changed environment-managed endpoint")
	}
	saved, verified, err := store.OpenCodeSettings(t.Context())
	if err != nil || saved != (config.OpenCodeSettings{Auth: config.OpenCodeAuthNone}) || verified != "" {
		t.Fatal("failed edit changed stored settings")
	}
	s.env = config.OpenCodeSettings{Endpoint: address}
	old := config.OpenCodeSettings{Endpoint: "https://previous.example", Username: "old", Password: "private"}
	resolved, _, err := s.resolve(old)
	if err != nil || resolved.Endpoint != address || resolved.Username != "" || resolved.Password != "" {
		t.Fatal("credentials followed the service address")
	}
}
