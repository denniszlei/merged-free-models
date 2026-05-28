// Package httpapi exposes the OpenAI-compatible surface and a status page.
package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/denniszlei/merged-free-models/internal/httpx"
	"github.com/denniszlei/merged-free-models/internal/provider"
)

type Server struct {
	registry  *provider.Registry
	addr      string
	apiKey    string
	startedAt time.Time
	handler   http.Handler
}

func NewServer(registry *provider.Registry, addr, apiKey string) http.Handler {
	s := &Server{
		registry:  registry,
		addr:      addr,
		apiKey:    apiKey,
		startedAt: time.Now(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleStatusHTML)
	mux.HandleFunc("GET /status", s.handleStatusJSON)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.Handle("POST /v1/chat/completions", s.requireAPIKey(s.handleForward(provider.OpChat)))
	mux.Handle("POST /v1/responses", s.requireAPIKey(s.handleForward(provider.OpResponses)))
	s.handler = mux
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	models := s.registry.Models()
	out := make([]provider.OpenAIModel, 0, len(models))
	for _, m := range models {
		out = append(out, m.ToOpenAI())
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": out})
}

func (s *Server) handleForward(op provider.Op) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
			return
		}
		var probe struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(body, &probe); err != nil || probe.Model == "" {
			httpx.WriteError(w, http.StatusBadRequest,
				"request body must be JSON with a non-empty \"model\" field",
				"invalid_request_error")
			return
		}
		p, original, ok := s.registry.Lookup(probe.Model)
		if !ok {
			httpx.WriteError(w, http.StatusBadRequest,
				"unknown model \""+probe.Model+"\"; ids must be prefixed with a provider (e.g. kilo/... or opencode/...)",
				"invalid_request_error")
			return
		}
		p.Forward(w, r, op, original, body)
	}
}

func (s *Server) requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.apiKey == "" || requestAPIKey(r) == s.apiKey {
			next.ServeHTTP(w, r)
			return
		}
		httpx.WriteError(w, http.StatusUnauthorized, "invalid API key", "authentication_error")
	})
}

func requestAPIKey(r *http.Request) string {
	if v := r.Header.Get("X-API-Key"); v != "" {
		return v
	}
	v := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(v), "bearer ") {
		return strings.TrimSpace(v[len("bearer "):])
	}
	return v
}
