<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCollapse,
  NCollapseItem,
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
  NTabPane,
  NTabs,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type { AgentEnrollResult, AgentSample, AgentServerConfig, AgentView } from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { formatBytes, formatTime, formatUptime } from '../utils/format'

const { isBusy, run } = useApiAction()

type AgentOs = 'windows' | 'linux' | 'docker'
type AgentStatusKey = 'online' | 'offline' | 'unbound'

const OS_TABS: Array<{ value: AgentOs; label: string }> = [
  { value: 'windows', label: 'Windows' },
  { value: 'linux', label: 'Linux' },
  { value: 'docker', label: 'Docker' },
]
const ROLE_OPTIONS: Array<{ label: string; value: AgentView['role'] }> = [
  { label: '出口 Outbound', value: 'outbound' },
  { label: '入口 Inbound', value: 'inbound' },
  { label: '双向 Both', value: 'both' },
]

/* ------------------------------ 状态 ------------------------------ */

const agents = ref<AgentView[]>([])
const server = ref<AgentServerConfig | null>(null)
const serverDraft = ref<ServerDraft>(emptyServer())
const serverLoaded = ref(false)

interface ServerDraft {
  enabled: boolean
  listen: string
  port: number | null
  public_url: string
}
function emptyServer(): ServerDraft {
  return { enabled: false, listen: '0.0.0.0', port: 9191, public_url: '' }
}

const stats = computed(() => {
  const total = agents.value.length
  const online = agents.value.filter((a) => statusKey(a) === 'online').length
  const unbound = agents.value.filter((a) => statusKey(a) === 'unbound').length
  return [
    { label: '节点总数', value: String(total), tone: '' },
    { label: '在线', value: String(online), tone: 'is-green' },
    { label: '离线', value: String(total - online - unbound), tone: '' },
    { label: '未绑定', value: String(unbound), tone: 'is-amber' },
  ]
})

function statusKey(a: AgentView): AgentStatusKey {
  if (a.online || a.status === 'online') return 'online'
  if (!a.bound || a.status === 'never') return 'unbound'
  return 'offline'
}
function statusLabel(a: AgentView): string {
  const k = statusKey(a)
  return k === 'online' ? '在线' : k === 'unbound' ? '未绑定' : '离线'
}
function statusTagType(a: AgentView): 'success' | 'warning' | 'default' {
  const k = statusKey(a)
  return k === 'online' ? 'success' : k === 'unbound' ? 'warning' : 'default'
}
function osLabel(os: string): string {
  const v = (os || '').toLowerCase()
  if (!v) return '未知'
  if (v.includes('docker')) return 'Docker'
  if (v.includes('win')) return 'Windows'
  if (v.includes('linux')) return 'Linux'
  if (v.includes('darwin') || v.includes('mac')) return 'macOS'
  return os
}
function roleLabel(role: AgentView['role']): string {
  return role === 'inbound' ? '入口' : role === 'outbound' ? '出口' : '双向'
}

/* --------------------------- 数值格式化 --------------------------- */

