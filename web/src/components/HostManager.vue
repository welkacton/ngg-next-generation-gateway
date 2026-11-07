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
  HostRole,
  HostStats,
  HostView,
  NodeDeployMode,
  NodeDeployView,
  NodeStatusResponse,
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
/** 秒 → 天/小时/分钟，用于节点运行时长。 */
function formatUptime(sec: number): string {
  const n = Number(sec || 0)
  if (!n) return '—'
  const d = Math.floor(n / 86400)
  const h = Math.floor((n % 86400) / 3600)
  const m = Math.floor((n % 3600) / 60)
  if (d > 0) return `${d} 天 ${h} 小时`
  if (h > 0) return `${h} 小时 ${m} 分`
  return `${m} 分钟`
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
  }
  const m = await run('metrics', () => api.getHostStats())
  if (m) metrics.value = m
  await loadNodeMetrics()
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
 * 协议配置绑定 + 节点指标（面板连入节点 API 拉取，未连接即显示未连接）
 * ------------------------------------------------------------------ */
const nodeMetrics = ref<Record<string, api.NodeMetric>>({})
const nodeMetricIntervalSec = ref(30)
let metricsTimer: number | undefined

function metricsOf(h: HostView): api.NodeMetric | null {
  return nodeMetrics.value[h.id] ?? null
}

/** 后端缓存了每个已部署节点的最近一次实测结果；一次请求刷新整个网格。 */
async function loadNodeMetrics(): Promise<void> {
  try {
    const res = await api.getHostNodeMetrics()
    nodeMetricIntervalSec.value = res.interval_sec || 30
    const map: Record<string, api.NodeMetric> = {}
    for (const m of res.nodes ?? []) map[m.host_id] = m
    nodeMetrics.value = map
  } catch {
    // 指标端点不可用时保留上一份数据，不回填 0
  }
}

async function bindProfile(h: HostView, profileId: string): Promise<void> {
  const payload = { ...h, profile: profileId } as api.RemoteHost
  const res = await run('bind:' + h.id, () => api.updateHost(h.id, payload), '配置文件已绑定')
  if (res) await refresh()
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
 * 节点部署：把已登记的主机真正绑定成可用节点。
 * 脚本由后端生成，只含该节点自己的证书与凭据；面板会话凭据不会进入脚本。
 * ------------------------------------------------------------------ */

const deployHost = ref<HostView | null>(null)
const showDeploy = ref(false)

/* 绑定的「协议配置」档案：脚本会安装该内核并把文件原样写入节点。 */
const deployProfiles = ref<api.Profile[]>([])
const deployProfileId = ref<string | null>(null)
let profilesLoaded = false

const deployProfileOptions = computed(() => [
  // Without an explicit "none" row the only way back to the built-in REALITY
  // config is a small clear icon that appears on hover. Operators kept the
  // custom profile selected (and kept failing its external certificate paths).
  { label: '不绑定 · 使用内置 REALITY 配置（推荐）', value: '' },
  ...deployProfiles.value.map((p) => ({
    label: `${p.name} · ${p.kernel}/${p.format}${p.enabled ? '' : '（未启用）'}`,
    value: p.id,
  })),
])
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

async function copyText(text: string, ok: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    window.$message?.success(ok)
  } catch {
    window.$message?.warning('浏览器未授予剪贴板权限')
  }
}

/* ------------------------------------------------------------------ *
 * 节点部署（Remnawave 风格：面板下发脚本 → 面板连入节点）
 *
 * 1) 生成方案与安装脚本（含本节点证书）
 * 2) 在服务器上以 root 执行一次脚本
 * 3) 面板用双向 TLS 连入节点：健康检查 → 下发内核配置 → 读取真实指标
 * 全程没有 SSH，也不需要节点能访问面板。
 * ------------------------------------------------------------------ */

