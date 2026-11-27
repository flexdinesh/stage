package serve_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/cli"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/serve"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSharedCLICredentialDelivery(t *testing.T) {
	short := isolate(t)
	addr := freePort(t)
	c := cfg(t, filepath.Join(short, "data"), addr)
	c.PublicURL = "http://" + addr
	output := &syncBuffer{}
	c.MultiUser = new(true)
	i, _ := startSharedOwner(t, serve.Options{Config: c, BootstrapOutput: output})
	agent := client.New(i.ready.AgentURL)
	assertMode(t, filepath.Dir(strings.TrimPrefix(i.ready.AgentURL, "unix://")), 0o700)
	if status, err := agent.AccessStatus(t.Context()); err != nil || !status.AgentOnly || status.Mode != "setup_pending" {
		t.Fatalf("pending agent socket status %+v: %v", status, err)
	}
	var agentStatus struct{ CanonicalURL string }
	(transportRequest{method: "GET", path: "/api/auth/status"}).send(t, agent.HTTP, "http://stage", 200, &agentStatus)
	if agentStatus.CanonicalURL != c.PublicURL {
		t.Fatal("shared status omitted the desktop canonical URL on the agent socket")
	}
	if _, err := agent.Projects(t.Context()); !client.IsStatus(err, 403) {
		t.Fatalf("pending agent socket access: %v", err)
	}
	browser := &http.Client{}
	browser.Jar, _ = cookiejar.New(nil)
	claimBrowser(t, browser, c.PublicURL, output.String())

	home := t.TempDir()
	values := map[string]string{"HOME": os.Getenv("HOME"), "STAGE_DATA_DIR": c.DataDir, "XDG_RUNTIME_DIR": os.Getenv("XDG_RUNTIME_DIR")}
	var commands [][]string
	var read int
	env := cli.Env{
		Home: home, Getenv: func(k string) string { return values[k] },
		Executable: "/example/bin/stage", LookPath: func(string) error { return nil },
		Exec: func(cmd string, args ...string) error {
			commands = append(commands, append([]string{cmd}, args...))
			return nil
		},
		ReadPassword: func() ([]byte, error) {
			read++
			return []byte("spindrift persimmon telescope"), nil
		},
	}
	run := func(want int, args ...string) string {
		t.Helper()
		var out, stderr bytes.Buffer
		e := env
		e.Stdout, e.Stderr = &out, &stderr
		if code := cli.Run(t.Context(), args, e); code != want {
			t.Fatalf("%v exited %d, want %d\n%s%s", args, code, want, out.String(), stderr.String())
		}
		text := out.String() + stderr.String()
		if strings.Contains(text, "spindrift persimmon telescope") {
			t.Fatal("password in CLI output")
		}
		return text
	}
	url := []string{"--url", c.PublicURL}
	login := append([]string{"auth", "login", "--email", "owner@example.test"}, url...)
	if text := run(0, login...); !strings.Contains(text, "Password: ") || read != 1 {
		t.Fatal("sign-in did not prompt once")
	}
	homeConfig := filepath.Join(home, ".config", buildinfo.Identity, "cli")
	paths, err := filepath.Glob(filepath.Join(homeConfig, "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("saved sessions: %v, %v", paths, err)
	}
	assertMode(t, homeConfig, 0o700)
	assertMode(t, paths[0], 0o600)
	human := loadCLICredential(t, paths[0])
	if human.Target != c.PublicURL || human.Audience != "cli" {
		t.Fatal("CLI session lacks human audience and target")
	}
	if text := run(0, append([]string{"auth", "whoami"}, url...)...); !strings.Contains(text, "owner@example.test (admin)") {
		t.Fatal("whoami did not identify the account and role")
	}
	run(0, append([]string{"projects", "ls", "--human"}, url...)...)
	run(1, append([]string{"projects", "ls"}, url...)...)
	run(2, append([]string{"mcp", "--human"}, url...)...)
	if _, err := agent.Projects(t.Context()); err != nil {
		t.Fatalf("local automatic agent access: %v", err)
	}
	run(0, "setup", "mcp", "--claude", "--url", c.PublicURL)
	if len(commands) != 2 || !strings.Contains(strings.Join(commands[1], " "), "--url "+i.ready.AgentURL) {
		t.Fatalf("did not pin the selected installation's agent socket: %v", commands)
	}
	secondAddr := freePort(t)
	secondCfg := cfg(t, filepath.Join(short, "other"), secondAddr)
	secondCfg.PublicURL = "http://" + secondAddr
	secondCfg.MultiUser = new(true)
	other, _ := startSharedOwner(t, serve.Options{Config: secondCfg, BootstrapOutput: &syncBuffer{}})
	if other.ready.AgentURL == i.ready.AgentURL {
		t.Fatal("shared installations must have distinct agent sockets")
	}
	run(1, "setup", "mcp", "--claude", "--url", secondCfg.PublicURL)
	if len(commands) != 2 {
		t.Fatal("setup must leave the current registration intact when the local socket matches a different installation")
	}
	run(1, "auth", "login", "--email", "owner@example.test", "--url", i.ready.AgentURL)
	if read != 1 {
		t.Fatal("asked for a password on the agent socket")
	}

	file := filepath.Join(home, "integration", "agent.json")
	rotate := append([]string{"auth", "agent", "rotate", "--out", file}, url...)
	run(0, rotate...)
	assertMode(t, file, 0o600)
	first := loadCLICredential(t, file)
	if first.Target != c.PublicURL || first.Audience != "agent" || time.Until(first.ExpiresAt) < 29*24*time.Hour {
		t.Fatal("agent credential lacks scope, target, or 30-day expiry")
	}
	values["STAGE_AGENT_CREDENTIAL_FILE"] = paths[0]
	run(1, append([]string{"projects", "ls"}, url...)...)
	values["STAGE_AGENT_CREDENTIAL_FILE"] = file
	run(0, append([]string{"projects", "ls"}, url...)...)
	cmd := exec.CommandContext(t.Context(), os.Args[0], "mcp", "--url", c.PublicURL)
	cmd.Env = append(os.Environ(), "SERVE_TEST_STAGE_CLI=1", "STAGE_AGENT_CREDENTIAL_FILE="+file)
	m := mcp.NewClient(&mcp.Implementation{Name: "network stdio", Version: "1"}, nil)
	stdio, err := m.Connect(t.Context(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stdio.Close()
	stdioProjects := func(wantError bool) {
		t.Helper()
		out, err := stdio.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_projects"})
		if err != nil || out.IsError != wantError {
			t.Fatalf("stdio projects: want tool error %v, result %v, transport error %v", wantError, out, err)
		}
	}
	stdioProjects(false)
	run(2, append([]string{"setup", "mcp", "--http"}, url...)...)
	commands = nil
	run(0, append([]string{"setup", "mcp", "--codex", "--credential-file", file}, url...)...)
	if len(commands) != 2 || !strings.Contains(strings.Join(commands[1], " "), "STAGE_AGENT_CREDENTIAL_FILE="+file) ||
		strings.Contains(fmt.Sprint(commands), first.Token) || strings.Contains(fmt.Sprint(commands), human.Token) {
		t.Fatal("MCP config must carry the private file path without either credential")
	}
	if err := os.Chmod(file, 0o640); err != nil {
		t.Fatal(err)
	}
	run(1, append([]string{"projects", "ls"}, url...)...)
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}

	run(0, rotate...)
	second := loadCLICredential(t, file)
	if second.Token == first.Token {
		t.Fatal("rotation reused the same credential")
	}
	stale := client.New(c.PublicURL).WithCredential(func() (client.Credential, error) { return first, nil })
	if _, err := stale.Projects(t.Context()); !client.IsStatus(err, 401) {
		t.Fatalf("superseded agent credential: %v", err)
	}
	run(0, append([]string{"projects", "ls"}, url...)...)
	stdioProjects(false)
	run(0, append([]string{"auth", "agent", "revoke"}, url...)...)
	run(1, append([]string{"projects", "ls"}, url...)...)
	stdioProjects(true)
	run(0, append([]string{"auth", "logout"}, url...)...)
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatalf("logout left the session file: %v", err)
	}
	loggedOut := client.New(c.PublicURL).WithCredential(func() (client.Credential, error) { return human, nil })
	if _, err := loggedOut.Me(t.Context()); !client.IsStatus(err, 401) {
		t.Fatalf("logout did not revoke the CLI session: %v", err)
	}
}

func loadCLICredential(t *testing.T, path string) client.Credential {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cred client.Credential
	if err := json.Unmarshal(b, &cred); err != nil {
		t.Fatal("saved credential did not decode")
	}
	return cred
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != want {
		t.Errorf("%s mode %v, want %v", path, fi.Mode().Perm(), want)
	}
}
