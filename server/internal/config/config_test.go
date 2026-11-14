package config

import "testing"

func TestProdDatabase(t *testing.T) {
	for raw, want := range map[string]bool{
		"postgres://stage:stage@127.0.0.1:5439/stage?sslmode=disable":          true,
		"postgres://stage:stage@127.0.0.1:5439/stage_dev_main?sslmode=disable": true, // prod server
		"postgres://stage:stage@127.0.0.1:5440/stage?sslmode=disable":          true, // prod name
		"postgres://stage:stage@127.0.0.1:5440/stage_dev_main?sslmode=disable": false,
		"postgres://stage:stage@127.0.0.1:5440/postgres?sslmode=disable":       false,
		"postgres://stage:stage@127.0.0.1:5440/stage_test_api?sslmode=disable": false,
		"postgres://%zz": true, // unparseable
	} {
		if got := ProdDatabase(raw); got != want {
			t.Errorf("ProdDatabase(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestLoadDevRefusesProd(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("DATABASE_URL", "postgres://stage:stage@127.0.0.1:5439/stage?sslmode=disable")
	if _, err := Load(); err == nil {
		t.Fatal("dev config accepted the production database")
	}
	t.Setenv("DATABASE_URL", "postgres://stage:stage@127.0.0.1:5440/stage_dev_main?sslmode=disable")
	cfg, err := Load()
	if err != nil || cfg.T3ClientName != "stage dev" {
		t.Fatalf("dev config %+v, %v", cfg, err)
	}
	t.Setenv("APP_ENV", "")
	t.Setenv("DATABASE_URL", "postgres://stage:stage@127.0.0.1:5439/stage?sslmode=disable")
	if cfg, err := Load(); err != nil || cfg.T3ClientName != "stage" {
		t.Fatalf("prod config %+v, %v", cfg, err)
	}
}
