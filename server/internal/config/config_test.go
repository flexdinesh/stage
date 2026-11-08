package config

import "testing"

func TestProdDatabase(t *testing.T) {
	for raw, want := range map[string]bool{
		"postgres://t3stage:t3stage@127.0.0.1:5439/t3stage?sslmode=disable":          true,
		"postgres://t3stage:t3stage@127.0.0.1:5439/t3stage_dev_main?sslmode=disable": true, // prod server
		"postgres://t3stage:t3stage@127.0.0.1:5440/t3stage?sslmode=disable":          true, // prod name
		"postgres://t3stage:t3stage@127.0.0.1:5440/t3stage_dev_main?sslmode=disable": false,
		"postgres://t3stage:t3stage@127.0.0.1:5440/postgres?sslmode=disable":         false,
		"postgres://t3stage:t3stage@127.0.0.1:5440/t3stage_test_api?sslmode=disable": false,
		"postgres://%zz": true, // unparseable
	} {
		if got := ProdDatabase(raw); got != want {
			t.Errorf("ProdDatabase(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestLoadDevRefusesProd(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("DATABASE_URL", "postgres://t3stage:t3stage@127.0.0.1:5439/t3stage?sslmode=disable")
	if _, err := Load(); err == nil {
		t.Fatal("dev config accepted the production database")
	}
	t.Setenv("DATABASE_URL", "postgres://t3stage:t3stage@127.0.0.1:5440/t3stage_dev_main?sslmode=disable")
	cfg, err := Load()
	if err != nil || cfg.T3ClientName != "t3-stage dev" {
		t.Fatalf("dev config %+v, %v", cfg, err)
	}
	t.Setenv("APP_ENV", "")
	t.Setenv("DATABASE_URL", "postgres://t3stage:t3stage@127.0.0.1:5439/t3stage?sslmode=disable")
	if cfg, err := Load(); err != nil || cfg.T3ClientName != "t3-stage" {
		t.Fatalf("prod config %+v, %v", cfg, err)
	}
}