function pct(used?: number, total?: number): number {
  if (!used || !total || total <= 0) return 0
  return Math.max(0, Math.min(100, (used / total) * 100))
}
function pctText(used?: number, total?: number): string {
  return `${pct(used, total).toFixed(0)}%`
}
function barWidth(value: number): string {
  return `${Math.max(0, Math.min(100, value)).toFixed(1)}%`
}
function formatRate(value?: number): string {
  if (value === undefined || value === null || Number.isNaN(value)) return '—'
  return `${formatBytes(value)}/s`
}
/** 相对时间：只依赖系统时间差，不引第三方库。 */
function relTime(value?: string): string {
  if (!value) return '—'
  const t = new Date(value).getTime()
  if (Number.isNaN(t)) return value
  const diff = Math.floor((Date.now() - t) / 1000)
  if (diff <= 5) return '刚刚'
  if (diff < 60) return `${diff} 秒前`
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`
  return `${Math.floor(diff / 86400)} 天前`
}
/** 采样序列 → SVG 折线路径（viewBox 固定 240×48，纵向拉伸由 CSS 控制）。 */
function sparkPath(values: number[], max?: number): string {
  if (!values.length) return ''
  const width = 240
  const height = 48
  const top = max && max > 0 ? max : Math.max(1, ...values)
  const step = values.length > 1 ? width / (values.length - 1) : width
  return values
    .map((v, i) => {
      const x = i * step
      const y = height - (Math.max(0, Math.min(v, top)) / top) * height
      return `${i === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)}`
    })
    .join(' ')
}

/* ------------------------------ 加载 ------------------------------ */

async function loadAgents(silent = false): Promise<void> {
  const res = await run(silent ? 'poll' : 'list', () => api.listAgents())
  if (!res) return
  agents.value = res.agents ?? []
  server.value = res.server ?? null
  if (!serverLoaded.value && res.server) {
    serverDraft.value = toServerDraft(res.server)
    serverLoaded.value = true
  }
}

function toServerDraft(cfg: AgentServerConfig): ServerDraft {
  return {
    enabled: cfg.enabled === true,
    listen: cfg.listen ?? '',
    port: typeof cfg.port === 'number' && cfg.port > 0 ? cfg.port : 9191,
    public_url: cfg.public_url ?? '',
  }
}

async function loadServer(): Promise<void> {
  const res = await run('server-load', () => api.getAgentServer())
  if (!res) return
  server.value = res
  serverDraft.value = toServerDraft(res)
  serverLoaded.value = true
}

async function saveServer(): Promise<void> {
  const draft = serverDraft.value
  const port = Number(draft.port ?? 0)
  if (!draft.listen.trim()) {
    window.$message?.warning('监听地址不能为空')
    return
  }
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    window.$message?.warning('端口需在 1–65535')
    return
  }
  const payload: AgentServerConfig = {
    enabled: draft.enabled,
    listen: draft.listen.trim(),
    port,
    public_url: draft.public_url.trim(),
  }
  const res = await run('server-save', () => api.saveAgentServer(payload), '接收端口设置已保存')
  if (!res) return
  server.value = res.server
  serverDraft.value = toServerDraft(res.server)
  if (res.requires_restart) {
    window.$message?.info('需重启 NGG 服务后生效', { duration: 6000 })
  }
}

/* ------------------------------ 新增 ------------------------------ */

interface AgentDraft {
  name: string
  address: string
  port: number | null
  role: AgentView['role']
  note: string
}
function emptyAgent(): AgentDraft {
  return { name: '', address: '', port: 9191, role: 'outbound', note: '' }
}

const showAdd = ref(false)
const addDraft = ref<AgentDraft>(emptyAgent())

function openAdd(): void {
  addDraft.value = emptyAgent()
  showAdd.value = true
}

function validAddress(address: string): boolean {
  return !!address && !address.includes('://') && !address.includes('/')
}

async function submitAdd(): Promise<void> {
  const d = addDraft.value
  if (!d.name.trim() || !d.address.trim()) {
    window.$message?.warning('名称与地址不能为空')
    return
  }
  if (!validAddress(d.address.trim())) {
    window.$message?.warning('地址只填 IP 或域名')
    return
  }
  const port = Number(d.port ?? 0)
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    window.$message?.warning('端口需在 1–65535')
    return
  }
  const res = await run(
    'create',
    () =>
      api.createAgent({
        name: d.name.trim(),
        address: d.address.trim(),
        port,
        role: d.role,
        note: d.note.trim(),
      }),
    '节点已添加',
  )
  if (!res) return
  showAdd.value = false
  openEnroll(res, '绑定远程节点')
  await loadAgents(true)
}

/* ------------------------------ 编辑 ------------------------------ */

const showEdit = ref(false)
const editDraft = ref<AgentDraft & { id: string }>({ ...emptyAgent(), id: '' })

function openEdit(a: AgentView): void {
  editDraft.value = {
    id: a.id,
    name: a.name,
    address: a.address,
    port: a.port,
    role: a.role,
    note: a.note ?? '',
  }
  showEdit.value = true
}

async function submitEdit(): Promise<void> {
  const d = editDraft.value
  if (!d.name.trim() || !d.address.trim()) {
    window.$message?.warning('名称与地址不能为空')
    return
  }
  if (!validAddress(d.address.trim())) {
    window.$message?.warning('地址只填 IP 或域名')
    return
  }
  const port = Number(d.port ?? 0)
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    window.$message?.warning('端口需在 1–65535')
    return
  }
  const res = await run(
    'edit',
    () =>
      api.updateAgent(d.id, {
        name: d.name.trim(),
        address: d.address.trim(),
        port,
        role: d.role,
        note: d.note.trim(),
      }),
    '节点已保存',
  )
  if (!res) return
  showEdit.value = false
  if (detail.value?.id === res.id) detail.value = res
  await loadAgents(true)
}

/* --------------------------- 删除 / 轮换 --------------------------- */

function confirmDelete(a: AgentView): void {
  window.$dialog?.warning({
    title: '删除远程节点',
    content: `移除 ${a.name}（${a.address}:${a.port}），Agent 上报将立即失效。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await run('del:' + a.id, () => api.deleteAgent(a.id), '节点已删除')
      if (!res) return
      if (detail.value?.id === a.id) showDetail.value = false
      await loadAgents(true)
    },
  })
}

