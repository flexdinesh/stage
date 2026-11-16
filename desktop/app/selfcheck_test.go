package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Under go test MyGo resolves resources to <cwd>/resources, so a fake CLI
// there stands in for the bundled one.
func TestSelfcheckReportsBundledCLI(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	bin := filepath.Join(dir, "resources", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n[ \"$1 $2\" = \"version --json\" ] || exit 2\necho '{\"identity\":\"stage-dev\",\"version\":\"1.2.3\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "stage"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := selfcheck(&out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	var r struct {
		CLI        string `json:"cli"`
		Identity   string `json:"identity"`
		CLIVersion struct {
			Identity string `json:"identity"`
			Version  string `json:"version"`
		} `json:"cliVersion"`
	}
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("report %q: %v", out.String(), err)
	}
	if r.CLI != filepath.Join(bin, "stage") || r.CLIVersion.Version != "1.2.3" || r.CLIVersion.Identity != "stage-dev" || r.Identity != appIdentity {
		t.Fatalf("report %+v", r)
	}
}

func TestSelfcheckFailsWithoutBundledCLI(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, errw bytes.Buffer
	if code := selfcheck(&out, &errw); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if out.Len() != 0 || !strings.Contains(errw.String(), "version --json") {
		t.Fatalf("out %q err %q", out.String(), errw.String())
	}
}
