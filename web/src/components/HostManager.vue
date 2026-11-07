<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDescriptions,
  NDescriptionsItem,
  NDrawer,
  NDrawerContent,
  NEmpty,
  NGrid,
  NGi,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type {
  HostChainHop,
  HostProvision,
  HostRole,
  HostStats,
  HostView,
  ProvisionProtocol,
  RemoteHost,
} from '../api/ngg'
import client from '../api/client'
import { useApiAction } from '../composables/useApi'
import { formatBytes, formatTime } from '../utils/format'

const props = defineProps<{
  role: HostRole | 'all'
  title: string
  subtitle: string
}>()

const { isBusy, run } = useApiAction()

/* ------------------------------------------------------------------ *
 * 选项（全部来自真实后端，不使用硬编码的假数据）
 * ------------------------------------------------------------------ */

const COUNTRIES: Array<{ code: string; name: string; flag: string }> = [
  { code: 'US', name: '美国 United States', flag: '🇺🇸' },
  { code: 'CN', name: '中国 China', flag: '🇨🇳' },
  { code: 'HK', name: '中国香港 Hong Kong, China', flag: '🇭🇰' },
  { code: 'MO', name: '中国澳门 Macao, China', flag: '🇲🇴' },
  { code: 'TW', name: '中国台湾 Taiwan, China', flag: '🇹🇼' },
  { code: 'JP', name: '日本 Japan', flag: '🇯🇵' },
  { code: 'SG', name: '新加坡 Singapore', flag: '🇸🇬' },
  { code: 'KR', name: '韩国 Korea', flag: '🇰🇷' },
  { code: 'DE', name: '德国 Germany', flag: '🇩🇪' },
  { code: 'GB', name: '英国 United Kingdom', flag: '🇬🇧' },
  { code: 'FR', name: '法国 France', flag: '🇫🇷' },
  { code: 'NL', name: '荷兰 Netherlands', flag: '🇳🇱' },
  { code: 'CA', name: '加拿大 Canada', flag: '🇨🇦' },
  { code: 'AU', name: '澳大利亚 Australia', flag: '🇦🇺' },
  { code: 'IN', name: '印度 India', flag: '🇮🇳' },
  { code: 'BR', name: '巴西 Brazil', flag: '🇧🇷' },
  { code: 'OTHER', name: '其他 / 未指定', flag: '🌐' },
]
const COUNTRY_OPTIONS = COUNTRIES.map((c) => ({
  label: `${c.code} · ${c.name}`,
  value: c.code,
}))
const ROLE_OPTIONS: Array<{ label: string; value: HostRole }> = [
  { label: '入站 Inbound（接收外部流量）', value: 'inbound' },
  { label: '出站 Outbound（作为出口）', value: 'outbound' },
  { label: '双向 Both（同时入站与出站）', value: 'both' },
]
const MAX_CHAIN_HOPS = 8

/** Windows Chrome has no flag-emoji glyphs; use a compact country-code pill. */
function flag(code: string): string {
  const c = String(code || '').toUpperCase()
  return c && c !== 'OTHER' ? c : '—'
}
function roleLabel(role: string): string {
  return role === 'inbound' ? '入站' : role === 'outbound' ? '出站' : '双向'
}
function roleType(role: string): 'success' | 'info' | 'warning' | 'default' {
  return role === 'inbound' ? 'info' : role === 'outbound' ? 'success' : 'warning'
}

/* ------------------------------------------------------------------ *
 * 状态
 * ------------------------------------------------------------------ */

interface ChainHopDraft {
  kind: HostChainHop['kind']
  address: string
  port: number | null
  username: string
  password: string
  enabled: boolean
}

interface HostForm
  extends Omit<
    RemoteHost,
    'node_port' | 'svc_port' | 'user_ratio' | 'node_ratio' | 'chain'
  > {
  node_port: number | null
  svc_port: number | null
  user_ratio: number | null
  node_ratio: number | null
  chain: ChainHopDraft[]
}

/** 合并页（role=all）新建节点时的默认角色：先按「双向」建，卡片上可一键改。 */
function defaultRole(): HostRole {
  return props.role === 'all' ? 'both' : props.role
}

function emptyHost(role: HostRole): HostForm {
  return {
    id: '', name: '', internal: '', country: 'US', address: '',
    // Node Port 是 NGG Agent 的监听端口，默认 3372（新增与详情共用）。
    node_port: 3372, svc_port: 443, role, enabled: true,
    protocol: 'vless-reality', core_id: '', profile: '',
    secret_key: '', proxy_url: '', plugin: '', provider: '',
    ip_addresses: [], tags: [], note: '', track_traffic: true,
    user_ratio: 1, node_ratio: 1, chain: [],
  }
}

/** 选中的节点最近一次真实探测结果（详情页头部与底部元信息共用）。 */
const currentProbe = computed(() => hosts.value.find((x) => x.id === editingId.value)?.probe ?? null)

function toChain(chain?: HostChainHop[] | null): ChainHopDraft[] {
  return (chain ?? []).map((h) => ({
    kind: h.kind === 'http' ? 'http' : 'socks5',
    address: h.address ?? '',
    port: typeof h.port === 'number' && h.port > 0 ? h.port : 1080,
    username: h.username ?? '',
    password: h.password ?? '',
    enabled: h.enabled !== false,
  }))
}

function chainPayload(rows: ChainHopDraft[]): HostChainHop[] {
  return rows.map((r) => {
    const hop: HostChainHop = {
      kind: r.kind,
      address: r.address.trim(),
      port: Number(r.port ?? 0),
      enabled: r.enabled,
    }
    const user = r.username.trim()
    if (user) hop.username = user
    if (r.password) hop.password = r.password
    return hop
  })
}

/** 前端先按后端约束校验：kind 枚举 / 地址非空且不含协议前缀 / 端口 1–65535 / 最多 8 跳。 */
function validateChain(rows: ChainHopDraft[]): string | null {
  if (rows.length > MAX_CHAIN_HOPS) return `链式代理最多 ${MAX_CHAIN_HOPS} 跳`
  for (let i = 0; i < rows.length; i += 1) {
    const row = rows[i]
    const tag = `第 ${i + 1} 跳`
    if (row.kind !== 'socks5' && row.kind !== 'http') return `${tag}类型无效`
    const addr = row.address.trim()
    if (!addr) return `${tag}地址不能为空`
    if (addr.includes('://')) return `${tag}地址不要带协议前缀`
    if (/\s/.test(addr)) return `${tag}地址不能包含空格`
    const port = Number(row.port ?? 0)
    if (!Number.isInteger(port) || port < 1 || port > 65535) return `${tag}端口需在 1–65535`
  }
  return null
}