function rotateToken(a: AgentView): void {
  window.$dialog?.warning({
    title: '轮换密钥',
    content: `${a.name} 的旧密钥立即失效，需要用新脚本重新绑定。`,
    positiveText: '轮换',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await run('rotate:' + a.id, () => api.rotateAgentToken(a.id))
      if (!res) return
      openEnroll(res, '轮换密钥')
      await loadAgents(true)
    },
  })
}

/* ---------------------------- 绑定向导 ---------------------------- */

const showEnroll = ref(false)
const enrollTitle = ref('绑定远程节点')
const enroll = ref<AgentEnrollResult | null>(null)
const enrollOs = ref<AgentOs>('windows')

function openEnroll(result: AgentEnrollResult, title: string): void {
  enroll.value = result
  enrollTitle.value = title
  enrollOs.value = 'windows'
  showEnroll.value = true
  void ensureScript()
}

const scriptText = computed(() => {
  const e = enroll.value
  if (!e) return ''
  return e.install[enrollOs.value] ?? ''
})

/** 后端未随结果返回该平台脚本时，再按需拉取一次。 */
async function ensureScript(): Promise<void> {
  const e = enroll.value
  if (!e || e.install[enrollOs.value]) return
  const os = enrollOs.value
  const res = await run('install-' + os, () => api.getAgentInstallScript(e.agent.id, os))
  if (res && enroll.value) enroll.value.install[os] = res.script
}

watch(enrollOs, () => {
  void ensureScript()
})

function onEnrollClosed(): void {
  enroll.value = null
  void loadAgents(true)
}

async function copyText(text: string, okMsg: string): Promise<void> {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    window.$message?.success(okMsg)
  } catch {
    window.$message?.warning('浏览器未授予剪贴板权限')
  }
}

/* ------------------------------ 详情 ------------------------------ */

const showDetail = ref(false)
const detail = ref<AgentView | null>(null)
const samples = ref<AgentSample[]>([])
const samplesLoading = ref(false)

async function openDetail(a: AgentView): Promise<void> {
  detail.value = a
  samples.value = []
  showDetail.value = true
  samplesLoading.value = true
  const res = await run('samples:' + a.id, () => api.getAgentSamples(a.id, 60))
  samplesLoading.value = false
  if (res) samples.value = res.samples ?? []
}

const memTotal = computed(() => detail.value?.metrics?.mem_total ?? 0)
const cpuSeries = computed(() => samples.value.map((s) => s.cpu_pct))
const memSeries = computed(() =>
  samples.value.map((s) => (memTotal.value > 0 ? (s.mem_used / memTotal.value) * 100 : 0)),
)
const rxSeries = computed(() => samples.value.map((s) => s.rx_rate))
const txSeries = computed(() => samples.value.map((s) => s.tx_rate))
const rateMax = computed(() => Math.max(1, ...rxSeries.value, ...txSeries.value))

function last<T>(list: T[]): T | undefined {
  return list.length ? list[list.length - 1] : undefined
}

const latestCpu = computed(() => {
  const v = last(cpuSeries.value)
  return v === undefined ? '—' : `${v.toFixed(0)}%`
})
const latestMem = computed(() => {
  const v = last(memSeries.value)
  return v === undefined ? '—' : `${v.toFixed(0)}%`
})
const latestRx = computed(() => formatRate(last(rxSeries.value)))
const latestTx = computed(() => formatRate(last(txSeries.value)))

/* ------------------------------ 轮询 ------------------------------ */

const POLL_MS = 10000
let timer: number | null = null

function stopPolling(): void {
  if (timer !== null) {
    window.clearInterval(timer)
    timer = null
  }
}

function startPolling(): void {
  stopPolling()
  timer = window.setInterval(() => {
    if (document.hidden) return
    void loadAgents(true)
  }, POLL_MS)
}

function onVisibility(): void {
  if (!document.hidden) void loadAgents(true)
}

onMounted(() => {
  void loadAgents()
  void loadServer()
  startPolling()
  document.addEventListener('visibilitychange', onVisibility)
})

onBeforeUnmount(() => {
  stopPolling()
  document.removeEventListener('visibilitychange', onVisibility)
})
</script>

