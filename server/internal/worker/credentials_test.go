package worker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const legacyCredentials = `{
  "schema": 1,
  "harnesses": {
    "t3": {"clientId":"id","token":"old-token","expiresAt":"2026-11-26T00:00:00Z","metadata":{"keep":true}},
    "t3-admin": "old-admin",
    "opencode": {"username":"user","password":"old-password"}
  }
}`

func legacyCredentialMappings() []LegacySecret {
	validateObject := func(raw json.RawMessage) error {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		if object == nil {
			return errors.New("expected a credential object")
		}
		return nil
	}
	validateString := func(raw json.RawMessage) error {
		var str *string
		if err := json.Unmarshal(raw, &str); err != nil {
			return err
		}
		if str == nil {
			return errors.New("expected a credential string")
		}
		return nil
	}
	return []LegacySecret{
		{Entry: "t3", Kind: "t3", Key: "token", Validate: validateObject},
		{Entry: "t3-admin", Kind: "t3", Key: "adminToken", Validate: validateString},
		{Entry: "opencode", Kind: "opencode", Key: "credential", Validate: validateObject},
	}
}

func TestCredentialStoreUpgrade(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ConfigFile)
	s := NewCredentialStore(configPath, legacyCredentialMappings()...)
	if err := os.WriteFile(s.Path(), []byte(legacyCredentials), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.Path(), 0o644); err != nil {
		t.Fatal(err)
	}
	oldFile, err := os.Open(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer oldFile.Close()

	// The first Get upgrades the file even when the requested key is absent.
	checkCredential(t, s, "missing", "key", "")
	reopened := NewCredentialStore(configPath)
	checkCredential(t, reopened, "t3", "token", `{"clientId":"id","token":"old-token","expiresAt":"2026-11-26T00:00:00Z","metadata":{"keep":true}}`)
	checkCredential(t, reopened, "t3", "adminToken", `"old-admin"`)
	checkCredential(t, reopened, "opencode", "credential", `{"username":"user","password":"old-password"}`)
	checkCredentialMode(t, reopened)

	var file Credentials
	if err := json.Unmarshal(readCredentialFile(t, reopened), &file); err != nil {
		t.Fatal(err)
	}
	if file.Schema != 2 || len(file.Harnesses) != 2 {
		t.Fatalf("upgraded schema = %d, harness count = %d", file.Schema, len(file.Harnesses))
	}
	if old, err := io.ReadAll(oldFile); err != nil || string(old) != legacyCredentials {
		t.Fatalf("upgrade modified the original inode: %s, %v", old, err)
	}
}

