package kilo

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

func TestFilterFreeAcceptsIsFreeAndSubstring(t *testing.T) {
	input := []upstreamModel{
		{ID: "kilo-auto/frontier", Name: "Auto Frontier", Created: 1},
		{ID: "kilo-auto/free", Name: "Auto Free", Created: 2},
		{ID: "vendor/model", Name: "Free Tier Model", Created: 3},
		{ID: "vendor/flagged", Name: "Paid", Created: 4, IsFree: true},
	}
	got := FilterFree(input, "free")
	if len(got) != 3 {
		t.Fatalf("expected 3 free models, got %d", len(got))
	}
	if got[0].ID != "kilo/kilo-auto/free" || got[0].Original != "kilo-auto/free" || got[0].OwnedBy != "kilo" {
		t.Fatalf("unexpected first model: %+v", got[0])
	}
	if got[2].Original != "vendor/flagged" {
		t.Fatalf("isFree=true model should be included: %+v", got[2])
	}
}

func TestFilterFreeRejectsAllWhenMatchEmptyAndNoFlag(t *testing.T) {
	got := FilterFree([]upstreamModel{{ID: "a"}, {ID: "b"}}, "")
	if len(got) != 0 {
		t.Fatalf("expected 0 models with empty match and no isFree flag, got %d", len(got))
	}
}

func TestRefreshPopulatesAndForwardRewritesModel(t *testing.T) {
	var lastBody string
	var lastPath string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		switch r.URL.Path {
		case "/models":
			_ = json.NewEncoder(w).Encode(upstreamList{Data: []upstreamModel{
				{ID: "x-ai/grok:free", Name: "Grok", Created: 42, IsFree: true},
			}})
		case "/v1/chat/completions":
			b, _ := io.ReadAll(r.Body)
			lastBody = string(b)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/responses":
			b, _ := io.ReadAll(r.Body)
			lastBody = string(b)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":"resp"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := config.Kilo{
		ModelsURL:    upstream.URL + "/models",
		ChatURL:      upstream.URL + "/v1/chat/completions",
		ResponsesURL: upstream.URL + "/v1/responses",
		FreeMatch:    "free",
	}
	p := New(upstream.Client(), cfg, 5*time.Second)

	if err := p.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	models := p.Models()
	if len(models) != 1 || models[0].ID != "kilo/x-ai/grok:free" {
		t.Fatalf("expected one kilo/x-ai/grok:free model, got %+v", models)
	}
	st := p.Status()
	if !st.Healthy || st.ModelCount != 1 {
		t.Fatalf("status not healthy: %+v", st)
	}

	// Chat forward should rewrite the prefixed id back to the original.
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"kilo/x-ai/grok:free","messages":[{"role":"user","content":"hi"}]}`))
	body, _ := io.ReadAll(req.Body)
	rec := httptest.NewRecorder()
	p.Forward(rec, req, provider.OpChat, "x-ai/grok:free", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat forward status=%d body=%s", rec.Code, rec.Body.String())
	}
	if lastPath != "/v1/chat/completions" {
		t.Fatalf("unexpected upstream path: %s", lastPath)
	}
	if !strings.Contains(lastBody, `"model":"x-ai/grok:free"`) {
		t.Fatalf("upstream did not see rewritten model: %s", lastBody)
	}

	// Responses forward should hit the other URL.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/responses",
		strings.NewReader(`{"model":"kilo/x-ai/grok:free","input":"hi"}`))
	body2, _ := io.ReadAll(req2.Body)
	rec2 := httptest.NewRecorder()
	p.Forward(rec2, req2, provider.OpResponses, "x-ai/grok:free", body2)
	if rec2.Code != http.StatusOK || lastPath != "/v1/responses" {
		t.Fatalf("responses forward routed to %s with status %d", lastPath, rec2.Code)
	}
}