<template>
  <div class="page ag">
    <div class="ag-toolbar">
      <span class="ag-meta mono">AGENT {{ agents.length }}</span>
      <div class="ag-toolbar__ops">
        <NButton size="small" :loading="isBusy('list')" @click="loadAgents()">刷新</NButton>
        <NButton size="small" type="primary" @click="openAdd">＋ 添加远程节点</NButton>
      </div>
    </div>

    <section class="ag-stats">
      <div v-for="s in stats" :key="s.label" class="ag-stat">
        <div class="ag-stat__label">{{ s.label }}</div>
        <div class="ag-stat__value" :class="s.tone">{{ s.value }}</div>
      </div>
    </section>

    <div v-if="!agents.length" class="ag-card ag-empty">
      <NEmpty>
        <template #extra>
          <NButton size="large" type="primary" @click="openAdd">＋ 添加远程节点</NButton>
        </template>
      </NEmpty>
    </div>

    <NGrid v-else cols="1 m:2 l:3" responsive="screen" :x-gap="12" :y-gap="12" class="ag-grid">
      <NGi v-for="a in agents" :key="a.id">
        <article class="ag-node" :class="'is-' + statusKey(a)">
          <header class="ag-node__head">
            <span class="ag-dot" />
            <span class="ag-node__name" :title="a.name">{{ a.name }}</span>
            <span class="ag-os mono">{{ osLabel(a.os) }}</span>
            <span class="ag-node__acts">
              <NButton size="tiny" quaternary @click="openEdit(a)">编辑</NButton>
              <NButton size="tiny" quaternary :loading="isBusy('rotate:' + a.id)" @click="rotateToken(a)">轮换密钥</NButton>
              <NButton size="tiny" quaternary type="error" @click="confirmDelete(a)">删除</NButton>
            </span>
          </header>

          <button type="button" class="ag-addr mono" :title="`${a.address}:${a.port}`" @click="copyText(`${a.address}:${a.port}`, '地址已复制')">
            {{ a.address }}:{{ a.port }}
            <span class="ag-addr__tag">{{ roleLabel(a.role) }}</span>
          </button>

          <div v-if="a.metrics && a.metrics.source === 'agent'" class="ag-metrics">
            <div class="ag-metric">
              <div class="ag-metric__row"><span>CPU</span><b>{{ a.metrics.cpu_pct.toFixed(0) }}%</b></div>
              <div class="ag-bar"><i :style="{ width: barWidth(a.metrics.cpu_pct) }" /></div>
            </div>
            <div class="ag-metric">
              <div class="ag-metric__row">
                <span>内存</span>
                <b>{{ formatBytes(a.metrics.mem_used) }} / {{ formatBytes(a.metrics.mem_total) }}</b>
              </div>
              <div class="ag-bar"><i :style="{ width: barWidth(pct(a.metrics.mem_used, a.metrics.mem_total)) }" /></div>
            </div>
            <div class="ag-metric">
              <div class="ag-metric__row">
                <span>磁盘</span>
                <b>{{ formatBytes(a.metrics.disk_used) }} / {{ formatBytes(a.metrics.disk_total) }}</b>
              </div>
              <div class="ag-bar"><i :style="{ width: barWidth(pct(a.metrics.disk_used, a.metrics.disk_total)) }" /></div>
            </div>
            <div class="ag-rates">
              <span>下行 <b class="mono">{{ formatRate(a.metrics.rx_rate) }}</b></span>
              <span>上行 <b class="mono">{{ formatRate(a.metrics.tx_rate) }}</b></span>
            </div>
            <div class="ag-kv">
              <span>网卡 {{ a.metrics.interfaces.length }}</span>
              <span>运行 {{ formatUptime(a.metrics.uptime_sec) }}</span>
            </div>
          </div>
          <div v-else class="ag-wait">等待插件上报</div>

          <footer class="ag-node__foot">
            <span class="ag-meta">{{ a.last_seen ? relTime(a.last_seen) : '无上报' }}</span>
            <NButton size="tiny" type="primary" @click="openDetail(a)">详情</NButton>
          </footer>
        </article>
      </NGi>
    </NGrid>

    <section class="ag-card ag-server">
      <header class="ag-card__head">
        <h3 class="ag-card__title">接收端口设置</h3>
        <div class="ag-card__ops">
          <span class="ag-meta mono">{{ server?.listen || '—' }}:{{ server?.port ?? '—' }}</span>
          <NButton size="tiny" quaternary :loading="isBusy('server-load')" @click="loadServer">刷新</NButton>
          <NButton size="tiny" type="primary" :loading="isBusy('server-save')" @click="saveServer">保存</NButton>
        </div>
      </header>
      <div class="ag-form">
        <div class="ag-field">
          <div class="ag-label">启用</div>
          <div class="ag-switch"><NSwitch v-model:value="serverDraft.enabled" size="small" /></div>
        </div>
        <div class="ag-field">
          <div class="ag-label">监听地址</div>
          <NInput v-model:value="serverDraft.listen" size="small" placeholder="0.0.0.0" />
        </div>
        <div class="ag-field">
          <div class="ag-label">端口</div>
          <NInputNumber v-model:value="serverDraft.port" size="small" :min="1" :max="65535" style="width: 100%" />
        </div>
        <div class="ag-field">
          <div class="ag-label">对外地址</div>
          <NInput v-model:value="serverDraft.public_url" size="small" placeholder="https://ngg.example.com:9191" />
        </div>
      </div>
    </section>

    <!-- 新增 -->
    <NModal v-model:show="showAdd" preset="card" title="添加远程节点" style="width: 520px" :bordered="false">
      <div class="ag-form ag-form--modal">
        <div class="ag-field">
          <div class="ag-label">名称</div>
          <NInput v-model:value="addDraft.name" size="small" placeholder="hk-edge-01" />
        </div>
        <div class="ag-field">
          <div class="ag-label">IP / 域名</div>
          <NInput v-model:value="addDraft.address" size="small" placeholder="203.0.113.10" />
        </div>
        <div class="ag-field">
          <div class="ag-label">管理端口</div>
          <NInputNumber v-model:value="addDraft.port" size="small" :min="1" :max="65535" style="width: 100%" />
        </div>
        <div class="ag-field">
          <div class="ag-label">角色</div>
          <NSelect v-model:value="addDraft.role" size="small" :options="ROLE_OPTIONS" />
        </div>
        <div class="ag-field ag-field--wide">
          <div class="ag-label">备注</div>
          <NInput v-model:value="addDraft.note" size="small" type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" />
        </div>
      </div>
      <template #footer>
        <NSpace justify="end" :size="8">
          <NButton size="small" @click="showAdd = false">取消</NButton>
          <NButton size="small" type="primary" :loading="isBusy('create')" @click="submitAdd">添加</NButton>
        </NSpace>
      </template>
    </NModal>

    <!-- 编辑 -->
    <NModal v-model:show="showEdit" preset="card" title="编辑远程节点" style="width: 520px" :bordered="false">
      <div class="ag-form ag-form--modal">
        <div class="ag-field">
          <div class="ag-label">名称</div>
          <NInput v-model:value="editDraft.name" size="small" />
        </div>
        <div class="ag-field">
          <div class="ag-label">IP / 域名</div>
          <NInput v-model:value="editDraft.address" size="small" />
        </div>
        <div class="ag-field">
          <div class="ag-label">管理端口</div>
          <NInputNumber v-model:value="editDraft.port" size="small" :min="1" :max="65535" style="width: 100%" />
        </div>
        <div class="ag-field">
          <div class="ag-label">角色</div>
          <NSelect v-model:value="editDraft.role" size="small" :options="ROLE_OPTIONS" />
        </div>
        <div class="ag-field ag-field--wide">
          <div class="ag-label">备注</div>
          <NInput v-model:value="editDraft.note" size="small" type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" />
        </div>
      </div>
      <template #footer>
        <NSpace justify="end" :size="8">
          <NButton size="small" @click="showEdit = false">取消</NButton>
          <NButton size="small" type="primary" :loading="isBusy('edit')" @click="submitEdit">保存</NButton>
        </NSpace>
      </template>
    </NModal>

    <!-- 绑定向导 -->
    <NModal
      v-model:show="showEnroll"
      preset="card"
      :title="enrollTitle"
      style="width: 780px"
      :bordered="false"
      @after-leave="onEnrollClosed"
    >
      <template v-if="enroll">
        <NAlert type="warning" :bordered="false" class="ag-alert">密钥只显示这一次</NAlert>
        <div class="ag-enroll-meta">
          <span class="mono">{{ enroll.agent.address }}:{{ enroll.agent.port }}</span>
          <span class="mono ag-token">{{ enroll.token }}</span>
          <NButton size="tiny" @click="copyText(enroll.token, '密钥已复制')">复制密钥</NButton>
        </div>

        <NTabs v-model:value="enrollOs" type="segment" size="small">
          <NTabPane v-for="o in OS_TABS" :key="o.value" :name="o.value" :tab="o.label">
            <div class="ag-code">
              <NButton class="ag-code__copy" size="tiny" @click="copyText(scriptText, '脚本已复制')">复制</NButton>
              <pre>{{ scriptText || '—' }}</pre>
            </div>
          </NTabPane>
        </NTabs>

        <NCollapse class="ag-yaml">
          <NCollapseItem title="config_yaml" name="yaml">
            <div class="ag-code">
              <NButton class="ag-code__copy" size="tiny" @click="copyText(enroll.config_yaml, '配置已复制')">复制</NButton>
              <pre>{{ enroll.config_yaml || '—' }}</pre>
            </div>
          </NCollapseItem>
        </NCollapse>
      </template>
    </NModal>

    <!-- 详情 -->
    <NDrawer v-model:show="showDetail" :width="520" placement="right">
      <NDrawerContent closable :native-scrollbar="false" :title="detail?.name || '节点详情'">
        <template v-if="detail">
          <NDescriptions :column="1" size="small" label-placement="left" bordered class="ag-desc">
            <NDescriptionsItem label="状态">
              <NTag size="small" :bordered="false" :type="statusTagType(detail)">{{ statusLabel(detail) }}</NTag>
            </NDescriptionsItem>
            <NDescriptionsItem label="地址">
              <span class="mono">{{ detail.address }}:{{ detail.port }}</span>
            </NDescriptionsItem>
            <NDescriptionsItem label="角色">{{ roleLabel(detail.role) }}</NDescriptionsItem>
            <NDescriptionsItem label="系统">
              {{ osLabel(detail.os) }}<span v-if="detail.arch" class="mono"> · {{ detail.arch }}</span>
            </NDescriptionsItem>
            <NDescriptionsItem label="主机名">
              <span class="mono">{{ detail.hostname || '—' }}</span>
            </NDescriptionsItem>
            <NDescriptionsItem label="Agent 版本">
              <span class="mono">{{ detail.agent_version || '—' }}</span>
            </NDescriptionsItem>
            <NDescriptionsItem label="最后上报">{{ formatTime(detail.last_seen) }}</NDescriptionsItem>
            <NDescriptionsItem label="节点 ID"><span class="mono">{{ detail.id }}</span></NDescriptionsItem>
            <NDescriptionsItem v-if="detail.note" label="备注">{{ detail.note }}</NDescriptionsItem>
          </NDescriptions>

          <div class="ag-charts">
            <div class="ag-chart">
              <div class="ag-chart__head"><span>CPU</span><b>{{ latestCpu }}</b></div>
              <svg v-if="cpuSeries.length" viewBox="0 0 240 48" preserveAspectRatio="none" class="ag-spark">
                <path :d="sparkPath(cpuSeries, 100)" fill="none" stroke="#0071e3" stroke-width="1.5" vector-effect="non-scaling-stroke" />
              </svg>
              <div v-else class="ag-spark ag-spark--empty">—</div>
            </div>
            <div class="ag-chart">
              <div class="ag-chart__head"><span>内存</span><b>{{ latestMem }}</b></div>
              <svg v-if="memSeries.length" viewBox="0 0 240 48" preserveAspectRatio="none" class="ag-spark">
                <path :d="sparkPath(memSeries, 100)" fill="none" stroke="#34c759" stroke-width="1.5" vector-effect="non-scaling-stroke" />
              </svg>
              <div v-else class="ag-spark ag-spark--empty">—</div>
            </div>
            <div class="ag-chart">
              <div class="ag-chart__head">
                <span>速率</span>
                <b class="mono">{{ latestRx }} / {{ latestTx }}</b>
              </div>
              <svg v-if="rxSeries.length" viewBox="0 0 240 48" preserveAspectRatio="none" class="ag-spark">
                <path :d="sparkPath(rxSeries, rateMax)" fill="none" stroke="#0071e3" stroke-width="1.5" vector-effect="non-scaling-stroke" />
                <path :d="sparkPath(txSeries, rateMax)" fill="none" stroke="#ff9f0a" stroke-width="1.5" vector-effect="non-scaling-stroke" />
              </svg>
              <div v-else class="ag-spark ag-spark--empty">—</div>
            </div>
          </div>
          <div class="ag-legend">
            <span class="ag-legend__i is-rx" />下行
            <span class="ag-legend__i is-tx" />上行
            <span class="ag-meta">{{ samples.length }} 采样</span>
            <NButton size="tiny" quaternary :loading="samplesLoading" @click="openDetail(detail)">刷新</NButton>
          </div>

          <div class="ag-sub">网卡</div>
          <table v-if="detail.metrics?.interfaces.length" class="ag-table">
            <thead>
              <tr>
                <th>名称</th>
                <th>MAC</th>
                <th>MTU</th>
                <th>状态</th>
                <th>地址</th>
                <th>收</th>
                <th>发</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="n in detail.metrics.interfaces" :key="n.name">
                <td class="mono">{{ n.name }}</td>
                <td class="mono ag-ellipsis">{{ n.mac || '—' }}</td>
                <td>{{ n.mtu }}</td>
                <td>
                  <span class="ag-state" :class="n.up ? 'is-up' : 'is-down'">{{ n.up ? 'UP' : 'DOWN' }}</span>
                </td>
                <td class="mono">{{ n.addresses.join(', ') || '—' }}</td>
                <td class="mono">{{ formatBytes(n.rx_bytes) }}</td>
                <td class="mono">{{ formatBytes(n.tx_bytes) }}</td>
              </tr>
            </tbody>
          </table>
          <div v-else class="ag-meta">—</div>

          <div class="ag-sub">磁盘</div>
          <table v-if="detail.metrics?.disks.length" class="ag-table">
            <thead>
              <tr><th>挂载点</th><th>文件系统</th><th>已用 / 总量</th><th>占比</th></tr>
            </thead>
            <tbody>
              <tr v-for="d in detail.metrics.disks" :key="d.mount">
                <td class="mono">{{ d.mount }}</td>
                <td>{{ d.fstype || '—' }}</td>
                <td class="mono">{{ formatBytes(d.used) }} / {{ formatBytes(d.total) }}</td>
                <td class="mono">{{ pctText(d.used, d.total) }}</td>
              </tr>
            </tbody>
          </table>
          <div v-else class="ag-meta">—</div>
        </template>
      </NDrawerContent>
    </NDrawer>
  </div>
