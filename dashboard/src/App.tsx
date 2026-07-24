import {
  Check, ChevronLeft, ChevronRight, CircleGauge, CloudDownload, Code2, FileClock,
  Moon, Plus, RefreshCw, RotateCcw, Save, Search, ServerCog, Settings2,
  Sun, Trash2, X,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { parse, parseDocument } from 'yaml'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import {
  Combobox, ComboboxEmpty, ComboboxInput, ComboboxItem, ComboboxList, ComboboxPopup,
} from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

type Theme = 'light' | 'dark'
type Page = 'overview' | 'config' | 'yaml' | 'logs'
type AnyConfig = Record<string, any>

type Status = {
  status: string
  gateway_addr: string
  admin_addr: string
  config_path: string
  disk_revision: string
  applied_revision: string
  in_sync: boolean
  applying: boolean
  last_error: string
  disk_error?: string
  request_log_enabled: boolean
  provider_count: number
  model_count: number
  gateway_started_at: string
}

type ConfigResponse = { yaml: string; revision: string }
type ModelDiscoveryResponse = { models: string[]; resolved_base_url: string }
type NameCount = { name: string; count: number }
type Stats = {
  enabled: boolean; total: number; errors: number; success: number
  input_tokens: number; output_tokens: number; cached_tokens: number
  cache_hit_rate: number; by_model: NameCount[]
}
type RequestLog = {
  id: number; request_id: string; timestamp: string; method: string; path: string
  status_code: number; model: string; protocol: string; provider: string
  upstream: string; user_agent: string; duration_ms: number; input_tokens: number
  output_tokens: number; cached_tokens: number; error: string
  req_body?: string; resp_body?: string
}
type LogList = { items: RequestLog[]; total: number; limit: number; offset: number }
type Filters = { model: string; upstream: string; protocol: string; errorsOnly: boolean }

const THEME_STORAGE = 'lite-cpa-theme'
const PAGE_SIZE = 50
const providerSections = [
  ['anthropic-messages', 'Anthropic Messages'],
  ['openai-responses', 'OpenAI Responses'],
  ['openai-completions', 'OpenAI Chat Completions'],
] as const

class APIError extends Error {
  status: number
  constructor(status: number, message: string) { super(message); this.status = status }
}

async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = init.method ?? 'GET'
  const response = await fetch(path, {
    ...init,
    headers: {
      ...(method !== 'GET' && method !== 'HEAD' ? { 'Content-Type': 'application/json' } : {}),
      ...init.headers,
    },
  })
  if (!response.ok) {
    const raw = await response.text()
    let message = raw
    try { message = JSON.parse(raw)?.error?.message ?? raw } catch { /* use response text */ }
    throw new APIError(response.status, message)
  }
  return response.json() as Promise<T>
}

function savedTheme(): Theme { return localStorage.getItem(THEME_STORAGE) === 'dark' ? 'dark' : 'light' }
function compact(value: number): string {
  return new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(value || 0)
}
function integer(value: number): string { return new Intl.NumberFormat().format(value || 0) }
function rate(value: number | null): string {
  return value == null ? '—' : new Intl.NumberFormat(undefined, { style: 'percent', maximumFractionDigits: 1 }).format(value)
}
function dateTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? value || '—' : date.toLocaleString()
}
function statusTone(status: number): string {
  if (status >= 500) return 'bg-red-500/12 text-red-700 dark:text-red-300'
  if (status >= 400) return 'bg-amber-500/12 text-amber-700 dark:text-amber-300'
  if (status >= 200 && status < 300) return 'bg-emerald-500/12 text-emerald-700 dark:text-emerald-300'
  return 'bg-muted text-muted-foreground'
}
function errorText(error: unknown): string { return error instanceof Error ? error.message : String(error) }
function lines(value: string): string[] { return value.split('\n').map((item) => item.trim()).filter(Boolean) }
function headerText(headers: Record<string, string> | undefined): string {
  return Object.entries(headers ?? {}).map(([key, value]) => `${key}: ${value}`).join('\n')
}
function parseHeaders(value: string): Record<string, string> {
  return Object.fromEntries(lines(value).map((line) => {
    const at = line.indexOf(':')
    return at < 0 ? [line, ''] : [line.slice(0, at).trim(), line.slice(at + 1).trim()]
  }))
}
function modelText(models: any[] | undefined): string {
  return (models ?? []).map((item) => item.alias && item.alias !== item.name ? `${item.name} => ${item.alias}` : item.name).join('\n')
}
function parseModels(value: string): any[] {
  return lines(value).map((line) => {
    const [name, alias] = line.split(/\s*=>\s*/, 2)
    return { name, alias: alias || name }
  })
}
function keyPoolText(entries: any[] | undefined): string {
  return (entries ?? []).map((entry) => entry.priority ? `${entry['api-key']} | ${entry.priority}` : entry['api-key']).join('\n')
}
function parseKeyPool(value: string): any[] {
  return lines(value).map((line) => {
    const [key, priority] = line.split(/\s*\|\s*/, 2)
    return priority == null || priority === '' ? { 'api-key': key } : { 'api-key': key, priority: Number(priority) }
  })
}

