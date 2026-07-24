# lite-cpa

[English](README.md)

轻量 Go 网关，在 **OpenAI Chat Completions**、**OpenAI Responses**、**Anthropic Messages** 之间做协议转换。

单二进制 · 配置多 Key · 可选请求记录（SQLite / Postgres）· 本地 Web 管理端。

基于 [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI)（Luis Pater / Router-For.ME）。

**作者：** Mieluoxxx

## 功能

- 三协议互转：`chat` ↔ `responses` ↔ `claude`
- 命名上游 + 多 Key 轮询 / 失败重试
- 可选 **渠道亲和性**（new-api 式规则粘滞：按 header/body 钉住上游 key）
- 按上游配置 header（含 `User-Agent`）
- 可选请求记录：SQLite 或 Postgres，带过期清理
- 本地 Web 管理：配置、应用、回滚、运行状态与请求日志
- 支持二进制 / Docker / Compose 部署

## 文档

- [AGENTS.md](AGENTS.md) — **config 说明 + 部署**（二进制 / Docker / Compose）
- 中文设计说明：[格式转换是如何实现的](docs/格式转换是如何实现的.md)
- English: [Protocol Conversion](docs/Protocol-Conversion.md)
- [渠道亲和、重试与故障切换](docs/渠道亲和与重试.md)
- English: [Channel Affinity and Retry](docs/Channel-Affinity-and-Retry.md)
- Wiki：https://github.com/Mieluoxxx/lite-cpa/wiki  
  （GitHub 要求先在网页创建第一个 Wiki 页面后，才能用 git 推送 wiki）

### 用 AI 助手部署

复制下面这段给编程助手（Claude / Cursor / Codex 等）：

```text
请阅读 https://github.com/Mieluoxxx/lite-cpa/blob/main/AGENTS.md ，并按该文档在本机部署 lite-cpa。严格遵循其中的「Configuring config.yaml」与「Deployment」：优先用 docker compose 启动（或直接运行本地二进制），然后在本地 Web 管理端添加上游。官方 API 用 failover-mode: key，中转站用 provider。
```


## 快速开始

```bash
go run ./cmd/lite-cpa

go build -trimpath -ldflags='-s -w' -o lite-cpa ./cmd/lite-cpa
./lite-cpa
```

如果当前目录没有 `config.yaml`，lite-cpa 会自动生成一份可启动的本地配置：业务网关绑定 `127.0.0.1:8317`，默认网关调用密钥为 `sk-lite-local`，上游列表为空。此时 Web 管理端可正常进入，添加上游后才能发起模型请求。已存在但内容错误的配置文件不会被覆盖。

本地管理控制台默认位于 **http://127.0.0.1:8318/**。管理端不设登录，且会明文显示已配置密钥，因此必须将管理监听器限制在回环地址；可用 `--admin-addr off` 关闭。

配置保存带 revision 冲突检查并采用原子替换，上一版保留为 `config.yaml.bak`。合法变更可直接应用；即使修改业务网关的 `host` / `port`，管理页面也不会随业务监听器一起停止。

每个上游卡片都有「获取模型」按钮。管理端会请求上游模型列表，自动识别 Base URL 是否缺少 `/v1`，并通过可搜索的多选下拉框添加模型；也仍然可以手动填写模型和别名。

```bash
curl http://127.0.0.1:8317/v1/chat/completions \
  -H 'Authorization: Bearer sk-lite-local' \
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

完整字段说明见 [AGENTS.md](AGENTS.md#configuring-configyaml)。

Provider 字段顺序：

1. `name`
2. `proxy-url`
3. `priority`
4. `failover-mode`
5. `headers`
6. `base-url`
7. `api-key`
8. `models`

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
    proxy-url: ""
    priority: 0
    failover-mode: key
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
    proxy-url: ""
    priority: 0
    failover-mode: key
    headers:
      User-Agent: "openai-python/1.40.0"
    base-url: "https://api.openai.com/v1"
    api-key: "sk-..."
    models:
      - name: gpt-5
        alias: gpt-5

openai-completions:
  - name: deepseek
    proxy-url: ""
    priority: 0
    failover-mode: key
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


### Provider Fast 模式

`speed: fast` 是仅由管理员配置的 provider 策略，会作用于该 provider 的每一次请求：Anthropic 上游会收到 `speed: "fast"` 与 `Anthropic-Beta: fast-mode-2026-02-01`；OpenAI 上游会收到 `service_tier: "priority"`。未配置时，lite-cpa 会移除客户端携带的 fast tier 字段，客户端不能自行改变速度或计费。只应在确认上游支持其原生 fast tier 时配置。

```yaml
anthropic-messages:
  - name: anthropic-fast
    speed: fast
    # ...

openai-responses:
  - name: openai-fast
    speed: fast
    # ...
```

### 故障切换模式

挂在 **provider（`name`）** 上，不是全局。可重试错误（401/403/429/5xx）后：

```yaml
openai-completions:
  - name: relay-a
    base-url: https://a.example/v1
    failover-mode: provider   # 官方 → key；中转（如 ai.laysath.cn）→ provider
    api-key-entries:
      - api-key: sk-a1
      - api-key: sk-a2
    models:
      - { name: grok-4.5, alias: grok-4.5 }

  - name: relay-b
    base-url: https://b.example/v1
    # 省略 failover-mode => key
    api-key: sk-b1
    models:
      - { name: grok-4.5, alias: grok-4.5 }
```

| 模式 | 行为 |
|---|---|
| `key` | 换下一把未用 key（任意供应商） |
| `provider` | 跳过 **该 `name` 下全部 key**，切到另一家 |

多个 provider 使用相同客户端模型 alias 时会合并成一个 key 池。


### 渠道亲和

同一会话钉住同一上游 API key（保 prompt cache）。进程内存，**默认开启**。

默认 TTL **600s**；亲和失败允许换 key（`skip-retry` 默认 false）。

```yaml
# 默认全部：claude/gpt/gemini/grok/glm/kimi/qwen/minimax
# channel-affinity: true

# 只开部分模型族
channel-affinity: [claude, gpt, grok]

# 关闭
# channel-affinity: false
```

亲和身份总表：`internal/affinity/cli_sessions.go`。优先级：产品头（`X-Claude-Code-Session-Id`、`x-opencode-session`、`x-session-affinity`）→ Codex/Pi session/thread → `X-Session-Id` → `X-Client-Request-Id` → 协议 body（`/v1/messages` 归一化 `metadata.user_id`；responses/chat 用 `prompt_cache_key`）。详见 [渠道亲和与重试](docs/渠道亲和与重试.md)。无身份 → 普通轮询。

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
mkdir -p config
cp config.example.yaml config/config.yaml
# 编辑 config/config.yaml

docker compose up -d --build
docker compose logs -f
docker compose down
```

挂载：

- `./config` → `/app/config`（可读写；原子保存和 `.bak` 必需）
- `./logs` → `/app/logs`（启用 sqlite 时）

业务网关监听 `8317`；管理端仅发布到宿主机回环地址 `127.0.0.1:8318`。默认时区 `Asia/Shanghai`（`TZ`）。
在原生 Linux Docker 上，创建目录后需确保容器 uid `10001` 可以写入 `config/` 和 `logs/`（例如 `sudo chown -R 10001:10001 config logs`）。

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
docker run --rm -p 8317:8317 -p 127.0.0.1:8318:8318 \
  -v "$PWD/config:/app/config" \
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
