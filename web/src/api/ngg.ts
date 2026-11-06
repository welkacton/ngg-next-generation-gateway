import client from './client'
import type { AxiosRequestConfig } from 'axios'

/* ------------------------------------------------------------------ *
 * 类型定义（与后端 REST 契约一一对应）
 * ------------------------------------------------------------------ */

export type CoreState =
  | 'not_installed'
  | 'installed'
  | 'starting'
  | 'running'
  | 'stopping'
  | 'stopped'
  | 'failed'
  | 'unknown'

export interface CoreCapabilities {
  protocols: string[]
  inbound_types: string[]
  features: string[]
  worker_mode: boolean
  [key: string]: unknown
}

export interface CoreStatus {
  id: string
  display_name: string
  state: CoreState
  installed: boolean
  version: string
  pid: number
  binary_path: string
  config_path: string
  ports: number[]
  auto_start: boolean
  message: string
  restarts: number
  updated_at: string
  capabilities: CoreCapabilities
}

export interface SystemInfo {
  ngg_version: string
  os: string
  arch: string
  platform: string
  go_version: string
  started_at: string
  listen_addr: string
  /** 后端为数字阶段号（Stage 1 → 1），兼容字符串写法。 */
  stage: number | string
  mode: string
  data_dir: string
}

export interface HealthInfo {
  status: string
  cores_running: number
  uptime_sec: number
}

export interface ActionResult {
  ok: boolean
  message: string
}

export interface VersionInfo {
  version: string
  raw: string
  channel: string
  detected_at: string
  asset_url: string
  digest_url: string
}

export interface ConfigFile {
  content: string
  path: string
  sha256: string
  size: number
  updated_at: string
}

export interface ConfigBackup {
  id: string
  core_id: string
  path: string
  sha256: string
  size: number
  reason: string
  created_at: string
}

/** PUT /config 返回 { ok, backup }，backup 可能是备份对象或备份 id。 */
export interface ConfigSaveResult {
  ok: boolean
  backup: ConfigBackup | string | null
}

export interface ValidateResult {
  ok: boolean
  output: string
}

export interface LogEntry {
  seq: number
  core_id: string
  stream: string
  line: string
  time: string
}

export interface CoreStats {
  pid: number
  cpu_percent: number
  memory_rss: number
  memory_vms: number
  threads: number
  uptime_sec: number
  sampled_at: string
}

export interface CoreDescriptor {
  id: string
  display_name: string
  description: string
  homepage: string
  repository: string
  capability: CoreCapabilities | Record<string, unknown>
  builtin: boolean
  source: string
}

export interface RegistryReloadResult {
  loaded: number
  problems: string[]
}

export interface PortAllocation {
  core_id: string
  ports: number[]
  names: Record<number, string>
}

export interface AuditRecord {
  id: string | number
  action: string
  core_id: string
  detail: string
  created_at: string
}

export interface BatchItemResult {
  id: string
  ok: boolean
  message: string
}

export interface BatchResult {
  results: BatchItemResult[]
}

export type EventType =
  | 'core.state'
  | 'core.log'
  | 'core.stats'
  | 'core.install'
  | 'system.ready'
  | 'system.alert'
  | string

export interface GatewayEvent {
  type: EventType
  core_id?: string
  payload: any
  timestamp: string
}

/* ------------------------------------------------------------------ *
 * 请求小工具：响应拦截器返回完整 AxiosResponse，这里统一取 .data
 * ------------------------------------------------------------------ */

async function get<T>(url: string, config?: AxiosRequestConfig): Promise<T> {
  const res = await client.get<T>(url, config)
  return res.data
}

async function post<T>(
  url: string,
  data?: unknown,
  config?: AxiosRequestConfig,
): Promise<T> {
  const res = await client.post<T>(url, data, config)
  return res.data
}

async function put<T>(
  url: string,
  data?: unknown,
  config?: AxiosRequestConfig,
): Promise<T> {
  const res = await client.put<T>(url, data, config)
  return res.data
}

async function del<T>(url: string, config?: AxiosRequestConfig): Promise<T> {
  const res = await client.delete<T>(url, config)
  return res.data
}

function core(id: string): string {
  return `/cores/${encodeURIComponent(id)}`
}

/* ------------------------------------------------------------------ *
 * 系统 / 健康
 * ------------------------------------------------------------------ */

export const getSystem = () => get<SystemInfo>('/system')
export const getHealth = () => get<HealthInfo>('/health')

/* ------------------------------------------------------------------ *
 * 内核
 * ------------------------------------------------------------------ */

export const listCores = () => get<CoreStatus[]>('/cores')
export const getCore = (id: string) => get<CoreStatus>(core(id))
export const startCore = (id: string) => post<ActionResult>(`${core(id)}/start`)
export const stopCore = (id: string) => post<ActionResult>(`${core(id)}/stop`)
export const restartCore = (id: string) => post<ActionResult>(`${core(id)}/restart`)
export interface InstallTask {
  id: string
  core_id: string
  version: string
  status: 'running' | 'succeeded' | 'failed'
  message: string
  started_at: string
  finished_at?: string
  result?: unknown
  error?: string
}

export interface InstallAccepted {
  ok: boolean
  accepted: boolean
  task_id: string
  status: string
  core_id: string
  message: string
}

/** Installs are background tasks: the POST returns 202 immediately. */
export const installCore = (id: string, version?: string) =>
  post<InstallAccepted>(`${core(id)}/install`, version ? { version } : {})
export const getInstallStatus = (coreId: string) =>
  get<InstallTask | { tasks: InstallTask[]; total: number }>('/cores/install/status', {
    params: { core: coreId },
  })
export const uninstallCore = (id: string) => del<ActionResult>(core(id))
export const upgradeCore = (id: string, version: string) =>
  post<ActionResult>(`${core(id)}/upgrade`, { version })
export const rollbackCore = (id: string, version: string) =>
  post<ActionResult>(`${core(id)}/rollback`, { version })

export const batchStart = (ids: string[], exclusive = false) =>
  post<BatchResult>('/cores/batch/start', { ids, exclusive })
export const batchStop = (ids: string[]) =>
  post<BatchResult>('/cores/batch/stop', { ids })

/* ------------------------------------------------------------------ *
 * 版本
 * ------------------------------------------------------------------ */

export const listVersions = (id: string) =>
  get<VersionInfo[]>(`${core(id)}/versions`)
export const getCurrentVersion = (id: string) =>
  get<VersionInfo>(`${core(id)}/version`)

/* ------------------------------------------------------------------ *
 * 配置
 * ------------------------------------------------------------------ */

