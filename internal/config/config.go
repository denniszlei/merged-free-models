// Package config loads runtime configuration from environment variables and
// an optional .env file.
package config

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Kilo struct {
	Enabled       bool
	ModelsURL     string
	ChatURL       string
	ResponsesURL  string
	Authorization string
	FreeMatch     string
}

type OpenCode struct {
	Enabled bool
	BaseURL string
	APIKey  string
	IsFree  bool
}

type Config struct {
	Addr              string
	ProxyAPIKey       string
	RefreshInterval   time.Duration
	ModelFetchTimeout time.Duration

	Kilo     Kilo
	OpenCode OpenCode
}

func Load(path string) (Config, error) {
	values, err := readDotEnv(path)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Addr:              getString(values, "ADDR", ":8080"),
		ProxyAPIKey:       getString(values, "PROXY_API_KEY", ""),
		RefreshInterval:   getDuration(values, "MODEL_REFRESH_INTERVAL", 10*time.Minute),
		ModelFetchTimeout: getDuration(values, "MODEL_FETCH_TIMEOUT", 30*time.Second),
		Kilo: Kilo{
			Enabled:       getBool(values, "KILO_ENABLED", true),
			ModelsURL:     getString(values, "KILO_MODELS_URL", "https://api.kilo.ai/api/gateway/models"),
			ChatURL:       getString(values, "KILO_CHAT_URL", "https://api.kilo.ai/api/gateway/v1/chat/completions"),
			ResponsesURL:  getString(values, "KILO_RESPONSES_URL", "https://api.kilo.ai/api/gateway/v1/responses"),
			Authorization: getString(values, "KILO_AUTHORIZATION", ""),
			FreeMatch:     strings.ToLower(getString(values, "KILO_FREE_MATCH", "free")),
		},
		OpenCode: OpenCode{
			Enabled: getBool(values, "OPENCODE_ENABLED", true),
			BaseURL: strings.TrimRight(getString(values, "OPENCODE_BASE_URL", "https://opencode.ai/zen/v1"), "/"),
			APIKey:  getString(values, "OPENCODE_API_KEY", "public"),
			IsFree:  getBool(values, "OPENCODE_IS_FREE", true),
		},
	}

	if cfg.RefreshInterval <= 0 {
		return Config{}, fmt.Errorf("MODEL_REFRESH_INTERVAL must be positive")
	}
	if cfg.ModelFetchTimeout <= 0 {
		return Config{}, fmt.Errorf("MODEL_FETCH_TIMEOUT must be positive")
	}
	if !cfg.Kilo.Enabled && !cfg.OpenCode.Enabled {
		return Config{}, fmt.Errorf("at least one provider must be enabled")
	}
	if cfg.OpenCode.Enabled {
		if u, err := url.Parse(cfg.OpenCode.BaseURL); err != nil || u.Scheme == "" || u.Host == "" {
			return Config{}, fmt.Errorf("OPENCODE_BASE_URL must be an absolute URL")
		}
	}

	return cfg, nil
}

func readDotEnv(path string) (map[string]string, error) {
	values := map[string]string{}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return values, nil
		}
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key != "" {
			values[key] = value
		}
	}
	return values, scanner.Err()
}

func lookup(values map[string]string, key string) (string, bool) {
	if v, ok := os.LookupEnv(key); ok {
		return v, true
	}
	v, ok := values[key]
	return v, ok
}

func getString(values map[string]string, key, fallback string) string {
	if v, ok := lookup(values, key); ok {
		return v
	}
	return fallback
}

func getBool(values map[string]string, key string, fallback bool) bool {
	raw, ok := lookup(values, key)
	if !ok || raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return v
}

func getDuration(values map[string]string, key string, fallback time.Duration) time.Duration {
	raw, ok := lookup(values, key)
	if !ok || raw == "" {
		return fallback
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		return time.Duration(seconds) * time.Second
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return v
}