function App() {
  const [theme, setTheme] = useState<Theme>(savedTheme)
  const [page, setPage] = useState<Page>('overview')
  const [status, setStatus] = useState<Status | null>(null)
  const [yamlText, setYamlText] = useState('')
  const [loadedYAML, setLoadedYAML] = useState('')
  const [revision, setRevision] = useState('')
  const [notice, setNotice] = useState('正在连接本地管理服务…')
  const [busy, setBusy] = useState(false)
  const [confirmAction, setConfirmAction] = useState<'reload' | 'rollback' | null>(null)
  const [selectedLog, setSelectedLog] = useState<RequestLog | null>(null)

  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark')
    localStorage.setItem(THEME_STORAGE, theme)
  }, [theme])

  const loadStatus = useCallback(async () => {
    try { setStatus(await api<Status>('/api/status')) }
    catch (error) { setNotice(`状态读取失败：${errorText(error)}`) }
  }, [])

  const loadConfig = useCallback(async () => {
    const result = await api<ConfigResponse>('/api/config')
    setYamlText(result.yaml)
    setLoadedYAML(result.yaml)
    setRevision(result.revision)
  }, [])

  useEffect(() => {
    Promise.all([loadStatus(), loadConfig()])
      .then(() => setNotice('管理端已就绪'))
      .catch((error) => setNotice(`初始化失败：${errorText(error)}`))
    const timer = window.setInterval(() => void loadStatus(), 5000)
    return () => window.clearInterval(timer)
  }, [loadConfig, loadStatus])

  const parsed = useMemo(() => {
    try { return { value: (parse(yamlText) ?? {}) as AnyConfig, error: '' } }
    catch (error) { return { value: {} as AnyConfig, error: errorText(error) } }
  }, [yamlText])
  const dirty = yamlText !== loadedYAML

  function updatePath(path: (string | number)[], value: unknown) {
    try {
      const doc = parseDocument(yamlText)
      doc.setIn(path, value)
      setYamlText(doc.toString({ lineWidth: 0 }))
    } catch (error) { setNotice(`无法更新配置：${errorText(error)}`) }
  }

  async function validateConfig() {
    setBusy(true)
    try {
      await api('/api/config/validate', { method: 'POST', body: JSON.stringify({ yaml: yamlText }) })
      setNotice('配置校验通过')
    } catch (error) { setNotice(`校验失败：${errorText(error)}`) }
    finally { setBusy(false) }
  }

  async function saveConfig(apply: boolean) {
    setBusy(true)
    try {
      const result = await api<{ revision: string }>(apply ? '/api/config/apply' : '/api/config', {
        method: apply ? 'POST' : 'PUT',
        body: JSON.stringify({ yaml: yamlText, expected_revision: revision }),
      })
      setRevision(result.revision)
      setLoadedYAML(yamlText)
      setNotice(apply ? '配置已保存并应用' : '配置已保存，尚未应用')
      await loadStatus()
    } catch (error) {
      setNotice(`${apply ? '应用' : '保存'}失败：${errorText(error)}`)
      if (error instanceof APIError && error.status === 409) setNotice('配置文件已被外部修改，请重新加载后再编辑')
    } finally { setBusy(false) }
  }

  async function runRecovery(action: 'reload' | 'rollback') {
    setConfirmAction(null)
    setBusy(true)
    try {
      await api(`/api/config/${action}`, { method: 'POST', body: '{}' })
      await Promise.all([loadConfig(), loadStatus()])
      setNotice(action === 'rollback' ? '已恢复并应用上一版配置' : '已重新加载磁盘配置')
    } catch (error) { setNotice(`${action === 'rollback' ? '回滚' : '重载'}失败：${errorText(error)}`) }
    finally { setBusy(false) }
  }

  const nav = [
    ['overview', CircleGauge, '概览'], ['config', Settings2, '配置管理'],
    ['yaml', Code2, 'YAML'], ['logs', FileClock, '请求日志'],
  ] as const

  return (
    <div className="apple-canvas min-h-screen text-foreground">
      <aside className="glass-sidebar fixed inset-y-0 left-0 z-30 hidden w-64 border-r p-4 lg:block">
        <Brand />
        <nav className="mt-8 space-y-1">
          {nav.map(([id, Icon, label]) => <button key={id} className={`nav-item ${page === id ? 'nav-item-active' : ''}`} onClick={() => setPage(id)}><Icon className="size-4" />{label}</button>)}
        </nav>
        <div className="absolute inset-x-4 bottom-5 rounded-xl border bg-background/45 p-3 text-xs text-muted-foreground">
          <div className="mb-1 flex items-center gap-2"><span className={`size-2 rounded-full ${status?.status === 'ok' ? 'bg-emerald-500' : 'bg-amber-500'}`} />本地管理端</div>
          <div className="truncate font-mono">{status?.admin_addr ?? '127.0.0.1:8318'}</div>
        </div>
      </aside>

      <main className="min-h-screen lg:pl-64">
        <header className="glass-header sticky top-0 z-20 flex min-h-16 items-center justify-between border-b px-4 sm:px-7">
          <div className="flex items-center gap-3 lg:hidden"><Brand compact /></div>
          <div className="hidden min-w-0 lg:block"><p className="truncate text-sm text-muted-foreground" aria-live="polite">{notice}</p></div>
          <div className="flex items-center gap-2">
            {dirty && <span className="rounded-full bg-amber-500/12 px-3 py-1 text-xs font-medium text-amber-700 dark:text-amber-300">未保存</span>}
            <Button variant="ghost" size="icon" aria-label="切换主题" onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}>{theme === 'dark' ? <Sun /> : <Moon />}</Button>
          </div>
        </header>
        <div className="border-b px-4 py-2 text-xs text-muted-foreground lg:hidden">{notice}</div>
        <div className="flex gap-1 overflow-x-auto border-b px-4 py-2 lg:hidden">
          {nav.map(([id, Icon, label]) => <Button key={id} size="sm" variant={page === id ? 'default' : 'ghost'} onClick={() => setPage(id)}><Icon />{label}</Button>)}
        </div>

        <div className="mx-auto max-w-[1400px] p-4 sm:p-7">
          {page === 'overview' && <Overview status={status} setPage={setPage} onReload={() => setConfirmAction('reload')} />}
          {page === 'config' && <ConfigForm config={parsed.value} parseError={parsed.error} updatePath={updatePath} />}
          {page === 'yaml' && <YAMLEditor value={yamlText} error={parsed.error} onChange={setYamlText} />}
          {page === 'logs' && <LogsPage status={status} onSelect={setSelectedLog} />}
        </div>

        {(page === 'config' || page === 'yaml') && <div className="glass-actionbar sticky bottom-0 z-20 border-t px-4 py-3 sm:px-7">
          <div className="mx-auto flex max-w-[1344px] flex-wrap items-center justify-between gap-3">
            <div className="text-xs text-muted-foreground">{parsed.error ? <span className="text-red-600">YAML 有错误</span> : dirty ? '有尚未保存的修改' : status?.in_sync ? '磁盘配置与运行态一致' : '磁盘配置尚未应用'}</div>
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" disabled={busy} onClick={() => setConfirmAction('rollback')}><RotateCcw />回滚</Button>
              <Button variant="outline" disabled={busy} onClick={() => void validateConfig()}><Search />校验</Button>
              <Button variant="outline" disabled={busy || !!parsed.error || !dirty} onClick={() => void saveConfig(false)}><Save />仅保存</Button>
              <Button loading={busy} disabled={!!parsed.error} onClick={() => void saveConfig(true)}><RefreshCw />保存并应用</Button>
            </div>
          </div>
        </div>}
      </main>

      {confirmAction && <Modal title={confirmAction === 'rollback' ? '恢复上一版配置？' : '重新加载磁盘配置？'} onClose={() => setConfirmAction(null)}>
        <p className="text-sm leading-6 text-muted-foreground">当前未保存的修改将丢失。此操作完成后会立即更新业务网关运行态。</p>
        <div className="mt-6 flex justify-end gap-2"><Button variant="outline" onClick={() => setConfirmAction(null)}>取消</Button><Button variant={confirmAction === 'rollback' ? 'destructive' : 'default'} onClick={() => void runRecovery(confirmAction)}>确认</Button></div>
      </Modal>}
      {selectedLog && <LogModal log={selectedLog} onClose={() => setSelectedLog(null)} />}
    </div>
  )
}

