# Protocol Conversion (English summary)

lite-cpa converts among three formats:

- `openai` — Chat Completions (`/v1/chat/completions`)
- `openai-response` — Responses API (`/v1/responses`)
- `claude` — Anthropic Messages (`/v1/messages`)

**Pipeline:** Handler → pick upstream key by model alias → `TranslateRequest(from→to)` → HTTP upstream → `TranslateStream/NonStream(to→from)` → client.

**Registry:** `Register(from,to, requestFn, {Stream,NonStream})` in `internal/translator/register.go`.

**Layout:** packages under `internal/translator/{from}/{to}/`. Identity converters live at package root.

**Hop:** Claude→Responses request/response is implemented as Claude↔Chat↔Responses with **two separate stream state bags**.

See the Chinese page [格式转换是如何实现的](格式转换是如何实现的) for the full write-up.
