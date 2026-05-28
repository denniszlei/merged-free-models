// Package kilo implements the Kilo upstream provider.
//
// Kilo serves both /v1/chat/completions and /v1/responses natively, so this
// provider is a thin reverse proxy with model-id rewriting.
package kilo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/denniszlei/merged-free-models/internal/config"
	"github.com/denniszlei/merged-free-models/internal/httpx"
	"github.com/denniszlei/merged-free-models/internal/provider"
)

const Name = "kilo"

type Provider struct {
	cfg          config.Kilo
	client       *http.Client
	fetchTimeout time.Duration

	mu          sync.RWMutex
	models      []provider.Model
	lastRefresh time.Time
	lastErr     error
	httpStatus  int
}

func New(client *http.Client, cfg config.Kilo, fetchTimeout time.Duration) *Provider {
	return &Provider{cfg: cfg, client: client, fetchTimeout: fetchTimeout}
}

func (p *Provider) Name() string { return Name }

func (p *Provider) Models() []provider.Model {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]provider.Model, len(p.models))
	copy(out, p.models)
	return out
}

func (p *Provider) Status() provider.Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	st := provider.Status{
		Name:        Name,
		URL:         p.cfg.ModelsURL,
		LastRefresh: p.lastRefresh,
		ModelCount:  len(p.models),
		HTTPStatus:  p.httpStatus,
		Healthy:     p.lastErr == nil && !p.lastRefresh.IsZero(),
		Endpoints:   []string{p.cfg.ChatURL, p.cfg.ResponsesURL},
	}
	if p.lastErr != nil {
		st.LastError = p.lastErr.Error()
	}
	return st
}

type upstreamList struct {
	Data []upstreamModel `json:"data"`
}

type upstreamModel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Created int64  `json:"created"`
	IsFree  bool   `json:"isFree"`
}

func (p *Provider) Refresh(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.ModelsURL, nil)
	if err != nil {
		p.setError(err, 0)
		return err
	}
	if p.cfg.Authorization != "" {
		req.Header.Set("Authorization", p.cfg.Authorization)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		p.setError(err, 0)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("upstream models returned %s", resp.Status)
		p.setError(err, resp.StatusCode)
		return err
	}

	var payload upstreamList
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		p.setError(err, resp.StatusCode)
		return err
	}

	models := FilterFree(payload.Data, p.cfg.FreeMatch)
	p.mu.Lock()
	p.models = models
	p.lastRefresh = time.Now()
	p.lastErr = nil
	p.httpStatus = resp.StatusCode
	p.mu.Unlock()
	return nil
}

func (p *Provider) setError(err error, status int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastErr = err
	p.httpStatus = status
}

// FilterFree returns models the upstream marks as free (isFree=true) or whose
// id/name contains match (case-insensitive). Match is ignored when empty.
func FilterFree(input []upstreamModel, match string) []provider.Model {
	match = strings.ToLower(match)
	out := make([]provider.Model, 0, len(input))
	for _, m := range input {
		if !isFree(m, match) {
			continue
		}
		out = append(out, provider.Model{
			ID:       Name + "/" + m.ID,
			Original: m.ID,
			Provider: Name,
			OwnedBy:  Name,
			Created:  m.Created,
		})
	}
	return out
}

func isFree(m upstreamModel, match string) bool {
	if m.IsFree {
		return true
	}
	if match == "" {
		return false
	}
	return strings.Contains(strings.ToLower(m.ID), match) || strings.Contains(strings.ToLower(m.Name), match)
}

func (p *Provider) Forward(w http.ResponseWriter, r *http.Request, op provider.Op, originalModel string, body []byte) {
	url := p.cfg.ChatURL
	if op == provider.OpResponses {
		url = p.cfg.ResponsesURL
	}

	body = httpx.RewriteModel(body, originalModel)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error(), "internal_error")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if httpx.WantsStream(body) {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if p.cfg.Authorization != "" {
		req.Header.Set("Authorization", p.cfg.Authorization)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, err.Error(), "upstream_error")
		return
	}
	defer resp.Body.Close()

	httpx.WriteUpstream(w, resp)
}
