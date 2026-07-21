# lite-cpa

**中文 | English**

Slim Go gateway for protocol conversion among OpenAI Chat Completions, OpenAI Responses, and Anthropic Messages.  
轻量 Go 网关，在 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 之间做协议转换。

Single binary · config multi-key pools · optional request recording (SQLite / Postgres) · no control plane.  
单二进制 · 配置多 Key · 可选请求记录（SQLite / Postgres）· 无控制面。

Based on [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI) by Luis Pater / Router-For.ME.  
基于 [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI)（Luis Pater / Router-For.ME）。

Author / 作者: **Mieluoxxx**

---

## Features / 功能

| | EN | 中文 |
|---|---|---|
| Protocols | `chat` ↔ `responses` ↔ `claude` | 三协议互转 |
| Config | Named providers + multi-key RR/retry | 命名上游 + 多 Key 轮询/重试 |
| Headers | Per-provider headers (e.g. User-Agent) | 按上游配置 header（含 UA） |
| Request log | Optional SQLite or Postgres + retention | 可选 SQLite/Postgres + 过期清理 |
| Deploy | Binary / Docker / Compose | 二进制 / Docker / Compose |

---

## Quick start / 快速开始

```bash
cp config.example.yaml config.yaml
# edit api-keys + upstream credentials / 填写网关 key 与上游凭证

go run ./cmd/lite-cpa --config config.yaml

go build -trimpath -ldflags='-s -w' -o lite-cpa ./cmd/lite-cpa
./lite-cpa --config config.yaml
```

```bash
curl http://127.0.0.1:8317/v1/chat/completions \
  -H 'Authorization: Bearer sk-lite-gateway' \
  -H 'Content-Type: application/json' \
  -d '{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}'
```

### Endpoints / 接口

| Method | Path | Client format |
|---|---|---|
| GET | `/healthz` | health |
| GET | `/v1/models` | model list |
| POST | `/v1/chat/completions` | openai-completions |
| POST | `/v1/responses` | openai-responses |
| POST | `/v1/messages` | anthropic-messages |

---

## Configuration / 配置

Provider field order / 上游字段顺序:

1. `name`
2. `headers`
3. `base-url`
4. `api-key`
5. `models`

```yaml
host: ""
port: 8317
api-keys:
  - "sk-lite-gateway"
request-retry: 2
debug: false

# Optional request recording / 可选请求记录
request-log:
  enabled: false
  backend: "sqlite"       # sqlite | postgres
  retention: "168h"       # delete rows older than this (hourly cleanup)
  store-body: false       # metadata only by default; streams never store resp body
  sqlite:
    path: "logs/requests.db"
  # postgres:
  #   dsn: "postgres://litecpa:litecpa@postgres:5432/litecpa?sslmode=disable"

anthropic-messages:
  - name: anthropic-official
    headers:
      User-Agent: "claude-cli/2.1.63 (external, cli)"
    base-url: "https://api.anthropic.com"
    api-key: "sk-ant-..."
    models:
      - name: claude-sonnet-4
        alias: claude-sonnet-4
    # api-key-entries:
    #   - api-key: sk-1
    #     priority: 0

openai-responses:
  - name: openai-responses
    headers:
      User-Agent: "openai-python/1.40.0"
    base-url: "https://api.openai.com/v1"
    api-key: "sk-..."
    models:
      - name: gpt-5
        alias: gpt-5

openai-completions:
  - name: deepseek
    headers:
      User-Agent: "curl/8.7.1"
    base-url: "https://api.deepseek.com/v1"
    api-key: "sk-..."
    models:
      - name: deepseek-chat
        alias: deepseek-chat
```

### User-Agent

Unset `headers.User-Agent` → Go default `Go-http-client/1.1`.  
未配置时使用 Go 默认 UA。

### Request log / 请求记录

| Field | Meaning |
|---|---|
| `enabled` | default `false` |
| `backend` | `sqlite` or `postgres` |
| `retention` | Go duration, e.g. `168h` (7d). Hourly cleanup. |
| `store-body` | default `false`; if true, bodies capped at 64KiB; **SSE responses are not stored** |
| `sqlite.path` | default `logs/requests.db` |
| `postgres.dsn` | required for postgres backend |

Process diagnostics still go to **stderr** only (not the DB).  
进程诊断日志仍只写 **stderr**。

---

## Docker

```bash
cp config.example.yaml config.yaml
# edit config.yaml

docker compose up -d --build
docker compose logs -f
docker compose down
```

Mounts:

- `./config.yaml` → `/app/config.yaml` (read-only)
- `./logs` → `/app/logs` (sqlite path when enabled)

Image listens on `8317`. Timezone default `Asia/Shanghai` (`TZ`).

### Postgres backend (optional)

1. Uncomment the `postgres` service in `docker-compose.yml`.
2. Set in `config.yaml`:

```yaml
request-log:
  enabled: true
  backend: postgres
  retention: "168h"
  store-body: false
  postgres:
    dsn: "postgres://litecpa:litecpa@postgres:5432/litecpa?sslmode=disable"
```

3. Uncomment `depends_on` for `lite-cpa`.

### Plain docker

```bash
docker build -t lite-cpa:local .
docker run --rm -p 8317:8317 \
  -v "$PWD/config.yaml:/app/config.yaml:ro" \
  -v "$PWD/logs:/app/logs" \
  lite-cpa:local
```

---

## Development / 开发

```bash
gofmt -w .
go test ./...
go build -trimpath -ldflags='-s -w' -o lite-cpa ./cmd/lite-cpa
```

Layout (translators):

```text
internal/translator/{from}/{to}/   # chat|claude|responses
```

---

## Performance notes / 性能说明

- Shared `http.Transport` (HTTP/2, idle conn reuse)
- Stream scanner cap 1MiB
- Single request translation per attempt
- Request log async + bounded queue; disabled by default
- Streaming responses: metadata only (no response body accumulation)

---

## Contributions / 贡献记录

| Date | Author | Change |
|---|---|---|
| 2026-07 | Mieluoxxx | Initial independent slim gateway (protocol conversion, multi-key, Docker) |
| 2026-07 | Mieluoxxx | Named providers + headers/User-Agent profiles |
| 2026-07 | Mieluoxxx | Optional request-log with SQLite/Postgres + retention |
| 2026-07 | Mieluoxxx | Translator layout `{from}/{to}`; bilingual README |

Contributions welcome via PR. Keep changes small; run `go test ./...` before submit.  
欢迎 PR：保持改动小，提交前运行 `go test ./...`。

---

## License / 许可证

MIT. Copyright notices:

- Luis Pater
- Router-For.ME (upstream CPA)
- Mieluoxxx

See [LICENSE](LICENSE).
