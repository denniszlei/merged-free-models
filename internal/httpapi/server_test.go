package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/denniszlei/merged-free-models/internal/provider"
)

type fakeProvider struct {
	name      string
	models    []provider.Model
	status    provider.Status
	called    int
	lastOp    provider.Op
	lastModel string
	lastBody  string
}

func (f *fakeProvider) Name() string                      { return f.name }
func (f *fakeProvider) Refresh(_ context.Context) error   { return nil }
func (f *fakeProvider) Models() []provider.Model          { return f.models }
func (f *fakeProvider) Status() provider.Status           { return f.status }
func (f *fakeProvider) Forward(w http.ResponseWriter, r *http.Request, op provider.Op, original string, body []byte) {
	f.called++
	f.lastOp = op
	f.lastModel = original
	f.lastBody = string(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"forwarded":true}`))
}

func newTestServer(t *testing.T, apiKey string) (*httptest.Server, *fakeProvider, *fakeProvider) {
	t.Helper()
	k := &fakeProvider{
		name: "kilo",
		models: []provider.Model{
			{ID: "kilo/x-ai/grok:free", Original: "x-ai/grok:free", Provider: "kilo", OwnedBy: "kilo", Created: 1},
		},
		status: provider.Status{Name: "kilo", URL: "https://api.kilo.ai/models", Healthy: true, ModelCount: 1, LastRefresh: time.Now()},
	}
	o := &fakeProvider{
		name: "opencode",
		models: []provider.Model{
			{ID: "opencode/claude-opus-4-7", Original: "claude-opus-4-7", Provider: "opencode", OwnedBy: "opencode", Created: 2},
		},
		status: provider.Status{Name: "opencode", URL: "https://opencode.ai/zen/v1/models", Healthy: true, ModelCount: 1, LastRefresh: time.Now()},
	}
	registry := provider.NewRegistry(time.Minute, k, o)
	h := NewServer(registry, ":8080", apiKey)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, k, o
}

func TestModelsListMerged(t *testing.T) {
	srv, _, _ := newTestServer(t, "")
	resp, err := http.Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var payload struct {
		Object string                 `json:"object"`
		Data   []provider.OpenAIModel `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	if payload.Object != "list" {
		t.Fatalf("object=%q", payload.Object)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("expected 2 merged models, got %d", len(payload.Data))
	}
	if payload.Data[0].ID != "kilo/x-ai/grok:free" {
		t.Fatalf("first id=%q", payload.Data[0].ID)
	}
	if payload.Data[1].ID != "opencode/claude-opus-4-7" {
		t.Fatalf("second id=%q", payload.Data[1].ID)
	}
}

func TestChatRoutesToCorrectProvider(t *testing.T) {
	srv, k, o := newTestServer(t, "")
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"opencode/claude-opus-4-7","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if k.called != 0 || o.called != 1 {
		t.Fatalf("expected opencode to be called once; kilo=%d opencode=%d", k.called, o.called)
	}
	if o.lastOp != provider.OpChat || o.lastModel != "claude-opus-4-7" {
		t.Fatalf("op=%v model=%q", o.lastOp, o.lastModel)
	}
}

func TestResponsesRoutesToCorrectProvider(t *testing.T) {
	srv, k, o := newTestServer(t, "")
	resp, err := http.Post(srv.URL+"/v1/responses", "application/json",
		strings.NewReader(`{"model":"kilo/x-ai/grok:free","input":"hi"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if k.called != 1 || o.called != 0 {
		t.Fatalf("kilo should have been called; kilo=%d opencode=%d", k.called, o.called)
	}
	if k.lastOp != provider.OpResponses {
		t.Fatalf("op=%v", k.lastOp)
	}
}

func TestUnknownModelPrefixRejected(t *testing.T) {
	srv, _, _ := newTestServer(t, "")
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"x-ai/grok:free","messages":[]}`)) // no kilo/ prefix
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "must be prefixed") {
		t.Fatalf("body=%s", body)
	}
}

func TestAuthRequired(t *testing.T) {
	srv, _, _ := newTestServer(t, "expected-key")

	// Missing key -> 401
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"kilo/x-ai/grok:free"}`))
	if err != nil {
		t.Fatalf("post (no auth): %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// X-API-Key path
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"kilo/x-ai/grok:free"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-API-Key", "expected-key")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post (X-API-Key): %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("X-API-Key auth status=%d", resp2.StatusCode)
	}
	resp2.Body.Close()

	// Bearer path
	req2, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"kilo/x-ai/grok:free"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req2.Header.Set("Authorization", "Bearer expected-key")
	resp3, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("post (Bearer): %v", err)
	}
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("Bearer auth status=%d", resp3.StatusCode)
	}
	resp3.Body.Close()
}

func TestStatusJSONShape(t *testing.T) {
	srv, _, _ := newTestServer(t, "secret")
	resp, err := http.Get(srv.URL + "/status")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var s statusResponse
	_ = json.NewDecoder(resp.Body).Decode(&s)
	if !s.AuthRequired {
		t.Fatal("auth_required should be true when apiKey set")
	}
	if s.Merged.TotalModels != 2 || s.Merged.HealthyCount != 2 || s.Merged.ProviderCount != 2 {
		t.Fatalf("bad merged stats: %+v", s.Merged)
	}
	if len(s.Providers) != 2 {
		t.Fatalf("expected 2 providers in status, got %d", len(s.Providers))
	}
}

func TestStatusHTMLRenders(t *testing.T) {
	srv, _, _ := newTestServer(t, "")
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type=%q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"Merged Free Models", "kilo", "opencode", "/v1/models", "kilo/x-ai/grok:free"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("status page missing %q", want)
		}
	}
}

func TestHealthzReportsDegradedWhenNoneHealthy(t *testing.T) {
	bad := &fakeProvider{name: "bad", status: provider.Status{Name: "bad", Healthy: false}}
	registry := provider.NewRegistry(time.Minute, bad)
	srv := httptest.NewServer(NewServer(registry, ":8080", ""))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("healthz=%d", resp.StatusCode)
	}
}
