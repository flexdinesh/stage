package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
)

// selfcheckEnv makes a packaged app print how it resolves its bundled CLI
// and exit before the single-instance lock, windows or the service
// (docs/release.md): release verification runs it on the built package.
const selfcheckEnv = "STAGE_DESKTOP_SELFCHECK"

type selfcheckReport struct {
	App        string          `json:"app"`
	Identifier string          `json:"identifier"`
	Identity   string          `json:"identity"`
	Version    string          `json:"version"`
	Build      string          `json:"build,omitempty"`
	Resources  string          `json:"resources"`
	CLI        string          `json:"cli"`
	CLIVersion json.RawMessage `json:"cliVersion"`
}

// selfcheck resolves <resources>/bin/stage as the launcher does, runs
// `stage version --json` and writes one JSON report; it returns the exit
// code.
func selfcheck(w io.Writer, errw io.Writer) int {
	res, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		fmt.Fprintf(errw, "stage desktop selfcheck: resources: %v\n", err)
		return 1
	}
	cli := filepath.Join(res, "bin", "stage")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cli, "version", "--json").Output()
	if err != nil {
		fmt.Fprintf(errw, "stage desktop selfcheck: %s version --json: %v\n", cli, err)
		return 1
	}
	if !json.Valid(out) {
		fmt.Fprintf(errw, "stage desktop selfcheck: %s version --json printed no JSON\n", cli)
		return 1
	}
	report := selfcheckReport{
		App: appName, Identifier: appIdentifier, Identity: appIdentity,
		Version: appVersion(), Build: appBuild, Resources: res, CLI: cli, CLIVersion: out,
	}
	if err := json.NewEncoder(w).Encode(report); err != nil {
		fmt.Fprintf(errw, "stage desktop selfcheck: %v\n", err)
		return 1
	}
	return 0
}