</template>

<style scoped>
.ag {
  color: #1d1d1f;
  font-size: 12px;
}
.mono {
  font-family: 'SF Mono Nerd Font', 'PingFang SC', 'Microsoft YaHei', monospace;
}
.ag-meta {
  font-size: 10.5px;
  color: #6e6e73;
  font-variant-numeric: tabular-nums;
}

.ag-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}
.ag-toolbar__ops {
  display: flex;
  align-items: center;
  gap: 6px;
}

.ag-stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
  margin-bottom: 12px;
}
.ag-stat {
  background: #fff;
  border: 1px solid #e6e6eb;
  border-radius: 10px;
  padding: 8px 10px;
  min-width: 0;
}
.ag-stat__label {
  font-size: 10.5px;
  color: #6e6e73;
  font-weight: 500;
}
.ag-stat__value {
  font-size: 18px;
  font-weight: 600;
  letter-spacing: -0.02em;
  line-height: 1.25;
  margin-top: 2px;
  font-variant-numeric: tabular-nums;
}
.ag-stat__value.is-green {
  color: #34c759;
}
.ag-stat__value.is-amber {
  color: #ff9f0a;
}

.ag-grid {
  margin-bottom: 12px;
}

.ag-node {
  background: #fff;
  border: 1px solid #e6e6eb;
  border-radius: 12px;
  padding: 10px 12px 8px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}
