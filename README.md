# lite-cpa

[中文文档](README_zh.md)

Slim Go gateway for protocol conversion among **OpenAI Chat Completions**, **OpenAI Responses**, and **Anthropic Messages**.

Single binary · config multi-key pools · optional request recording (SQLite / Postgres) · no control plane.

Based on [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI) by Luis Pater / Router-For.ME.

**Author:** Mieluoxxx

## Features

- Cross-format conversion: `chat` ↔ `responses` ↔ `claude`
- Named upstream providers with multi-key round-robin and retry
- Per-provider headers (including `User-Agent`)
- Optional request log: SQLite or Postgres with retention cleanup
- Binary / Docker / Compose deployment

## Docs

- [Protocol conversion design](docs/Protocol-Conversion.md)
- Chinese design notes: [格式转换是如何实现的](docs/格式转换是如何实现的.md)
- Wiki: https://github.com/Mieluoxxx/lite-cpa/wiki  
  (GitHub requires creating the first Wiki page in the web UI before git push works.)

## Quick start

```bash
cp config.example.yaml config.yaml
# edit api-keys and upstream credentials

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

### Endpoints

| Method | Path | Client format |
|---|---|---|
| GET | `/healthz` | health |
| GET | `/v1/models` | model list |
| POST | `/v1/chat/completions` | openai-completions |
| POST | `/v1/responses` | openai-responses |
| POST | `/v1/messages` | anthropic-messages |

## Configuration

Provider field order:

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

# Optional request recording
request-log:
  enabled: false
  backend: "sqlite"       # sqlite | postgres
  retention: "168h"       # Go duration; hourly cleanup
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

If `headers.User-Agent` is unset, Go sends the default `Go-http-client/1.1`.

### Request log

| Field | Meaning |
|---|---|
| `enabled` | default `false` |
| `backend` | `sqlite` or `postgres` |
| `retention` | Go duration, e.g. `168h` (7 days). Cleaned hourly. |
| `store-body` | default `false`; if true, bodies capped at 64KiB; **SSE response bodies are not stored** |
| `sqlite.path` | default `logs/requests.db` |
| `postgres.dsn` | required when backend is postgres |

Process diagnostics still go to **stderr** only (not the DB).

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

Listens on port `8317`. Default timezone `Asia/Shanghai` (`TZ`).

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

## Development

```bash
gofmt -w .
go test ./...
go build -trimpath -ldflags='-s -w' -o lite-cpa ./cmd/lite-cpa
```

Translator layout:

```text
internal/translator/{from}/{to}/   # chat | claude | responses
```

## Performance notes

- Shared `http.Transport` (HTTP/2, idle connection reuse)
- Stream scanner cap 1MiB
- Single request translation per attempt
- Request log is async with a bounded queue; disabled by default
- Streaming responses: metadata only (no response body accumulation)

## Contributing

Contributions are welcome.

1. Fork the repository and create a feature branch.
2. Keep changes small and focused.
3. Run checks before opening a PR:

```bash
gofmt -w .
go test ./...
go build -o /tmp/lite-cpa ./cmd/lite-cpa
```

4. Prefer English for code comments and PR descriptions.
5. Do not commit secrets (`config.yaml`, API keys, local log DBs).
6. For protocol conversion changes, add or update tests covering:
   - same-format passthrough
   - cross-format non-stream
   - cross-format stream (SSE framing)

Open a pull request against `main` with a short summary of the problem and the fix.

## License

MIT. Copyright notices:

- Luis Pater
- Router-For.ME (upstream CPA)
- Mieluoxxx

See [LICENSE](LICENSE).
