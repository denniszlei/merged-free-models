package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return p
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADDR", "")
	t.Setenv("PROXY_API_KEY", "")
	t.Setenv("KILO_ENABLED", "")
	t.Setenv("OPENCODE_ENABLED", "")

	cfg, err := Load(filepath.Join(dir, "missing.env"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Fatalf("Addr=%q", cfg.Addr)
	}
	if !cfg.Kilo.Enabled || !cfg.OpenCode.Enabled {
		t.Fatalf("expected both providers enabled by default")
	}
	if cfg.OpenCode.APIKey != "public" {
		t.Fatalf("OpenCode.APIKey=%q", cfg.OpenCode.APIKey)
	}
	if cfg.RefreshInterval != 10*time.Minute {
		t.Fatalf("RefreshInterval=%v", cfg.RefreshInterval)
	}
}

func TestLoadDotEnvAndOverrides(t *testing.T) {
	dir := t.TempDir()
	envPath := writeFile(t, dir, ".env", `
ADDR=:9090
PROXY_API_KEY=secret
KILO_ENABLED=false
OPENCODE_BASE_URL=https://example.com/api/v1/
MODEL_REFRESH_INTERVAL=45s
MODEL_FETCH_TIMEOUT=5
`)

	// Env overrides .env.
	t.Setenv("OPENCODE_API_KEY", "from-env")

	cfg, err := Load(envPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9090" {
		t.Fatalf("Addr=%q", cfg.Addr)
	}
	if cfg.ProxyAPIKey != "secret" {
		t.Fatalf("ProxyAPIKey=%q", cfg.ProxyAPIKey)
	}
	if cfg.Kilo.Enabled {
		t.Fatal("expected KILO_ENABLED=false to apply")
	}
	if cfg.OpenCode.BaseURL != "https://example.com/api/v1" {
		t.Fatalf("OpenCode.BaseURL=%q (trailing slash should be trimmed)", cfg.OpenCode.BaseURL)
	}
	if cfg.OpenCode.APIKey != "from-env" {
		t.Fatalf("OpenCode.APIKey=%q (env should win)", cfg.OpenCode.APIKey)
	}
	if cfg.RefreshInterval != 45*time.Second {
		t.Fatalf("RefreshInterval=%v", cfg.RefreshInterval)
	}
	if cfg.ModelFetchTimeout != 5*time.Second {
		t.Fatalf("ModelFetchTimeout=%v (bare number should parse as seconds)", cfg.ModelFetchTimeout)
	}
}

func TestLoadRejectsAllDisabled(t *testing.T) {
	dir := t.TempDir()
	envPath := writeFile(t, dir, ".env", "KILO_ENABLED=false\nOPENCODE_ENABLED=false\n")
	t.Setenv("KILO_ENABLED", "")
	t.Setenv("OPENCODE_ENABLED", "")
	if _, err := Load(envPath); err == nil {
		t.Fatal("expected error when both providers disabled")
	}
}

func TestLoadRejectsInvalidOpenCodeURL(t *testing.T) {
	dir := t.TempDir()
	envPath := writeFile(t, dir, ".env", "OPENCODE_BASE_URL=not-a-url\n")
	t.Setenv("OPENCODE_BASE_URL", "")
	if _, err := Load(envPath); err == nil {
		t.Fatal("expected error for invalid OPENCODE_BASE_URL")
	}
}