function Brand({ compact = false }: { compact?: boolean }) {
  return <div className="flex items-center gap-3"><span className="grid size-10 place-items-center rounded-xl bg-gradient-to-br from-blue-500 via-indigo-500 to-violet-500 shadow-lg shadow-blue-500/20"><ServerCog className="size-5 text-white" /></span>{!compact && <div><h1 className="font-semibold tracking-tight">lite-cpa</h1><p className="text-xs text-muted-foreground">Local Control</p></div>}</div>
}

function PageTitle({ title, description }: { title: string; description: string }) {
  return <div className="mb-6"><h2 className="text-2xl font-semibold tracking-tight">{title}</h2><p className="mt-1 text-sm text-muted-foreground">{description}</p></div>
}

function MetricCard({ label, value, hint }: { label: string; value: string; hint: string }) {
  return <Card className="glass-surface rounded-2xl"><CardContent className="p-5"><p className="text-sm text-muted-foreground">{label}</p><p className="mt-3 text-3xl font-semibold tracking-tight">{value}</p><p className="mt-1 truncate text-xs text-muted-foreground">{hint}</p></CardContent></Card>
}

function Overview({ status, setPage, onReload }: { status: Status | null; setPage: (page: Page) => void; onReload: () => void }) {
  return <><PageTitle title="概览" description="本机网关运行状态与配置同步情况。" />
    {status?.last_error && <Notice tone="error">最近一次操作失败：{status.last_error}</Notice>}
    {!status?.in_sync && <Notice tone="warning">磁盘配置与当前运行态不一致。可以进入配置页应用，或重新加载磁盘配置。</Notice>}
    <section className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <MetricCard label="业务网关" value={status?.status === 'ok' ? '运行中' : '连接中'} hint={status?.gateway_addr ?? '—'} />
      <MetricCard label="上游渠道" value={String(status?.provider_count ?? 0)} hint="已配置 Provider" />
      <MetricCard label="模型映射" value={String(status?.model_count ?? 0)} hint="全部 Provider 模型条目" />
      <MetricCard label="请求日志" value={status?.request_log_enabled ? '已启用' : '未启用'} hint={status?.request_log_enabled ? '可在日志页查询' : '在配置页开启'} />
    </section>
    <Card className="glass-surface mt-6 rounded-2xl"><CardContent className="grid gap-5 p-6 md:grid-cols-[1fr_auto] md:items-center"><div><h3 className="font-semibold">配置状态</h3><dl className="mt-4 grid gap-2 text-sm sm:grid-cols-[8rem_1fr]"><dt className="text-muted-foreground">配置文件</dt><dd className="break-all font-mono text-xs">{status?.config_path ?? '—'}</dd><dt className="text-muted-foreground">业务启动时间</dt><dd>{dateTime(status?.gateway_started_at ?? '')}</dd><dt className="text-muted-foreground">同步状态</dt><dd>{status?.in_sync ? '磁盘与运行态一致' : '存在待应用变更'}</dd></dl></div><div className="flex flex-wrap gap-2"><Button variant="outline" onClick={onReload}><RefreshCw />重新加载</Button><Button onClick={() => setPage('config')}><Settings2 />管理配置</Button></div></CardContent></Card>
  </>
}