export const getConfig = (id: string) => get<ConfigFile>(`${core(id)}/config`)
export const saveConfig = (id: string, content: string, reason?: string) =>
  put<ConfigSaveResult>(`${core(id)}/config`, {
    content,
    ...(reason ? { reason } : {}),
  })
export const validateConfig = (id: string, content: string) =>
  post<ValidateResult>(`${core(id)}/config/validate`, { content })
export const listBackups = (id: string) =>
  get<ConfigBackup[]>(`${core(id)}/config/backups`)
export const rollbackConfig = (id: string, backupId: string) =>
  post<ActionResult>(`${core(id)}/config/rollback`, { backup_id: backupId })

export interface ExportedConfig {
  blob: Blob
  filename: string
}

/** GET /config/export 返回文件 blob，文件名优先取 Content-Disposition。 */
export async function exportConfig(id: string): Promise<ExportedConfig> {
  const res = await client.get<Blob>(`${core(id)}/config/export`, {
    responseType: 'blob',
  })
  const disposition = String(res.headers['content-disposition'] || '')
  let filename = `${id}-config.json`
  const matched = disposition.match(/filename\*?=(?:UTF-8'')?"?([^";]+)"?/i)
  if (matched && matched[1]) {
    try {
      filename = decodeURIComponent(matched[1])
    } catch {
      filename = matched[1]
    }
  }
  return { blob: res.data, filename }
}

/** POST /config/import 使用 multipart/form-data，字段名 file。 */
export async function importConfig(id: string, file: File): Promise<ActionResult> {
  const form = new FormData()
  form.append('file', file)
  const res = await client.post<ActionResult>(`${core(id)}/config/import`, form)
  return res.data
}

/* ------------------------------------------------------------------ *
 * 日志 / 统计
 * ------------------------------------------------------------------ */

export const getLogs = (id: string, after = 0, limit = 200) =>
  get<LogEntry[]>(`${core(id)}/logs`, { params: { after, limit } })
export const clearLogs = (id: string) => del<ActionResult>(`${core(id)}/logs`)
export const getStats = (id: string) => get<CoreStats>(`${core(id)}/stats`)

/* ------------------------------------------------------------------ *
 * 注册表 / 端口 / 审计
 * ------------------------------------------------------------------ */

export const getRegistry = () => get<CoreDescriptor[]>('/registry')
export const reloadRegistry = () =>
  post<RegistryReloadResult>('/registry/reload')
export const getPorts = () => get<PortAllocation[]>('/ports')
export const getAudit = (limit = 50) =>
  get<AuditRecord[]>('/audit', { params: { limit } })

/* ------------------------------------------------------------------ *
 * 通用动作返回：不同端点结构不一，页面统一渲染原始 JSON，避免假数据。
 * ------------------------------------------------------------------ */

export type GenericResult = Record<string, unknown>

/* ------------------------------------------------------------------ *
 * Stage 2 · 统一网络引擎 /network
 * ------------------------------------------------------------------ */

export interface NetworkIngress {
  enabled: boolean
  address: string
  protocols: string[]
  [key: string]: unknown
}

export interface NetworkTun {
  configured: boolean
  active: boolean
  name: string
  mtu: number
  stats: Record<string, unknown>
  [key: string]: unknown
}

/** 健康端点（字段随后端实现扩展，保留索引签名）。 */
export interface NetworkEndpoint {
  id?: string
  core_id?: string
  address?: string
  protocol?: string
  healthy?: boolean
  latency_ms?: number
  [key: string]: unknown
}

/** 会话记录（字段随后端实现扩展，保留索引签名）。 */
export interface NetworkSession {
  id?: string
  [key: string]: unknown
}

/**
 * /network/status 全字段。dns / rules / sessions / healthy / resources /
 * metrics / plugins 形态可能随阶段演进，用 unknown 承载，页面会渲染
 * 完整原始 JSON，确保不遗漏任何后端字段。
 */
export interface NetworkStatus {
  running: boolean
  stage: string | number
  fail_closed: boolean
  failover: boolean
  default_core?: string
  direct_on_failure?: boolean
  bypass_tun?: boolean
  components?: NetworkComponent[]
  ingress: NetworkIngress
  tun: NetworkTun
  dns: unknown
  rules: unknown
  sessions: unknown
  endpoints: NetworkEndpoint[]
  healthy: unknown
  resources: unknown
  metrics: unknown
  plugins: unknown
  [key: string]: unknown
}

export interface NetworkConfig {
  fail_closed: boolean
  failover: boolean
  default_core: string
  direct_on_failure: boolean
  egress_mark: number
  bypass_tun: boolean
  [key: string]: unknown
}

export interface NetworkConfigSaveResult {
  changed?: string[] | number | boolean
  requires_restart?: boolean
  ok?: boolean
  message?: string
  [key: string]: unknown
}

export interface NetworkSelftestResult {
  path?: string
  latency_ms?: number
  ok?: boolean
  echo?: unknown
  [key: string]: unknown
}

export const getNetworkStatus = () => get<NetworkStatus>('/network/status')
export const startNetwork = () => post<GenericResult>('/network/start')
export const stopNetwork = () => post<GenericResult>('/network/stop')
export const networkSelftest = (coreId: string) =>
  post<NetworkSelftestResult>('/network/selftest', { core_id: coreId })
export const networkPackettest = () =>
  post<GenericResult>('/network/packettest')
export const getNetworkSessions = () =>
  get<NetworkSession[]>('/network/sessions')
export const getNetworkEndpoints = () =>
  get<NetworkEndpoint[]>('/network/endpoints')
/* 真实流量历史采样：后端每 2s 从统一网络引擎的计数器取一次，绝不插值/伪造。
 * rate_* 是相邻真实计数差除以真实时间差，网络引擎未运行时 running=false。 */
export interface MetricsSample {
  ts: number
  running: boolean
  bytes_in: number
  bytes_out: number
  rate_in: number
  rate_out: number
  active: number
  connections: number
  direct: number
  proxied: number
  dropped: number
  dns_queries: number
  rule_matches: number
  failovers: number
}

export interface MetricsHistoryResult {
  running: boolean
  interval_sec: number
  window_minutes: number
  count: number
  samples: MetricsSample[]
  note: string
}

export const getNetworkMetricsHistory = (minutes = 5) =>
  get<MetricsHistoryResult>(`/network/metrics/history?minutes=${minutes}`)

/* 接管全局：Windows 系统代理（用户态，立即生效）+ TUN 真实能力检测。
 * TUN 需要 wintun 驱动与管理员权限，后端如实上报，不伪造网卡。 */
