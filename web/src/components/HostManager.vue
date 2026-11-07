<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
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
  NRadioButton,
  NRadioGroup,
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
import { formatBytes, formatTime, prettyJson } from '../utils/format'

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
const HOP_KIND_OPTIONS: Array<{ label: string; value: HostChainHop['kind'] }> = [
  { label: 'SOCKS5', value: 'socks5' },
  { label: 'HTTP', value: 'http' },
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

function emptyHop(): ChainHopDraft {
  return { kind: 'socks5', address: '', port: 1080, username: '', password: '', enabled: true }
}

/** 合并页（role=all）新建节点时的默认角色：先按「双向」建，卡片上可一键改。 */
function defaultRole(): HostRole {
  return props.role === 'all' ? 'both' : props.role
}

function emptyHost(role: HostRole): HostForm {
  return {
    id: '', name: '', internal: '', country: 'US', address: '',
    node_port: 2222, svc_port: 443, role, enabled: true,
    protocol: 'vless-reality', core_id: '', profile: '',
    secret_key: '', proxy_url: '', plugin: '', provider: '',
    ip_addresses: [], tags: [], note: '', track_traffic: true,
    user_ratio: 1, node_ratio: 1, chain: [],
  }
}

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

function addHop(rows: ChainHopDraft[]): void {
  if (rows.length >= MAX_CHAIN_HOPS) {
    window.$message?.warning(`链式代理最多 ${MAX_CHAIN_HOPS} 跳`)
    return
  }
  rows.push(emptyHop())
}

function removeHop(rows: ChainHopDraft[], index: number): void {
  rows.splice(index, 1)
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
const showJson = ref(false)
const editingId = ref<string | null>(null)
const form = ref<HostForm>(emptyHost(defaultRole()))
const addForm = ref<HostForm>(emptyHost(defaultRole()))
const jsonText = ref('')
const newIP = ref('')
const tagInput = ref('')
const revealSecret = ref(false)

/* ------------------------------------------------------------------ *
 * v2.3.0 节点能力：配置文件 / 采集数据 / 密钥配对 / 连接测试
 * 全部数值来自真实后端接口，密钥只在页面内展示、不落盘不写日志。
 * ------------------------------------------------------------------ */
const selectedHostId = ref('')
const hostOptions = computed(() =>
  hosts.value.map((h) => ({ label: `${h.name || h.id} (${h.id})`, value: h.id })),
)
const inboundHostOptions = computed(() =>
  hosts.value
    .filter((h) => h.role === 'inbound' || h.role === 'both')
    .map((h) => ({ label: `${h.name || h.id} (${h.id})`, value: h.id })),
)
const outboundHostOptions = computed(() =>
  hosts.value
    .filter((h) => h.role === 'outbound' || h.role === 'both')
    .map((h) => ({ label: `${h.name || h.id} (${h.id})`, value: h.id })),
)
const KERNEL_OPTIONS = [
  { label: 'Mihomo', value: 'mihomo' },
  { label: 'Xray-core', value: 'xray-core' },
  { label: 'sing-box', value: 'sing-box' },
]
const KIND_OPTIONS = [
  { label: 'WireGuard', value: 'wireguard' },
  { label: 'UUID', value: 'uuid' },
  { label: 'Password', value: 'password' },
]
const FORMAT_OPTIONS = [
  { label: 'JSON', value: 'json' },
  { label: 'YAML', value: 'yaml' },
]

const configCoreId = ref('sing-box')
const configFileText = ref('')
const configFileFormat = ref('json')
const configFileResult = ref<api.NodeConfigFileResult | null>(null)
const nodeMetrics = ref<api.NodeMetrics | null>(null)
const keyKind = ref('wireguard')
const keyPair = ref<api.NodeKeyPair | null>(null)
const pairInboundId = ref('')
const pairOutboundId = ref('')
const pairResult = ref<api.NodePairResult | null>(null)
const nodeProbeResult = ref<api.NodeProbeResult | null>(null)

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
  selectedHostId.value = h.id
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

async function openJson() {
  const res = await run('export', () => api.exportHosts())
  if (res) {
    jsonText.value = JSON.stringify({ hosts: res.hosts }, null, 2)
    showJson.value = true
  }
}

async function importJson() {
  try {
    const parsed = JSON.parse(jsonText.value) as { hosts?: RemoteHost[] }
    if (!Array.isArray(parsed.hosts)) throw new Error('JSON 需包含 hosts 数组')
    const res = await run('import', () => api.importHosts(parsed.hosts!), '节点库已全量导入')
    if (res) {
      showJson.value = false
      await refresh()
    }
  } catch (e) {
    window.$message?.error(`JSON 解析失败：${e instanceof Error ? e.message : String(e)}`)
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

const PROVISION_OPTIONS: Array<{ label: string; value: ProvisionProtocol }> = [
  { label: 'VLESS', value: 'vless' },
  { label: 'Trojan', value: 'trojan' },
  { label: 'Shadowsocks', value: 'shadowsocks' },
]
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
  deployProfileId.value = saved?.profile_id ?? null
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

watch(deployProtocol, (p) => {
  // 只在端口还是某个协议默认值时才联动，避免覆盖手填端口
  const next = PROVISION_DEFAULT_PORT[p]
  if (!next) return
  const current = Number(deployPort.value ?? 0)
  if (!current || Object.values(PROVISION_DEFAULT_PORT).includes(current)) {
    deployPort.value = next
  }
})

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

/* ------------------------------------------------------------------ *
 * 节点能力操作
 * ------------------------------------------------------------------ */
function requireHostId(): string | null {
  if (!selectedHostId.value) {
    window.$message?.warning('请先选择节点')
    return null
  }
  return selectedHostId.value
}

/** getConfig 读的是内核当前配置，不是该节点已保存的配置文件，按钮文字已如实标明。 */
async function loadConfigFile() {
  const d = await run('cfgfile-load', () => api.getConfig(configCoreId.value))
  if (d) {
    configFileText.value = d.content ?? ''
    const ext = (d.path || '').split('.').pop()?.toLowerCase()
    if (ext === 'yaml' || ext === 'yml') configFileFormat.value = 'yaml'
    else if (ext === 'json') configFileFormat.value = 'json'
  }
}

async function saveConfigFile() {
  const id = requireHostId()
  if (!id) return
  const r = await run(
    'cfgfile-save',
    () => api.saveNodeConfigFile(id, { content: configFileText.value, format: configFileFormat.value }),
    '节点配置文件已保存',
  )
  if (r) configFileResult.value = r
}

async function refreshNodeMetrics() {
  const id = requireHostId()
  if (!id) return
  const r = await run('node-metrics', () => api.getNodeMetrics(id))
  if (r) nodeMetrics.value = r
}

async function generateKeys() {
  const r = await run('keys-gen', () =>
    api.generateNodeKeys({ kind: keyKind.value as 'wireguard' | 'uuid' | 'password' }),
  )
  if (r) keyPair.value = r
}

async function copyKeys() {
  if (!keyPair.value) return
  try {
    await navigator.clipboard.writeText(prettyJson(keyPair.value))
    window.$message?.success('密钥已复制')
  } catch {
    window.$message?.warning('浏览器未授予剪贴板权限')
  }
}

async function pairKeys() {
  if (!pairInboundId.value || !pairOutboundId.value) {
    window.$message?.warning('请选择入口与出口节点')
    return
  }
  const r = await run(
    'keys-pair',
    () => api.pairNodeKeys({
      inbound_id: pairInboundId.value,
      outbound_id: pairOutboundId.value,
      kind: keyKind.value,
    }),
    '密钥已写入节点',
  )
  if (r) pairResult.value = r
}

async function probeNodeTcp() {
  const id = requireHostId()
  if (!id) return
  const r = await run('node-probe', () => api.probeNode(id, { timeout_ms: 3000 }))
  if (r) {
    nodeProbeResult.value = r
    await refresh()
  }
}

onMounted(() => {
  void refresh()
  void loadOptions()
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
        <NButton size="small" :loading="isBusy('probe-all')" @click="probeAll">重新探测</NButton>
        <NButton size="small" @click="openJson">JSON 导入 / 导出</NButton>
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

    <NGrid v-else cols="1 m:2 l:3" responsive="screen" :x-gap="12" :y-gap="12" class="section-gap">
      <NGi v-for="h in filtered" :key="h.id">
        <NCard size="small" class="host-card" :class="cardStatusClass(h)">
          <template #header>
            <div class="host-card__head">
              <span class="host-card__flag">{{ flag(h.country) }}</span>
              <span class="host-card__name">{{ h.name }}</span>
              <NTag size="small" :bordered="false" :type="roleType(h.role)">{{ roleLabel(h.role) }}</NTag>
            </div>
          </template>
          <template #header-extra>
            <NTag size="small" :bordered="false" :type="probeType(h)">{{ h.probe ? (h.probe.online ? '在线' : '离线') : '未检测' }}</NTag>
          </template>

          <div class="host-card__addr mono">{{ h.address }}:{{ h.node_port }}</div>
          <div class="host-card__meta">
            <span>{{ h.internal || h.id }}</span>
            <span>·</span>
            <span>{{ h.protocol || '未设协议' }}</span>
            <span v-if="h.provider">· {{ h.provider }}</span>
          </div>
          <div class="host-card__probe" :class="h.probe?.online ? 'is-online' : 'is-offline'">
            {{ probeText(h) }}
          </div>
          <div class="host-card__chain">
            <span class="host-card__chain-k">链路</span>
            <NTag size="small" :bordered="false" :type="chainHops(h.chain).length ? 'info' : 'default'">
              {{ chainLabel(h.chain) }}
            </NTag>
            <span v-if="chainHops(h.chain).length" class="host-card__chain-n">{{ chainHops(h.chain).length }} 跳</span>
          </div>
          <div class="host-card__chain">
            <span class="host-card__chain-k">部署</span>
            <NTag size="small" :bordered="false" :type="provisions[h.id] ? 'success' : 'default'">
              {{ provisionLabel(h) }}
            </NTag>
          </div>
          <div v-if="h.tags?.length" class="host-card__tags">
            <NTag v-for="t in h.tags.slice(0, 4)" :key="t" size="small" :bordered="false">{{ t }}</NTag>
          </div>
          <div class="host-card__foot">
            <NSpace :size="6" :wrap="true">
              <NButton size="tiny" type="primary" @click="openDetail(h)">详情</NButton>
              <NButton size="tiny" type="info" @click="openDeploy(h)">部署到 VPS</NButton>
              <NButton size="tiny" :loading="isBusy('probe:' + h.id)" @click="probeOne(h)">探测</NButton>
              <NButton size="tiny" :loading="isBusy('role:' + h.id)" @click="switchRole(h, 'inbound')">设为入站</NButton>
              <NButton size="tiny" :loading="isBusy('role:' + h.id)" @click="switchRole(h, 'outbound')">设为出站</NButton>
              <NButton size="tiny" quaternary @click="copySummary(h)">复制</NButton>
              <NButton size="tiny" quaternary type="error" @click="confirmDelete(h)">删除</NButton>
            </NSpace>
          </div>
        </NCard>
      </NGi>
    </NGrid>

    <!-- ---------------------------------------------------------------- *
     * 节点详情抽屉：两栏布局，对齐节点管理面板的信息密度
     * ---------------------------------------------------------------- -->
    <NDrawer v-model:show="showDrawer" :width="860" placement="right">
      <NDrawerContent closable :native-scrollbar="false">
        <template #header>
          <NSpace :size="8" align="center">
            <span>{{ flag(form.country) }} {{ form.name || '节点详情' }}</span>
            <NTag size="small" :bordered="false" :type="roleType(form.role)">{{ roleLabel(form.role) }}</NTag>
          </NSpace>
        </template>

        <NGrid cols="1 m:2" responsive="screen" :x-gap="14" :y-gap="14">
          <!-- 左栏 -->
          <NGi>
            <NCard size="small" title="节点状态" class="host-panel">
              <NSpace vertical :size="10" style="width: 100%">
                <div class="kv-label">角色（可随时改成入站 / 出站 / 双向）</div>
                <NSelect v-model:value="form.role" :options="ROLE_OPTIONS" />
                <div class="kv-label">启用</div>
                <NSwitch v-model:value="form.enabled" />
                <div class="host-status-line">
                  <span class="hint">最近真实探测：</span>
                  <span>{{ probeText(hosts.find((x) => x.id === editingId)) }}</span>
                </div>
                <div class="hint">节点 ID：{{ editingId }}</div>
                <div class="hint">创建：{{ formatTime(form.created_at) }}</div>
              </NSpace>
            </NCard>

            <NCard size="small" title="基本信息" class="host-panel">
              <NSpace vertical :size="9" style="width: 100%">
                <NGrid cols="2" :x-gap="10" :y-gap="6">
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
                    <div class="kv-label">Node Port（管理端口）</div>
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
                    <div class="kv-label">Secret Key</div>
                    <NInput
                      v-model:value="form.secret_key"
                      :type="revealSecret ? 'text' : 'password'"
                      placeholder="可选，留空表示未设置"
                    >
                      <template #suffix>
                        <NButton text size="tiny" @click="revealSecret = !revealSecret">
                          {{ revealSecret ? '隐藏' : '显示' }}
                        </NButton>
                      </template>
                    </NInput>
                  </NGi>
                  <NGi :span="2">
                    <div class="kv-label">代理 URL（可选，面板到节点的内部 API 走此代理）</div>
                    <NInput v-model:value="form.proxy_url" placeholder="socks5://user:pass@host:port" />
                  </NGi>
                  <NGi>
                    <div class="kv-label">协议</div>
                    <NSelect
                      v-model:value="form.protocol"
                      :options="protocolOptions"
                      filterable
                      tag
                      placeholder="选择或输入协议"
                    />
                  </NGi>
                  <NGi>
                    <div class="kv-label">插件</div>
                    <NInput v-model:value="form.plugin" placeholder="可选" />
                  </NGi>
                </NGrid>
              </NSpace>
            </NCard>

            <NCard size="small" title="流量与计费" class="host-panel">
              <NSpace vertical :size="10" style="width: 100%">
                <NGrid cols="2" :x-gap="10">
                  <NGi>
                    <div class="kv-label">用户流量倍率</div>
                    <NInputNumber v-model:value="form.user_ratio" :min="0.1" :max="100" :step="0.1" style="width: 100%" />
                  </NGi>
                  <NGi>
                    <div class="kv-label">节点流量倍率</div>
                    <NInputNumber v-model:value="form.node_ratio" :min="0.1" :max="100" :step="0.1" style="width: 100%" />
                  </NGi>
                </NGrid>
                <div class="kv-label">网络提供商</div>
                <NInput v-model:value="form.provider" placeholder="可选，例如 Hetzner / 阿里云" />
                <div class="kv-label">跟踪流量使用</div>
                <NSwitch v-model:value="form.track_traffic" />
                <div class="kv-label">标签（逗号分隔）</div>
                <NInput
                  v-model:value="tagInput"
                  placeholder="输入后回车添加"
                  @keyup.enter="addTagFromInput"
                />
                <div v-if="form.tags.length" class="host-tags">
                  <NTag
                    v-for="t in form.tags"
                    :key="t"
                    size="small"
                    closable
                    :bordered="false"
                    @close="form.tags = form.tags.filter((x) => x !== t)"
                  >{{ t }}</NTag>
                </div>
                <div class="kv-label">备注</div>
                <NInput v-model:value="form.note" type="textarea" :autosize="{ minRows: 2, maxRows: 5 }" />
              </NSpace>
            </NCard>

            <NCard size="small" title="链式代理" class="host-panel">
              <div class="chain" :data-count="form.chain.length">
                <div class="chain__head">
                  <span class="chain__summary">{{ chainLabel(chainPayload(form.chain)) }}</span>
                  <span class="chain__count mono">{{ form.chain.length }} / {{ MAX_CHAIN_HOPS }}</span>
                  <NButton
                    size="tiny"
                    quaternary
                    type="primary"
                    :disabled="form.chain.length >= MAX_CHAIN_HOPS"
                    @click="addHop(form.chain)"
                  >＋ 添加一跳</NButton>
                </div>
                <div v-if="!form.chain.length" class="chain__empty mono">直连</div>
                <div v-for="(hop, i) in form.chain" :key="i" class="chain__row">
                  <span class="chain__idx mono">{{ i + 1 }}</span>
                  <NSelect v-model:value="hop.kind" size="small" :options="HOP_KIND_OPTIONS" />
                  <NInput v-model:value="hop.address" size="small" placeholder="host" />
                  <NSwitch v-model:value="hop.enabled" size="small" />
                  <NButton size="tiny" quaternary type="error" @click="removeHop(form.chain, i)">删除</NButton>
                  <div class="chain__auth">
                    <NInputNumber v-model:value="hop.port" size="small" :min="1" :max="65535" placeholder="port" style="width: 100%" />
                    <NInput v-model:value="hop.username" size="small" placeholder="user" />
                    <NInput v-model:value="hop.password" size="small" type="password" show-password-on="click" placeholder="pass" />
                  </div>
                </div>
              </div>
            </NCard>
          </NGi>

          <!-- 右栏 -->
          <NGi>
            <NCard size="small" title="核心配置" class="host-panel">
              <div class="kv-label">绑定配置档案（来自「协议配置」）</div>
              <NSelect v-model:value="form.profile" :options="profileOptions" clearable placeholder="不绑定" />
              <div class="hint" style="margin-top: 8px">
                档案决定该主机上运行的内核原生配置；NGG 不重写内核协议栈。
              </div>
              <div class="host-kv"><span>Node Port</span><span class="mono">{{ form.node_port }}</span></div>
              <div class="host-kv"><span>服务端口</span><span class="mono">{{ form.svc_port || '未设置' }}</span></div>
            </NCard>

            <NCard size="small" title="IP 地址" class="host-panel">
              <NSpace :size="6" style="width: 100%">
                <NInput v-model:value="newIP" placeholder="添加该主机的 IP" @keyup.enter="addIP" />
                <NButton size="small" @click="addIP">添加</NButton>
              </NSpace>
              <div v-if="!form.ip_addresses.length" class="hint" style="margin-top: 10px">还没有填写 IP 地址</div>
              <div v-else class="host-tags" style="margin-top: 10px">
                <NTag
                  v-for="ip in form.ip_addresses"
                  :key="ip"
                  size="small"
                  closable
                  :bordered="false"
                  @close="form.ip_addresses = form.ip_addresses.filter((x) => x !== ip)"
                >{{ ip }}</NTag>
              </div>
            </NCard>

            <NCard size="small" title="最近一次真实探测" class="host-panel">
              <template v-if="hosts.find((x) => x.id === editingId)?.probe">
                <div class="host-kv">
                  <span>可达</span>
                  <NTag
                    size="small"
                    :bordered="false"
                    :type="hosts.find((x) => x.id === editingId)?.probe?.online ? 'success' : 'error'"
                  >
                    {{ hosts.find((x) => x.id === editingId)?.probe?.online ? '在线' : '离线' }}
                  </NTag>
                </div>
                <div class="host-kv">
                  <span>延迟</span>
                  <span class="mono">{{ hosts.find((x) => x.id === editingId)?.probe?.latency_ms ?? 0 }} ms</span>
                </div>
                <div class="host-kv">
                  <span>目标</span>
                  <span class="mono">{{ hosts.find((x) => x.id === editingId)?.probe?.target || '—' }}</span>
                </div>
                <div v-if="hosts.find((x) => x.id === editingId)?.probe?.error" class="hint" style="margin-top: 6px">
                  {{ hosts.find((x) => x.id === editingId)?.probe?.error }}
                </div>
              </template>
              <div v-else class="hint">尚未探测。点列表上的「探测」做一次真实 TCP 连接。</div>
            </NCard>
          </NGi>
        </NGrid>

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
            <div class="kv-label">Node Port</div>
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
          <NGi>
            <div class="kv-label">内部名称</div>
            <NInput v-model:value="addForm.internal" placeholder="us01" />
          </NGi>
          <NGi>
            <div class="kv-label">服务端口</div>
            <NInputNumber v-model:value="addForm.svc_port" :min="0" :max="65535" style="width: 100%" />
          </NGi>
        </NGrid>

        <div class="chain">
          <div class="chain__head">
            <span class="chain__summary">{{ chainLabel(chainPayload(addForm.chain)) }}</span>
            <span class="chain__count mono">{{ addForm.chain.length }} / {{ MAX_CHAIN_HOPS }}</span>
            <NButton
              size="tiny"
              quaternary
              type="primary"
              :disabled="addForm.chain.length >= MAX_CHAIN_HOPS"
              @click="addHop(addForm.chain)"
            >＋ 添加一跳</NButton>
          </div>
          <div v-if="!addForm.chain.length" class="chain__empty mono">直连</div>
          <div v-for="(hop, i) in addForm.chain" :key="i" class="chain__row">
            <span class="chain__idx mono">{{ i + 1 }}</span>
            <NSelect v-model:value="hop.kind" size="small" :options="HOP_KIND_OPTIONS" />
            <NInput v-model:value="hop.address" size="small" placeholder="host" />
            <NSwitch v-model:value="hop.enabled" size="small" />
            <NButton size="tiny" quaternary type="error" @click="removeHop(addForm.chain, i)">删除</NButton>
            <div class="chain__auth">
              <NInputNumber v-model:value="hop.port" size="small" :min="1" :max="65535" placeholder="port" style="width: 100%" />
              <NInput v-model:value="hop.username" size="small" placeholder="user" />
              <NInput v-model:value="hop.password" size="small" type="password" show-password-on="click" placeholder="pass" />
            </div>
          </div>
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

    <!-- 部署到 VPS：生成可粘贴执行的脚本，并展示真实连接参数 -->
    <NModal
      v-model:show="showDeploy"
      preset="card"
      :title="`部署到 VPS · ${deployHost?.name ?? ''}`"
      style="width: 760px"
      :bordered="false"
    >
      <NSpace vertical :size="12" style="width: 100%">
        <div class="hint">
          选择「协议配置」里已保存的配置文件并绑定到这台节点，脚本会安装对应内核并把该文件原样写进去；
          复制到 VPS 上以 root 执行即可。不选配置文件时退回最小配置模式（按协议现生成）。
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

        <NGrid cols="1 m:3" responsive="screen" :x-gap="10" :y-gap="8">
          <NGi v-if="!deployProfileId">
            <div class="kv-label">协议</div>
            <NRadioGroup v-model:value="deployProtocol" size="small">
              <NRadioButton v-for="o in PROVISION_OPTIONS" :key="o.value" :value="o.value">
                {{ o.label }}
              </NRadioButton>
            </NRadioGroup>
          </NGi>
          <NGi>
            <div class="kv-label">{{ deployProfileId ? '端口（可空 · 从文件推断）' : '端口' }}</div>
            <NInputNumber v-model:value="deployPort" :min="1" :max="65535" style="width: 100%" clearable />
          </NGi>
          <NGi>
            <div class="kv-label">SNI（可空）</div>
            <NInput v-model:value="deploySni" placeholder="留空则用文件里的 SNI / 主机域名" />
          </NGi>
        </NGrid>

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
          该节点尚未生成部署脚本。选择配置文件（或协议）后点「生成部署脚本」。
        </NAlert>
      </NSpace>
    </NModal>

    <!-- JSON 全量导入导出 -->
    <NModal v-model:show="showJson" preset="card" title="节点库 JSON（全量）" style="width: 720px" :bordered="false">
      <NSpace vertical :size="10" style="width: 100%">
        <div class="hint">导出的是真实登记数据；导入为全量替换，任一条校验失败则整个节点库保持不变。</div>
        <NInput
          v-model:value="jsonText"
          type="textarea"
          :autosize="{ minRows: 12, maxRows: 20 }"
          class="mono"
        />
      </NSpace>
      <template #footer>
        <NSpace justify="end">
          <NButton size="small" @click="showJson = false">关闭</NButton>
          <NButton size="small" type="primary" :loading="isBusy('import')" @click="importJson">
            全量导入
          </NButton>
        </NSpace>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
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

.chain {
  border: 1px solid #f0f0f2;
  border-radius: 10px;
  background: #fafafb;
  padding: 8px 10px;
}
.chain__head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 6px;
}
.chain__summary {
  font-size: 11.5px;
  color: #3a3a3c;
  font-weight: 500;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.chain__count {
  font-size: 10.5px;
  color: #8e8e93;
  margin-left: auto;
}
.chain__empty {
  font-size: 11.5px;
  color: #a1a1a6;
  padding: 2px 0 4px;
}
.chain__row {
  display: grid;
  grid-template-columns: 16px 84px minmax(0, 1fr) 30px auto;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  background: #fff;
  border: 1px solid #ececf0;
  border-radius: 8px;
  margin-bottom: 6px;
  min-width: 0;
}
.chain__row:last-child {
  margin-bottom: 0;
}
.chain__idx {
  font-size: 10px;
  color: #8e8e93;
  text-align: center;
}
.chain__row :deep(.n-select) {
  width: 100%;
  min-width: 0;
}
.chain__auth {
  grid-column: 2 / -1;
  display: grid;
  grid-template-columns: 84px minmax(0, 1fr) minmax(0, 1fr);
  gap: 6px;
  min-width: 0;
}

/* 部署到 VPS */
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
