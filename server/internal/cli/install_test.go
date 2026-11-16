package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/install"
)

func TestInstallUsage(t *testing.T) {
	if code, _, errOut := runService(t, &noManager{}, "install"); code != 2 || !strings.Contains(errOut, "--from DIR is required") {
		t.Fatalf("no --from: %d %q", code, errOut)
	}
	if code, _, _ := runService(t, &noManager{}, "install", "--from", "x", "--headless", "--desktop"); code != 2 {
		t.Fatalf("both modes: %d", code)
	}
	if code, _, _ := runService(t, &noManager{}, "update", "extra"); code != 2 {
		t.Fatalf("update arg: %d", code)
	}
}

// A binary the installer did not install does not update or uninstall.
func TestUpdateRefusesUninstalledBinary(t *testing.T) {
	code, out, _ := runService(t, &noManager{}, "update", "--json")
	var r install.Result
	if code != install.CodeUnsupported || json.Unmarshal([]byte(out), &r) != nil || r.OK || !strings.Contains(r.Message, "not installed by the Stage installer") || r.Fix == "" {
		t.Fatalf("update: %d %s", code, out)
	}
	code, _, errOut := runService(t, &noManager{}, "uninstall")
	if code != install.CodeUnsupported || !strings.Contains(errOut, "no Stage installed by the installer") {
		t.Fatalf("uninstall: %d %s", code, errOut)
	}
}

func TestInstallBadDirFailsVerification(t *testing.T) {
	code, out, _ := runService(t, &noManager{}, "install", "--from", t.TempDir(), "--headless", "--json")
	var r install.Result
	if code != install.CodeVerify || json.Unmarshal([]byte(out), &r) != nil || !strings.Contains(r.Message, "invalid release") {
		t.Fatalf("install: %d %s", code, out)
	}
}