export interface GlobalProxyStatus {
  supported: boolean
  attached: boolean
  server?: string
  bypass?: string
  managed?: boolean
  system_proxy_on?: boolean
  ingress?: string
  mode?: string
  error?: string
}

export interface TunDriverInfo {
  present: boolean
  path: string
  signed: boolean
  version: string
  url: string
  sha256: string
}

export interface TunStats {
  bytes_in: number
  bytes_out: number
  packets_in: number
  packets_out: number
  sessions: number
  since: string
}

export interface TunAdapterStatus {
  open: boolean
  adapter: string
  mtu: number
  ipv4: string
  route_all: boolean
  elevated: boolean
  wintun_dll: string
  os: string
  stack: string
  since?: string
  stats: TunStats
}

export interface TunCapabilities {
  os: string
  tun_supported: boolean
  admin: boolean
  wintun_dll: string
  reason: string
  virtual_adapters: string[]
  note: string
  driver?: TunDriverInfo
  adapter?: TunAdapterStatus
}

export interface GlobalTakeoverResult {
  proxy: GlobalProxyStatus
  tun: TunCapabilities
}

export interface TunStatusResult {
  adapter: TunAdapterStatus
  capabilities: TunCapabilities
  running: boolean
}

export const getGlobalStatus = () => get<GlobalTakeoverResult>('/network/global/status')
export const attachGlobalProxy = () =>
  post<{ ok: boolean; proxy: GlobalProxyStatus }>('/network/global/attach')
export const detachGlobalProxy = () =>
  post<{ ok: boolean; proxy: GlobalProxyStatus }>('/network/global/detach')

export const installTunDriver = () =>
  post<{ ok: boolean; driver: { path: string; version: string; downloaded: boolean; signature: string; message: string } }>(
    '/network/tun/driver/install',
  )
export const getTunStatus = () => get<TunStatusResult>('/network/tun/status')
export const attachTun = (routeAll: boolean) =>
  post<{ ok: boolean; adapter: TunAdapterStatus; route_all: boolean; message: string }>('/network/tun/attach', {
    route_all: routeAll,
  })
export const detachTun = () => post<{ ok: boolean; adapter: TunAdapterStatus }>('/network/tun/detach', {})

export const getNetworkConfig = () => get<NetworkConfig>('/network/config')
export const saveNetworkConfig = (config: NetworkConfig) =>
  put<NetworkConfigSaveResult>('/network/config', config)

/* ------------------------------------------------------------------ *
 * Stage 3 · 规则与 DNS /network/rules /network/dns
 * ------------------------------------------------------------------ */

export interface Rule {
  id: string
  type: string
  value: string
  action: string
  core_id?: string
  chain?: string[] | string
  tenant_id?: string
  priority: number
  enabled: boolean
  comment?: string
  [key: string]: unknown
}

export interface RulesConfig {
  rules: Rule[]
  default_action: string
  default_core: string
  [key: string]: unknown
}

export interface DnsResolveResult {
  name?: string
  type?: string
  answers?: unknown[]
  [key: string]: unknown
}

export interface DnsHostsPayload {
  hosts: Record<string, string>
  block: string[]
}

/** 批量启停：ids 为空时必须传 all=true；后端只翻转 enabled，不会全量替换规则表。 */
export interface TogglePayload {
  ids?: string[]
  all?: boolean
  enabled: boolean
}
export interface ToggleResult {
  ok: boolean
  changed: number
  enabled: boolean
  scope: 'selected' | 'all'
  [key: string]: unknown
}

export const getRules = () => get<RulesConfig>('/network/rules')
export const toggleRules = (payload: TogglePayload) =>
  post<ToggleResult>('/network/rules/toggle', payload)
export const addRule = (rule: Rule) => post<GenericResult>('/network/rules', rule)
export const replaceRules = (payload: {
  rules: Rule[]
  default_action: string
  default_core: string
}) => put<GenericResult>('/network/rules', payload)
export const deleteRule = (id: string) =>
  del<GenericResult>(`/network/rules/${encodeURIComponent(id)}`)
export const loadRulesFromFile = (path: string) =>
  post<GenericResult>('/network/rules/load', { path })
export const getDnsStatus = () => get<GenericResult>('/network/dns')
export const resolveDns = (name: string, type: string) =>
  post<DnsResolveResult>('/network/dns/resolve', { name, type })
export const setDnsHosts = (payload: DnsHostsPayload) =>
  post<GenericResult>('/network/dns/hosts', payload)

/* ------------------------------------------------------------------ *
 * 产品代际与仓库更新 /update/*
 * ------------------------------------------------------------------ */

export interface UpdateStatus {
  generation: number
  generation_label: string
  generation_name: string
  release: string
  repo: string
  os: string
  arch: string
  platform: string
  install_dir: string
  update_dir: string
  script_path: string
  script_ready: boolean
  token_configured: boolean
  [key: string]: unknown
}

export interface UpdateAsset {
  name: string
  size: number
  browser_download_url: string
  url: string
  [key: string]: unknown
}

export interface UpdateRelease {
  tag_name: string
  name: string
  published_at: string
  body: string
  html_url: string
  prerelease: boolean
  draft: boolean
  assets: UpdateAsset[]
  [key: string]: unknown
}

export interface UpdateCheckResult {
  status: UpdateStatus
  current: string
  latest: string
  update_available: boolean
  release: UpdateRelease | null
  asset: UpdateAsset | null
  checked_at: string
  source: string
  note?: string
  [key: string]: unknown
}

export interface UpdateDownloadResult {
  tag: string
  asset: string
  path: string
  sha256: string
  verified: boolean
  size: number
  [key: string]: unknown
}

export interface UpdateApplyResult {
  started: boolean
  tag: string
  script_path: string
  log_path: string
  manual_command?: string
  note?: string
  [key: string]: unknown
}

export interface UpdateTarget {
  tag?: string
  asset?: string
}

export const getUpdateStatus = () => get<UpdateStatus>('/update/status')
export const checkUpdate = () => post<UpdateCheckResult>('/update/check', {})
export const downloadUpdate = (payload: UpdateTarget = {}) =>
  post<UpdateDownloadResult>('/update/download', payload)
export const applyUpdate = (payload: UpdateTarget = {}) =>
  post<UpdateApplyResult>('/update/apply', payload)

/* ------------------------------------------------------------------ *
 * Stage 4 · 协议中心 /protocols
 * ------------------------------------------------------------------ */

export interface Protocol {
  id: string
  name: string
  category: string
  group: string
  security: string
  transport: string
  config_kind: string
  default_port: number
  ports: string[]
  kernels: string[]
  features: string[]
  pq_capable: boolean
  requires_root: boolean
  description: string
  enabled: boolean
  custom: boolean
  params?: Record<string, unknown>
  config_content?: string
  config_format?: string
  [key: string]: unknown
}

