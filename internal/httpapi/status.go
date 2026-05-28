package httpapi

import (
	_ "embed"
	"encoding/json"
	"html/template"
	"net/http"
	"time"

	"github.com/denniszlei/merged-free-models/internal/provider"
	"github.com/denniszlei/merged-free-models/internal/version"
)

//go:embed templates/status.html
var statusHTMLTemplate string

var statusTmpl = template.Must(template.New("status").Parse(statusHTMLTemplate))

type mergedStats struct {
	TotalModels   int      `json:"total_models"`
	HealthyCount  int      `json:"healthy_count"`
	ProviderCount int      `json:"provider_count"`
	Endpoints     []string `json:"endpoints"`
}

type statusResponse struct {
	Service      string            `json:"service"`
	Version      string            `json:"version"`
	Commit       string            `json:"commit"`
	BuildDate    string            `json:"build_date"`
	StartedAt    time.Time         `json:"started_at"`
	Uptime       string            `json:"uptime"`
	Addr         string            `json:"addr"`
	AuthRequired bool              `json:"auth_required"`
	Providers    []provider.Status `json:"providers"`
	Merged       mergedStats       `json:"merged"`
	Models       []provider.Model  `json:"models,omitempty"`
}

func (s *Server) buildStatus() statusResponse {
	statuses := s.registry.Statuses()
	models := s.registry.Models()
	healthy := 0
	for _, st := range statuses {
		if st.Healthy {
			healthy++
		}
	}
	return statusResponse{
		Service:      "merged-free-models",
		Version:      version.Version,
		Commit:       version.Commit,
		BuildDate:    version.Date,
		StartedAt:    s.startedAt,
		Uptime:       time.Since(s.startedAt).Truncate(time.Second).String(),
		Addr:         s.addr,
		AuthRequired: s.apiKey != "",
		Providers:    statuses,
		Merged: mergedStats{
			TotalModels:   len(models),
			HealthyCount:  healthy,
			ProviderCount: len(statuses),
			Endpoints:     []string{"/v1/models", "/v1/chat/completions", "/v1/responses"},
		},
		Models: models,
	}
}

func (s *Server) handleStatusJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.buildStatus())
}

func (s *Server) handleStatusHTML(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := statusTmpl.Execute(w, s.buildStatus()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	statuses := s.registry.Statuses()
	healthy := false
	for _, st := range statuses {
		if st.Healthy {
			healthy = true
			break
		}
	}
	code := http.StatusOK
	state := "ok"
	if !healthy {
		code = http.StatusServiceUnavailable
		state = "degraded"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": state})
}