function Notice({ children, tone }: { children: React.ReactNode; tone: 'error' | 'warning' }) {
  return <div className={`mb-5 rounded-xl border px-4 py-3 text-sm ${tone === 'error' ? 'border-red-500/20 bg-red-500/8 text-red-700 dark:text-red-300' : 'border-amber-500/20 bg-amber-500/8 text-amber-800 dark:text-amber-200'}`}>{children}</div>
}

function ConfigForm({ config, parseError, updatePath }: { config: AnyConfig; parseError: string; updatePath: (path: (string | number)[], value: unknown) => void }) {
  if (parseError) return <><PageTitle title="配置管理" description="修复 YAML 错误后才能使用组件表单。" /><Notice tone="error">{parseError}</Notice></>
  const affinityValue = config['channel-affinity']
  const affinity = Array.isArray(affinityValue)
    ? { models: affinityValue }
    : typeof affinityValue === 'object' && affinityValue !== null
      ? affinityValue
      : typeof affinityValue === 'boolean' ? { enabled: affinityValue } : {}
  const requestLog = config['request-log'] ?? {}
  return <><PageTitle title="配置管理" description="修改常用设置、渠道、密钥池与模型映射。密钥按本地工具约定以明文显示。" />
    <Section title="基础设置" description="业务网关监听、认证和重试策略。">
      <div className="form-grid">
        <Field label="Host"><Input value={config.host ?? ''} placeholder="留空表示全部网卡" onChange={(e) => updatePath(['host'], e.target.value)} /></Field>
        <Field label="Port"><Input type="number" value={config.port ?? 8317} onChange={(e) => updatePath(['port'], Number(e.target.value))} /></Field>
        <Field label="额外重试次数"><Input type="number" min={0} value={config['request-retry'] ?? 0} onChange={(e) => updatePath(['request-retry'], Number(e.target.value))} /></Field>
        <Field label="最大请求体（bytes）"><Input type="number" min={1} value={config['max-body-bytes'] ?? 33554432} onChange={(e) => updatePath(['max-body-bytes'], Number(e.target.value))} /></Field>
        <Field label="全局 Proxy URL" wide><Input value={config['proxy-url'] ?? ''} placeholder="http://127.0.0.1:7890" onChange={(e) => updatePath(['proxy-url'], e.target.value)} /></Field>
        <Field label="Gateway API Keys" hint="每行一个。它们不是上游密钥。" wide><Textarea className="font-mono text-xs" value={(config['api-keys'] ?? []).join('\n')} onChange={(e) => updatePath(['api-keys'], lines(e.target.value))} /></Field>
        <label className="flex items-center gap-3 text-sm"><input type="checkbox" checked={!!config.debug} onChange={(e) => updatePath(['debug'], e.target.checked)} />启用 Debug 日志</label>
      </div>
    </Section>

    {providerSections.map(([key, label]) => <ProviderSection key={key} sectionKey={key} label={label} providers={config[key] ?? []} globalProxy={config['proxy-url'] ?? ''} updatePath={updatePath} />)}

    <Section title="渠道亲和" description="按客户端会话将成功请求固定到同一个上游密钥；高级 rules 请在 YAML 页维护。">
      <div className="form-grid">
        <label className="flex items-center gap-3 text-sm"><input type="checkbox" checked={config['channel-affinity'] !== false && affinity.enabled !== false} onChange={(e) => updatePath(['channel-affinity'], { ...affinity, enabled: e.target.checked })} />启用渠道亲和</label>
        <label className="flex items-center gap-3 text-sm"><input type="checkbox" checked={affinity['switch-on-success'] !== false} onChange={(e) => updatePath(['channel-affinity'], { ...affinity, 'switch-on-success': e.target.checked })} />成功后切换绑定</label>
        <Field label="默认 TTL（秒）"><Input type="number" value={affinity['default-ttl-seconds'] ?? 600} onChange={(e) => updatePath(['channel-affinity'], { ...affinity, 'default-ttl-seconds': Number(e.target.value) })} /></Field>
        <Field label="最大缓存条目"><Input type="number" value={affinity['max-entries'] ?? 100000} onChange={(e) => updatePath(['channel-affinity'], { ...affinity, 'max-entries': Number(e.target.value) })} /></Field>
        <Field label="模型家族" hint="逗号分隔；留空使用默认家族。" wide><Input value={(affinity.models ?? []).join(', ')} onChange={(e) => updatePath(['channel-affinity'], { ...affinity, models: e.target.value.split(',').map((x) => x.trim()).filter(Boolean) })} /></Field>
      </div>
    </Section>

    <Section title="请求日志" description="SQLite 适合本地使用；变更后会重新打开日志后端。">
      <div className="form-grid">
        <label className="flex items-center gap-3 text-sm"><input type="checkbox" checked={!!requestLog.enabled} onChange={(e) => updatePath(['request-log'], { ...requestLog, enabled: e.target.checked })} />启用请求日志</label>
        <Field label="Backend"><select className="glass-select w-full px-3 text-sm" value={requestLog.backend ?? 'sqlite'} onChange={(e) => updatePath(['request-log'], { ...requestLog, backend: e.target.value })}><option value="sqlite">SQLite</option><option value="postgres">PostgreSQL</option></select></Field>
        <Field label="保留时间"><Input value={requestLog.retention ?? '168h'} onChange={(e) => updatePath(['request-log'], { ...requestLog, retention: e.target.value })} /></Field>
        <label className="flex items-center gap-3 text-sm"><input type="checkbox" checked={!!requestLog['store-body']} onChange={(e) => updatePath(['request-log'], { ...requestLog, 'store-body': e.target.checked })} />保存请求/响应正文</label>
        <Field label="SQLite 路径" wide><Input value={requestLog.sqlite?.path ?? 'logs/requests.db'} onChange={(e) => updatePath(['request-log'], { ...requestLog, sqlite: { ...(requestLog.sqlite ?? {}), path: e.target.value } })} /></Field>
        <Field label="PostgreSQL DSN" wide><Input value={requestLog.postgres?.dsn ?? ''} onChange={(e) => updatePath(['request-log'], { ...requestLog, postgres: { ...(requestLog.postgres ?? {}), dsn: e.target.value } })} /></Field>
      </div>
    </Section>
  </>
}

