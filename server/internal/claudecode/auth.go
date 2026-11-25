package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Auth is the subset of `claude auth status --json` Stage reads. Stage
// stores no Claude credentials; it reads where Claude keeps its config and
// transcripts, and whether it is logged in.
type Auth struct {
	LoggedIn bool `json:"loggedIn"`
	// ConfigDirectory is the connection's stable identity.
	ConfigDirectory string `json:"configDirectory"`
	// ProjectsDirectory is the transcript root.
	ProjectsDirectory string `json:"projectsDirectory"`
	AuthMethod        string `json:"authMethod"`
}

// ParseAuth parses `claude auth status --json`.
func ParseAuth(b []byte) (Auth, error) {
	var a Auth
	if err := json.Unmarshal(b, &a); err != nil {
		return Auth{}, fmt.Errorf("malformed claude auth status: %w", err)
	}
	if a.ConfigDirectory == "" {
		return Auth{}, fmt.Errorf("claude auth status reports no config directory")
	}
	return a, nil
}

// ReadAuth runs `claude auth status --json` (bounded by ctx). The command
// exits non-zero when logged out but still prints the status.
func ReadAuth(ctx context.Context, cli *CLI) (Auth, error) {
	res, err := cli.Run(ctx, "", "auth", "status", "--json")
	if err != nil {
		return Auth{}, err
	}
	a, perr := ParseAuth([]byte(res.Stdout))
	if perr != nil && res.Code != 0 {
		return Auth{}, exitError("auth status", res)
	}
	return a, perr
}

// ReadVersion runs `claude --version`: "2.1.332 (Claude Code)" gives
// "2.1.332".
func ReadVersion(ctx context.Context, cli *CLI) (string, error) {
	res, err := cli.Run(ctx, "", "--version")
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", exitError("--version", res)
	}
	v := strings.TrimSpace(res.Stdout)
	if i := strings.IndexByte(v, ' '); i > 0 {
		v = v[:i]
	}
	if v == "" {
		return "", fmt.Errorf("claude --version printed nothing")
	}
	return v, nil
}

// authCache is the adapter's last auth status, reused for authTTL.
type authCache struct {
	at   time.Time
	auth Auth
	err  error
}

const authTTL = 30 * time.Second
