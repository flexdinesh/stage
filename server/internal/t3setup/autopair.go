package t3setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// Why auto-pair is unavailable.
const (
	ReasonNotLocal    = "not_local"     // the T3 URL is not a loopback address
	ReasonCLINotFound = "cli_not_found" // no t3 CLI on this machine
	ReasonFake        = "fake"          // the in-memory fake T3 client
)

// AutoPairTimeout bounds one `t3 auth pairing create`.
const AutoPairTimeout = 15 * time.Second

// Runner runs bin with args and returns its stdout; a failure's error
// carries its stderr.
type Runner func(ctx context.Context, bin string, args []string) ([]byte, error)

// AutoPair is whether POST /api/t3/pair/auto can work.
type AutoPair struct {
	Available bool
	Reason    string // "" when available
	Bin       string // the t3 CLI found (when available)
}

// autoPair: available when the client is real, the T3 URL is loopback and
// a t3 CLI is found.
func (s *Service) autoPair() AutoPair {
	switch {
	case s.o.Fake:
		return AutoPair{Reason: ReasonFake}
	case !IsLoopbackURL(s.Endpoint().URL):
		return AutoPair{Reason: ReasonNotLocal}
	}
	bin := s.findCLI()
	if bin == "" {
		return AutoPair{Reason: ReasonCLINotFound}
	}
	return AutoPair{Available: true, Bin: bin}
}

// IsLoopbackURL reports whether u's host is localhost or a loopback IP
// (127.0.0.0/8, ::1).
func IsLoopbackURL(u string) bool {
	p, err := url.Parse(u)
	if err != nil {
		return false
	}
	h := strings.ToLower(p.Hostname())
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// cliDirs are where installers put the t3 CLI when it is not on the
// server's PATH (a service manager's PATH is minimal).
func cliDirs(home string) []string {
	var dirs []string
	if home != "" {
		for _, d := range []string{".local/bin", ".bun/bin", ".local/share/pnpm", ".npm-global/bin", ".volta/bin"} {
			dirs = append(dirs, filepath.Join(home, d))
		}
	}
	return append(dirs, "/usr/local/bin", "/opt/homebrew/bin")
}

// findCLI is the t3 CLI: T3_BIN (only it, when set), else PATH, else the
// usual install dirs. "" when none.
func (s *Service) findCLI() string {
	if b := s.o.Config.T3Bin; b != "" {
		p, err := s.o.LookPath(b)
		if err != nil {
			return ""
		}
		return p
	}
	if p, err := s.o.LookPath("t3"); err == nil {
		return p
	}
	home := s.o.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	for _, d := range cliDirs(home) {
		if p, err := s.o.LookPath(filepath.Join(d, "t3")); err == nil {
			return p
		}
	}
	return ""
}

// PairingArgs are the `t3` arguments that mint a pairing code for stage.
func PairingArgs(clientName string) []string {
	args := []string{"auth", "pairing", "create", "--json", "--label", t3.PairingLabel(clientName), "--ttl", "5m"}
	for _, sc := range t3.FullAccessScopes() {
		args = append(args, "--scope", sc)
	}
	return args
}

// AutoPair mints a pairing code with the local t3 CLI and redeems it (see
// Pair). 409 when auto-pair is unavailable; 502 when T3 is not answering
// (nothing is minted) or the CLI or the pairing fails.
func (s *Service) AutoPair(ctx context.Context) error {
	ap := s.autoPair()
	if !ap.Available {
		return db.Conflict("%s", unavailableMessage(ap.Reason, s.Endpoint().URL))
	}
	// A code minted for a T3 that is not answering could not be redeemed.
	if p := s.o.Client.Probe(ctx); !p.Reachable {
		return pairErr("no T3 server answered at %s (%s); start it and try again", s.Endpoint().URL, orNoAnswer(p.Err))
	}
	rctx, cancel := context.WithTimeout(ctx, AutoPairTimeout)
	out, err := s.o.Run(rctx, ap.Bin, PairingArgs(s.o.Config.T3ClientName))
	cancel()
	if err != nil {
		return pairErr("t3 auth pairing create failed: %v", err)
	}
	var res struct {
		Credential string `json:"credential"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &res); err != nil {
		return pairErr("t3 auth pairing create: unexpected output: %s", truncate(strings.TrimSpace(string(out)), 200))
	}
	if res.Credential == "" {
		return pairErr("t3 auth pairing create returned no credential")
	}
	return s.Pair(ctx, res.Credential)
}

func unavailableMessage(reason, t3URL string) string {
	switch reason {
	case ReasonFake:
		return "auto-pair is not available with the fake T3 client"
	case ReasonNotLocal:
		return fmt.Sprintf("auto-pair needs T3 on this machine, and %s is not a loopback address; pair with a code from `t3 pair` on the T3 host", t3URL)
	}
	return "auto-pair needs the t3 CLI, which was not found on this machine (set T3_BIN); pair with a code from `t3 pair`"
}

func execLookPath(file string) (string, error) { return exec.LookPath(file) }

// execRunner runs the CLI with its own directory first on PATH (script
// shims find their runtime next to them).
func execRunner(ctx context.Context, bin string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := truncate(strings.TrimSpace(stderr.String()), 300); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func orNoAnswer(reason string) string {
	if reason == "" {
		return "no answer"
	}
	return reason
}