function ProviderSection({ sectionKey, label, providers, globalProxy, updatePath }: { sectionKey: string; label: string; providers: any[]; globalProxy: string; updatePath: (path: (string | number)[], value: unknown) => void }) {
  const [modelOptions, setModelOptions] = useState<Record<number, string[]>>({})
  const [fetchingModels, setFetchingModels] = useState<number | null>(null)
  const [modelErrors, setModelErrors] = useState<Record<number, string>>({})

  function add() {
    let number = providers.length + 1
    const names = new Set(providers.map((provider) => provider.name))
    while (names.has(`${sectionKey}-${number}`)) number += 1
    updatePath([sectionKey], [...providers, { name: `${sectionKey}-${number}`, 'base-url': '', 'api-key': '', models: [] }])
  }
  function remove(index: number) { updatePath([sectionKey], providers.filter((_, i) => i !== index)) }

  async function discoverModels(provider: any, index: number) {
    setFetchingModels(index)
    setModelErrors((current) => ({ ...current, [index]: '' }))
    const apiKey = provider['api-key'] || provider['api-key-entries']?.[0]?.['api-key'] || ''
    try {
      const result = await api<ModelDiscoveryResponse>('/api/providers/models', {
        method: 'POST',
        body: JSON.stringify({
          provider_type: sectionKey,
          base_url: provider['base-url'] ?? '',
          api_key: apiKey,
          proxy_url: provider['proxy-url'] || globalProxy,
          headers: provider.headers ?? {},
        }),
      })
      setModelOptions((current) => ({ ...current, [index]: result.models }))
      if (result.resolved_base_url && result.resolved_base_url !== provider['base-url']) {
        updatePath([sectionKey, index, 'base-url'], result.resolved_base_url)
      }
    } catch (error) {
      setModelErrors((current) => ({ ...current, [index]: errorText(error) }))
    } finally {
      setFetchingModels(null)
    }
  }

  function setDiscoveredModels(provider: any, index: number, selected: string[]) {
    const options = modelOptions[index] ?? []
    const existing = (provider.models ?? []) as any[]
    const byName = new Map(existing.map((model) => [model.name, model]))
    const manual = existing.filter((model) => !options.includes(model.name))
    const discovered = selected.map((name) => byName.get(name) ?? { name, alias: name })
    updatePath([sectionKey, index, 'models'], [...manual, ...discovered])
  }

  return <Section title={label} description={`${providers.length} 个 Provider`} action={<Button size="sm" variant="outline" onClick={add}><Plus />添加渠道</Button>}>
    {providers.length === 0 && <div className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">尚未配置此类渠道。</div>}
    <div className="space-y-4">{providers.map((provider, index) => <Card key={`${sectionKey}-${index}`} className="rounded-xl"><CardContent className="p-5"><div className="mb-4 flex items-center justify-between"><div><h4 className="font-medium">{provider.name || `未命名渠道 ${index + 1}`}</h4><p className="text-xs text-muted-foreground">{provider['base-url'] || '尚未设置 Base URL'}</p></div><Button variant="ghost" size="icon" aria-label="删除渠道" onClick={() => remove(index)}><Trash2 /></Button></div>
      <div className="form-grid">
        <Field label="Name"><Input value={provider.name ?? ''} onChange={(e) => updatePath([sectionKey, index, 'name'], e.target.value)} /></Field>
        <Field label="Priority"><Input type="number" value={provider.priority ?? 0} onChange={(e) => updatePath([sectionKey, index, 'priority'], Number(e.target.value))} /></Field>
        <Field label="Base URL" wide><Input value={provider['base-url'] ?? ''} onChange={(e) => updatePath([sectionKey, index, 'base-url'], e.target.value)} /></Field>
        <Field label="Proxy URL"><Input value={provider['proxy-url'] ?? ''} onChange={(e) => updatePath([sectionKey, index, 'proxy-url'], e.target.value)} /></Field>
        <Field label="Failover"><select className="glass-select w-full px-3 text-sm" value={provider['failover-mode'] ?? 'key'} onChange={(e) => updatePath([sectionKey, index, 'failover-mode'], e.target.value)}><option value="key">key</option><option value="provider">provider</option></select></Field>
        <Field label="Speed"><select className="glass-select w-full px-3 text-sm" value={provider.speed ?? ''} onChange={(e) => updatePath([sectionKey, index, 'speed'], e.target.value)}><option value="">默认</option><option value="fast">fast</option></select></Field>
        <Field label="单个 API Key" hint="存在密钥池时此字段会被忽略。" wide><Input className="font-mono" value={provider['api-key'] ?? ''} onChange={(e) => updatePath([sectionKey, index, 'api-key'], e.target.value)} /></Field>
        <Field label="API Key Pool" hint="每行 api-key 或 api-key | priority" wide><Textarea className="font-mono text-xs" value={keyPoolText(provider['api-key-entries'])} onChange={(e) => updatePath([sectionKey, index, 'api-key-entries'], parseKeyPool(e.target.value))} /></Field>
        <Field label="Headers" hint="每行 Header: value" wide><Textarea className="font-mono text-xs" value={headerText(provider.headers)} onChange={(e) => updatePath([sectionKey, index, 'headers'], parseHeaders(e.target.value))} /></Field>
        <Field label="从上游获取模型" hint="会自动识别 API 根地址是否缺少 /v1。" wide>
          <div className="flex flex-wrap items-center gap-2">
            <Button type="button" variant="outline" loading={fetchingModels === index} disabled={!provider['base-url']} onClick={() => void discoverModels(provider, index)}><CloudDownload />获取模型</Button>
            {(modelOptions[index]?.length ?? 0) > 0 && <Button type="button" variant="ghost" onClick={() => setDiscoveredModels(provider, index, modelOptions[index])}><Check />全部添加</Button>}
            {(modelOptions[index]?.length ?? 0) > 0 && <span className="text-xs text-muted-foreground">已发现 {modelOptions[index].length} 个模型</span>}
          </div>
          {modelErrors[index] && <p className="mt-2 text-xs text-red-600 dark:text-red-300">{modelErrors[index]}</p>}
          {(modelOptions[index]?.length ?? 0) > 0 && <div className="mt-3">
            <Combobox multiple items={modelOptions[index]} value={(provider.models ?? []).map((model: any) => model.name).filter((name: string) => modelOptions[index].includes(name))} onValueChange={(value) => setDiscoveredModels(provider, index, value)}>
              <ComboboxInput placeholder="搜索并多选模型…" />
              <ComboboxPopup>
                <ComboboxEmpty>没有匹配模型</ComboboxEmpty>
                <ComboboxList>{modelOptions[index].map((model) => <ComboboxItem key={model} value={model}>{model}</ComboboxItem>)}</ComboboxList>
              </ComboboxPopup>
            </Combobox>
          </div>}
        </Field>
        <Field label="Models" hint="每行 name 或 name => alias" wide><Textarea className="font-mono text-xs" value={modelText(provider.models)} onChange={(e) => updatePath([sectionKey, index, 'models'], parseModels(e.target.value))} /></Field>
      </div>
    </CardContent></Card>)}</div>
  </Section>
}

function Section({ title, description, action, children }: { title: string; description: string; action?: React.ReactNode; children: React.ReactNode }) {
  return <Card className="glass-surface mb-5 rounded-2xl"><CardContent className="p-0"><div className="flex items-center justify-between gap-4 border-b px-5 py-4 sm:px-6"><div><h3 className="font-semibold">{title}</h3><p className="mt-0.5 text-xs text-muted-foreground">{description}</p></div>{action}</div><div className="p-5 sm:p-6">{children}</div></CardContent></Card>
}
function Field({ label, hint, wide, children }: { label: string; hint?: string; wide?: boolean; children: React.ReactNode }) {
  return <label className={`block ${wide ? 'sm:col-span-2' : ''}`}><span className="mb-1.5 block text-sm font-medium">{label}</span>{children}{hint && <span className="mt-1 block text-xs text-muted-foreground">{hint}</span>}</label>
}

function YAMLEditor({ value, error, onChange }: { value: string; error: string; onChange: (value: string) => void }) {
  return <><PageTitle title="原始 YAML" description="完整编辑 config.yaml；这里会显示所有上游密钥，请注意屏幕隐私。" />
    {error && <Notice tone="error">{error}</Notice>}
    <Card className="glass-surface rounded-2xl"><CardContent className="p-0"><Textarea aria-label="配置 YAML" spellCheck={false} className="yaml-editor min-h-[calc(100vh-15rem)] resize-y rounded-2xl border-0 p-5 font-mono text-[13px] leading-6 shadow-none focus-visible:ring-0" value={value} onChange={(e) => onChange(e.target.value)} /></CardContent></Card>
  </>
}

function LogsPage({ status, onSelect }: { status: Status | null; onSelect: (log: RequestLog) => void }) {
  const [stats, setStats] = useState<Stats | null>(null)
  const [logs, setLogs] = useState<LogList>({ items: [], total: 0, limit: PAGE_SIZE, offset: 0 })
  const [filters, setFilters] = useState<Filters>({ model: '', upstream: '', protocol: '', errorsOnly: false })
  const [draft, setDraft] = useState(filters)
  const [loading, setLoading] = useState(false)
  const load = useCallback(async (offset = logs.offset) => {
    setLoading(true)
    const list = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(offset) })
    const summary = new URLSearchParams()
    for (const query of [list, summary]) {
      if (filters.model) query.set('model', filters.model)
      if (filters.upstream) query.set('upstream', filters.upstream)
      if (filters.protocol) query.set('protocol', filters.protocol)
      if (filters.errorsOnly) query.set('errors', '1')
    }
    try {
      const [nextStats, nextLogs] = await Promise.all([api<Stats>(`/api/logs/stats?${summary}`), api<LogList>(`/api/logs?${list}`)])
      setStats(nextStats); setLogs(nextLogs)
    } finally { setLoading(false) }
  }, [filters, logs.offset])
  useEffect(() => { void load(0) }, [filters]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { const timer = window.setInterval(() => void load(), 5000); return () => window.clearInterval(timer) }, [load])
  async function clearLogs() { await api('/api/logs', { method: 'DELETE', body: '{}' }); await load(0) }
  return <><PageTitle title="请求日志" description="查看调用量、Token、缓存命中和上游错误。" />
    {!status?.request_log_enabled && <Notice tone="warning">request-log 尚未启用，请在配置页开启后保存并应用。</Notice>}
    <section className="mb-5 grid grid-cols-2 gap-3 lg:grid-cols-5"><MetricCard label="总请求" value={integer(stats?.total ?? 0)} hint="当前筛选" /><MetricCard label="输入 Token" value={compact(stats?.input_tokens ?? 0)} hint={integer(stats?.input_tokens ?? 0)} /><MetricCard label="输出 Token" value={compact(stats?.output_tokens ?? 0)} hint={integer(stats?.output_tokens ?? 0)} /><MetricCard label="成功率" value={rate(stats?.total ? (stats.success / stats.total) : null)} hint={`${stats?.success ?? 0} 成功`} /><MetricCard label="缓存命中率" value={rate(stats?.input_tokens ? stats.cache_hit_rate : null)} hint={`${stats?.cached_tokens ?? 0} 缓存 Token`} /></section>
    <Card className="glass-surface overflow-hidden rounded-2xl"><CardContent className="p-0"><form className="grid gap-3 border-b p-4 sm:grid-cols-2 xl:grid-cols-[1fr_1fr_10rem_auto_auto_auto]" onSubmit={(e) => { e.preventDefault(); setFilters(draft) }}><Input placeholder="模型" value={draft.model} onChange={(e) => setDraft({ ...draft, model: e.target.value })} /><Input placeholder="上游名称" value={draft.upstream} onChange={(e) => setDraft({ ...draft, upstream: e.target.value })} /><select className="glass-select px-3 text-sm" value={draft.protocol} onChange={(e) => setDraft({ ...draft, protocol: e.target.value })}><option value="">所有协议</option><option value="chat">chat</option><option value="responses">responses</option><option value="claude">claude</option></select><label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={draft.errorsOnly} onChange={(e) => setDraft({ ...draft, errorsOnly: e.target.checked })} />仅错误</label><Button type="submit"><Search />筛选</Button><Button variant="outline" loading={loading} onClick={() => void load(0)}><RefreshCw />刷新</Button></form>
      <div className="overflow-x-auto"><table className="w-full min-w-[900px] text-left text-sm"><thead className="border-b bg-muted/40 text-xs text-muted-foreground"><tr><th className="px-5 py-3">时间</th><th className="px-3">状态</th><th className="px-3">输入 / 输出 / 缓存</th><th className="px-3">模型</th><th className="px-3">上游</th><th className="px-3">协议</th></tr></thead><tbody className="divide-y">{logs.items.map((record) => <tr key={record.id} className="glass-row cursor-pointer" onClick={() => onSelect(record)}><td className="px-5 py-3 font-mono text-xs">{dateTime(record.timestamp)}</td><td className="px-3"><span className={`rounded-full px-2 py-1 text-xs font-semibold ${statusTone(record.status_code)}`}>{record.status_code}</span></td><td className="px-3 font-mono text-xs">{compact(record.input_tokens)} / {compact(record.output_tokens)} / {compact(record.cached_tokens)}</td><td className="max-w-48 truncate px-3 font-mono text-xs">{record.model || '—'}</td><td className="max-w-48 truncate px-3 font-mono text-xs">{record.upstream || '—'}</td><td className="px-3 font-mono text-xs">{record.protocol || '—'}</td></tr>)}</tbody></table>{!loading && logs.items.length === 0 && <div className="p-16 text-center text-sm text-muted-foreground">暂无请求记录。</div>}</div>
      <div className="flex items-center justify-between border-t px-5 py-4"><Button variant="destructive-outline" size="sm" disabled={!logs.total} onClick={() => void clearLogs()}><Trash2 />清空</Button><div className="flex items-center gap-2 text-sm text-muted-foreground"><span>{logs.total ? logs.offset + 1 : 0}–{Math.min(logs.offset + logs.items.length, logs.total)} / {logs.total}</span><Button size="sm" variant="outline" disabled={logs.offset <= 0} onClick={() => void load(Math.max(0, logs.offset - PAGE_SIZE))}><ChevronLeft /></Button><Button size="sm" variant="outline" disabled={logs.offset + logs.limit >= logs.total} onClick={() => void load(logs.offset + PAGE_SIZE)}><ChevronRight /></Button></div></div>
    </CardContent></Card>
  </>
}

