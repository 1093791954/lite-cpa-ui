# AGENTS.md

lite-cpa is a slim Go gateway: protocol conversion among OpenAI Chat Completions, OpenAI Responses, and Anthropic Messages; multi-key / multi-provider routing; optional channel affinity and request logging. No control plane, no OAuth account reverse-proxy.

## Scope

- Keep the binary small and the hot path simple.
- Prefer config-driven behavior over new code paths.
- Comments and code identifiers: English. User-facing docs may be bilingual when the file already is (`README.md` / `README_zh.md`, `config.example.yaml`).
- Do not invent OAuth / account sticky / Redis affinity unless explicitly requested.
- After Go edits: `gofmt` and targeted `go test` on touched packages.

## Layout

| Path | Role |
|---|---|
| `cmd/lite-cpa/` | Entrypoint |
| `internal/server/` | HTTP handlers, retry / failover loop |
| `internal/pool/` | Build registry from config; key selection |
| `internal/registry/` | Model alias → `UpstreamKey` pool (merged by alias) |
| `internal/affinity/` | Sticky key memory + CLI session catalog (`cli_sessions.go`) |
| `internal/executor/` | Upstream HTTP + stream/non-stream |
| `internal/translator/` | Format conversion (`chat` ↔ `responses` ↔ `claude`) |
| `internal/config/` | YAML load, defaults, validation |
| `internal/reqlog/` | Optional SQLite / Postgres request log |
| `config.example.yaml` | Minimal scaffold (details live in this file) |
| `docs/Channel-Affinity-and-Retry.md` | Affinity + retry + failover (EN) |
| `docs/渠道亲和与重试.md` | 亲和 + 重试 + 切换（中文） |

## Local commands

```bash
cp config.example.yaml config.yaml   # then fill secrets
go run ./cmd/lite-cpa --config config.yaml
go test ./internal/...
go build -trimpath -ldflags='-s -w' -o lite-cpa ./cmd/lite-cpa
```

## Endpoints

| Method | Path | Client format |
|---|---|---|
| GET | `/healthz` | health |
| GET | `/v1/models` | model list |
| POST | `/v1/chat/completions` | openai chat |
| POST | `/v1/responses` | openai responses |
| POST | `/v1/messages` | anthropic messages |

---

## Configuring `config.yaml`

Copy `config.example.yaml` → `config.yaml`. **At least one** of `anthropic-messages`, `openai-responses`, `openai-completions` must be present with a valid key.

### Top-level fields