.ag-node.is-online {
  border-color: #d7ebdc;
}
.ag-node.is-unbound {
  border-color: #f6e6cf;
}
.ag-node__head {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.ag-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #c7c7cc;
  flex: 0 0 auto;
}
.ag-node.is-online .ag-dot {
  background: #34c759;
  box-shadow: 0 0 0 3px rgba(52, 199, 89, 0.14);
}
.ag-node.is-unbound .ag-dot {
  background: #ff9f0a;
  box-shadow: 0 0 0 3px rgba(255, 159, 10, 0.14);
}
.ag-node__name {
  font-size: 13px;
  font-weight: 600;
  letter-spacing: -0.01em;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}
.ag-os {
  flex: 0 0 auto;
  font-size: 10px;
  color: #6e6e73;
  background: #f5f5f7;
  border: 1px solid #ececf0;
  border-radius: 5px;
  padding: 0 5px;
  line-height: 16px;
}
.ag-node__acts {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 2px;
  flex: 0 0 auto;
}

.ag-addr {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  border: 0;
  background: none;
  padding: 0;
  font-size: 12px;
  color: #3a3a3c;
  cursor: pointer;
  text-align: left;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ag-addr:hover {
  color: #0071e3;
}
.ag-addr__tag {
  font-family: -apple-system, BlinkMacSystemFont, 'PingFang SC', 'Microsoft YaHei', sans-serif;
  font-size: 10px;
  color: #0071e3;
  background: rgba(0, 113, 227, 0.08);
  border-radius: 5px;
  padding: 0 5px;
  line-height: 16px;
  flex: 0 0 auto;
}

.ag-metrics {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.ag-metric__row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  font-size: 10.5px;
  color: #6e6e73;
  min-width: 0;
}
.ag-metric__row b {
  color: #3a3a3c;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ag-bar {
  display: block;
  height: 3px;
  margin-top: 3px;
  border-radius: 2px;
  background: #f0f0f2;
  overflow: hidden;
}
.ag-bar i {
  display: block;
  height: 100%;
  border-radius: 2px;
  background: #0071e3;
  transition: width 0.4s ease;
}
.ag-rates {
  display: flex;
  gap: 12px;
  font-size: 10.5px;
  color: #6e6e73;
}
.ag-rates b {
  color: #3a3a3c;
  font-weight: 500;
}
.ag-kv {
  display: flex;
  gap: 12px;
  font-size: 10.5px;
  color: #6e6e73;
}
.ag-wait {
  font-size: 11px;
  color: #a1a1a6;
  padding: 10px 0;
}
.ag-node__foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  border-top: 1px solid #f0f0f2;
  margin-top: auto;
  padding-top: 8px;
}