function Modal({ children, onClose, title, wide = false }: { children: React.ReactNode; onClose: () => void; title: string; wide?: boolean }) {
  return <div className="fixed inset-0 z-50 grid place-items-center bg-slate-950/45 p-4 backdrop-blur-md" onMouseDown={onClose}><Card className={`glass-modal max-h-[calc(100vh-2rem)] w-full overflow-auto rounded-2xl ${wide ? 'max-w-3xl' : 'max-w-md'}`} role="dialog" aria-modal="true" onMouseDown={(e) => e.stopPropagation()}><CardContent className="p-6"><div className="mb-5 flex items-center justify-between"><h2 className="text-lg font-semibold">{title}</h2><Button variant="ghost" size="icon" onClick={onClose}><X /></Button></div>{children}</CardContent></Card></div>
}
function LogModal({ log, onClose }: { log: RequestLog; onClose: () => void }) {
  return <Modal title="请求详情" wide onClose={onClose}><dl className="grid gap-3 text-sm sm:grid-cols-[9rem_1fr]">{Object.entries({ request_id: log.request_id, time: dateTime(log.timestamp), status: `${log.status_code} · ${log.duration_ms} ms`, tokens: `${log.input_tokens} / ${log.output_tokens} / ${log.cached_tokens}`, model: log.model || '—', upstream: `${log.upstream || '—'} (${log.provider || '—'})`, protocol: log.protocol || '—', path: `${log.method} ${log.path}`, error: log.error || '—' }).map(([key, value]) => <div className="contents" key={key}><dt className="font-mono text-xs text-muted-foreground">{key}</dt><dd className="break-words font-mono text-xs">{value}</dd></div>)}</dl>{(log.req_body || log.resp_body) && <div className="mt-5 space-y-4">{log.req_body && <Payload title="Request body" value={log.req_body} />}{log.resp_body && <Payload title="Response body" value={log.resp_body} />}</div>}</Modal>
}
function Payload({ title, value }: { title: string; value: string }) { return <section><h3 className="mb-2 text-sm font-medium">{title}</h3><pre className="max-h-64 overflow-auto rounded-xl bg-muted p-4 text-xs whitespace-pre-wrap">{value}</pre></section> }

export default App
