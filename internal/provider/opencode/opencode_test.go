package opencode

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/denniszlei/merged-free-models/internal/config"
	"github.com/denniszlei/merged-free-models/internal/provider"
)

func TestFilterFreeStripsSuffix(t *testing.T) {
	in := []upstreamModel{
		{ID: "claude-opus-4-7", OwnedBy: "opencode", Created: 1},
		{ID: "deepseek-v4-flash-free", Created: 2},
		{ID: "gpt-5-free", OwnedBy: "openai", Created: 3},
	}
	got := FilterFree(in, true)
	if len(got) != 2 {
		t.Fatalf("expected 2 free models, got %d: %+v", len(got), got)
	}
	if got[0].ID != "opencode/deepseek-v4-flash" || got[0].Original != "deepseek-v4-flash" {
		t.Fatalf("unexpected first: %+v", got[0])
	}
	if got[1].OwnedBy != "openai" {
		t.Fatalf("OwnedBy should be preserved from upstream when set, got %+v", got[1])
	}
}

func TestFilterFreeFalsePassesEverything(t *testing.T) {
	in := []upstreamModel{{ID: "claude-opus-4-7"}, {ID: "gpt-5"}}
	got := FilterFree(in, false)
	if len(got) != 2 || got[0].ID != "opencode/claude-opus-4-7" {
		t.Fatalf("unexpected output: %+v", got)
	}
}

func TestForwardAppendsFreeSuffixAndAppliesHeaders(t *testing.T) {
	var seenModel string
	var seenAuth, seenUA, seenSession string
	var seenPath string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenAuth = r.Header.Get("Authorization")
		seenUA = r.Header.Get("User-Agent")
		seenSession = r.Header.Get("X-Opencode-Session")

		b, _ := io.ReadAll(r.Body)
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(b, &payload)
		seenModel = payload.Model

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	cfg := config.OpenCode{
		Enabled: true,
		BaseURL: upstream.URL,
		APIKey:  "public",
		IsFree:  true,
	}
	p := New(upstream.Client(), cfg, 5*time.Second)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"opencode/deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}`))
	body, _ := io.ReadAll(req.Body)
	rec := httptest.NewRecorder()

	p.Forward(rec, req, provider.OpChat, "deepseek-v4-flash", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("forward status=%d body=%s", rec.Code, rec.Body.String())
	}
	if seenPath != "/chat/completions" {
		t.Fatalf("upstream path=%s", seenPath)
	}
	if seenModel != "deepseek-v4-flash-free" {
		t.Fatalf("expected -free suffix re-appended, got %q", seenModel)
	}
	if seenAuth != "Bearer public" {
		t.Fatalf("auth=%q", seenAuth)
	}
	if seenUA == "" || !strings.HasPrefix(seenUA, "opencode/") {
		t.Fatalf("missing OpenCode user-agent: %q", seenUA)
	}
	if seenSession != "ses_1" {
		t.Fatalf("session header=%q", seenSession)
	}
}

func TestForwardDoesNotDoubleAppendFreeSuffix(t *testing.T) {
	var seenModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(b, &payload)
		seenModel = payload.Model
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	cfg := config.OpenCode{BaseURL: upstream.URL, APIKey: "x", IsFree: true}
	p := New(upstream.Client(), cfg, time.Second)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses",
		strings.NewReader(`{"model":"opencode/foo-free","input":"hi"}`))
	body, _ := io.ReadAll(req.Body)
	p.Forward(httptest.NewRecorder(), req, provider.OpResponses, "foo-free", body)
	if seenModel != "foo-free" {
		t.Fatalf("expected unchanged model when suffix already present, got %q", seenModel)
	}
}

func TestRefreshFiltersFreeModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(upstreamList{
			Object: "list",
			Data: []upstreamModel{
				{ID: "claude-opus-4-7", Created: 1},
				{ID: "deepseek-v4-flash-free", Created: 2},
				{ID: "gpt-5-nano-free", Created: 3},
			},
		})
	}))
	defer upstream.Close()

	cfg := config.OpenCode{BaseURL: upstream.URL, APIKey: "public", IsFree: true}
	p := New(upstream.Client(), cfg, 5*time.Second)
	if err := p.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	models := p.Models()
	if len(models) != 2 {
		t.Fatalf("expected 2 free models, got %d: %+v", len(models), models)
	}
	if models[0].ID != "opencode/deepseek-v4-flash" {
		t.Fatalf("first model id=%q", models[0].ID)
	}
}

// Regression for the "invalid character '\x1b'" production bug: the refresh
// request must not advertise encodings (br, zstd) the stdlib can't decode,
// since Cloudflare otherwise picks brotli and our JSON parser chokes.
func TestRefreshDoesNotAdvertiseBrotliOrZstd(t *testing.T) {
	var ae string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ae = r.Header.Get("Accept-Encoding")
		_ = json.NewEncoder(w).Encode(upstreamList{Data: []upstreamModel{{ID: "foo-free"}}})
	}))
	defer upstream.Close()

	cfg := config.OpenCode{BaseURL: upstream.URL, APIKey: "public", IsFree: true}
	p := New(upstream.Client(), cfg, 5*time.Second)
	if err := p.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	for _, banned := range []string{"br", "zstd", "deflate"} {
		if strings.Contains(ae, banned) {
			t.Errorf("refresh Accept-Encoding must not include %q (got %q)", banned, ae)
		}
	}
}

// If the upstream ignores our hint and still returns a body encoded with
// something we cannot decode, surface a clear error instead of letting the
// JSON decoder report it as a stray binary character.
func TestRefreshReportsUnsupportedEncoding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "br")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte{0x1b, 0xff, 0xfe, 0xfd}) // looks like the start of a brotli stream
	}))
	defer upstream.Close()

	cfg := config.OpenCode{BaseURL: upstream.URL, APIKey: "public", IsFree: true}
	p := New(upstream.Client(), cfg, 5*time.Second)
	err := p.Refresh(t.Context())
	if err == nil {
		t.Fatal("expected an error when upstream returns an undecodable encoding")
	}
	if !strings.Contains(err.Error(), "Content-Encoding") {
		t.Errorf("expected encoding-aware error, got: %v", err)
	}
}
