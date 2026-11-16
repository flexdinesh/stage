package alerts

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLoadPrefsMissingIsDefault(t *testing.T) {
	p, err := LoadPrefs(filepath.Join(t.TempDir(), "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Enabled || p.Cursor != nil || p.Denied || len(p.Shown) != 0 || p.Version != 1 {
		t.Fatalf("defaults = %+v", p)
	}
}

func TestLoadPrefsCorruptIsDefault(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"garbage": "{not json",
		"version": `{"version":2,"enabled":false}`,
		"type":    `{"version":1,"cursor":"x"}`,
	} {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		p, err := LoadPrefs(path)
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		if !p.Enabled || p.Cursor != nil {
			t.Errorf("%s: prefs = %+v, want defaults", name, p)
		}
	}
}

func TestLoadPrefsKeepsMissingFieldsDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "n.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"cursor":9}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := LoadPrefs(path)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Enabled || p.Cursor == nil || *p.Cursor != 9 {
		t.Fatalf("prefs = %+v", p)
	}
}

func TestSavePrefsRoundTripAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "notifications.json")
	c := int64(123)
	want := Prefs{Version: 1, Enabled: false, Cursor: &c, Denied: true, Shown: []string{"task-7"}}
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
	if string(b) != `{"version":1,"enabled":false,"cursor":123,"denied":true,"shown":["task-7"]}` {
		t.Errorf("file = %s", b)
	}
	got, err := LoadPrefs(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.Cursor == nil || *got.Cursor != 123 || !got.Denied || len(got.Shown) != 1 || got.Shown[0] != "task-7" {
		t.Errorf("round trip = %+v", got)
	}
	ents, _ := os.ReadDir(filepath.Dir(path))
	if len(ents) != 1 {
		t.Errorf("leftover files: %v", ents)
	}
}

func TestShownCapped(t *testing.T) {
	p := DefaultPrefs()
	for i := range 60 {
		p.addShown("task-" + strconv.Itoa(i))
	}
	p.addShown("task-15")
	if len(p.Shown) != maxShown || p.Shown[0] != "task-10" || p.Shown[maxShown-1] != "task-15" {
		t.Fatalf("shown = %v", p.Shown)
	}
	path := filepath.Join(t.TempDir(), "n.json")
	big := DefaultPrefs()
	for i := range 70 {
		big.Shown = append(big.Shown, "task-"+strconv.Itoa(i))
	}
	if err := SavePrefs(path, big); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadPrefs(path)
	if len(got.Shown) != maxShown || got.Shown[0] != "task-20" {
		t.Fatalf("saved shown = %v", got.Shown)
	}
}