| Field | Default | Meaning |
|---|---|---|
| `host` | `""` (all interfaces) | Bind host |
| `port` | `8317` | Bind port |
| `api-keys` | **required** | Gateway auth keys (Bearer / `x-api-key`). Not upstream keys. |
| `request-retry` | `0` if unset (example uses `2`) | Extra attempts after first failure; total tries ≤ number of keys for that model |
| `debug` | `false` | Verbose stderr |
| `max-body-bytes` | `32MiB` | Inbound body limit |
| `proxy-url` | empty | Global outbound proxy fallback if provider has none |
| `channel-affinity` | on by default | See [Channel affinity](#channel-affinity) |
| `request-log` | disabled | See [Request log](#request-log) |

### Provider sections

Three lists (same `Provider` shape):

- `anthropic-messages` → upstream Anthropic `/v1/messages` (`Provider` type `claude`)
- `openai-responses` → upstream OpenAI `/responses` (`openai-response`)
- `openai-completions` → upstream OpenAI-compatible `/chat/completions` (`openai`)

#### Field order (preferred)

1. `name`
2. `proxy-url`
3. `priority`
4. `failover-mode`
5. `headers`
6. `speed`
7. `base-url`
8. `api-key` and/or `api-key-entries`
9. `models`

#### Field reference

| Field | Level | Meaning |
|---|---|---|
| `name` | provider | Stable id. Failover unit when `failover-mode: provider`. |
| `proxy-url` | provider only | Outbound proxy for **all** keys under this name. Not per-key. Empty → global `proxy-url` → env. |
| `priority` | provider / entry | Lower number = higher priority. |
| `failover-mode` | provider | `key` (default) or `provider`. See [Failover](#failover). |
| `headers` | provider | Extra upstream headers. `User-Agent` recommended; unset → Go default `Go-http-client/1.1`. |
| `speed` | provider | Optional `fast` only. Administrator-controlled: Anthropic sends `speed: "fast"` plus `fast-mode-2026-02-01`; OpenAI sends `service_tier: "priority"`. When absent, client-selected fast tiers are removed. Configure only for upstreams that support their native tier. |
| `base-url` | provider | Upstream base (no trailing slash required). |
| `api-key` | provider | Single key. Ignored if `api-key-entries` is non-empty. |
| `api-key-entries[]` | provider | Multi-key pool: `api-key`, optional `priority`. |
| `models[]` | provider | `name` = upstream model id; `alias` = client-visible id (defaults to `name`). |

#### Multi-provider same model

If two providers register the **same client `alias`**, keys are **merged** into one pool. Selection uses priority + round-robin; failover uses each key’s provider `name` + `failover-mode`.

### Failover

Retriable errors: **401 / 403 / 429 / ≥500** and network errors.

| `failover-mode` | Behavior | Use when |
|---|---|---|
| `key` | Try next unused key (may stay on same site) | Official multi-key, key-level rate limits |
| `provider` | One failure skips **all keys** with that `name` | Relay / 中转站 (one site down ⇒ all keys dead) |

Aliases accepted: `keys` → `key`; `supplier` / `site` → `provider`.

**Recommendation:** official APIs → `key`; relays (e.g. `ai.laysath.cn`) → `provider`.

### Channel affinity

Sticky routing pins a successful `UpstreamKey.ID` so the same session hits the same upstream key (prompt cache). Process-local memory only (no Redis).

**Defaults**

- Enabled when omitted
- `default-ttl-seconds`: **600**
- Family rules use `skip-retry-on-failure: false` (failure may rotate; success re-pins)
- `switch-on-success`: true

**YAML forms**

```yaml
# default families (claude, gpt, gemini, grok, glm, kimi, qwen, minimax)
channel-affinity: true

# subset
channel-affinity: [claude, gpt, grok]

# off
channel-affinity: false

# mapping form
channel-affinity:
  models: [claude, grok]
  default-ttl-seconds: 600
```

**Identity extraction order** (catalog: `internal/affinity/cli_sessions.go`)

1. Sticky session headers (first non-empty), including:
   - Claude Code: `X-Claude-Code-Session-Id`
   - OpenCode: `x-opencode-session`, `x-session-affinity`, `X-Session-Id`
   - Codex / Pi / oh-my-pi: `session-id` / `session_id`, `thread-id` / `thread_id`, `X-Client-Request-Id`
   - Amp: `X-Amp-Thread-Id`
2. Protocol body by path:
   - `/v1/messages` → `metadata.user_id` (Claude/OpenCode formats normalized to session UUID) then `prompt_cache_key`
   - `/v1/responses` and chat completions → `prompt_cache_key` then `metadata.user_id`
3. No identity field → no stickiness; normal round-robin

Priority CLIs covered in the catalog: claude-code, codex, pi, oh-my-pi, opencode, kimi-code, mimo-code, zcode.

**Model family match:** substring, case-insensitive (e.g. `proxy-claude-x` matches `claude`).

Advanced: set `rules:` to fully override generated family rules (see `internal/config` types). Empty `rules` with enabled affinity expands from `models` / defaults.

### Request log

Optional structured log (stderr diagnostics always available).

| Field | Meaning |
|---|---|
| `enabled` | default false |
| `backend` | `sqlite` or `postgres` |
| `retention` | Go duration, e.g. `168h`; hourly cleanup |
| `store-body` | default false; bodies capped 64KiB; **streams omit response body** |
| `sqlite.path` | default `logs/requests.db` |
| `postgres.dsn` | required when backend is postgres |

### Minimal recipes

**Official only**

```yaml
api-keys: ["sk-gateway"]
request-retry: 2
openai-responses:
  - name: openai-official
    failover-mode: key
    headers:
      User-Agent: "codex_cli_rs/0.144.1"
    base-url: "https://api.openai.com/v1"
    api-key: "sk-..."
    models:
      - name: gpt-5
        alias: gpt-5
```

**Official + relay (same alias, provider failover on relay)**

```yaml
api-keys: ["sk-gateway"]
request-retry: 3
openai-responses:
  - name: openai-official
    priority: 0
    failover-mode: key
    headers:
      User-Agent: "codex_cli_rs/0.144.1"
    base-url: "https://api.openai.com/v1"
    api-key: "sk-official"
    models:
      - { name: gpt-5, alias: gpt-5 }

  - name: laysath
    priority: 1
    failover-mode: provider
    headers:
      User-Agent: "codex_cli_rs/0.144.1"
    base-url: "https://ai.laysath.cn/v1"
    api-key-entries:
      - { api-key: sk-laysath-1 }
      - { api-key: sk-laysath-2 }
    models:
      - { name: gpt-5, alias: gpt-5 }
      - { name: grok-4.5, alias: grok-4.5 }
```

**Claude official**

```yaml
anthropic-messages:
  - name: anthropic-official
    failover-mode: key
    headers:
      User-Agent: "claude-cli/2.1.63 (external, cli)"
    base-url: "https://api.anthropic.com"
    api-key: "sk-ant-..."
    models:
      - { name: claude-sonnet-4, alias: claude-sonnet-4 }
```

### Secrets

- Never commit `config.yaml` with real keys.
- Prefer env-mounted secrets in Docker; keep example placeholders only.

---

## Deployment

Repository: https://github.com/Mieluoxxx/lite-cpa  
Default listen: **8317**. Config path flag: `--config` (default used by Docker: `/app/config.yaml`).

### Prerequisites

1. Copy and edit config (never commit secrets):

```bash
cp config.example.yaml config.yaml
# set api-keys + at least one provider (see Configuring config.yaml)
```

2. Health check after start:

```bash
curl -sS http://127.0.0.1:8317/healthz
```

### Binary

```bash
go build -trimpath -ldflags='-s -w' -o lite-cpa ./cmd/lite-cpa
./lite-cpa --config config.yaml
```

Optional: run under systemd/supervisor; redirect stdout/stderr for process logs. Request DB (if enabled) is separate under `request-log`.

### Docker Compose (recommended)

```bash
cp config.example.yaml config.yaml
# edit config.yaml

docker compose up -d --build
docker compose logs -f
docker compose down
```

| Host path | Container | Mode |
|---|---|---|
| `./config.yaml` | `/app/config.yaml` | read-only |
| `./logs` | `/app/logs` | rw (sqlite request-log) |

- Image: multi-stage `golang:1.26-alpine` → `alpine:3.23`, non-root user `lite` (uid 10001).
- Env: `TZ=Asia/Shanghai` (override as needed).
- Restart after config edits: `docker compose restart` (config is not hot-reloaded).

#### Optional Postgres for request-log

1. Uncomment the `postgres` service in `docker-compose.yml`.
2. Uncomment `depends_on` on `lite-cpa`.
3. In `config.yaml`:

```yaml
request-log:
  enabled: true
  backend: postgres
  retention: "168h"
  store-body: false
  postgres:
    dsn: "postgres://litecpa:litecpa@postgres:5432/litecpa?sslmode=disable"
```

### Plain Docker

```bash
docker build -t lite-cpa:local .
docker run --rm -p 8317:8317 \
  -e TZ=Asia/Shanghai \
  -v "$PWD/config.yaml:/app/config.yaml:ro" \
  -v "$PWD/logs:/app/logs" \
  lite-cpa:local
```

Entrypoint: `lite-cpa --config /app/config.yaml`.

### Reverse proxy notes

- Preserve client body size limits if you raise `max-body-bytes`.
- For streaming, disable response buffering (e.g. nginx `proxy_buffering off`).
- Do not strip headers used for affinity: `session-id` / `session_id` / `X-Session-Id` / `Thread-Id` / `x-opencode-session` / `X-Claude-Code-Session-Id` / `X-Client-Request-Id` / `x-session-affinity` (nginx by default drops headers with underscores unless `underscores_in_headers on`).

### Checklist

- [ ] `config.yaml` has gateway `api-keys` and ≥1 upstream provider
- [ ] Official vs relay: `failover-mode` set (`key` vs `provider`)
- [ ] `curl /healthz` and one real model call succeed
- [ ] Logs: process → stderr; optional request-log → sqlite/postgres path


---

## Coding notes

- Hot path: `server.handleProxy` → affinity lookup → `selector.Pick` → `executor.Execute` → affinity record/clear.
- Sticky pin is `UpstreamKey.ID` (`{name}-{index}`), not provider name alone.
- When `failover-mode: provider`, a failed key adds its `Name` to `skipSuppliers` for the rest of that request.
- Translator registry: `internal/translator/register.go`; path is client format, upstream type comes from selected key.
- Request log is async and drop-on-full; do not block the request path on insert.
