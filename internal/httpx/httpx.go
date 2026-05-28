// Package httpx contains small HTTP helpers shared between providers.
package httpx

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

var hopByHop = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

func IsHopByHop(key string) bool {
	_, ok := hopByHop[http.CanonicalHeaderKey(key)]
	return ok
}

// CopyHeader copies non-hop-by-hop headers from src to dst.
func CopyHeader(dst, src http.Header) {
	for k, vs := range src {
		if IsHopByHop(k) {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// IsEventStream reports whether resp is server-sent events.
func IsEventStream(h http.Header) bool {
	return strings.Contains(strings.ToLower(h.Get("Content-Type")), "text/event-stream")
}

// WantsStream is true when the request body has "stream": true.
func WantsStream(body []byte) bool {
	var probe struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &probe)
	return probe.Stream
}

// RewriteModel replaces the "model" field of a JSON body with newModel.
// If the body is not valid JSON or has no "model" field, it is returned
// unchanged so that upstream errors stay visible to the client.
func RewriteModel(body []byte, newModel string) []byte {
	if len(bytes.TrimSpace(body)) == 0 {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	if _, ok := payload["model"]; !ok {
		return body
	}
	payload["model"] = newModel
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}

// WriteUpstream copies an upstream HTTP response to w. gzip is transparently
// decoded (and Content-Encoding stripped); other encodings (br, zstd) pass
// through. SSE responses are streamed with flushes on chunk boundaries.
func WriteUpstream(w http.ResponseWriter, resp *http.Response) {
	body, closeFn, err := decodeBody(resp)
	if err != nil {
		WriteError(w, http.StatusBadGateway, err.Error(), "upstream_error")
		return
	}
	defer closeFn()

	CopyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	flusher, _ := w.(http.Flusher)
	if flusher != nil && IsEventStream(resp.Header) {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				flusher.Flush()
			}
			if rerr != nil {
				return
			}
		}
	}
	_, _ = io.Copy(w, body)
}

func decodeBody(resp *http.Response) (io.Reader, func(), error) {
	if !strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		return resp.Body, func() {}, nil
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")
	return gz, func() { _ = gz.Close() }, nil
}

// DecodeBody returns a reader over resp.Body, transparently decompressing
// gzip and stripping the Content-Encoding/Length headers. The returned closer
// must be called when the caller is done with the reader.
func DecodeBody(resp *http.Response) (io.Reader, func(), error) {
	return decodeBody(resp)
}

// WriteError writes a JSON error envelope shaped like OpenAI's.
func WriteError(w http.ResponseWriter, status int, message, kind string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": message, "type": kind},
	})
}
