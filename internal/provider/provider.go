// Package provider defines the upstream-provider abstraction (Kilo, OpenCode)
// and the registry that merges their models behind a single OpenAI-compatible
// surface.
package provider

import (
	"context"
	"net/http"
	"time"
)

// Op identifies a forwarded operation. The registry chooses by request path;
// each provider maps Op to the right upstream URL.
type Op string

const (
	OpChat      Op = "chat"
	OpResponses Op = "responses"
)

// Model is a single entry in the merged catalogue.
type Model struct {
	ID       string // public, prefixed id (kilo/... or opencode/...)
	Original string // post-prefix-strip id used internally by the provider
	Provider string
	OwnedBy  string
	Created  int64
}

// OpenAIModel is the wire shape returned by GET /v1/models.
type OpenAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func (m Model) ToOpenAI() OpenAIModel {
	return OpenAIModel{ID: m.ID, Object: "model", Created: m.Created, OwnedBy: m.OwnedBy}
}

// Status is the per-provider health snapshot used by /status and the HTML page.
type Status struct {
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Healthy     bool      `json:"healthy"`
	LastRefresh time.Time `json:"last_refresh"`
	LastError   string    `json:"last_error,omitempty"`
	ModelCount  int       `json:"model_count"`
	HTTPStatus  int       `json:"http_status,omitempty"`
	Endpoints   []string  `json:"endpoints,omitempty"`
}

// Provider abstracts one upstream.
type Provider interface {
	Name() string
	Refresh(ctx context.Context) error
	Models() []Model
	Status() Status
	// Forward writes an upstream response into w. body is the (already-read)
	// request body; originalModel is the post-prefix-strip id.
	Forward(w http.ResponseWriter, r *http.Request, op Op, originalModel string, body []byte)
}