const DEPLOY_MODE_OPTIONS: Array<{ label: string; value: api.NodeDeployMode }> = [
  { label: 'VLESS + REALITY（推荐 · 无需证书）', value: 'reality' },
  { label: 'VLESS + TLS（自签证书）', value: 'vless-tls' },
  { label: 'Trojan + TLS（自签证书）', value: 'trojan-tls' },
]

const deployMode = ref<api.NodeDeployMode>('reality')
const deployPort = ref<number | null>(443)
/** 管理端口：面板连入这个端口，与客户端用的数据端口分开。 */
const deployNodePort = ref<number | null>(2244)
const deploySni = ref('')
const deployPlan = ref<api.NodeDeployView | null>(null)
const nodeStatus = ref<api.NodeStatusResponse | null>(null)
const connectResult = ref<api.NodeConnectResult | null>(null)
let statusTimer: number | undefined

const deployHasProfile = computed(() => !!deployProfileId.value)
const deployLegacy = computed(() => deployPlan.value?.legacy === true)
const deployProfileKernel = computed(() => (selectedProfile.value?.kernel ?? '').toLowerCase())
/** 在线部署只支持 xray-core：mihomo / sing-box 档案走手动部署脚本。 */
const deployKernelOk = computed(() => !deployHasProfile.value || deployProfileKernel.value === 'xray-core')

const nodeOnline = computed(() => nodeStatus.value?.online === true)
const nodeApiURL = computed(() => nodeStatus.value?.node_url || '')
const nodeMetricsLive = computed(() => nodeStatus.value?.metrics ?? null)

function stopStatusPolling(): void {
  if (statusTimer !== undefined) {
    window.clearInterval(statusTimer)
    statusTimer = undefined
  }
}

/** 打开弹窗时轮询状态：数据来自后端缓存，不在这里现场拨号。 */
function startStatusPolling(id: string): void {
  stopStatusPolling()
  statusTimer = window.setInterval(() => void loadNodeStatus(id), 15000)
}

function openDeploy(h: HostView): void {
  deployHost.value = h
  deployProfileId.value = h.profile || null
  deployMode.value = 'reality'
  deployPort.value = h.node_port || 443
  deployNodePort.value = 2244
  deploySni.value = ''
  deployPlan.value = null
  nodeStatus.value = null
  connectResult.value = null
  showDeploy.value = true
  void loadDeployProfiles()
  void loadDeployPlan(h.id)
  void loadNodeStatus(h.id)
  startStatusPolling(h.id)
}

/** 读取已保存的方案；404 表示还没生成过，必须静默。 */
async function loadDeployPlan(id: string): Promise<void> {
  try {
    const plan = await api.getNodeDeploy(id)
    deployPlan.value = plan ?? null
    if (plan?.spec) {
      if (plan.spec.mode && plan.spec.mode !== 'profile') deployMode.value = plan.spec.mode
      if (plan.spec.port) deployPort.value = plan.spec.port
      if (plan.spec.node_port) deployNodePort.value = plan.spec.node_port
      if (plan.spec.sni) deploySni.value = plan.spec.sni === 'www.bing.com' ? '' : plan.spec.sni
      // The saved plan is authoritative. Without this, opening a REALITY plan
      // on a host that still has a profile bound on its card would silently
      // re-select the custom profile and regenerate an unusable script.
      deployProfileId.value =
        plan.spec.mode === 'profile' ? (plan.spec.profile_id || null) : null
      if (plan.last_connect) connectResult.value = plan.last_connect
    }
  } catch {
    deployPlan.value = null
  }
}

async function loadNodeStatus(id: string): Promise<void> {
  if (!id) return
  try {
    nodeStatus.value = await api.getNodeStatus(id)
  } catch {
    nodeStatus.value = null
  }
}

