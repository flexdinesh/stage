package worker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLink(t *testing.T) {
	for _, tc := range []struct{ in, base, code string }{
		{"https://stage.example.com/join#stage_pc_1_abc", "https://stage.example.com", "stage_pc_1_abc"},
		{"http://10.0.0.5:7380/join#c", "http://10.0.0.5:7380", "c"},
		{"https://h.example/stage/join/#c", "https://h.example/stage", "c"},
		{"  http://lan.box/join#c\n", "http://lan.box", "c"},
	} {
		base, code, err := ParseLink(tc.in)
		if err != nil || base != tc.base || code != tc.code {
			t.Errorf("%q: %q %q %v", tc.in, base, code, err)
		}
	}
	for _, bad := range []string{"https://h/join", "https://h/other#c", "ftp://h/join#c", "/join#c", "https://u:p@h/join#c"} {
		if _, _, err := ParseLink(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestConfigRoundTripAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w", ConfigFile)
	if _, err := LoadConfig(path); !errors.Is(err, ErrNotJoined) {
		t.Fatalf("missing: %v", err)
	}
	c := Config{Schema: ConfigSchema, URL: "https://s.example", MachineID: 4, Label: "l", Token: "stage_mt_1_x", JoinedAt: time.Unix(1, 0).UTC()}
	if err := SaveConfig(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil || got != c {
		t.Fatalf("round trip %+v %v", got, err)
	}
	st, _ := os.Stat(path)
	dst, _ := os.Stat(filepath.Dir(path))
	if st.Mode().Perm() != 0o600 || dst.Mode().Perm() != 0o700 {
		t.Fatalf("modes %v %v", st.Mode(), dst.Mode())
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("group-readable accepted: %v", err)
	}
	_ = os.Chmod(path, 0o600)
	if err := os.WriteFile(path, []byte(`{"schema":1,"url":"https://s","machineId":0,"token":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("incomplete config accepted")
	}
}

func TestConfigPaths(t *testing.T) {
	env := map[string]string{"HOME": "/h"}
	get := func(k string) string { return env[k] }
	if p, _ := DefaultConfigPath(get); p != "/h/.local/share/stage/worker/worker.json" {
		t.Fatalf("default %s", p)
	}
	env["STAGE_DATA_DIR"] = "/d"
	if p, _ := ResolveConfigPath("", get); p != "/d/worker/worker.json" {
		t.Fatalf("data dir %s", p)
	}
	env["STAGE_WORKER_CONFIG"] = "/e/w.json"
	if p, _ := ResolveConfigPath("", get); p != "/e/w.json" {
		t.Fatalf("env %s", p)
	}
	if p, _ := ResolveConfigPath("/f/w.json", get); p != "/f/w.json" {
		t.Fatalf("flag %s", p)
	}
	// The service ignores STAGE_DATA_DIR and STAGE_WORKER_CONFIG in its
	// environment; only worker.env names another config.
	if p, _ := ManagedConfigPath(get, "linux", map[string]string{}); p != "/h/.local/share/stage/worker/worker.json" {
		t.Fatalf("managed %s", p)
	}
	if p, _ := ManagedConfigPath(get, "linux", map[string]string{"STAGE_WORKER_CONFIG": "/x/w.json"}); p != "/x/w.json" {
		t.Fatalf("managed worker.env %s", p)
	}
}

func TestJoinErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("join sent Authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid or expired pairing code","code":"pairing_code_invalid"}`))
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), ConfigFile)
	_, err := Join(context.Background(), JoinOptions{Link: srv.URL + "/join#stage_pc_1_x", ConfigPath: path})
	if err == nil || !strings.Contains(err.Error(), "create a new one") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("config written after a failed join")
	}
}

func TestLeaveLocalWithoutServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), ConfigFile)
	if err := SaveConfig(path, Config{Schema: ConfigSchema, URL: "http://127.0.0.1:1", MachineID: 2, Token: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Leave(context.Background(), LeaveOptions{ConfigPath: path}); err == nil || !strings.Contains(err.Error(), "--local") {
		t.Fatalf("unreachable server: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("config deleted after a failed leave")
	}
	res, err := Leave(context.Background(), LeaveOptions{ConfigPath: path, Local: true})
	if err != nil || res.Removed {
		t.Fatalf("local leave %+v %v", res, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("config kept")
	}
}

func TestRunNotJoinedIsInvalid(t *testing.T) {
	err := Run(context.Background(), RunOptions{ConfigPath: filepath.Join(t.TempDir(), ConfigFile)})
	if !errors.Is(err, ErrNotJoined) {
		t.Fatalf("err = %v", err)
	}
}
