package opencode_test

import (
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

// Any OpenCode version connects while its contract has what Stage calls.
func TestHealthAcceptsAnyVersionWithTheContract(t *testing.T) {
	for _, version := range []string{"2.3.0", "2.3.2", "2.4.0"} {
		fake := opencodetest.New(t, "")
		fake.SetVersion(version)
		a, err := opencode.New(2, opencode.Settings{Endpoint: fake.URL})
		if err != nil {
			t.Fatal(err)
		}
		if h := a.Health(t.Context()); !h.Available || !strings.HasSuffix(h.Identity, "|fake|42") {
			t.Errorf("%s: %+v", version, h)
		}
	}
}