export interface ProtocolConfigResult {
  content: string
  format: string
  validated: boolean
  customized?: boolean
  annotated_content?: string
  doc_url?: string
  [key: string]: unknown
}

export interface ProtocolField {
  key: string
  label: string
  type: string
  options?: string[]
  default: string
  description: string
  required: boolean
}

export interface KernelSchema {
  kernel: string
  label: string
  syntax: string
  format: string
  doc_url: string
  doc_name: string
  description: string
  fields: ProtocolField[]
}

export interface ProtocolSchemaResult {
  schemas: KernelSchema[]
  schema: KernelSchema
}

export interface ProtocolAnnotateResult {
  ok: boolean
  content: string
  plain: string
  doc_url?: string
}

export interface ProtocolMeta {
  groups: { value: string; label: string }[]
  categories: { value: string; label: string }[]
  config_kinds: string[]
  kernels: string[]
  formats: string[]
}

export interface ProtocolListFilter {
  category?: string
  group?: string
  search?: string
  enabled?: string
  kernel?: string
  include_all?: string
}

export type ProtocolToggleResult = GenericResult & {
  changed: number
  enabled: boolean
  scope: string
}

export interface ProtocolValidateResult {
  ok: boolean
  valid: boolean
  format: string
  error?: string
}

const protocol = (id: string) => `/protocols/${encodeURIComponent(id)}`

export const listProtocols = (filter: ProtocolListFilter = {}) =>
  get<Protocol[]>('/protocols', { params: filter })
export const getProtocolMeta = () => get<ProtocolMeta>('/protocols/meta')
export const getProtocolSchemas = () => get<ProtocolSchemaResult>('/protocols/schema')
export const annotateProtocolConfig = (payload: {
  content: string
  format: string
  kernel: string
  on: boolean
}) => post<ProtocolAnnotateResult>('/protocols/annotate', payload)
export const previewProtocolConfig = (payload: {
  spec: Partial<Protocol>
  kernel: string
  params: Record<string, unknown>
}) => post<ProtocolConfigResult>('/protocols/preview', payload)
export const getProtocol = (id: string) => get<Protocol>(protocol(id))
export const enableProtocol = (id: string, enabled: boolean) =>
  post<GenericResult>(`${protocol(id)}/enable`, { enabled })
export const toggleProtocols = (payload: {
  ids?: string[]
  all?: boolean
  enabled: boolean
}) => post<ProtocolToggleResult>('/protocols/toggle', payload)
export const createProtocol = (spec: Partial<Protocol>) =>
  post<GenericResult>('/protocols', spec)
export const updateProtocol = (id: string, spec: Partial<Protocol>) =>
  put<GenericResult>(protocol(id), spec)
export const deleteProtocol = (id: string) => del<GenericResult>(protocol(id))
export const validateProtocolConfig = (content: string, format: string) =>
  post<ProtocolValidateResult>('/protocols/validate', { content, format })
export const saveProtocolConfig = (id: string, content: string, format: string) =>
  put<GenericResult>(`${protocol(id)}/config`, { content, format })
export const clearProtocolConfig = (id: string) =>
  del<GenericResult>(`${protocol(id)}/config`)
export const generateProtocolConfig = (
  id: string,
  kernel: string,
  params: Record<string, unknown>,
  regenerate = false,
) =>
  post<ProtocolConfigResult>(`${protocol(id)}/config`, {
    kernel,
    params,
    regenerate,
  })

/* ------------------------------------------------------------------ *
 * 协议配置：官网协议目录（只读）+ 可编辑配置文件
 * ------------------------------------------------------------------ */

export interface CatalogProtocol {
  slug: string
  name: string
  doc_url: string
}

export interface CatalogKernel {
  kernel: string
  label: string
  site_url: string
  source_url: string
  protocols: CatalogProtocol[]
  count: number
  synced_at: string
  from_cache: boolean
  error?: string
}

export interface ProtocolCatalog {
  kernels: CatalogKernel[]
  synced_at: string
  next_sync_at: string
  interval_minutes: number
  source: 'official-docs' | 'cache' | 'builtin'
  stale: boolean
  message: string
}

export interface ProfileSection {
  key: string
  label: string
  hint: string
  doc_url: string
  content: string
}

export interface Profile {
  id: string
  name: string
  kernel: string
  format: string
  description: string
  enabled: boolean
  default: boolean
  sections: ProfileSection[]
  created_at: string
  updated_at: string
}

export interface ProfileListResult {
  profiles: Profile[]
  active: string
  total: number
}

export interface ProfileKernelOption {
  kernel: string
  label: string
  format: string
  doc_url: string
}

export interface ProfileOptions {
  kernels: ProfileKernelOption[]
  section_keys: string[]
  section_defs: ProfileSection[]
  doc_urls: Record<string, string>
}

export interface ProfileValidateResult {
  ok: boolean
  validated: boolean
  content: string
  format: string
  message: string
}

export interface ProfileRenderResult {
  ok: boolean
  content: string
  format: string
  name: string
  kernel: string
}

export interface ProfileSplitResult {
  ok: boolean
  sections: ProfileSection[]
  format: string
  kernel: string
  message: string
  warnings?: string[]
  changed: string[]
}

export interface ProfileMutationResult {
  ok: boolean
  profile: Profile
}

export const getProtocolCatalog = () => get<ProtocolCatalog>('/protocols/catalog')
export const refreshProtocolCatalog = () =>
  post<ProtocolCatalog>('/protocols/catalog/refresh', {})
export const getProfileOptions = () => get<ProfileOptions>('/profiles/options')
export const listProfiles = () => get<ProfileListResult>('/profiles')
export const getProfile = (id: string) =>
  get<Profile>(`/profiles/${encodeURIComponent(id)}`)
export const createProfile = (p: Partial<Profile>) =>
  post<ProfileMutationResult>('/profiles', p)
export const updateProfile = (id: string, p: Partial<Profile>) =>
  put<ProfileMutationResult>(`/profiles/${encodeURIComponent(id)}`, p)
export const deleteProfile = (id: string) =>
  del<GenericResult>(`/profiles/${encodeURIComponent(id)}`)
export const getProfileTemplate = (kernel: string, name: string) =>
  post<Profile>('/profiles/template', { kernel, name })
export const validateProfile = (p: Partial<Profile>) =>
  post<ProfileValidateResult>('/profiles/validate', p)