/** 生成（或重新生成）方案：签发新证书并渲染安装脚本。 */
async function generatePlan(): Promise<void> {
  const h = deployHost.value
  if (!h) return
  const port = Number(deployPort.value ?? 0)
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    window.$message?.warning('数据端口需在 1–65535')
    return
  }
  const nodePort = Number(deployNodePort.value ?? 0)
  if (!Number.isInteger(nodePort) || nodePort < 1 || nodePort > 65535) {
    window.$message?.warning('管理端口需在 1–65535')
    return
  }
  if (nodePort === port) {
    window.$message?.warning('管理端口不能与数据端口相同')
    return
  }
  if (!deployKernelOk.value) {
    window.$message?.warning('绑定的配置文件内核不是 xray-core，无法在线部署')
    return
  }
  const res = await run(
    'plan',
    () =>
      api.deployNode(h.id, {
        mode: deployHasProfile.value ? 'profile' : deployMode.value,
        port,
        node_port: nodePort,
        sni: deploySni.value.trim(),
        profile_id: deployProfileId.value ?? '',
      }),
    '安装方案已生成：复制脚本到服务器上执行',
  )
  if (res) {
    deployPlan.value = res
    void loadNodeStatus(h.id)
  }
}

/** 连接节点：健康检查 → 下发内核配置 → 读取指标。 */
async function connectToNode(): Promise<void> {
  const h = deployHost.value
  if (!h) return
  if (deployLegacy.value) {
    window.$message?.warning('旧方案已作废：先重新生成安装脚本，并在服务器上重新执行')
    return
  }
  if (!deployPlan.value) {
    window.$message?.warning('先在服务器上执行安装脚本，再点连接')
    return
  }
  const res = await run('connect', () => api.connectNode(h.id, true))
  if (!res) return
  connectResult.value = res.last_connect
  await loadNodeStatus(h.id)
  await loadNodeMetrics()
  if (res.last_connect.ok) {
    window.$message?.success('已连上节点：内核配置已下发，指标为节点实测')
  } else {
    window.$message?.error(res.last_connect.error || '连接失败')
  }
}

async function forgetPlan(): Promise<void> {
  const h = deployHost.value
  if (!h) return
  const res = await run('forget', () => api.deleteNodeDeploy(h.id), '部署方案已删除')
  if (res) {
    deployPlan.value = null
    connectResult.value = null
    await loadNodeStatus(h.id)
  }
}

onMounted(() => {
  void refresh()
  void loadOptions()
  metricsTimer = window.setInterval(
    () => void loadNodeMetrics(),
    Math.max(10, nodeMetricIntervalSec.value) * 1000,
  )
})

