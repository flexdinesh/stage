package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const secret = "s3cret-pw"

func TestParseDatabaseInvalid(t *testing.T) {
	for _, v := range []string{
		"POSTGRES://u:" + secret + "@h/db", "Postgresql://u:" + secret + "@h/db",
		"mysql://u:" + secret + "@h/db", "sqlite:///x.db", "file:stage.db",
		"host=h user=u password=" + secret, " postgres://u:" + secret + "@h/db", "postgres:/h",
	} {
		_, err := ParseDatabase(v)
		if err == nil || errors.Is(err, ErrNoPostgres) {
			t.Fatalf("%q: err %v, want invalid", v, err)
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), v) {
			t.Fatalf("%q: error echoes the value: %v", v, err)
		}
	}
}

func TestParseDatabaseUnset(t *testing.T) {
	d, err := ParseDatabase("")
	if err != nil || d.Engine() != EngineSQLite || d.URL.Reveal() != "" {
		t.Fatalf("unset: %+v, %v", d, err)
	}
	if (Database{}).Engine() != EngineSQLite {
		t.Fatal("zero Database is not SQLite")
	}
}

func TestLoadDatabase(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir()}
	getenv := func(k string) string { return env[k] }
	cfg, err := LoadFrom(getenv, nil)
	if err != nil || cfg.Database.Engine() != EngineSQLite {
		t.Fatalf("unset: %v, %v", cfg.Database.Engine(), err)
	}
	env[DatabaseEnv] = "mysql://u:" + secret + "@h/db"
	if _, err := LoadFrom(getenv, nil); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("invalid URL: %v", err)
	}
}

// TestDatabaseURLNeverPrints: a Config holding a URL prints, logs and
// marshals without it.
func TestDatabaseURLNeverPrints(t *testing.T) {
	cfg := Config{DataDir: "/d", Database: Database{URL: DatabaseURL{s: "postgres://u:" + secret + "@h/db"}}}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "cfg", cfg, "db", cfg.Database, "url", cfg.Database.URL)
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "url", cfg.Database.URL)
	j, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg), fmt.Sprint(cfg.Database.URL), buf.String(), string(j)} {
		if strings.Contains(out, secret) || strings.Contains(out, "postgres://") {
			t.Fatalf("URL printed: %s", out)
		}
	}
}