func TestCredentialStoreRejectsMalformedFiles(t *testing.T) {
	for _, tc := range []struct{ name, contents string }{
		{"syntax", `{"schema":1,"harnesses":{`},
		{"no schema", `{"harnesses":{"t3":{}}}`},
		{"future schema", `{"schema":3,"harnesses":{}}`},
		{"legacy map", `{"schema":1,"harnesses":[]}`},
		{"legacy value", `{"schema":1,"harnesses":{"t3":{},"t3-admin":{"unexpected":true}}}`},
		{"unmapped legacy entry", `{"schema":1,"harnesses":{"t3":{},"unknown":"keep"}}`},
		{"current kind", `{"schema":2,"harnesses":{"t3":"old-token"}}`},
		{"null current kind", `{"schema":2,"harnesses":{"t3":null}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewCredentialStore(filepath.Join(t.TempDir(), ConfigFile), legacyCredentialMappings()...)
			if err := os.WriteFile(s.Path(), []byte(tc.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if raw, err := s.Get(ctx, "t3", "token"); err == nil || raw != nil {
				t.Fatalf("Get = %s, %v", raw, err)
			}
			if err := s.Set(ctx, "t3", "token", json.RawMessage(`"replacement"`)); err == nil {
				t.Fatal("Set accepted malformed file")
			}
			if err := s.Delete(ctx, "t3", "token"); err == nil {
				t.Fatal("Delete accepted malformed file")
			}
			if got := readCredentialFile(t, s); string(got) != tc.contents {
				t.Fatalf("changed malformed file: %s", got)
			}
		})
	}
}

func TestCredentialStoreFailedUpgrade(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to read-only directories")
	}
	dir := t.TempDir()
	s := NewCredentialStore(filepath.Join(dir, ConfigFile), legacyCredentialMappings()...)
	if err := os.WriteFile(s.Path(), []byte(legacyCredentials), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Error(err)
		}
	})
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if raw, err := s.Get(t.Context(), "t3", "adminToken"); raw != nil || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Get = %s, %v, want permission error", raw, err)
	}
	if got := readCredentialFile(t, s); string(got) != legacyCredentials {
		t.Fatalf("failed upgrade changed the file: %s", got)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	checkCredential(t, s, "t3", "adminToken", `"old-admin"`)
	checkCredentialMode(t, s)
}

func TestCredentialStoreRotateAndDelete(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "worker", ConfigFile)
	s := NewCredentialStore(configPath)
	ctx := t.Context()
	checkCredential(t, s, "a", "token", "")
	if err := s.Delete(ctx, "a", "token"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Path()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing credential created a file: %v", err)
	}
	if err := s.Set(ctx, "a", "token", json.RawMessage(`"old"`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "a", "admin", json.RawMessage(`"keep"`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "b", "token", json.RawMessage(`null`)); err != nil {
		t.Fatal(err)
	}
	other := NewCredentialStore(configPath)
	if err := other.Set(ctx, "a", "token", json.RawMessage(`"new"`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "a", "token", json.RawMessage(`{`)); err == nil {
		t.Fatal("Set accepted invalid JSON")
	}
	checkCredential(t, s, "a", "token", `"new"`)
	if err := other.Delete(ctx, "a", "token"); err != nil {
		t.Fatal(err)
	}
	checkCredential(t, s, "a", "token", "")
	checkCredential(t, s, "a", "admin", `"keep"`)
	checkCredential(t, s, "b", "token", `null`)
	checkCredentialMode(t, s)
}

func TestCredentialStoreConcurrentUpgradeAndWrites(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ConfigFile)
	s := NewCredentialStore(configPath, legacyCredentialMappings()...)
	if err := os.WriteFile(s.Path(), []byte(legacyCredentials), 0o600); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			handle := NewCredentialStore(configPath, legacyCredentialMappings()...)
			key := fmt.Sprintf("entry%d", i)
			<-start
			if err := handle.Set(t.Context(), "third", key, json.RawMessage(`[true]`)); err != nil {
				t.Errorf("Set %s: %v", key, err)
			}
		})
	}
	close(start)
	wg.Wait()
	for i := range 16 {
		checkCredential(t, s, "third", fmt.Sprintf("entry%d", i), `[true]`)
	}
	checkCredential(t, s, "t3", "adminToken", `"old-admin"`)
	checkCredential(t, s, "opencode", "credential", `{"username":"user","password":"old-password"}`)
}

func readCredentialFile(t *testing.T, s *CredentialStore) []byte {
	t.Helper()
	b, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func checkCredential(t *testing.T, s *CredentialStore, kind, key, want string) {
	t.Helper()
	raw, err := s.Get(t.Context(), kind, key)
	if err != nil {
		t.Fatal(err)
	}
	if want == "" {
		if raw != nil {
			t.Fatalf("Get %s/%s = %s, want nil", kind, key, raw)
		}
		return
	}
	var got bytes.Buffer
	if err := json.Compact(&got, raw); err != nil || got.String() != want {
		t.Fatalf("Get %s/%s = %s, %v, want %s", kind, key, raw, err, want)
	}
}

func checkCredentialMode(t *testing.T, s *CredentialStore) {
	t.Helper()
	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("credential file mode = %o, want 0600", got)
	}
}