onUnmounted(() => {
  if (metricsTimer !== undefined) window.clearInterval(metricsTimer)
  stopStatusPolling()
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
            <span v-if="metricsOf(h)?.online" class="node-card__os">{{ metricsOf(h)?.os || 'Linux' }}</span>
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

          <!-- 真实系统信息：面板连入节点管理端口（双向 TLS）读取，连不上就明说，不伪造数值 -->
          <div v-if="metricsOf(h)?.online" class="node-res">
            <div class="node-res__item">
              <div class="node-res__top"><span>CPU</span><b>{{ Math.round(metricsOf(h)?.cpu_usage_pct ?? 0) }}%</b></div>
              <div class="node-res__track">
                <i :class="levelClass(metricsOf(h)?.cpu_usage_pct ?? 0)" :style="{ width: barPct(metricsOf(h)?.cpu_usage_pct ?? 0, 100) + '%' }" />
              </div>
              <div class="node-res__sub">{{ metricsOf(h)?.cpu_cores ?? 0 }} 核 · {{ metricsOf(h)?.kernel || '—' }}</div>
            </div>
            <div class="node-res__item">
              <div class="node-res__top"><span>内存</span><b>{{ barPct(metricsOf(h)?.mem_used ?? 0, metricsOf(h)?.mem_total ?? 0) }}%</b></div>
              <div class="node-res__track">
                <i :class="levelClass(barPct(metricsOf(h)?.mem_used ?? 0, metricsOf(h)?.mem_total ?? 0))" :style="{ width: barPct(metricsOf(h)?.mem_used ?? 0, metricsOf(h)?.mem_total ?? 0) + '%' }" />
              </div>
              <div class="node-res__sub">{{ formatBytes(Number(metricsOf(h)?.mem_used ?? 0)) }} / {{ formatBytes(Number(metricsOf(h)?.mem_total ?? 0)) }}</div>
            </div>
            <div class="node-res__item">
              <div class="node-res__top"><span>硬盘</span><b>{{ barPct(metricsOf(h)?.disk_used ?? 0, metricsOf(h)?.disk_total ?? 0) }}%</b></div>
              <div class="node-res__track">
                <i :class="levelClass(barPct(metricsOf(h)?.disk_used ?? 0, metricsOf(h)?.disk_total ?? 0))" :style="{ width: barPct(metricsOf(h)?.disk_used ?? 0, metricsOf(h)?.disk_total ?? 0) + '%' }" />
              </div>
              <div class="node-res__sub">{{ formatBytes(Number(metricsOf(h)?.disk_used ?? 0)) }} / {{ formatBytes(Number(metricsOf(h)?.disk_total ?? 0)) }}</div>
            </div>
            <div class="node-res__item">
              <div class="node-res__top"><span>流量</span><b>↓{{ rateText(Number(metricsOf(h)?.net_rx_rate ?? 0)) }} ↑{{ rateText(Number(metricsOf(h)?.net_tx_rate ?? 0)) }}</b></div>
              <div class="node-res__sub node-res__sub--wide">
                累计 ↓{{ formatBytes(Number(metricsOf(h)?.net_rx_bytes ?? 0)) }} · ↑{{ formatBytes(Number(metricsOf(h)?.net_tx_bytes ?? 0)) }}
              </div>
            </div>
          </div>
          <div v-else class="node-res__empty">
            <span class="hint">
              {{
                metricsOf(h)?.error
                  ? '连接失败：' + metricsOf(h)?.error
                  : '节点未连接：CPU / 内存 / 硬盘 / 流量 需要面板连入节点读取'
              }}
            </span>
            <NButton size="tiny" quaternary type="primary" @click="openDeploy(h)">去部署 / 连接</NButton>
          </div>

          <div class="node-card__foot">
            <NSpace :size="6" :wrap="true">
              <NButton size="tiny" type="primary" @click="openDetail(h)">详情</NButton>
              <NButton size="tiny" type="info" @click="openDeploy(h)">部署 / 连接</NButton>
              <NButton size="tiny" :loading="isBusy('node-metrics')" @click="loadNodeMetrics">刷新</NButton>
              <NButton size="tiny" quaternary type="error" @click="confirmDelete(h)">删除</NButton>
            </NSpace>
            <span class="node-card__time">{{ metricsOf(h) ? '读取于 ' + formatTime(metricsOf(h)?.collected_at ?? '') : '' }}</span>
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

    <!-- 部署到 节点服务器：面板下发脚本 → 面板连入节点（双向 TLS，无 SSH） -->
    <NModal
      v-model:show="showDeploy"
      preset="card"
      :title="`部署到 节点服务器 · ${deployHost?.name ?? ''}`"
      style="width: 820px"
      :bordered="false"
    >
      <NSpace vertical :size="12" style="width: 100%">
        <div class="hint">
          面板生成安装脚本 → 在服务器上以 root 执行一次 → 面板用双向 TLS 连入节点：下发内核配置、读取真实指标。
          不需要 SSH，也不需要节点能访问面板；面板只需要能连上节点的管理端口。
        </div>
        <NAlert v-if="deployPlan?.warning" :type="deployLegacy ? 'warning' : 'info'" :bordered="false">
          {{ deployPlan.warning }}
        </NAlert>

        <!-- 1 方案 -->
        <div class="deploy-block">
          <div class="deploy-block__head">
            <span class="deploy-block__title">1 · 部署方案</span>
            <NTag v-if="nodeStatus?.online" size="tiny" type="success">已连接</NTag>
            <NTag v-else-if="deployPlan" size="tiny" type="warning">待连接</NTag>
            <NTag v-else size="tiny">未部署</NTag>
          </div>

          <div>
            <div class="kv-label">配置文件（来自「协议配置」，可选）</div>
            <NSelect
              v-model:value="deployProfileId"
              :options="deployProfileOptions"
              clearable
              filterable
              placeholder="留空＝使用内置 REALITY 配置（推荐）"
            />
            <div v-if="deployProfileId" class="hint" style="margin-top: 6px">
              <template v-if="selectedProfile">
                已绑定 · 内核 {{ selectedProfile.kernel }} / {{ selectedProfile.format }} ·
                脚本会写入该文件的当前内容（保存后重新生成即可更新）
              </template>
              <template v-else>已绑定配置文件 {{ deployProfileId }}（列表加载中）</template>
            </div>
            <div v-if="deployProfileId" class="hint" style="margin-top: 4px; color: #b26a00">
              自定义档案会原样写入节点：档案里引用的证书 / 密钥文件必须已存在于服务器，
              否则脚本第 7 步会校验失败。不确定就清空本项，用内置 REALITY 先部署成功。
            </div>
            <div v-else-if="!deployProfiles.length" class="hint" style="margin-top: 6px">
              还没有可用的配置文件：先到「配置引擎 → 协议配置」新建并保存，再回来绑定。
            </div>
          </div>

          <NGrid cols="1 m:3" responsive="screen" :x-gap="10" :y-gap="8" style="margin-top: 8px">
            <NGi>
              <div class="kv-label">传输方式</div>
              <NSelect
                v-model:value="deployMode"
                :options="DEPLOY_MODE_OPTIONS"
                :disabled="deployHasProfile"
                size="small"
              />
              <div v-if="deployHasProfile" class="hint" style="margin-top: 4px">
                已绑定配置文件，传输方式由文件内容决定（内核 {{ deployProfileKernel || '未知' }}）
              </div>
            </NGi>
            <NGi>
              <div class="kv-label">数据端口（客户端连接）</div>
              <NInputNumber v-model:value="deployPort" size="small" :min="1" :max="65535" style="width: 100%" />
              <div class="hint" style="margin-top: 4px">默认取节点登记的端口；443 常被已有网站占用</div>
            </NGi>
            <NGi>
              <div class="kv-label">管理端口（面板连入）</div>
              <NInputNumber v-model:value="deployNodePort" size="small" :min="1" :max="65535" style="width: 100%" />
              <div class="hint" style="margin-top: 4px">
                默认 2244；2222 常被 Remnawave remnanode 占用，冲突时换空闲端口；只对面板 IP 开放
              </div>
            </NGi>
          </NGrid>

          <NAlert v-if="!deployKernelOk" type="warning" :bordered="false" style="margin-top: 8px">
            绑定的配置文件内核是 {{ deployProfileKernel }}，在线部署只支持 xray-core。
          </NAlert>

          <NSpace align="center" :size="10" :wrap="true" style="margin-top: 10px">
            <NButton size="small" type="primary" :loading="isBusy('plan')" @click="generatePlan">
              生成安装脚本
            </NButton>
            <NButton
              v-if="deployPlan && !deployLegacy"
              size="small"
              text
              tag="a"
              :href="api.nodeDeployScriptUrl(deployPlan.host_id)"
              target="_blank"
            >
              下载脚本
            </NButton>
            <NButton v-if="deployPlan" size="small" text type="error" :loading="isBusy('forget')" @click="forgetPlan">
              丢弃方案
            </NButton>
            <span class="hint">
              目标：<span class="mono">{{ deployHost?.address ?? '—' }}</span> ·
              管理端口 <span class="mono">{{ deployNodePort }}</span>
            </span>
          </NSpace>
        </div>

        <!-- 2 脚本 -->
        <div v-if="deployPlan && !deployLegacy" class="deploy-block">
          <div class="deploy-block__head">
            <span class="deploy-block__title">2 · 在服务器上执行</span>
            <NButton size="tiny" @click="copyText(deployPlan.script ?? '', '安装脚本已复制')">复制脚本</NButton>
          </div>
          <div class="hint">
            以 root 执行（保存为 ngg-node-{{ deployPlan.host_id }}.sh 后
            <span class="mono">bash ngg-node-{{ deployPlan.host_id }}.sh</span>）。
            脚本会安装 ngg-node（管理接口）与 ngg-core（内核）、写入本节点证书，并在最后自检双向认证。
          </div>
          <pre class="mono deploy-pre">{{ deployPlan.script }}</pre>
        </div>

        <!-- 3 连接 -->
        <div class="deploy-block">
          <div class="deploy-block__head">
            <span class="deploy-block__title">3 · 连接节点</span>
            <NTag :type="nodeOnline ? 'success' : 'default'" size="tiny">
              {{ nodeOnline ? '在线' : '未连接' }}
            </NTag>
          </div>
          <div class="hint">
            连接会把当前内核配置写入节点并启动 ngg-core，然后读取节点自己报的指标。
            节点证书的 CN 与节点 ID 不一致时会拒绝连接（不会把配置推到别的机器上）。
          </div>
          <NSpace align="center" :size="10" :wrap="true" style="margin-top: 8px">
            <NButton
              size="small"
              type="primary"
              :loading="isBusy('connect')"
              :disabled="!deployPlan || deployLegacy"
              @click="connectToNode"
            >
              连接节点并下发配置
            </NButton>
            <span class="hint mono">{{ nodeApiURL || '—' }}</span>
          </NSpace>
          <NAlert
            v-if="connectResult"
            :type="connectResult.ok ? 'success' : 'error'"
            :bordered="false"
            style="margin-top: 8px"
          >
            <template v-if="connectResult.ok">
              已连接（{{ connectResult.duration_ms }} ms）· 内核
              {{ connectResult.health?.xrayVersion || '—' }} · 配置{{ connectResult.config_pushed ? '已下发' : '未下发' }}
            </template>
            <template v-else>
              连接失败（{{ connectResult.stage || '未知' }} 阶段）：{{ connectResult.error }}
            </template>
          </NAlert>
        </div>

        <!-- 4 节点信息 -->
        <NCard v-if="nodeMetricsLive" size="small" title="节点信息（面板连入读取）">
          <NDescriptions :column="2" size="small" label-placement="left" :label-style="{ whiteSpace: 'nowrap' }" bordered>
            <NDescriptionsItem label="连接状态">
              <NTag size="tiny" :type="nodeOnline ? 'success' : 'warning'">
                {{ nodeOnline ? '在线' : '离线' }}
              </NTag>
            </NDescriptionsItem>
            <NDescriptionsItem label="往返延迟">{{ nodeMetricsLive.latency_ms }} ms</NDescriptionsItem>
            <NDescriptionsItem label="主机" :span="2">
              <span class="mono">{{ nodeMetricsLive.hostname || '—' }}</span>
              <span class="hint">
                · {{ nodeMetricsLive.os || '—' }} {{ nodeMetricsLive.kernel || '' }} · {{ nodeMetricsLive.arch || '—' }}
              </span>
            </NDescriptionsItem>
            <NDescriptionsItem label="CPU">
              {{ nodeMetricsLive.cpu_usage_pct.toFixed(1) }}%
              <span class="hint"> · {{ nodeMetricsLive.cpu_cores }} 核 · load {{ nodeMetricsLive.load1 }}</span>
            </NDescriptionsItem>
            <NDescriptionsItem label="内存">
              {{ formatBytes(nodeMetricsLive.mem_used) }} / {{ formatBytes(nodeMetricsLive.mem_total) }}
            </NDescriptionsItem>
            <NDescriptionsItem label="磁盘">
              {{ formatBytes(nodeMetricsLive.disk_used) }} / {{ formatBytes(nodeMetricsLive.disk_total) }}
            </NDescriptionsItem>
            <NDescriptionsItem label="运行时长">{{ formatUptime(nodeMetricsLive.uptime_sec) }}</NDescriptionsItem>
            <NDescriptionsItem label="累计流量">
              ↓ {{ formatBytes(nodeMetricsLive.net_rx_bytes) }} · ↑ {{ formatBytes(nodeMetricsLive.net_tx_bytes) }}
            </NDescriptionsItem>
            <NDescriptionsItem label="实时速率">
              ↓ {{ rateText(nodeMetricsLive.net_rx_rate) }} · ↑ {{ rateText(nodeMetricsLive.net_tx_rate) }}
            </NDescriptionsItem>
            <NDescriptionsItem label="节点服务">
              <span class="mono">{{ nodeMetricsLive.node_version || '—' }}</span>
            </NDescriptionsItem>
            <NDescriptionsItem label="内核">
              <span class="mono">{{ nodeMetricsLive.xray_version || '—' }}</span>
              <NTag size="tiny" :type="nodeMetricsLive.core_active ? 'success' : 'default'" style="margin-left: 6px">
                {{ nodeMetricsLive.core_active ? 'ngg-core active' : 'ngg-core 未运行' }}
              </NTag>
            </NDescriptionsItem>
            <NDescriptionsItem label="采集时间" :span="2">
              {{ formatTime(nodeMetricsLive.collected_at) }}
              <span class="hint"> · 面板每 {{ nodeMetricIntervalSec }} 秒连入读取一次</span>
            </NDescriptionsItem>
            <NDescriptionsItem v-if="nodeMetricsLive.error" label="错误" :span="2">
              {{ nodeMetricsLive.error }}
            </NDescriptionsItem>
          </NDescriptions>
        </NCard>
        <NAlert v-else type="info" :bordered="false">
          {{ nodeStatus?.warning || nodeStatus?.error || '还没有连上过该节点：执行脚本后点「连接节点并下发配置」' }}
        </NAlert>
      </NSpace>
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

/* ------------------------------------------------------------------ *
 * 在线部署（控制面 → 节点）
 * ------------------------------------------------------------------ */
.deploy-online {
  padding: 12px;
  border: 1px solid #e6e8ec;
  border-radius: 12px;
  background: #fafbfc;
}
.deploy-online__head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 6px;
}
.deploy-steps {
  margin-top: 12px;
  padding: 10px 12px;
  border: 1px solid #ececf0;
  border-radius: 10px;
  background: #fff;
}
.deploy-steps__head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.deploy-step-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.deploy-step-list li {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: #3a3a3c;
  min-width: 0;
}
.deploy-step__idx {
  flex: 0 0 auto;
  width: 18px;
  height: 18px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  background: #eef0f4;
  color: #6e6e73;
  font-size: 10.5px;
  font-weight: 600;
}
.deploy-step-list li.is-done .deploy-step__idx { background: #e3f6e9; color: #1f8a43; }
.deploy-step-list li.is-failed .deploy-step__idx { background: #fdeaea; color: #c4342b; }
.deploy-step-list li.is-running .deploy-step__idx { background: #e6f0fd; color: #0b63ce; }
.deploy-step__title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.deploy-step__detail {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: #c4342b;
  font-size: 11px;
}
.deploy-notes-wrap {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px dashed #e6e8ec;
}
.deploy-note {
  font-size: 11px;
  line-height: 1.6;
  color: #6e6e73;
}
.deploy-note::before {
  content: '· ';
  color: #b0b0b8;
}
.deploy-error {
  margin-top: 8px;
  padding: 6px 8px;
  border-radius: 8px;
  background: #fdeaea;
  color: #a3241c;
  font-size: 11.5px;
  line-height: 1.5;
}
.deploy-pre--small {
  max-height: 200px;
  font-size: 10.5px;
  line-height: 1.5;
}
.deploy-status {
  margin-top: 10px;
}
.deploy-sep {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  margin: 14px 0 4px;
  color: #a1a1a6;
  font-size: 11px;
}
.deploy-sep::before,
.deploy-sep::after {
  content: '';
  flex: 1;
  height: 1px;
  background: #ececf0;
}
.deploy-sep span {
  padding: 0 10px;
  white-space: nowrap;
}

</style>
