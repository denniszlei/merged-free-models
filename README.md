# merged-free-models

A single OpenAI-compatible Go service that merges free models from
[Kilo](https://kilo.ai) and [OpenCode](https://opencode.ai) into one
`/v1/models`, routes `/v1/chat/completions` and `/v1/responses` to the right
upstream by model-id prefix, and exposes a status page.

This is a fusion of the earlier [`kilo_auto`](../kilo_auto) and
[`opencode_proxy`](../opencode_proxy) projects.

## Quick start

```bash
cp .env.example .env
go run ./cmd/server
# open http://localhost:8080/
```

## Endpoints

| Method | Path                     | Auth     | Description                                                            |
| ------ | ------------------------ | -------- | ---------------------------------------------------------------------- |
| GET    | `/`                      | none     | HTML status page (auto-refreshes every 30s).                           |
| GET    | `/status`                | none     | Same data as `/`, but JSON.                                            |
| GET    | `/healthz`               | none     | 200 if any provider is healthy, 503 otherwise.                         |
| GET    | `/v1/models`             | none     | Merged catalogue. Each id is prefixed with its provider name.          |
| POST   | `/v1/chat/completions`   | required | Routed to the provider named in the `model` field's prefix.            |
| POST   | `/v1/responses`          | required | Same routing. OpenCode support is per-model; upstream errors pass through. |

Auth uses `Authorization: Bearer <PROXY_API_KEY>` (or `X-API-Key:`). When
`PROXY_API_KEY` is empty, auth is disabled.

## Model id prefixes

All ids returned by `/v1/models` are prefixed:

- `kilo/<original-kilo-id>` — e.g. `kilo/x-ai/grok-code-fast-1:optimized:free`
- `opencode/<stripped-id>` — e.g. `opencode/deepseek-v4-flash`
  (the upstream `-free` suffix is stripped on the way out and re-applied
  when forwarding.)

Send the prefixed id back in `/v1/chat/completions` and the proxy figures out
where to route.

```bash
curl -s http://localhost:8080/v1/models | jq '.data[].id'

curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $PROXY_API_KEY" \
  -d '{
    "model": "kilo/kilo-auto/free",
    "messages": [{"role":"user","content":"hi"}]
  }'
```

## Configuration

See [`.env.example`](.env.example). Every key can be set in `.env` or via real
environment variables (env wins). The most relevant ones:

| Key | Default | Notes |
| --- | --- | --- |
| `ADDR` | `:8080` | Listen address. |
| `PROXY_API_KEY` | _(empty)_ | Required by `/v1/chat/completions` and `/v1/responses` when set. |
| `MODEL_REFRESH_INTERVAL` | `10m` | How often to repoll each upstream's model list. |
| `MODEL_FETCH_TIMEOUT` | `30s` | Per-fetch timeout. |
| `KILO_ENABLED` | `true` | Set `false` to drop Kilo entirely. |
| `KILO_FREE_MATCH` | `free` | Substring match used when an upstream entry doesn't set `isFree`. |
| `OPENCODE_ENABLED` | `true` | Same idea for OpenCode. |
| `OPENCODE_IS_FREE` | `true` | When `true`, keep only ids ending in `-free`, strip the suffix in the public list, and re-add it when forwarding. |

## Docker

Images are published on every tag at `ghcr.io/denniszlei/merged-free-models`.

```bash
docker run --rm -p 8080:8080 \
  -e PROXY_API_KEY=change-me \
  ghcr.io/denniszlei/merged-free-models:latest
```

The image is distroless/nonroot, so it has no shell. Pass configuration via
`-e KEY=VALUE` rather than mounting `.env`.

## Releases

Tag and push:

```bash
git tag v0.1.0 && git push --tags
```

GitHub Actions builds binaries for linux/macOS/windows on amd64/arm64 (where
applicable), uploads them to the GitHub Release, and publishes a multi-arch
Docker image to GHCR.

## Layout

```
cmd/server/                   entry point
internal/config/              .env + env loader, provider sections
internal/provider/            Provider interface + Registry
internal/provider/kilo/       Kilo upstream: refresh + forward
internal/provider/opencode/   OpenCode upstream: refresh + forward (with -free rewrite)
internal/httpapi/             HTTP server, routes, auth, status page
internal/httpx/                shared HTTP helpers (header copy, gzip decode, model rewrite)
internal/version/             build metadata injected via -ldflags
```

## License

AGPL-3.0. See [`LICENSE`](LICENSE). Inherited from the upstream `opencode_proxy`
project that this is partially derived from.