// Reverse of validate/render: an edited complete config is split back into
// the six editable sections. `config` lets the editor silence the global
// error toast while auto-syncing on every keystroke.
export const splitProfile = (
  payload: { profile: Partial<Profile>; content: string },
  config?: AxiosRequestConfig,
) => post<ProfileSplitResult>('/profiles/split', payload, config)
export const duplicateProfile = (id: string, name: string) =>
  post<ProfileMutationResult>(`/profiles/${encodeURIComponent(id)}/duplicate`, { name })
export const activateProfile = (id: string) =>
  post<GenericResult>(`/profiles/${encodeURIComponent(id)}/activate`, {})
export const renderProfile = (id: string) =>
  post<ProfileRenderResult>(`/profiles/${encodeURIComponent(id)}/render`, {})
export interface ProfileToggleResult {
  ok: boolean
  changed: number
  enabled: boolean
  scope: string
  profiles: Profile[]
  active: string
}
export const toggleProfiles = (payload: {
  ids?: string[]
  all?: boolean
  enabled: boolean
}) => post<ProfileToggleResult>('/profiles/toggle', payload)

/* ------------------------------------------------------------------ *
 * 后量子 /pq
 * ------------------------------------------------------------------ */

export interface PqStatus {
  suite: string
  mlkem: string
  classical: string
  kdf: string
  aead: string
  key_bytes: number
  implemented: boolean
  go_version: string
  note: string
  [key: string]: unknown
}

export const getPqStatus = () => get<PqStatus>('/pq/status')
export const pqSelftest = () => post<GenericResult>('/pq/selftest')

/* ------------------------------------------------------------------ *
 * 插件运行时 /plugins
 * ------------------------------------------------------------------ */

export interface PluginManifest {
  id: string
  name: string
  kind: 'javascript' | 'wasm' | string
  source: string
  hooks: string[]
  permissions: string[]
  enabled: boolean
  description: string
  [key: string]: unknown
}

export type Plugin = PluginManifest

export interface PluginTestResult {
  result?: unknown
  [key: string]: unknown
}

export interface PluginReloadResult {
  loaded: number
  problems: string[]
  [key: string]: unknown
}

export const listPlugins = () => get<Plugin[]>('/plugins')
export const installPlugin = (manifest: PluginManifest) =>
  post<GenericResult>('/plugins', manifest)
export const enablePlugin = (id: string, enabled: boolean) =>
  post<GenericResult>(`/plugins/${encodeURIComponent(id)}/enable`, { enabled })
export const testPlugin = (id: string, input: Record<string, unknown>) =>
  post<PluginTestResult>(`/plugins/${encodeURIComponent(id)}/test`, { input })
export const deletePlugin = (id: string) =>
  del<GenericResult>(`/plugins/${encodeURIComponent(id)}`)
export const reloadPlugins = () => post<PluginReloadResult>('/plugins/reload')

/* ------------------------------------------------------------------ *
 * Stage 5 · SD-WAN /sdwan
 * ------------------------------------------------------------------ */

export interface SdwanLink {
  id: string
  name: string
  interface: string
  gateway: string
  probe_target: string
  weight: number
  cost: number
  priority: number
  enabled: boolean
  // 运行时健康字段（来自 /sdwan/status 的 links）
  healthy?: boolean
  rtt_ms?: number
  latency_ms?: number
  loss?: number
  packet_loss?: number
  probes?: number
  probe_count?: number
  [key: string]: unknown
}

export interface SdwanStatus {
  policy: string
  active: string
  running: boolean
  switches: number
  links: SdwanLink[]
  [key: string]: unknown
}

export const getSdwanStatus = () => get<SdwanStatus>('/sdwan/status')
export const listSdwanLinks = () => get<SdwanLink[]>('/sdwan/links')
export const addSdwanLink = (link: SdwanLink) =>
  post<GenericResult>('/sdwan/links', link)
export const deleteSdwanLink = (id: string) =>
  del<GenericResult>(`/sdwan/links/${encodeURIComponent(id)}`)
export const startSdwan = () => post<GenericResult>('/sdwan/start')
export const stopSdwan = () => post<GenericResult>('/sdwan/stop')
export const failoverSdwan = () => post<GenericResult>('/sdwan/failover')
export const setSdwanPolicy = (policy: string) =>
  post<GenericResult>('/sdwan/policy', { policy })

/* ------------------------------------------------------------------ *
 * 跨平台 /platforms
 * ------------------------------------------------------------------ */

export interface PlatformInfo {
  host: Record<string, unknown> | string
  go: string
  targets: string[]
  clients: string[]
  note: string
  [key: string]: unknown
}

export interface PlatformBuildResult {
  target: string
  ok: boolean
  output: string
  path: string
  [key: string]: unknown
}

export const getPlatforms = () => get<PlatformInfo>('/platforms')
export const buildPlatforms = (targets: string[]) =>
  post<PlatformBuildResult[]>('/platforms/build', { targets })

/* ------------------------------------------------------------------ *
 * 阶段总览 /stages
 * ------------------------------------------------------------------ */

export interface StageInfo {
  id?: string | number
  stage?: string | number
  name: string
  status: string
  note: string
  [key: string]: unknown
}

export const getStages = () => get<StageInfo[]>('/stages')

/* ------------------------------------------------------------------ *
 * 配置引擎 · 入口节点 / DNS / 出口节点 配置补丁
 * PUT /network/config 支持扁平策略字段与嵌套 ingress/dns/tun/egress，
 * 未提供的字段保持不变（后端按字段 patch，不用零值覆盖）。
 * ------------------------------------------------------------------ */

export interface IngressNodeConfig {
  enabled: boolean
  listen: string
  port: number
  auth_user: string
  auth_pass: string
}

export interface TunNodeConfig {
  name: string
  mtu: number
  ipv4: string
  ipv6: string
  auto_route: boolean
  strict_route: boolean
}

export interface DnsEngineConfig {
  enabled: boolean
  listen: string
  port: number
  upstreams: string[]
  cache_ttl: number
  hosts: Record<string, string>
  block: string[]
}

export interface EgressNodeConfig {
  interface: string
  local_addr: string
  mark: number
  bypass_tun: boolean
}

export interface NetworkConfigPatch {
  fail_closed?: boolean
  failover?: boolean
  default_core?: string
  direct_on_failure?: boolean
  ingress?: IngressNodeConfig
  dns?: DnsEngineConfig
  tun?: TunNodeConfig
  egress?: EgressNodeConfig
}

export interface NetworkConfigPatchResult {
  ok?: boolean
  changed?: string[]
  requires_restart?: string[]
  warnings?: string[]
  persisted?: boolean
  config?: Record<string, unknown>
  [key: string]: unknown
}

