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
export const installCore = (id: string, version?: string) =>
  post<ActionResult>(`${core(id)}/install`, version ? { version } : {})
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
  chain?: string
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

export const getRules = () => get<RulesConfig>('/network/rules')
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
 * Stage 4 · 协议中心 /protocols
 * ------------------------------------------------------------------ */

export interface Protocol {
  id: string
  name: string
  category: string
  transport: string
  default_port: number
  kernels: string[]
  pq_capable: boolean
  enabled: boolean
  [key: string]: unknown
}

export interface ProtocolConfigResult {
  content: string
  format: string
  validated: boolean
  [key: string]: unknown
}

export const listProtocols = (category?: string) =>
  get<Protocol[]>('/protocols', category ? { params: { category } } : undefined)
export const getProtocol = (id: string) =>
  get<Protocol>(`/protocols/${encodeURIComponent(id)}`)
export const enableProtocol = (id: string, enabled: boolean) =>
  post<GenericResult>(`/protocols/${encodeURIComponent(id)}/enable`, { enabled })
export const generateProtocolConfig = (
  id: string,
  kernel: string,
  params: Record<string, unknown>,
) =>
  post<ProtocolConfigResult>(`/protocols/${encodeURIComponent(id)}/config`, {
    kernel,
    params,
  })

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
 * 多租户 /tenants
 * ------------------------------------------------------------------ */

export interface Tenant {
  id: string
  name: string
  enabled: boolean
  max_sessions: number
  quota_bytes: number
  used_bytes: number
  allowed_cores: string[]
  allowed_protocols: string[]
  default_core: string
  rule_set_tag: string
  [key: string]: unknown
}

export const listTenants = () => get<Tenant[]>('/tenants')
export const createTenant = (tenant: Tenant) =>
  post<GenericResult>('/tenants', tenant)
export const deleteTenant = (id: string) =>
  del<GenericResult>(`/tenants/${encodeURIComponent(id)}`)

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