function chainHops(chain?: HostChainHop[] | null): HostChainHop[] {
  return (chain ?? []).filter((h) => h.enabled !== false)
}

/** 直连 / SOCKS5 → HTTP 摘要。 */
function chainLabel(chain?: HostChainHop[] | null): string {
  const hops = chainHops(chain)
  if (!hops.length) return '直连'
  return hops.map((h) => (h.kind === 'http' ? 'HTTP' : 'SOCKS5')).join(' → ')
}

function toForm(h: RemoteHost): HostForm {
  const { chain, ...rest } = h
  return {
    ...rest,
    ip_addresses: [...(h.ip_addresses ?? [])],
    tags: [...(h.tags ?? [])],
    chain: toChain(chain),
  }
}
function toPayload(f: HostForm): RemoteHost {
  return {
    ...f,
    node_port: Number(f.node_port ?? 0),
    svc_port: Number(f.svc_port ?? 0),
    user_ratio: Number(f.user_ratio ?? 1),
    node_ratio: Number(f.node_ratio ?? 1),
    ip_addresses: f.ip_addresses ?? [],
    tags: f.tags ?? [],
    chain: chainPayload(f.chain),
  }
}

const hosts = ref<HostView[]>([])
const stats = ref<HostStats | null>(null)
const metrics = ref<api.HostMetricsResult | null>(null)
const protocolOptions = ref<Array<{ label: string; value: string }>>([])
const profileOptions = ref<Array<{ label: string; value: string }>>([])
const keyword = ref('')
const showAll = ref(false)

const showDrawer = ref(false)
const showAdd = ref(false)
const editingId = ref<string | null>(null)
const form = ref<HostForm>(emptyHost(defaultRole()))
const addForm = ref<HostForm>(emptyHost(defaultRole()))
const newIP = ref('')
const tagInput = ref('')
const revealSecret = ref(false)

/** 网络引擎未启动时，0 字节不是「真实流量」，应如实显示未采集。 */
const hasMetrics = computed(() => metrics.value?.network_running === true)

const filtered = computed(() => {
  const q = keyword.value.trim().toLowerCase()
  if (!q) return hosts.value
  return hosts.value.filter((h) =>
    [h.name, h.internal, h.address, h.id, h.provider, ...(h.tags ?? [])]
      .join(' ')
      .toLowerCase()
      .includes(q),
  )
})

async function refresh() {
  const scope = props.role === 'all' || showAll.value ? 'all' : props.role
  const res = await run('list', () => api.listHosts(scope))
  if (res) {
    hosts.value = res.hosts ?? []
    stats.value = res.stats ?? null
    void loadAllProvisions()
  }
  const m = await run('metrics', () => api.getHostStats())
  if (m) metrics.value = m
  await loadMonitor()
}

async function loadOptions() {
  const p = await run('protocols', () => api.listNodeProtocols('outbound'))
  if (p) protocolOptions.value = p.map((x) => ({ label: `${x.id} · ${x.name}`, value: x.id }))
  const f = await run('profiles', () => api.listProfiles())
  if (f) {
    profileOptions.value = [
      { label: '不绑定（使用内核默认）', value: '' },
      ...(f.profiles ?? []).map((x) => ({ label: `${x.name}（${x.kernel}）`, value: x.id })),
    ]
  }
}

/* ------------------------------------------------------------------ *
 * 协议配置绑定 + SSH 系统信息（真实采集，未配置时一律显示「未采集」）
 * ------------------------------------------------------------------ */
const monitorMetrics = ref<Record<string, api.HostMonitorMetrics>>({})
const monitorConfigs = ref<api.HostMonitorConfig[]>([])
const monitorIntervalSec = ref(30)
let monitorTimer: number | undefined

function monitorOf(h: HostView): api.HostMonitorMetrics | null {
  return monitorMetrics.value[h.id] ?? null
}

function monitorConfigOf(h: HostView): api.HostMonitorConfig | null {
  return (
    monitorConfigs.value.find((c) => c.host_id === h.id || (!c.host_id && c.host === h.address)) ?? null
  )
}

/** 后端按 host_id 存指标；host_id 为空的预置配置按其地址回填到对应节点。 */
async function loadMonitor(): Promise<void> {
  const res = await run('monitor', () => api.getHostMonitor())
  if (!res) return
  monitorConfigs.value = res.configs ?? []
  monitorIntervalSec.value = res.interval_sec || 30
  const map: Record<string, api.HostMonitorMetrics> = {}
  for (const m of res.metrics ?? []) {
    if (m.host_id) {
      map[m.host_id] = m
      continue
    }
    const cfg = (res.configs ?? []).find((c) => !c.host_id && !!c.host)
    const target = cfg ? hosts.value.find((h) => h.address === cfg.host) : null
    if (target) map[target.id] = { ...m, host_id: target.id }
  }
  monitorMetrics.value = map
}

async function bindProfile(h: HostView, profileId: string): Promise<void> {
  const payload = { ...h, profile: profileId } as api.RemoteHost
  const res = await run('bind:' + h.id, () => api.updateHost(h.id, payload), '配置文件已绑定')
  if (res) await refresh()
}

const showMonitor = ref(false)
const monitorHost = ref<HostView | null>(null)
const monitorForm = ref({ host: '', port: 22, user: 'root', password: '' })

function openMonitor(h: HostView): void {
  monitorHost.value = h
  const c = monitorConfigOf(h)
  monitorForm.value = {
    host: c?.host || h.address,
    port: c?.port || 22,
    user: c?.user || 'root',
    password: '',
  }
  showMonitor.value = true
}

async function saveMonitor(): Promise<void> {
  const h = monitorHost.value
  if (!h) return
  const payload: api.HostMonitorInput = {
    host: monitorForm.value.host.trim(),
    port: Number(monitorForm.value.port) || 22,
    user: monitorForm.value.user.trim() || 'root',
  }
  // 留空表示沿用已保存的密码，避免每次编辑都被清空。
  if (monitorForm.value.password) payload.password = monitorForm.value.password
  const res = await run(
    'monitor-save',
    () => api.setHostMonitor(h.id, payload),
    'SSH 监控已保存并开始采集',
  )
  if (res) {
    showMonitor.value = false
    await loadMonitor()
  }
}

async function removeMonitor(): Promise<void> {
  const h = monitorHost.value
  if (!h) return
  const res = await run('monitor-del', () => api.deleteHostMonitor(h.id), 'SSH 监控已停用')
  if (res) {
    showMonitor.value = false
    await loadMonitor()
  }
}