export const patchNetworkConfig = (patch: NetworkConfigPatch) =>
  put<NetworkConfigPatchResult>('/network/config', patch)

/* ------------------------------------------------------------------ *
 * 配置引擎 · 入口 / 出口节点（加密协议节点）
 * ------------------------------------------------------------------ */

export type NodeKind = 'inbound' | 'outbound'

export interface ProtocolNode {
  id: string
  name: string
  kind: NodeKind
  protocol: string
  address: string
  port: number
  enabled: boolean
  core_id: string
  params: Record<string, unknown>
  tags: string[]
  description: string
}

export interface NodeProtocolInfo {
  id: string
  name: string
  category: string
  kinds: NodeKind[]
  default_port: number
  encrypted: boolean
  pq_capable: boolean
  description: string
}

export interface NodeListResult {
  nodes: ProtocolNode[]
  count: number
  path?: string
  [key: string]: unknown
}

export interface NodeConfigResult {
  ok?: boolean
  node_id?: string
  kernel?: string
  format?: string
  content?: string
  validated?: boolean
  note?: string
  [key: string]: unknown
}

const node = (id: string) => `/nodes/${encodeURIComponent(id)}`

export const listNodes = (kind?: NodeKind) =>
  get<NodeListResult>(kind ? `/nodes?kind=${kind}` : '/nodes')
export const getNode = (id: string) => get<ProtocolNode>(node(id))
export const addNode = (n: ProtocolNode) => post<GenericResult>('/nodes', n)
export const updateNode = (id: string, n: ProtocolNode) =>
  put<GenericResult>(node(id), n)
export const deleteNode = (id: string) => del<GenericResult>(node(id))
export const listNodeProtocols = (kind: NodeKind) =>
  get<NodeProtocolInfo[]>(`/nodes/protocols?kind=${kind}`)
export const exportNodes = () => get<NodeListResult>('/nodes/export')
export const importNodes = (nodes: ProtocolNode[]) =>
  put<GenericResult>('/nodes/import', { nodes })
export const generateNodeConfig = (id: string, kernel: string) =>
  post<NodeConfigResult>(`${node(id)}/config`, { kernel })

/** 单条规则编辑（配置引擎 · 规则表）。 */
export const updateRule = (id: string, rule: Rule) =>
  put<GenericResult>(`/network/rules/${encodeURIComponent(id)}`, rule)

/* ------------------------------------------------------------------ *
 * 完整内核原生配置（Xray / sing-box JSON，Mihomo YAML）
 * ------------------------------------------------------------------ */

export interface NativeRoutingBinding {
  inbound_tag: string
  outbound_tag: string
  rule_type?: string
}

export interface NativeParseResult {
  kernel: string
  nodes: ProtocolNode[]
  routing: NativeRoutingBinding[]
  detected: Record<string, number>
  warnings: string[]
  [key: string]: unknown
}

/** 解析整份内核原生配置，只返回结果、不落库。 */
export const parseNativeConfig = (kernel: string, content: string) =>
  post<NativeParseResult>('/nodes/parse', { kernel, content })

/** 解析后把识别出的入口/出口节点合并进配置引擎节点库。 */
export const importNativeNodes = (kernel: string, content: string) =>
  post<GenericResult>('/nodes/from-native', { kernel, content })

export interface NodeTemplate {
  id: string
  name: string
  kernel: string
  content: string
}

/** 内置的整份原生配置示例（3X-UI / Xray / Remnawave 风格）。 */
export const listNodeTemplates = () => get<NodeTemplate[]>('/nodes/templates')

/* ------------------------------------------------------------------ *
 * 配置引擎 · 自定义规则集（内联 / URL 订阅 / 上传附件）
 * ------------------------------------------------------------------ */

export type RuleSetSource = 'inline' | 'url' | 'upload'

export interface ParsedRule {
  type: string
  value: string
  action: string
  raw?: string
}

export interface RuleSetDef {
  id: string
  name: string
  description: string
  format: string
  source: RuleSetSource
  url: string
  filename?: string
  content?: string
  rules: ParsedRule[]
  warnings: string[]
  rule_count: number
  enabled: boolean
  kernel?: string
  created_at?: string
  updated_at?: string
}

export interface RuleSetListResult {
  rulesets: RuleSetDef[]
  count: number
  [key: string]: unknown
}

export interface RuleSetParsePreview {
  format: string
  count: number
  warnings: string[]
  sample: ParsedRule[]
}

export interface RuleSetUploadResult {
  ok: boolean
  ruleset: RuleSetDef
  parse: RuleSetParsePreview
  [key: string]: unknown
}

export const listRuleSets = () => get<RuleSetListResult>('/rulesets')
export const toggleRuleSets = (payload: TogglePayload) =>
  post<ToggleResult>('/rulesets/toggle', payload)
export const addRuleSet = (set: Partial<RuleSetDef>) =>
  post<GenericResult>('/rulesets', set)
export const updateRuleSet = (id: string, set: Partial<RuleSetDef>) =>
  put<GenericResult>(`/rulesets/${encodeURIComponent(id)}`, set)
export const deleteRuleSet = (id: string) =>
  del<GenericResult>(`/rulesets/${encodeURIComponent(id)}`)

/* ------------------------------------------------------------------ *
 * GitHub 规则集：目录发现 + 批量挂载 / 导入
 * ------------------------------------------------------------------ */

export interface GitHubPreset {
  repo: string
  label: string
  branch: string
  formats: string[]
  homepage: string
  note: string
  custom?: boolean
}

export interface GitHubCategory {
  name: string
  path: string
  file: string
  url: string
  format: string
  ext: string
}

export interface GitHubCatalog {
  repo: string
  branch: string
  format: string
  categories: GitHubCategory[]
  total: number
  synced_at: string
  source: 'github-api' | 'cache'
  message: string
  truncated: boolean
}

export interface GitHubMountItem {
  category: string
  url: string
  recognised: number
  rules_added: number
  mounted: boolean
  ruleset_id?: string
  error?: string
}

export interface GitHubMountResult {
  ok: boolean
  mode: string
  results: GitHubMountItem[]
  categories: number
  recognised: number
  rules_added: number
  mounted: number
  failed: number
  message: string
}

export const getGitHubPresets = () =>
  get<{ presets: GitHubPreset[]; custom?: string[] }>('/rulesets/github/presets')
export const getGitHubCatalog = (repo: string, branch = '', format = '', refresh = false) =>
  get<GitHubCatalog>('/rulesets/github/catalog', {
    params: { repo, branch, format, refresh: refresh ? 'true' : '' },
  })
export const refreshGitHubCatalog = (repo: string, branch = '', format = '') =>
  post<GitHubCatalog>('/rulesets/github/catalog/refresh', { repo, branch, format })
