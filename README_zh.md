# lite-cpa

[English](README.md)

轻量 Go 网关，在 **OpenAI Chat Completions**、**OpenAI Responses**、**Anthropic Messages** 之间做协议转换。

单二进制 · 配置多 Key · 可选请求记录（SQLite / Postgres）· 无控制面。

基于 [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI)（Luis Pater / Router-For.ME）。

**作者：** Mieluoxxx

## 功能

- 三协议互转：`chat` ↔ `responses` ↔ `claude`
- 命名上游 + 多 Key 轮询 / 失败重试
- 按上游配置 header（含 `User-Agent`）
- 可选请求记录：SQLite 或 Postgres，带过期清理
- 支持二进制 / Docker / Compose 部署

## 文档

- 中文设计说明：[格式转换是如何实现的](docs/格式转换是如何实现的.md)
- English: [Protocol Conversion](docs/Protocol-Conversion.md)
- Wiki：https://github.com/Mieluoxxx/lite-cpa/wiki  
  （GitHub 要求先在网页创建第一个 Wiki 页面后，才能用 git 推送 wiki）

## 快速开始

```bash
cp config.example.yaml config.yaml
# 填写 api-keys 与上游凭证

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

### 接口

| Method | Path | 客户端格式 |
|---|---|---|
| GET | `/healthz` | 健康检查 |
| GET | `/v1/models` | 模型列表 |
| POST | `/v1/chat/completions` | openai-completions |
| POST | `/v1/responses` | openai-responses |
| POST | `/v1/messages` | anthropic-messages |

## 配置

上游字段顺序：

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

# 可选请求记录
request-log:
  enabled: false
  backend: "sqlite"       # sqlite | postgres
  retention: "168h"       # Go duration；每小时清理
  store-body: false       # 默认只记元数据；流式响应不存 body
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

未配置 `headers.User-Agent` 时，Go 默认发送 `Go-http-client/1.1`。

### 请求记录

| 字段 | 含义 |
|---|---|
| `enabled` | 默认 `false` |
| `backend` | `sqlite` 或 `postgres` |
| `retention` | Go duration，如 `168h`（7 天）；每小时清理 |
| `store-body` | 默认 `false`；为 true 时 body 截断 64KiB；**SSE 响应 body 不存储** |
| `sqlite.path` | 默认 `logs/requests.db` |
| `postgres.dsn` | postgres 后端必填 |

进程诊断日志仍只写 **stderr**（不进数据库）。

## Docker

```bash
cp config.example.yaml config.yaml
# 编辑 config.yaml

docker compose up -d --build
docker compose logs -f
docker compose down
```

挂载：

- `./config.yaml` → `/app/config.yaml`（只读）
- `./logs` → `/app/logs`（启用 sqlite 时）

监听 `8317`。默认时区 `Asia/Shanghai`（`TZ`）。

### Postgres 后端（可选）

1. 在 `docker-compose.yml` 取消注释 `postgres` 服务。
2. 在 `config.yaml` 中设置：

```yaml
request-log:
  enabled: true
  backend: postgres
  retention: "168h"
  store-body: false
  postgres:
    dsn: "postgres://litecpa:litecpa@postgres:5432/litecpa?sslmode=disable"
```

3. 取消注释 `lite-cpa` 的 `depends_on`。

### 普通 docker

```bash
docker build -t lite-cpa:local .
docker run --rm -p 8317:8317 \
  -v "$PWD/config.yaml:/app/config.yaml:ro" \
  -v "$PWD/logs:/app/logs" \
  lite-cpa:local
```

## 开发

```bash
gofmt -w .
go test ./...
go build -trimpath -ldflags='-s -w' -o lite-cpa ./cmd/lite-cpa
```

转换器目录：

```text
internal/translator/{from}/{to}/   # chat | claude | responses
```

## 性能说明

- 共享 `http.Transport`（HTTP/2、连接复用）
- 流式 scanner 上限 1MiB
- 每次尝试只做一次请求翻译
- 请求记录异步 + 有界队列；默认关闭
- 流式响应只记元数据（不累积完整响应 body）

## 贡献方式

欢迎贡献。

1. Fork 仓库并创建功能分支。
2. 保持改动小而聚焦。
3. 提交 PR 前运行：

```bash
gofmt -w .
go test ./...
go build -o /tmp/lite-cpa ./cmd/lite-cpa
```

4. 代码注释与 PR 描述优先使用英文。
5. 不要提交密钥（`config.yaml`、API Key、本地日志库）。
6. 涉及协议转换时，请补充或更新测试：
   - 同格式透传
   - 跨格式非流式
   - 跨格式流式（SSE 帧）

向 `main` 发起 Pull Request，并简要说明问题与修复。

## 许可证

MIT。版权声明：

- Luis Pater
- Router-For.ME（上游 CPA）
- Mieluoxxx

详见 [LICENSE](LICENSE)。