async function refreshMonitorNow(h: HostView): Promise<void> {
  await run('monitor-refresh:' + h.id, () => api.refreshHostMonitor(h.id))
  await loadMonitor()
}

function barPct(used: number, total: number): number {
  if (!total) return 0
  return Math.max(0, Math.min(100, Math.round((used / total) * 100)))
}
function rateText(v: number): string {
  return v > 0 ? `${formatBytes(v)}/s` : '0 B/s'
}
function levelClass(v: number): string {
  if (v >= 85) return 'is-high'
  if (v >= 65) return 'is-warn'
  return 'is-ok'
}

watch(showAll, () => { void refresh() })

/* ------------------------------------------------------------------ *
 * 操作
 * ------------------------------------------------------------------ */

function openAdd() {
  addForm.value = emptyHost(defaultRole())
  showAdd.value = true
}

async function createHost() {
  if (!addForm.value.id.trim() || !addForm.value.name.trim() || !addForm.value.address.trim()) {
    window.$message?.warning('请填写 ID、节点名称与地址')
    return
  }
  const chainError = validateChain(addForm.value.chain)
  if (chainError) {
    window.$message?.warning(chainError)
    return
  }
  const res = await run('add', () => api.addHost(toPayload(addForm.value)), '节点已添加并执行真实连通性探测')
  if (res) {
    showAdd.value = false
    await refresh()
  }
}

function openDetail(h: HostView) {
  editingId.value = h.id
  form.value = toForm(h)
  revealSecret.value = false
  newIP.value = ''
  showDrawer.value = true
}

async function saveHost() {
  if (!editingId.value) return
  if (!form.value.name.trim() || !form.value.address.trim()) {
    window.$message?.warning('节点名称与地址不能为空')
    return
  }
  const chainError = validateChain(form.value.chain)
  if (chainError) {
    window.$message?.warning(chainError)
    return
  }
  const res = await run('save', () => api.updateHost(editingId.value!, toPayload(form.value)), '节点已保存')
  if (res) {
    showDrawer.value = false
    await refresh()
  }
}

