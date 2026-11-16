package alerts

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/flexdinesh/stage/desktop/app/target"
)

func TestLoadPrefsMissingIsDefault(t *testing.T) {
	p, err := LoadPrefs(filepath.Join(t.TempDir(), "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Enabled || p.Denied || len(p.Servers) != 0 || p.Version != 2 {
		t.Fatalf("defaults = %+v", p)
	}
}

func TestLoadPrefsCorruptIsDefault(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"garbage": "{not json",
		"version": `{"version":3,"enabled":false}`,
		"type":    `{"version":2,"servers":{"local":{"cursor":"x"}}}`,
		"typeV1":  `{"version":1,"cursor":"x"}`,
	} {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		p, err := LoadPrefs(path)
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		if !p.Enabled || len(p.Servers) != 0 {
			t.Errorf("%s: prefs = %+v, want defaults", name, p)
		}
	}
}

// TestLoadPrefsMigratesV1: a version 1 file (one server) becomes this
// machine's entry, IDs unchanged (the OS knows them by those).
func TestLoadPrefsMigratesV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "n.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"enabled":false,"cursor":9,"denied":true,"shown":["task-7","summary"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := LoadPrefs(path)
	if err != nil {
		t.Fatal(err)
	}
	sp := p.Servers[target.LocalID]
	if p.Enabled || !p.Denied || !p.migrated || sp == nil || sp.Cursor == nil || *sp.Cursor != 9 || len(sp.Shown) != 2 || sp.Shown[0] != "task-7" {
		t.Fatalf("migrated = %+v local %+v", p, sp)
	}
	// Missing fields keep their defaults.
	if err := os.WriteFile(path, []byte(`{"version":1,"cursor":4}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, _ = LoadPrefs(path)
	if !p.Enabled || *p.Servers[target.LocalID].Cursor != 4 {
		t.Fatalf("partial v1 = %+v", p)
	}
}

func TestLoadPrefsKeepsMissingFieldsDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "n.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"servers":{"local":{"cursor":9},"s-1":{"cursor":-3},"a/b":{"cursor":1}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := LoadPrefs(path)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Enabled || p.Servers["local"].Cursor == nil || *p.Servers["local"].Cursor != 9 || p.Servers["s-1"].Cursor != nil || p.Servers["a/b"] != nil {
		t.Fatalf("prefs = %+v", p)
	}
}

func TestSavePrefsRoundTripAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "notifications.json")
	c := int64(123)
	want := Prefs{Version: 2, Enabled: false, Denied: true, Servers: map[string]*ServerPrefs{"local": {Cursor: &c, Shown: []string{"local/task-7"}}}}
	if err := SavePrefs(path, want); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(path)
	if string(b) != `{"version":2,"enabled":false,"denied":true,"servers":{"local":{"cursor":123,"shown":["local/task-7"]}}}` {
		t.Errorf("file = %s", b)
	}
	got, err := LoadPrefs(path)
	if err != nil {
		t.Fatal(err)
	}
	sp := got.Servers["local"]
	if got.Enabled || !got.Denied || sp == nil || sp.Cursor == nil || *sp.Cursor != 123 || len(sp.Shown) != 1 || sp.Shown[0] != "local/task-7" {
		t.Errorf("round trip = %+v", got)
	}
	ents, _ := os.ReadDir(filepath.Dir(path))
	if len(ents) != 1 {
		t.Errorf("leftover files: %v", ents)
	}
}

func TestShownCapped(t *testing.T) {
	sp := &ServerPrefs{}
	for i := range 60 {
		sp.addShown("local/task-" + strconv.Itoa(i))
	}
	sp.addShown("local/task-15")
	if len(sp.Shown) != maxShown || sp.Shown[0] != "local/task-10" || sp.Shown[maxShown-1] != "local/task-15" {
		t.Fatalf("shown = %v", sp.Shown)
	}
	path := filepath.Join(t.TempDir(), "n.json")
	big := DefaultPrefs()
	for i := range 70 {
		big.server("local").Shown = append(big.server("local").Shown, "local/task-"+strconv.Itoa(i))
	}
	if err := SavePrefs(path, big); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadPrefs(path)
	if s := got.Servers["local"].Shown; len(s) != maxShown || s[0] != "local/task-20" {
		t.Fatalf("saved shown = %v", s)
	}
}