export const mountGitHubRuleSets = (payload: {
  repo: string
  branch?: string
  format?: string
  categories: string[]
  mode: 'mount' | 'import' | 'both'
  action?: string
  core_id?: string
  enabled?: boolean
}) => post<GitHubMountResult>('/rulesets/github/mount', payload)
export const fetchRuleSet = (id: string) =>
  post<GenericResult>(`/rulesets/${encodeURIComponent(id)}/fetch`)
export const applyRuleSet = (id: string, coreId?: string) =>
  post<GenericResult>(`/rulesets/${encodeURIComponent(id)}/apply`, coreId ? { core_id: coreId } : {})
export const listRuleSetFormats = () =>
  get<Array<{ id: string; name: string }>>('/rulesets/formats')

/** 上传附件：后端自动识别格式、解析规则并立即附加到规则集列表。 */
export const uploadRuleSet = (file: File, name?: string, description?: string) => {
  const form = new FormData()
  form.append('file', file)
  if (name) form.append('name', name)
  if (description) form.append('description', description)
  return client.post<RuleSetUploadResult>('/rulesets/upload', form).then((r) => r.data)
}

/* ------------------------------------------------------------------ *
 * 配置引擎 · 远程主机节点（入口 / 出口控制台共用）
 *
 * 一台主机 = 一台任意位置的 VPS / 电脑 / 软路由，角色可随时改成入站、
 * 出站或双向。online 只来自后端真实 TCP 探测，前端不生成任何模拟数值。
 * ------------------------------------------------------------------ */

export type HostRole = 'inbound' | 'outbound' | 'both'

export interface RemoteHost {
  id: string
  name: string
  internal: string
  country: string
  address: string
  node_port: number
  svc_port: number
  role: HostRole
  enabled: boolean
  protocol: string
  core_id: string
  profile: string
  secret_key: string
  proxy_url: string
  plugin: string
  provider: string
  ip_addresses: string[]
  tags: string[]
  note: string
  track_traffic: boolean
  user_ratio: number
  node_ratio: number
  created_at?: string
  updated_at?: string
}

export interface HostProbe {
  online: boolean
  latency_ms: number
  target: string
  checked_at: string
  error?: string
}

export interface HostView extends RemoteHost {
  probe: HostProbe | null
}

export interface HostStats {
  total: number
  enabled: number
  inbound: number
  outbound: number
  both: number
  online: number
  checked: number
}

export interface HostListResult {
  hosts: HostView[]
  count: number
  stats: HostStats
  path: string
}

export interface HostMetricsResult {
  registry: HostStats
  network_running?: boolean
  bytes_in?: number
  bytes_out?: number
  active?: number
  connections?: number
}

export interface HostProbeAllResult {
  ok: boolean
  checked: number
  online: number
  results: Record<string, HostProbe>
}

const host = (id: string) => `/hosts/${encodeURIComponent(id)}`

export const listHosts = (role?: HostRole | 'all') =>
  get<HostListResult>(role ? `/hosts?role=${role}` : '/hosts')
export const getHostStats = () => get<HostMetricsResult>('/hosts/stats')
export const addHost = (h: RemoteHost) => post<GenericResult>('/hosts', h)
export const updateHost = (id: string, h: RemoteHost) =>
  put<GenericResult>(host(id), h)
export const deleteHost = (id: string) => del<GenericResult>(host(id))
export const probeHost = (id: string) =>
  post<{ ok: boolean; probe: HostProbe }>(`${host(id)}/probe`)
export const probeAllHosts = () => post<HostProbeAllResult>('/hosts/probe')
export const setHostRole = (id: string, role: HostRole) =>
  post<GenericResult>(`${host(id)}/role`, { role })
export const exportHosts = () =>
  get<{ hosts: RemoteHost[]; count: number }>('/hosts/export')
export const importHosts = (hosts: RemoteHost[]) =>
  put<GenericResult>('/hosts/import', { hosts })

/* ------------------------------------------------------------------ *
 * 配置引擎 · 用户管理 /users（替代已移除的多租户）
 *
 * 状态由后端根据 enabled / expires_at / data_limit 真实推导；
 * used_bytes 只由数据面累加，前端不生成任何用量数字。
 * ------------------------------------------------------------------ */

export type UserStatus = 'active' | 'expired' | 'limited' | 'disabled'
export type UserResetStrategy = 'never' | 'monthly' | 'weekly'

export interface PortalUser {
  id: string
  username: string
  enabled: boolean
  email: string
  telegram_id: string
  tag: string
  group: string
  note: string
  last_node_id: string
  last_node_name: string
  last_seen_at: string
  expires_at: string
  data_limit: number
  used_bytes: number
  reset_strategy: UserResetStrategy
  hwid_disabled: boolean
  hwid_limit: number
  subscription_url: string
  created_at: string
  updated_at: string
}

export interface UserStats {
  total: number
  active: number
  expired: number
  limited: number
  disabled: number
}

export interface UserListResult {
  users: PortalUser[]
  count: number
  stats: UserStats
  path: string
}

const portalUser = (id: string) => `/users/${encodeURIComponent(id)}`

export const listUsers = (status?: UserStatus | 'all') =>
  get<UserListResult>(status ? `/users?status=${status}` : '/users')
export const getUserStats = () => get<{ stats: UserStats }>('/users/stats')
export const addUser = (u: PortalUser) => post<GenericResult>('/users', u)
export const updateUser = (id: string, u: PortalUser) =>
  put<GenericResult>(portalUser(id), u)
export const deleteUser = (id: string) => del<GenericResult>(portalUser(id))
export const resetUserUsage = (id: string) =>
  post<GenericResult>(`${portalUser(id)}/reset-usage`)

/* ------------------------------------------------------------------ *
 * 认证 /auth —— 首次创建管理员，之后只有登录
 * ------------------------------------------------------------------ */

export interface AuthPolicy {
  min_password: number
  session_hours: number
  remember_hours: number
  max_failures: number
  lockout_seconds: number
}

export interface AuthState {
  setup_required: boolean
  authenticated: boolean
  username: string
  api_token_login: boolean
  policy: AuthPolicy
  host: string
}

export interface AuthResult {
  ok: boolean
  username: string
  expires_at: string
}

export const getAuthState = () => get<AuthState>('/auth/state')

export const authSetup = (username: string, password: string) =>
  post<AuthResult>('/auth/setup', { username, password })

export const authLogin = (username: string, password: string, remember: boolean) =>
  post<AuthResult>('/auth/login', { username, password, remember })

export const authLogout = () => post<{ ok: boolean }>('/auth/logout', {})

