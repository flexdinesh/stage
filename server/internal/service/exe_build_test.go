package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
)

// version --json carries the release stamp only when a build set it.
func TestThisBuildStamp(t *testing.T) {
	old := buildinfo.Stamp
	t.Cleanup(func() { buildinfo.Stamp = old })

	buildinfo.Stamp = ""
	out, err := json.Marshal(ThisBuild())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"build"`) {
		t.Fatalf("unstamped build reports build: %s", out)
	}

	buildinfo.Stamp = "stage-build/1 identity=stage version=1.2.3 commit=abc;"
	if got := ThisBuild().Build; got != buildinfo.Stamp {
		t.Fatalf("Build = %q, want the stamp", got)
	}
}
