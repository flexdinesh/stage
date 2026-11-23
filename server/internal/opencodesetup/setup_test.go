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

// A saved address carrying credentials, a query or a fragment is never
// echoed by Status.
func TestStatusNeverEchoesSavedSecrets(t *testing.T) {
	var temp dbtest.DB
	store := db.NewStore(temp.Reset(t))
	setup := config.OpenCodeSetup{Endpoint: "https://user:private@example.com", BrowserEndpoint: "https://example.com?secret=private", WebClient: "https://example.com#private", Auth: config.OpenCodeAuthBasic}
	if err := store.SaveOpenCodeSettings(t.Context(), db.LocalMachine, setup, config.OpenCodeCredential{Username: "user", Password: "private"}); err != nil {
		t.Fatal(err)
	}
	s, err := New(t.Context(), storePorts(store, func(int64, int64, execution.Adapter) {}), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(s.Status(t.Context()))
	if err != nil || strings.Contains(string(body), "private") {
		t.Fatalf("public status contains the address or credentials: %s", body)
	}
}