export const changePassword = (oldPassword: string, newPassword: string) =>
  post<{ ok: boolean; message: string }>('/auth/password', {
    old_password: oldPassword,
    new_password: newPassword,
  })

/* ------------------------------------------------------------------ *
 * 链路编排 /pipeline —— 把配置引擎六个二级页面串成一条可执行链路
 * ------------------------------------------------------------------ */

export type PipelineStepStatus = 'ok' | 'warn' | 'fail' | 'skip'

export interface PipelineStep {
  key: string
  title: string
  order: number
  status: PipelineStepStatus
  value: string
  detail: string
  page: string
  fix_hint: string
}

export interface PipelineInbound {
  host_id: string
  listen: string
  port: number
  allow_lan: boolean
}

export interface PipelineOutbound {
  host_ids: string[]
  mode: 'select' | 'url-test' | 'fallback'
  direct_fallback: boolean
}

export interface PipelineConfig {
  id: string
  name: string
  kernel: string
  protocol: string
  inbound: PipelineInbound
  outbound: PipelineOutbound
  rule_set_ids: string[]
  dns_enabled: boolean
  user_ids: string[]
  enabled: boolean
  test_target: string
  updated_at: string
}

export interface PipelineView {
  config: PipelineConfig
  steps: PipelineStep[]
  blocked: boolean
  warning_count: number
  store_path: string
}

export interface PipelineValidateResult {
  steps: PipelineStep[]
  blocked: boolean
  blocking_step: PipelineStep | null
}

export interface PipelineApplyResult {
  ok: boolean
  kernel?: string
  restarted?: boolean
  steps?: PipelineStep[]
  warnings?: string[]
  listen?: string
  error?: string
}

export interface PipelineTestResult {
  ok: boolean
  listen: string
  target: string
  latency_ms?: number
  message: string
  error?: string
}

export const getPipeline = () => get<PipelineView>('/pipeline')
export const savePipeline = (cfg: PipelineConfig) =>
  put<{ ok: boolean; config: PipelineConfig; view: PipelineView }>('/pipeline', cfg)
export const validatePipeline = () => post<PipelineValidateResult>('/pipeline/validate', {})
export const renderPipeline = () =>
  post<{ ok: boolean; content: string; warnings: string[]; lines: number }>('/pipeline/render', {})
export const applyPipeline = () => post<PipelineApplyResult>('/pipeline/apply', {})
export const testPipeline = (target?: string) =>
  post<PipelineTestResult>('/pipeline/test', target ? { target } : {})

/* ================================================================== *
 * v2.3.0 · 规则配置 / 接管全局 / DNS / 节点 / 用户管理
 * 契约见同批后端实现；此处只放类型与请求函数，不含模拟数据。
 * ================================================================== */

/** 三内核官方配置 schema 的公共字段描述。 */
export interface KernelConfigField {
  key: string
  label: string
  type: 'text' | 'number' | 'bool' | 'select' | 'list'
  options?: string[]
  default: string
  description: string
  required: boolean
}

export interface KernelConfigSchema {
  kernel: string
  label: string
  format: 'yaml' | 'json'
  syntax: string
  doc_url: string
  doc_name: string
  description: string
  fields: KernelConfigField[]
  example: string
}

export interface RuleSetSyntaxResult { schemas: KernelConfigSchema[] }
export interface DnsSchemaResult { schemas: KernelConfigSchema[] }

// 规则配置 -------------------------------------------------------------
export interface RuleDeleteResult {
  ok: boolean
  deleted: number
  scope: 'selected' | 'all'
  rules: Rule[]
  default_action: string
  default_core: string
}
export const deleteRules = (payload: { ids?: string[]; all?: boolean }) =>
  post<RuleDeleteResult>('/network/rules/delete', payload)
export const getRuleSetSyntax = () => get<RuleSetSyntaxResult>('/rulesets/syntax')
export const addGitHubPreset = (preset: Partial<GitHubPreset>) =>
  post<{ ok: boolean; preset: GitHubPreset }>('/rulesets/github/presets', preset)
export const deleteGitHubPreset = (repo: string) =>
  del<{ ok: boolean; deleted: string }>(`/rulesets/github/presets/${encodeURIComponent(repo)}`)

// 接管全局 -------------------------------------------------------------
export interface NetworkComponent {
  key: string
  label: string
  enabled: boolean
  running: boolean
  description: string
}
export interface ComponentToggleResult {
  ok: boolean
  key: string
  enabled: boolean
  running: boolean
  requires_restart: boolean
  message: string
}
export const toggleNetworkComponent = (payload: { key: string; enabled: boolean }) =>
  post<ComponentToggleResult>('/network/components/toggle', payload)

// DNS ------------------------------------------------------------------
export const getDnsSchemas = () => get<DnsSchemaResult>('/profiles/dns-schema')

// 入口 / 出口节点 ------------------------------------------------------
export interface NodeMetrics {
  ok: boolean
  node_id: string
  cpu_percent: number
  memory_rss: number
  bytes_in: number
  bytes_out: number
  active_sessions: number
  sampled_at: string
  source: 'local' | 'unavailable'
  note: string
}
export interface NodeKeyPair {
  ok: boolean
  kind: string
  private_key: string
  public_key: string
  uuid?: string
  password?: string
  created_at: string
}
export interface NodePairResult {
  ok: boolean
  kind: string
  paired: boolean
  persisted: boolean
  inbound: Record<string, unknown>
  outbound: Record<string, unknown>
}
export interface NodeProbeResult {
  ok: boolean
  node_id: string
  address: string
  port: number
  reachable: boolean
  latency_ms: number
  error?: string
  checked_at: string
}
export interface NodeConfigFileResult {
  ok: boolean
  node_id: string
  path: string
  bytes: number
  validated: boolean
}
export const getNodeMetrics = (id: string) =>
  get<NodeMetrics>(`/nodes/${encodeURIComponent(id)}/metrics`)
export const generateNodeKeys = (payload: { kind: 'wireguard' | 'uuid' | 'password' }) =>
  post<NodeKeyPair>('/nodes/keys/generate', payload)
export const pairNodeKeys = (payload: { inbound_id: string; outbound_id: string; kind: string }) =>
  post<NodePairResult>('/nodes/keys/pair', payload)
export const probeNode = (id: string, payload: { timeout_ms?: number } = {}) =>
  post<NodeProbeResult>(`/nodes/${encodeURIComponent(id)}/probe`, payload)
export const saveNodeConfigFile = (id: string, payload: { content: string; format?: string }) =>
  put<NodeConfigFileResult>(`/nodes/${encodeURIComponent(id)}/config-file`, payload)