function confirmDelete(h: Pick<HostView, 'id' | 'name' | 'address'>) {
  window.$dialog?.warning({
    title: '删除节点',
    content: `确定从节点库移除 ${h.name}（${h.address}）吗？此操作只删除 NGG 中的登记信息，不会改动该服务器。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await run('del:' + h.id, () => api.deleteHost(h.id), '节点已删除')
      if (res) {
        if (editingId.value === h.id) showDrawer.value = false
        await refresh()
      }
    },
  })
}

async function switchRole(h: Pick<HostView, 'id' | 'name'>, role: HostRole) {
  const res = await run(
    'role:' + h.id,
    () => api.setHostRole(h.id, role),
    `${h.name} 已设为${roleLabel(role)}`,
  )
  if (res) {
    if (editingId.value === h.id) form.value.role = role
    await refresh()
  }
}

async function probeOne(h: HostView) {
  const res = await run('probe:' + h.id, () => api.probeHost(h.id))
  if (res) {
    window.$message?.[res.probe.online ? 'success' : 'warning'](
      res.probe.online
        ? `${h.name} 可达（${res.probe.latency_ms} ms）`
        : `${h.name} 不可达：${res.probe.error ?? '连接失败'}`,
    )
    await refresh()
  }
}

async function probeAll() {
  const res = await run('probe-all', () => api.probeAllHosts())
  if (res) {
    window.$message?.info(`真实探测完成：${res.checked} 个节点，${res.online} 个可达`)
    await refresh()
  }
}

function addIP() {
  const v = newIP.value.trim()
  if (!v) return
  if (!form.value.ip_addresses.includes(v)) form.value.ip_addresses.push(v)
  newIP.value = ''
}

function addTagFromInput() {
  const v = tagInput.value.trim()
  if (!v) return
  for (const t of v.split(/[,，\s]+/)) {
    if (t && !form.value.tags.includes(t)) form.value.tags.push(t)
  }
  tagInput.value = ''
}

async function copySummary(h: HostView) {
  const text = [
    `NGG 节点：${h.name}（${h.id}）`,
    `角色：${roleLabel(h.role)}`,
    `地址：${h.address}:${h.node_port}`,
    `协议：${h.protocol || '未设置'}`,
    `状态：${h.probe ? (h.probe.online ? `在线 ${h.probe.latency_ms}ms` : '离线') : '未检测'}`,
  ].join('\n')
  try {
    await navigator.clipboard.writeText(text)
    window.$message?.success('节点信息已复制')
  } catch {
    window.$message?.warning('浏览器未授予剪贴板权限')
  }
}

function probeText(h?: HostView | null): string {
  if (!h || !h.probe) return '未检测'
  return h.probe.online ? `在线 · ${h.probe.latency_ms} ms` : `离线 · ${h.probe.error ?? '不可达'}`
}
function probeType(h: HostView): 'success' | 'error' | 'default' {
  if (!h.probe) return 'default'
  return h.probe.online ? 'success' : 'error'
}

function cardStatusClass(h: HostView): string {
  if (!h.probe) return 'host-card--unknown'
  return h.probe.online ? 'host-card--online' : 'host-card--offline'
}

/* ------------------------------------------------------------------ *
 * VPS 部署（provision）：把已登记的主机真正绑定成可用节点
 * 脚本由后端生成，只含该节点自己的凭据；NGG 的会话凭据不会进入脚本。
 * ------------------------------------------------------------------ */

const PROVISION_DEFAULT_PORT: Record<ProvisionProtocol, number> = {
  vless: 443,
  trojan: 443,
  shadowsocks: 8388,
}

const provisions = ref<Record<string, HostProvision>>({})
const showDeploy = ref(false)
const deployHost = ref<HostView | null>(null)
const deployProtocol = ref<ProvisionProtocol>('vless')
/** 绑定档案时协议可能是 hysteria2 / anytls 等，不在最小配置模式的枚举里。 */
function isProvisionProtocol(v: string): v is ProvisionProtocol {
  return Object.prototype.hasOwnProperty.call(PROVISION_DEFAULT_PORT, v)
}
const deployPort = ref<number | null>(443)
const deploySni = ref('')
const deployProvision = ref<HostProvision | null>(null)

/* 部署时可绑定「协议配置」里的档案：脚本会安装该内核并原样写入该文件。 */
const deployProfiles = ref<api.Profile[]>([])
const deployProfileId = ref<string | null>(null)
let profilesLoaded = false

const deployProfileOptions = computed(() =>
  deployProfiles.value.map((p) => ({
    label: `${p.name} · ${p.kernel}/${p.format}${p.enabled ? '' : '（未启用）'}`,
    value: p.id,
  })),
)
const selectedProfile = computed(
  () => deployProfiles.value.find((p) => p.id === deployProfileId.value) ?? null,
)

async function loadDeployProfiles(): Promise<void> {
  if (profilesLoaded) return
  try {
    const res = await api.listProfiles()
    deployProfiles.value = res.profiles ?? []
    profilesLoaded = true
  } catch {
    // 读不到档案时保持空列表并如实提示，不伪造选项
  }
}

function provisionLabel(h: HostView): string {
  const p = provisions.value[h.id]
  return p ? `${p.protocol.toUpperCase()} :${p.port}` : '未部署'
}

/** 读取已保存的部署脚本：404 表示「还没生成过」，必须静默，不能弹全局错误。 */
async function loadProvision(id: string): Promise<void> {
  try {
    const res = await client.get<HostProvision>(`/hosts/${encodeURIComponent(id)}/provision`, {
      silentError: true,
    })
    provisions.value = { ...provisions.value, [id]: res.data }
    if (deployHost.value?.id === id) deployProvision.value = res.data
  } catch {
    // 无已保存的部署脚本
  }
}

async function loadAllProvisions(): Promise<void> {
  await Promise.allSettled(hosts.value.map((h) => loadProvision(h.id)))
}

function openDeploy(h: HostView): void {
  deployHost.value = h
  const saved = provisions.value[h.id]
  deployProvision.value = saved ?? null
  deployProfileId.value = saved?.profile_id || h.profile || null
  if (saved) {
    if (isProvisionProtocol(saved.protocol)) deployProtocol.value = saved.protocol
    deployPort.value = saved.port
    deploySni.value = saved.endpoint.sni === 'www.bing.com' ? '' : saved.endpoint.sni
  } else {
    deployProtocol.value = 'vless'
    deployPort.value = PROVISION_DEFAULT_PORT.vless
    deploySni.value = ''
  }
  showDeploy.value = true
  void loadDeployProfiles()
  void loadProvision(h.id)
}

async function generateProvision(): Promise<void> {
  const h = deployHost.value
  if (!h) return
  const profileId = deployProfileId.value ?? ''
  const port = Number(deployPort.value ?? 0)
  // 绑定档案时端口可留空（由文件内容推断）；最小配置模式必须给端口。
  if (!profileId && (!Number.isInteger(port) || port < 1 || port > 65535)) {
    window.$message?.warning('端口需在 1–65535')
    return
  }
  if (profileId && port !== 0 && (!Number.isInteger(port) || port < 1 || port > 65535)) {
    window.$message?.warning('端口需在 1–65535，或留空让脚本从配置文件推断')
    return
  }
  const res = await run(
    'deploy-gen',
    () =>
      api.provisionHost(
        h.id,
        profileId
          ? { profile_id: profileId, port: port || undefined, sni: deploySni.value.trim() }
          : { protocol: deployProtocol.value, port, sni: deploySni.value.trim() },
      ),
    profileId ? '部署脚本已按绑定的配置文件生成' : '部署脚本已生成',
  )
  if (res) {
    deployProvision.value = res
    provisions.value = { ...provisions.value, [h.id]: res }
  }
}

async function copyText(text: string, ok: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    window.$message?.success(ok)
  } catch {
    window.$message?.warning('浏览器未授予剪贴板权限')
  }
}

onMounted(() => {
  void refresh()
  void loadOptions()
  monitorTimer = window.setInterval(() => void loadMonitor(), Math.max(10, monitorIntervalSec.value) * 1000)
})

onUnmounted(() => {
  if (monitorTimer !== undefined) window.clearInterval(monitorTimer)
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">{{ title }}</h2>
        <p class="page-subtitle">{{ subtitle }}</p>
      </div>
      <NSpace :size="8" :wrap="true">
        <NButton size="small" :loading="isBusy('list')" @click="refresh">刷新</NButton>
        <NButton size="small" type="primary" @click="openAdd">＋ 新增节点</NButton>
      </NSpace>
    </div>

    <!-- 统计条：全部为真实计数 / 真实网络指标 -->
    <NGrid cols="2 m:4" responsive="screen" :x-gap="12" :y-gap="12" class="section-gap">
      <NGi>
        <NCard size="small" class="host-stat">
          <div class="host-stat__label">节点总数</div>
          <div class="host-stat__value">{{ stats?.total ?? '—' }}</div>
          <div class="host-stat__hint">当前视图 {{ filtered.length }} 个</div>
        </NCard>
      </NGi>
      <NGi>
        <NCard size="small" class="host-stat">
          <div class="host-stat__label">在线节点（真实探测）</div>
          <div class="host-stat__value host-stat__value--green">{{ stats?.online ?? '—' }}</div>
          <div class="host-stat__hint">已探测 {{ stats?.checked ?? 0 }} 个</div>
        </NCard>
      </NGi>
      <NGi>
        <NCard size="small" class="host-stat">
          <div class="host-stat__label">入站节点</div>
          <div class="host-stat__value">{{ stats?.inbound ?? '—' }}</div>
          <div class="host-stat__hint">双向 {{ stats?.both ?? 0 }} 个</div>
        </NCard>
      </NGi>
      <NGi>
        <NCard size="small" class="host-stat">
          <div class="host-stat__label">出站节点</div>
          <div class="host-stat__value">{{ stats?.outbound ?? '—' }}</div>
          <div class="host-stat__hint">启用 {{ stats?.enabled ?? 0 }} 个</div>
        </NCard>
      </NGi>
    </NGrid>

    <div class="host-metrics section-gap">
      <span>NGG 全局实时指标：</span>
      <span>下行 {{ hasMetrics && metrics?.bytes_in !== undefined ? formatBytes(Number(metrics.bytes_in)) : '未采集' }}</span>
      <span>上行 {{ hasMetrics && metrics?.bytes_out !== undefined ? formatBytes(Number(metrics.bytes_out)) : '未采集' }}</span>
      <span>活跃连接 {{ hasMetrics && metrics?.active !== undefined ? metrics.active : '未采集' }}</span>
      <span class="hint">（来自统一网络引擎，不是单节点流量；网络引擎未启动时显示未采集）</span>
    </div>

    <NCard size="small" class="section-gap">
      <NSpace align="center" :wrap="true" :size="10">
        <NInput v-model:value="keyword" size="small" clearable placeholder="搜索名称 / 地址 / 标签" style="width: 240px" />
        <template v-if="props.role !== 'all'">
          <NSpace :size="6" align="center">
            <NSwitch v-model:value="showAll" size="small" />
            <span class="hint">显示全部角色</span>
          </NSpace>
          <NTag :bordered="false" size="small">{{ showAll ? '全部节点' : (props.role === 'inbound' ? '仅入站 / 双向' : '仅出站 / 双向') }}</NTag>
        </template>
        <NTag v-else :bordered="false" size="small">入站 / 出站 / 双向 全部</NTag>
        <span class="hint">点「＋ 新增节点」登记任意服务器，卡片上可直接切换入站 / 出站角色。</span>
      </NSpace>
    </NCard>

    <div v-if="!filtered.length" class="section-gap">
      <NCard size="small">
        <NEmpty description="节点库为空。点击「＋ 新增节点」登记你的第一台服务器。">
          <template #extra>
            <NButton size="small" type="primary" @click="openAdd">＋ 新增节点</NButton>
          </template>
        </NEmpty>
      </NCard>
    </div>

    <NGrid v-else cols="1 m:2" responsive="screen" :x-gap="14" :y-gap="14" class="section-gap">
      <NGi v-for="h in filtered" :key="h.id">
        <NCard size="small" class="node-card" :class="cardStatusClass(h)">
          <template #header>
            <div class="node-card__head">
              <span class="node-card__flag">{{ flag(h.country) }}</span>
              <span class="node-card__name" :title="h.name">{{ h.name }}</span>
              <button
                type="button"
                class="node-card__role"
                :title="'点击切换为' + (h.role === 'inbound' ? '出站' : '入站')"
                @click="switchRole(h, h.role === 'inbound' ? 'outbound' : 'inbound')"
              >{{ roleLabel(h.role) }}</button>
            </div>
          </template>
          <template #header-extra>
            <NTag size="small" :bordered="false" :type="probeType(h)">
              {{ h.probe ? (h.probe.online ? '在线' : '离线') : '未检测' }}
            </NTag>
          </template>

          <div class="node-card__id">
            <span class="mono">{{ h.address }}:{{ h.node_port }}</span>
            <span class="node-card__os">{{ h.protocol || '未设协议' }}</span>
            <span v-if="monitorOf(h)?.online" class="node-card__os">{{ monitorOf(h)?.os || 'Linux' }}</span>
          </div>

          <!-- 协议配置：直接在这里绑定，部署脚本会沿用这个选择 -->
          <div class="node-bind">
            <span class="node-bind__label">协议配置文件</span>
            <NSelect
              :value="h.profile || ''"
              :options="profileOptions"
              size="small"
              clearable
              filterable
              placeholder="选择「协议配置」里的文件"
              @update:value="(v: string | null) => bindProfile(h, String(v ?? ''))"
            />
          </div>

          <!-- 真实系统信息：全部来自 SSH 只读采集，未配置时不伪造数值 -->
          <div v-if="monitorOf(h)?.online" class="node-res">
            <div class="node-res__item">
              <div class="node-res__top"><span>CPU</span><b>{{ Math.round(monitorOf(h)?.cpu_usage_pct ?? 0) }}%</b></div>
              <div class="node-res__track">
                <i :class="levelClass(monitorOf(h)?.cpu_usage_pct ?? 0)" :style="{ width: barPct(monitorOf(h)?.cpu_usage_pct ?? 0, 100) + '%' }" />
              </div>
              <div class="node-res__sub">{{ monitorOf(h)?.cpu_cores ?? 0 }} 核 · {{ monitorOf(h)?.kernel || '—' }}</div>
            </div>
            <div class="node-res__item">
              <div class="node-res__top"><span>内存</span><b>{{ barPct(monitorOf(h)?.mem_used ?? 0, monitorOf(h)?.mem_total ?? 0) }}%</b></div>
              <div class="node-res__track">
                <i :class="levelClass(barPct(monitorOf(h)?.mem_used ?? 0, monitorOf(h)?.mem_total ?? 0))" :style="{ width: barPct(monitorOf(h)?.mem_used ?? 0, monitorOf(h)?.mem_total ?? 0) + '%' }" />
              </div>
              <div class="node-res__sub">{{ formatBytes(Number(monitorOf(h)?.mem_used ?? 0)) }} / {{ formatBytes(Number(monitorOf(h)?.mem_total ?? 0)) }}</div>
            </div>
            <div class="node-res__item">
              <div class="node-res__top"><span>硬盘</span><b>{{ barPct(monitorOf(h)?.disk_used ?? 0, monitorOf(h)?.disk_total ?? 0) }}%</b></div>
              <div class="node-res__track">
                <i :class="levelClass(barPct(monitorOf(h)?.disk_used ?? 0, monitorOf(h)?.disk_total ?? 0))" :style="{ width: barPct(monitorOf(h)?.disk_used ?? 0, monitorOf(h)?.disk_total ?? 0) + '%' }" />
              </div>
              <div class="node-res__sub">{{ formatBytes(Number(monitorOf(h)?.disk_used ?? 0)) }} / {{ formatBytes(Number(monitorOf(h)?.disk_total ?? 0)) }}</div>
            </div>
            <div class="node-res__item">
              <div class="node-res__top"><span>流量</span><b>↓{{ rateText(Number(monitorOf(h)?.net_rx_rate ?? 0)) }} ↑{{ rateText(Number(monitorOf(h)?.net_tx_rate ?? 0)) }}</b></div>
              <div class="node-res__sub node-res__sub--wide">
                累计 ↓{{ formatBytes(Number(monitorOf(h)?.net_rx_bytes ?? 0)) }} · ↑{{ formatBytes(Number(monitorOf(h)?.net_tx_bytes ?? 0)) }}
              </div>
            </div>
          </div>
          <div v-else class="node-res__empty">
            <span class="hint">{{ monitorOf(h)?.error ? '采集失败：' + monitorOf(h)?.error : '系统信息未采集（CPU / 内存 / 硬盘 / 流量）' }}</span>
            <NButton size="tiny" quaternary type="primary" @click="openMonitor(h)">配置 SSH 监控</NButton>
          </div>

          <div class="node-card__foot">
            <NSpace :size="6" :wrap="true">
              <NButton size="tiny" type="primary" @click="openDetail(h)">详情</NButton>
              <NButton size="tiny" type="info" @click="openDeploy(h)">部署到 节点服务器</NButton>
              <NButton size="tiny" @click="openMonitor(h)">系统信息</NButton>
              <NButton size="tiny" :loading="isBusy('monitor-refresh:' + h.id)" @click="refreshMonitorNow(h)">刷新</NButton>
              <NButton size="tiny" quaternary type="error" @click="confirmDelete(h)">删除</NButton>
            </NSpace>
            <span class="node-card__time">{{ monitorOf(h) ? '采集于 ' + formatTime(monitorOf(h)?.collected_at ?? '') : '' }}</span>
          </div>
        </NCard>
      </NGi>
    </NGrid>

    <!-- ---------------------------------------------------------------- *
     * 节点详情抽屉：两栏布局，对齐节点管理面板的信息密度
     * ---------------------------------------------------------------- -->
    <NDrawer v-model:show="showDrawer" :width="580" placement="right">
      <NDrawerContent closable :native-scrollbar="false">
        <template #header>
          <NSpace :size="8" align="center" :wrap="true">
            <span>{{ flag(form.country) }} {{ form.name || '节点详情' }}</span>
            <NTag size="small" :bordered="false" :type="roleType(form.role)">{{ roleLabel(form.role) }}</NTag>
            <NTag v-if="currentProbe" size="small" :bordered="false" :type="currentProbe.online ? 'success' : 'error'">
              {{ currentProbe.online ? '在线' : '离线' }} · {{ currentProbe.latency_ms ?? 0 }} ms
            </NTag>
          </NSpace>
        </template>

        <div class="node-form">
          <section class="node-form__section">
            <div class="node-form__legend">角色与状态</div>
            <NGrid cols="2" :x-gap="12" :y-gap="10">
              <NGi>
                <div class="kv-label">角色（入站 / 出站 / 双向）</div>
                <NSelect v-model:value="form.role" :options="ROLE_OPTIONS" />
              </NGi>
              <NGi>
                <div class="kv-label">启用</div>
                <div class="node-form__switch">
                  <NSwitch v-model:value="form.enabled" />
                  <span class="hint">{{ form.enabled ? '参与调度' : '已停用' }}</span>
                </div>
              </NGi>
            </NGrid>
          </section>

          <section class="node-form__section">
            <div class="node-form__legend">节点信息</div>
            <NGrid cols="2" :x-gap="12" :y-gap="10">
              <NGi>
                <div class="kv-label">国家 / 地区</div>
                <NSelect v-model:value="form.country" :options="COUNTRY_OPTIONS" />
              </NGi>
              <NGi>
                <div class="kv-label">内部名称</div>
                <NInput v-model:value="form.internal" placeholder="us01" />
              </NGi>
              <NGi :span="2">
                <div class="kv-label">节点名称</div>
                <NInput v-model:value="form.name" placeholder="US us01" />
              </NGi>
              <NGi>
                <div class="kv-label">地址（IP / 域名）</div>
                <NInput v-model:value="form.address" placeholder="203.0.113.10" />
              </NGi>
              <NGi>
                <div class="kv-label">监听端口号（Node Port）</div>
                <NInputNumber v-model:value="form.node_port" :min="1" :max="65535" style="width: 100%" />
              </NGi>
              <NGi>
                <div class="kv-label">服务端口</div>
                <NInputNumber v-model:value="form.svc_port" :min="0" :max="65535" style="width: 100%" />
              </NGi>
              <NGi>
                <div class="kv-label">执行内核 core_id</div>
                <NInput v-model:value="form.core_id" placeholder="xray-core / sing-box / mihomo" />
              </NGi>
              <NGi :span="2">
                <div class="kv-label">协议配置文件（来自「协议配置」）</div>
                <NSelect
                  v-model:value="form.profile"
                  :options="profileOptions"
                  clearable
                  filterable
                  placeholder="不绑定（使用内核默认）"
                />
              </NGi>
            </NGrid>
          </section>

          <div class="node-form__meta">
            <span>节点 ID <b class="mono">{{ editingId }}</b></span>
            <span>创建 <b>{{ formatTime(form.created_at) }}</b></span>
            <span v-if="currentProbe?.target">探测目标 <b class="mono">{{ currentProbe.target }}</b></span>
          </div>
        </div>

        <template #footer>
          <NSpace justify="end" :size="8">
            <NButton
              size="small"
              :disabled="!editingId"
              @click="editingId && switchRole({ id: editingId, name: form.name }, form.role === 'inbound' ? 'outbound' : 'inbound')"
            >
              快速切换角色
            </NButton>
            <NButton
              size="small"
              type="error"
              :disabled="!editingId"
              @click="editingId && confirmDelete({ id: editingId, name: form.name, address: form.address })"
            >
              删除节点
            </NButton>
            <NButton size="small" type="primary" :loading="isBusy('save')" @click="saveHost">保存</NButton>
          </NSpace>
        </template>
      </NDrawerContent>
    </NDrawer>

    <!-- 新增节点 -->
    <NModal v-model:show="showAdd" preset="card" title="新增节点" style="width: 560px" :bordered="false">
      <NSpace vertical :size="10" style="width: 100%">
                <NGrid cols="2" :x-gap="10" :y-gap="6">
          <NGi>
            <div class="kv-label">ID（必填，唯一）</div>
            <NInput v-model:value="addForm.id" placeholder="us01" />
          </NGi>
          <NGi>
            <div class="kv-label">节点名称（必填）</div>
            <NInput v-model:value="addForm.name" placeholder="US us01" />
          </NGi>
          <NGi>
            <div class="kv-label">地址（必填）</div>
            <NInput v-model:value="addForm.address" placeholder="203.0.113.10 或 vps.example.com" />
          </NGi>
          <NGi>
            <div class="kv-label">监听端口号（Node Port）</div>
            <NInputNumber v-model:value="addForm.node_port" :min="1" :max="65535" style="width: 100%" />
          </NGi>
          <NGi>
            <div class="kv-label">国家 / 地区</div>
            <NSelect v-model:value="addForm.country" :options="COUNTRY_OPTIONS" />
          </NGi>
          <NGi>
            <div class="kv-label">角色（之后可随时改）</div>
            <NSelect v-model:value="addForm.role" :options="ROLE_OPTIONS" />
          </NGi>
        </NGrid>

        <div class="node-bind node-bind--form">
          <span class="node-bind__label">协议配置文件（来自「协议配置」）</span>
          <NSelect
            v-model:value="addForm.profile"
            :options="profileOptions"
            size="small"
            clearable
            filterable
            placeholder="不绑定（使用内核默认）"
          />
          <span class="hint">绑定后「部署到 节点服务器」会自动带上这份文件。</span>
        </div>
      </NSpace>
      <template #footer>
        <NSpace justify="end">
          <NButton size="small" @click="showAdd = false">取消</NButton>
          <NButton size="small" type="primary" :loading="isBusy('add')" @click="createHost">
            添加并探测
          </NButton>
        </NSpace>
      </template>
    </NModal>

    <!-- 部署到 节点服务器：生成可粘贴执行的脚本，并展示真实连接参数 -->
    <NModal
      v-model:show="showDeploy"
      preset="card"
      :title="`部署到 节点服务器 · ${deployHost?.name ?? ''}`"
      style="width: 760px"
      :bordered="false"
    >
      <NSpace vertical :size="12" style="width: 100%">
        <div class="hint">
          选择「协议配置」里已保存的配置文件并绑定到这台节点，脚本会安装对应内核并把该文件原样写进去，
          端口与 SNI 直接从文件推断；复制到节点服务器上以 root 执行即可。
          不选配置文件时按默认 VLESS:443 生成最小配置。
        </div>

        <div>
          <div class="kv-label">配置文件（来自「协议配置」）</div>
          <NSelect
            v-model:value="deployProfileId"
            :options="deployProfileOptions"
            clearable
            filterable
            placeholder="选择要绑定的配置文件（推荐）"
          />
          <div v-if="deployProfileId" class="hint" style="margin-top: 6px">
            <template v-if="selectedProfile">
              已绑定 · 内核 {{ selectedProfile.kernel }} / {{ selectedProfile.format }} ·
              脚本会写入该文件的当前内容（保存后重新生成即可更新）
            </template>
            <template v-else>已绑定配置文件 {{ deployProfileId }}（列表加载中）</template>
          </div>
          <div v-else-if="!deployProfiles.length" class="hint" style="margin-top: 6px">
            还没有可用的配置文件：先到「配置引擎 → 协议配置」新建并保存，再回来绑定。
          </div>
        </div>

        <NSpace align="center" :size="10" :wrap="true">
          <NButton size="small" type="primary" :loading="isBusy('deploy-gen')" @click="generateProvision">
            生成部署脚本
          </NButton>
          <span class="hint">目标机器：<span class="mono">{{ deployHost?.address ?? '—' }}</span></span>
        </NSpace>

        <template v-if="deployProvision">
          <div class="deploy-block">
            <div class="deploy-block__head">
              <span class="deploy-block__title">部署脚本（bash）</span>
              <NButton size="tiny" @click="copyText(deployProvision.script, '脚本已复制')">复制</NButton>
            </div>
            <pre class="mono deploy-pre">{{ deployProvision.script }}</pre>
          </div>

          <NCard size="small" title="连接参数（客户端按此填写）">
            <NDescriptions :column="2" size="small" label-placement="left" bordered>
              <NDescriptionsItem v-if="deployProvision.profile_name" label="绑定配置文件" :span="2">
                {{ deployProvision.profile_name }}
                <span class="mono hint">（{{ deployProvision.kernel }}）</span>
              </NDescriptionsItem>
              <NDescriptionsItem label="协议">{{ deployProvision.endpoint.protocol.toUpperCase() }}</NDescriptionsItem>
              <NDescriptionsItem label="地址"><span class="mono">{{ deployProvision.endpoint.address }}</span></NDescriptionsItem>
              <NDescriptionsItem label="端口">{{ deployProvision.endpoint.port }}</NDescriptionsItem>
              <NDescriptionsItem label="传输">{{ deployProvision.endpoint.transport }}</NDescriptionsItem>
              <NDescriptionsItem label="TLS">{{ deployProvision.endpoint.tls ? '开启（自签证书）' : '关闭' }}</NDescriptionsItem>
              <NDescriptionsItem label="SNI">{{ deployProvision.endpoint.sni }}</NDescriptionsItem>
              <NDescriptionsItem label="凭据" :span="2">
                <div class="deploy-cred">
                  <span class="mono">{{ deployProvision.endpoint.credential }}</span>
                  <NButton size="tiny" quaternary @click="copyText(deployProvision.endpoint.credential, '凭据已复制')">
                    复制凭据
                  </NButton>
                </div>
              </NDescriptionsItem>
              <NDescriptionsItem label="生成时间" :span="2">{{ formatTime(deployProvision.generated_at) }}</NDescriptionsItem>
            </NDescriptions>
          </NCard>

          <div>
            <div class="kv-label">部署说明（脚本实际行为与限制）</div>
            <ul class="deploy-notes">
              <li v-for="(n, i) in deployProvision.notes" :key="i">{{ n }}</li>
            </ul>
          </div>
        </template>
        <NAlert v-else type="info" :bordered="false">
          该节点尚未生成部署脚本。选择配置文件后点「生成部署脚本」。
        </NAlert>
      </NSpace>
    </NModal>

    <!-- 系统信息采集（SSH 只读） -->
    <NModal
      v-model:show="showMonitor"
      preset="card"
      :title="`系统信息采集 · ${monitorHost?.name ?? ''}`"
      style="width: 560px"
      :bordered="false"
    >
      <NSpace vertical :size="12" style="width: 100%">
        <NAlert type="info" :bordered="false">
          通过 SSH 执行只读命令采集 CPU / 内存 / 硬盘 / 流量，不会在服务器上安装或写入任何东西。
          凭据只保存在本机数据目录，页面不会回显密码。
        </NAlert>
        <NGrid cols="2" :x-gap="10" :y-gap="8">
          <NGi>
            <div class="kv-label">SSH 主机</div>
            <NInput v-model:value="monitorForm.host" placeholder="IP 或域名" />
          </NGi>
          <NGi>
            <div class="kv-label">端口</div>
            <NInputNumber v-model:value="monitorForm.port" :min="1" :max="65535" style="width: 100%" />
          </NGi>
          <NGi>
            <div class="kv-label">用户名</div>
            <NInput v-model:value="monitorForm.user" placeholder="root" />
          </NGi>
          <NGi>
            <div class="kv-label">密码（留空沿用已保存的）</div>
            <NInput v-model:value="monitorForm.password" type="password" show-password-on="click" placeholder="不修改请留空" />
          </NGi>
        </NGrid>
        <div class="hint">
          首次连接会记录服务器 SSH 指纹并校验后续连接，指纹变化会拒绝连接。
          每 {{ monitorIntervalSec }} 秒自动采集一次。
        </div>
        <NAlert
          v-if="monitorHost && monitorOf(monitorHost) && !monitorOf(monitorHost)?.online"
          type="warning"
          :bordered="false"
        >{{ monitorOf(monitorHost)?.error || '尚未采集到数据' }}</NAlert>
      </NSpace>
      <template #footer>
        <NSpace justify="space-between" style="width: 100%">
          <NButton
            size="small"
            type="error"
            quaternary
            :loading="isBusy('monitor-del')"
            @click="removeMonitor"
          >停用监控</NButton>
          <NSpace :size="8">
            <NButton size="small" @click="showMonitor = false">取消</NButton>
            <NButton size="small" type="primary" :loading="isBusy('monitor-save')" @click="saveMonitor">
              保存并开始采集
            </NButton>
          </NSpace>
        </NSpace>
      </template>
    </NModal>
  </div>
</template>

<style scoped>

/* ------------------------------------------------------------------ *
 * v2.11.0 节点管理：Data / Dashboard 风格（沿用项目蓝 / 中性灰 token）
 * 结构：KPI 行 → 节点卡（身份 / 绑定 / 资源 / 操作）；避免信息过载，
 * 所有交互元素保留可见文字与 focus 轮廓，不靠颜色单独表达状态。
 * ------------------------------------------------------------------ */
.node-card {
  border: 1px solid #ececf0;
  border-radius: 12px;
  background: #fff;
}
.node-card :deep(.n-card-header) {
  padding: 12px 14px 8px;
}
.node-card :deep(.n-card__content) {
  padding: 0 14px 12px;
}
.node-card__head {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.node-card__flag {
  flex: 0 0 auto;
  min-width: 28px;
  padding: 2px 5px;
  border-radius: 6px;
  background: #f2f4f7;
  color: #3a3a3c;
  font-size: 10px;
  font-weight: 700;
  text-align: center;
}
.node-card__name {
  font-size: 14px;
  font-weight: 600;
  color: #1d1d1f;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.node-card__role {
  flex: 0 0 auto;
  border: 1px solid #d2d2d7;
  border-radius: 999px;
  background: #fff;
  color: #3a3a3c;
  font-size: 10.5px;
  line-height: 18px;
  padding: 0 8px;
  cursor: pointer;
}
.node-card__role:hover { border-color: #0071e3; color: #0071e3; }
.node-card__role:focus-visible { outline: 2px solid #0071e3; outline-offset: 2px; }
.node-card__id {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 11.5px;
  color: #6e6e73;
  margin-bottom: 10px;
}
.node-card__os {
  padding: 1px 6px;
  border-radius: 5px;
  background: #f5f5f7;
  font-size: 10.5px;
  color: #6e6e73;
}
.node-bind {
  display: grid;
  grid-template-columns: 88px minmax(0, 1fr);
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border: 1px solid #ececf0;
  border-radius: 9px;
  background: #fafafb;
}
.node-bind__label {
  font-size: 11px;
  font-weight: 600;
  color: #6e6e73;
  white-space: nowrap;
}
.node-bind--form {
  grid-template-columns: 1fr;
  gap: 6px;
}
.node-res {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px 14px;
  margin-top: 10px;
}
.node-res__item { min-width: 0; }
.node-res__top {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 6px;
  font-size: 11px;
  color: #6e6e73;
}
.node-res__top b {
  font-size: 12px;
  font-weight: 600;
  color: #1d1d1f;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.node-res__track {
  height: 5px;
  margin: 4px 0 3px;
  border-radius: 3px;
  background: #eef0f3;
  overflow: hidden;
}
.node-res__track i {
  display: block;
  height: 100%;
  border-radius: 3px;
  transition: width .3s ease;
}
.node-res__track i.is-ok { background: #34c759; }
.node-res__track i.is-warn { background: #ff9f0a; }
.node-res__track i.is-high { background: #ff3b30; }
.node-res__sub {
  font-size: 10.5px;
  color: #8e8e93;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.node-res__sub--wide { white-space: normal; line-height: 1.4; }
.node-res__empty {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 10px;
  padding: 8px 10px;
  border: 1px dashed #e5e5ea;
  border-radius: 9px;
}
.node-card__foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px solid #f0f0f2;
}
.node-card__time {
  font-size: 10px;
  color: #a1a1a6;
  white-space: nowrap;
}

/* 节点详情：单列表单，按「角色与状态 / 节点信息」两段排列，宽度收到 580px。 */
.node-form {
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.node-form__section {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.node-form__legend {
  font-size: 11.5px;
  font-weight: 600;
  color: #6e6e73;
  letter-spacing: .02em;
}
.node-form__switch {
  display: flex;
  align-items: center;
  gap: 8px;
}
.node-form__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 18px;
  padding-top: 12px;
  border-top: 1px solid #f0f0f2;
  font-size: 11px;
  color: #8e8e93;
}
.node-form__meta b {
  font-weight: 500;
  color: #1d1d1f;
}

/* 节点详情：单列表单，按「角色与状态 / 节点信息」两段排列，宽度收到 580px。 */
.node-form {
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.node-form__section {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.node-form__legend {
  font-size: 11.5px;
  font-weight: 600;
  color: #6e6e73;
  letter-spacing: .02em;
}
.node-form__switch {
  display: flex;
  align-items: center;
  gap: 8px;
}
.node-form__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 18px;
  padding-top: 12px;
  border-top: 1px solid #f0f0f2;
  font-size: 11px;
  color: #8e8e93;
}
.node-form__meta b {
  font-weight: 500;
  color: #1d1d1f;
}

.host-card__chain {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 6px;
  min-width: 0;
  flex-wrap: wrap;
}
.host-card__chain-k {
  font-size: 10.5px;
  color: #8e8e93;
  flex: 0 0 auto;
}
.host-card__chain-n {
  font-size: 10.5px;
  color: #6e6e73;
  font-variant-numeric: tabular-nums;
}

/* 部署到 节点服务器 */
.deploy-block {
  border: 1px solid #ececf0;
  border-radius: 10px;
  overflow: hidden;
}
.deploy-block__head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  background: #fafafb;
  border-bottom: 1px solid #ececf0;
}
.deploy-block__title {
  font-size: 11.5px;
  font-weight: 500;
  color: #3a3a3c;
  margin-right: auto;
}
.deploy-pre {
  margin: 0;
  padding: 10px 12px;
  max-height: 300px;
  overflow: auto;
  font-size: 11.5px;
  line-height: 1.55;
  background: #fff;
  white-space: pre;
  tab-size: 2;
}
.deploy-cred {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  min-width: 0;
}
.deploy-cred .mono {
  word-break: break-all;
}
.deploy-notes {
  margin: 6px 0 0;
  padding-left: 18px;
  font-size: 11.5px;
  line-height: 1.7;
  color: #3a3a3c;
}
.deploy-notes li {
  margin-bottom: 2px;
}

</style>
