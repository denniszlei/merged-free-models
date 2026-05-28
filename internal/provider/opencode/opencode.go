// Package opencode implements the OpenCode upstream provider.
//
// OpenCode requires a specific header set (User-Agent, X-Opencode-*) and
// addresses free models with a "-free" suffix. We strip the suffix when
// exposing the catalogue and re-append it when forwarding.
package opencode

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

const (
	Name       = "opencode"
	FreeSuffix = "-free"
	UserAgent  = "opencode/1.15.4 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.13"
)

type Provider struct {
	cfg          config.OpenCode
	client       *http.Client
	fetchTimeout time.Duration

	mu          sync.RWMutex
	models      []provider.Model
	lastRefresh time.Time
	lastErr     error
	httpStatus  int
}

func New(client *http.Client, cfg config.OpenCode, fetchTimeout time.Duration) *Provider {
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
		URL:         p.cfg.BaseURL + "/models",
		LastRefresh: p.lastRefresh,
		ModelCount:  len(p.models),
		HTTPStatus:  p.httpStatus,
		Healthy:     p.lastErr == nil && !p.lastRefresh.IsZero(),
		Endpoints:   []string{p.cfg.BaseURL + "/chat/completions", p.cfg.BaseURL + "/responses"},
	}
	if p.lastErr != nil {
		st.LastError = p.lastErr.Error()
	}
	return st
}

type upstreamModel struct {
	ID      string `json:"id"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type upstreamList struct {
	Object string          `json:"object"`
	Data   []upstreamModel `json:"data"`
}

func (p *Provider) Refresh(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.BaseURL+"/models", nil)
	if err != nil {
		p.setError(err, 0)
		return err
	}
	p.applyHeaders(req)
	// Refresh parses the response body. Drop Accept-Encoding so Go's HTTP
	// transport adds gzip on its own and transparently decodes; if we leave
	// the broader "gzip, deflate, br, zstd" set by applyHeaders in place,
	// Cloudflare may return brotli which the stdlib cannot decode and the
	// JSON parser then chokes on the binary stream.
	req.Header.Del("Accept-Encoding")

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

	// Defensive: if upstream ignores our Accept-Encoding and still returns
	// something we cannot decode, fail with a clear message instead of
	// letting json.Decoder report it as a stray binary character.
	if enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))); enc != "" && enc != "identity" && enc != "gzip" {
		err := fmt.Errorf("upstream returned unsupported Content-Encoding %q; only gzip is decoded", enc)
		p.setError(err, resp.StatusCode)
		return err
	}

	body, closeFn, err := httpx.DecodeBody(resp)
	if err != nil {
		p.setError(err, resp.StatusCode)
		return err
	}
	defer closeFn()

	var payload upstreamList
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		p.setError(err, resp.StatusCode)
		return err
	}

	models := FilterFree(payload.Data, p.cfg.IsFree)
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

// FilterFree converts upstream entries to provider.Model. When isFree is true,
// only entries ending in -free survive and the suffix is stripped from the
// exposed id. When false, every model passes through.
func FilterFree(input []upstreamModel, isFree bool) []provider.Model {
	out := make([]provider.Model, 0, len(input))
	for _, m := range input {
		display := m.ID
		if isFree {
			if !strings.HasSuffix(m.ID, FreeSuffix) {
				continue
			}
			display = strings.TrimSuffix(m.ID, FreeSuffix)
		}
		owner := m.OwnedBy
		if owner == "" {
			owner = Name
		}
		out = append(out, provider.Model{
			ID:       Name + "/" + display,
			Original: display,
			Provider: Name,
			OwnedBy:  owner,
			Created:  m.Created,
		})
	}
	return out
}

func (p *Provider) Forward(w http.ResponseWriter, r *http.Request, op provider.Op, originalModel string, body []byte) {
	upstreamModel := originalModel
	if p.cfg.IsFree && !strings.HasSuffix(upstreamModel, FreeSuffix) {
		upstreamModel += FreeSuffix
	}
	body = httpx.RewriteModel(body, upstreamModel)

	path := "/chat/completions"
	if op == provider.OpResponses {
		path = "/responses"
	}
	url := p.cfg.BaseURL + path

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error(), "internal_error")
		return
	}
	p.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	if httpx.WantsStream(body) {
		req.Header.Set("Accept", "text/event-stream")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, err.Error(), "upstream_error")
		return
	}
	defer resp.Body.Close()

	httpx.WriteUpstream(w, resp)
}

func (p *Provider) applyHeaders(req *http.Request) {
	key := p.cfg.APIKey
	if key == "" {
		key = "public"
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	req.Header.Set("X-Opencode-Client", "cli")
	req.Header.Set("X-Opencode-Project", "global")
	req.Header.Set("X-Opencode-Request", "msg_1")
	req.Header.Set("X-Opencode-Session", "ses_1")
}
