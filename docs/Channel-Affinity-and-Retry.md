# Channel Affinity, Retry, and Failover

How lite-cpa pins sessions to upstream API keys, when it retries, and how relay sites should fail over.

## Why affinity exists

Multi-key and multi-provider pools improve availability, but **prompt cache and session state are usually bound to one upstream credential**. If consecutive turns of the same CLI session land on different keys (or different relays), cache hits drop and behavior can diverge.

Channel affinity is a **success memory**: after a request succeeds, remember which `UpstreamKey` served that session identity, and prefer it next time.

It is **not** ordinary load balancing. Priority and round-robin still apply when there is no pin, or when the pin is cleared / expired.

## What gets pinned

lite-cpa is API-key only (no OAuth accounts). The sticky value is:

```text
session identity  →  UpstreamKey.ID   (e.g. laysath-0)
```

`UpstreamKey.ID` is `{provider-name}-{index}` from config expansion—not the provider display name alone.

## When a request is sticky

### 1. Model family must match

Default families (enabled when affinity is on and no custom `rules`):  
`claude`, `gpt`, `gemini`, `grok`, `glm`, `kimi`, `qwen`, `minimax`.

Match is **substring**, case-insensitive (`proxy-claude-x` matches `claude`).

### 2. Session identity must be present

Extraction order:

1. **Session headers** (first non-empty):  
   `Session-Id`, `session_id`, `X-Session-Id`, `Thread-Id`, …
2. **Protocol body** (if no session header):
   - path contains `/messages` → `metadata.user_id`, then `prompt_cache_key`
   - `/responses` or chat completions → `prompt_cache_key`, then `metadata.user_id`

If neither is present → **no stickiness**; normal selection.

### 3. Cache lookup

```text
cacheKey ≈ "{rule-name}:{affinity-value}"  →  key ID
TTL default: 600 seconds (process memory only)
```

- **Hit** → first try that key (if still in the model’s key pool)
- **Miss** → round-robin / priority among keys
- **Success** → `Record` pin (switch-on-success: pin the key that actually succeeded)
- **Failure** → `Clear` pin for that cache key

## Retry budget

Controlled by top-level `request-retry`:

```text
maxAttempts = min(1 + request-retry, number of keys for that model)
```

Example: `request-retry: 2` and 5 keys → at most **3** tries on that request.

Retriable upstream outcomes (rotate to another key):

- HTTP **401 / 403 / 429 / ≥500**
- Network / transport errors

Other 4xx are returned to the client without rotating.

There is **no time-based backoff** between tries on the same request—rotation is immediate.

## Failover mode (per provider `name`)

After a retriable failure, behavior depends on the **failed key’s** provider:

| `failover-mode` | Behavior | Typical use |
|---|---|---|
| `key` (default) | Mark this key tried; pick another unused key (may be same site) | Official multi-key, key-level limits |
| `provider` | Skip **all** keys under that `name` for the rest of the request | Relay / 中转站 (one site down ⇒ all keys dead) |

```yaml
- name: openai-official
  failover-mode: key
  base-url: https://api.openai.com/v1
  ...

- name: laysath
  failover-mode: provider
  base-url: https://ai.laysath.cn/v1
  ...
```

Same client model `alias` across providers is **merged** into one pool. Selection still prefers lower `priority`, then round-robin.

## Affinity × retry × failover (one request)

```text
1. Lookup affinity → optional preferred key ID
2. attempt loop (≤ maxAttempts):
     pick preferred (once) or Selector.Pick
       - prefer same supplier when possible
       - skip suppliers marked dead (provider mode)
     Execute upstream
     on failure:
       Clear affinity pin if this was the sticky key
       if skip-retry-on-failure and sticky was used → stop
       if failover-mode=provider → skip whole name
       continue
     on success:
       Record affinity pin → return
3. return last error
```

### Defaults that matter for production

| Setting | Default | Rationale |
|---|---|---|
| Affinity enabled | on | Cache-friendly sessions |
| Affinity TTL | **600s** | Avoid hour-long sticky to dead relays |
| `skip-retry-on-failure` (family rules) | **false** | Failover can run; success re-pins |
| `switch-on-success` | true | Pin the key that actually worked |
| `failover-mode` | `key` unless set | Explicit `provider` on relays |

## Common pitfalls (“old channel still hit”)

1. **TTL still valid** — pin remains until 600s (or your `default-ttl-seconds`) elapses.  
2. **`skip-retry-on-failure: true`** (custom rules) — this request will not rotate after sticky failure.  
3. **Relay with `failover-mode: key`** — rotates keys on the same dead site. Use `provider`.  
4. **New high-priority provider added** — existing pins still prefer the old key until clear/expire.  
5. **No identity field** — affinity never engages; pure RR.  
6. **Process restart** — memory cache is empty (no Redis).

lite-cpa currently has **no admin “clear affinity” API**. Short TTL + restart is the practical reset; failure already clears the pin for that identity.

## Recommended configs

### Official only

```yaml
request-retry: 2
openai-responses:
  - name: openai-official
    failover-mode: key
    base-url: https://api.openai.com/v1
    api-key: sk-...
    models: [{ name: gpt-5, alias: gpt-5 }]
```

### Official + relay (merged alias)

```yaml
request-retry: 3
openai-responses:
  - name: openai-official
    priority: 0
    failover-mode: key
    base-url: https://api.openai.com/v1
    api-key: sk-official
    models: [{ name: gpt-5, alias: gpt-5 }]

  - name: laysath
    priority: 1
    failover-mode: provider
    base-url: https://ai.laysath.cn/v1
    api-key-entries:
      - { api-key: sk-a }
      - { api-key: sk-b }
    models:
      - { name: gpt-5, alias: gpt-5 }
      - { name: grok-4.5, alias: grok-4.5 }
```

### Affinity knobs

```yaml
channel-affinity: true                    # or [claude, gpt, grok]
# channel-affinity: false
# channel-affinity:
#   models: [claude, grok]
#   default-ttl-seconds: 600
```

## Debugging

With `debug: true`, stderr logs affinity hit / clear / record and rotation.  
Request-log (optional) stores provider name and status; it does not yet store affinity fingerprint fields.

## Related

- Full config reference: [AGENTS.md](../AGENTS.md#configuring-configyaml)
- Deployment: [AGENTS.md](../AGENTS.md#deployment)
- Chinese: [渠道亲和与重试](渠道亲和与重试.md)