.ag-card {
  background: #fff;
  border: 1px solid #e6e6eb;
  border-radius: 10px;
  padding: 10px 12px;
}
.ag-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.ag-card__title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  letter-spacing: -0.01em;
}
.ag-card__ops {
  display: flex;
  align-items: center;
  gap: 6px;
}
.ag-empty {
  padding: 24px 12px;
}

.ag-form {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px 10px;
}
.ag-form--modal {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}
.ag-field {
  min-width: 0;
}
.ag-field--wide {
  grid-column: 1 / -1;
}
.ag-label {
  font-size: 10.5px;
  color: #6e6e73;
  font-weight: 500;
  margin-bottom: 4px;
}
.ag-switch {
  display: flex;
  align-items: center;
  height: 28px;
}

.ag-alert {
  margin-bottom: 10px;
}
.ag-enroll-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 10px;
  font-size: 12px;
  color: #3a3a3c;
}
.ag-token {
  flex: 1 1 240px;
  min-width: 0;
  background: #f5f5f7;
  border: 1px solid #ececf0;
  border-radius: 6px;
  padding: 3px 8px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ag-code {
  position: relative;
}
.ag-code__copy {
  position: absolute;
  top: 6px;
  right: 6px;
  z-index: 1;
}
.ag-code pre {
  margin: 0;
  padding: 10px 12px;
  background: #f7f7f9;
  border: 1px solid #e6e6eb;
  border-radius: 10px;
  color: #3a3a3c;
  font-family: 'SF Mono Nerd Font', 'PingFang SC', 'Microsoft YaHei', monospace;
  font-feature-settings: 'calt' 0, 'liga' 0;
  font-variant-ligatures: none;
  font-size: 12px;
  line-height: 1.6;
  max-height: 260px;
  overflow: auto;
  white-space: pre;
}
.ag-yaml {
  margin-top: 10px;
}

.ag-desc {
  margin-bottom: 12px;
}
.ag-charts {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 6px;
}
.ag-chart {
  border: 1px solid #f0f0f2;
  border-radius: 10px;
  padding: 6px 8px 4px;
}
.ag-chart__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  font-size: 10.5px;
  color: #6e6e73;
}
.ag-chart__head b {
  color: #3a3a3c;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}
.ag-spark {
  display: block;
  width: 100%;
  height: 44px;
}
.ag-spark--empty {
  color: #c7c7cc;
  font-size: 11px;
  line-height: 44px;
}
.ag-legend {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 10.5px;
  color: #6e6e73;
  margin-bottom: 12px;
}
.ag-legend__i {
  width: 10px;
  height: 2px;
  border-radius: 1px;
  display: inline-block;
}
.ag-legend__i.is-rx {
  background: #0071e3;
  margin-left: 4px;
}
.ag-legend__i.is-tx {
  background: #ff9f0a;
  margin-left: 8px;
}
.ag-sub {
  font-size: 12px;
  font-weight: 600;
  color: #1d1d1f;
  margin: 12px 0 6px;
}
.ag-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 11px;
  table-layout: fixed;
}
.ag-table th {
  text-align: left;
  font-weight: 500;
  color: #8e8e93;
  font-size: 10.5px;
  padding: 4px 6px;
  border-bottom: 1px solid #e6e6eb;
  white-space: nowrap;
}
.ag-table td {
  padding: 5px 6px;
  border-bottom: 1px solid #f4f4f6;
  color: #3a3a3c;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ag-ellipsis {
  max-width: 120px;
}
.ag-state {
  font-size: 10px;
  border-radius: 5px;
  padding: 0 5px;
  line-height: 16px;
  background: #f2f2f5;
  color: #6e6e73;
}
.ag-state.is-up {
  background: rgba(52, 199, 89, 0.12);
  color: #1f7a3a;
}

@media (max-width: 900px) {
  .ag-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .ag-form {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 620px) {
  .ag-form,
  .ag-form--modal {
    grid-template-columns: 1fr;
  }
}
</style>
