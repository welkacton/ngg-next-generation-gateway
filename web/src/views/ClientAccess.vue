<script setup lang="ts">
import { computed, h, onMounted, reactive, ref, watch } from 'vue'
import {
  NButton,
  NDataTable,
  NDatePicker,
  NDrawer,
  NDrawerContent,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  NSwitch,
  NTag,
  type DataTableColumns,
} from 'naive-ui'
import * as api from '../api/ngg'
import type {
  AccessConfig,
  ClientConfigResult,
  ClientPlatform,
  ClientProtocol,
  ClientView,
} from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { formatBytes, formatTime } from '../utils/format'

const { isBusy, run } = useApiAction()

type ConfigFormat = 'singbox' | 'mihomo' | 'wireguard' | 'socks'

const PLATFORM_LABEL: Record<ClientPlatform, string> = {
  ios: 'iOS',
  android: 'Android',
  windows: 'Windows',
  macos: 'macOS',
  linux: 'Linux',
}
const PLATFORM_OPTIONS = (Object.keys(PLATFORM_LABEL) as ClientPlatform[]).map((id) => ({
  label: PLATFORM_LABEL[id],
  value: id,
}))

const PROTOCOL_LABEL: Record<ClientProtocol, string> = {
  socks5: 'SOCKS5',
  http: 'HTTP',
  wireguard: 'WireGuard',
}
const PROTOCOL_OPTIONS = (Object.keys(PROTOCOL_LABEL) as ClientProtocol[]).map((id) => ({
  label: PROTOCOL_LABEL[id],
  value: id,
}))
const PROTOCOLS: Array<{ id: ClientProtocol; label: string }> = [
  { id: 'socks5', label: 'SOCKS5' },
  { id: 'http', label: 'HTTP' },
  { id: 'wireguard', label: 'WireGuard' },
]

const FORMAT_OPTIONS: Array<{ label: string; value: ConfigFormat }> = [
  { label: 'sing-box', value: 'singbox' },
  { label: 'Mihomo', value: 'mihomo' },
  { label: 'WireGuard', value: 'wireguard' },
  { label: 'SOCKS5', value: 'socks' },
]

type ClientState = 'online' | 'offline' | 'disabled' | 'expired'
const STATE_LABEL: Record<ClientState, string> = {
  online: '在线',
  offline: '离线',
  disabled: '已禁用',
  expired: '已过期',
}
const STATE_TYPE: Record<ClientState, 'success' | 'default' | 'warning' | 'error'> = {
  online: 'success',
  offline: 'default',
  disabled: 'warning',
  expired: 'error',
}

const accessDraft = reactive({
  public_host: '',
  public_port: 1080 as number | null,
  protocol: 'socks5' as ClientProtocol,
  tls: false,
  note: '',
})

const clients = ref<ClientView[]>([])
const loading = ref(false)
let accessSynced = false

const showCreate = ref(false)
const clientDraft = reactive({
  name: '',
  platform: 'ios' as ClientPlatform,
  protocol: 'socks5' as ClientProtocol,
  expires: null as number | null,
  quotaGb: null as number | null,
  note: '',
})

const showCredential = ref(false)
const credential = ref('')
const credentialClient = ref<ClientView | null>(null)

const showDrawer = ref(false)
const drawerClient = ref<ClientView | null>(null)
const format = ref<ConfigFormat>('singbox')
const config = ref<ClientConfigResult | null>(null)

const stats = computed(() => {
  const host = accessDraft.public_host.trim()
  const port = accessDraft.public_port
  return [
    {
      label: '接入地址',
      value: host ? `${host}:${typeof port === 'number' ? port : '—'}` : '未设置',
    },
    { label: '协议', value: PROTOCOL_LABEL[accessDraft.protocol] },
    { label: '客户端总数', value: String(clients.value.length) },
    { label: '在线', value: String(clients.value.filter((c) => c.online).length) },
  ]
})

const formatOptions = computed(() => {
  const client = drawerClient.value
  const allowed = client ? formatsOf(client) : FORMAT_OPTIONS.map((o) => o.value)
  return FORMAT_OPTIONS.filter((o) => allowed.includes(o.value))
})

function toApiFormat(raw: string): ConfigFormat | null {
  const key = raw.toLowerCase().replace(/[^a-z0-9]/g, '')
  if (key === 'singbox') return 'singbox'
  if (key === 'mihomo' || key === 'clash' || key === 'clashmeta') return 'mihomo'
  if (key === 'wireguard' || key === 'wg') return 'wireguard'
  if (key === 'socks' || key === 'socks5') return 'socks'
  return null
}

function formatsOf(client: ClientView): ConfigFormat[] {
  const list = Array.isArray(client.formats) ? client.formats : []
  const out: ConfigFormat[] = []
  for (const raw of list) {
    const item = toApiFormat(String(raw))
    if (item && !out.includes(item)) out.push(item)
  }
  return out.length ? out : FORMAT_OPTIONS.map((o) => o.value)
}

function isExpired(client: ClientView): boolean {
  if (!client.expires_at) return false
  const time = new Date(client.expires_at).getTime()
  return !Number.isNaN(time) && time <= Date.now()
}

function stateOf(client: ClientView): ClientState {
  if (!client.enabled) return 'disabled'
  if (isExpired(client)) return 'expired'
  return client.online ? 'online' : 'offline'
}

const columns: DataTableColumns<ClientView> = [
  {
    title: '名称',
    key: 'name',
    minWidth: 140,
    render: (row) => h('span', { class: 'cell-clip', title: row.name }, row.name || '—'),
  },
  {
    title: '平台',
    key: 'platform',
    width: 96,
    render: (row) => PLATFORM_LABEL[row.platform] ?? row.platform,
  },
  {
    title: '协议',
    key: 'protocol',
    width: 92,
    render: (row) => PROTOCOL_LABEL[row.protocol] ?? row.protocol,
  },
  {
    title: '状态',
    key: 'state',
    width: 88,
    render: (row) => {
      const state = stateOf(row)
      return h(
        NTag,
        { size: 'small', bordered: false, type: STATE_TYPE[state] },
        { default: () => STATE_LABEL[state] },
      )
    },
  },
  {
    title: '已用流量',
    key: 'used',
    width: 150,
    render: (row) => {
      const over = row.quota_bytes > 0 && row.used_bytes > row.quota_bytes
      const text =
        row.quota_bytes > 0
          ? `${formatBytes(row.used_bytes)} / ${formatBytes(row.quota_bytes)}`
          : formatBytes(row.used_bytes)
      return h('span', { class: over ? 'flow is-over' : 'flow' }, text)
    },
  },
  {
    title: '过期时间',
    key: 'expires_at',
    width: 168,
    render: (row) => (row.expires_at ? formatTime(row.expires_at) : '永久'),
  },
  {
    title: '操作',
    key: 'ops',
    width: 300,
    fixed: 'right',
    render: (row) =>
      h('div', { class: 'row-actions' }, [
        h(
          NButton,
          { size: 'tiny', quaternary: true, type: 'primary', onClick: () => openConfig(row) },
          { default: () => '配置' },
        ),
        h(
          NButton,
          { size: 'tiny', quaternary: true, onClick: () => rotateClient(row) },
          { default: () => '轮换' },
        ),
        h(
          NButton,
          { size: 'tiny', quaternary: true, onClick: () => toggleClient(row) },
          { default: () => (row.enabled ? '禁用' : '启用') },
        ),
        h(
          NButton,
          { size: 'tiny', quaternary: true, type: 'error', onClick: () => removeClient(row) },
          { default: () => '删除' },
        ),
      ]),
  },
]

function clientRowKey(row: ClientView): string {
  return row.id
}

async function copyText(text: string, okMsg = '已复制到剪贴板'): Promise<void> {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    window.$message?.success(okMsg)
  } catch {
    window.$message?.warning('浏览器拒绝了剪贴板访问，请手动全选复制')
  }
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await api.listClients().catch(() => null)
    if (!res) return
    clients.value = Array.isArray(res.clients) ? res.clients : []
    if (!accessSynced) {
      syncAccess(res.access)
      accessSynced = true
    }
  } finally {
    loading.value = false
  }
}

function syncAccess(access: AccessConfig): void {
  if (!access) return
  accessDraft.public_host = access.public_host ?? ''
  accessDraft.public_port =
    typeof access.public_port === 'number' && access.public_port > 0 ? access.public_port : 1080
  accessDraft.protocol =
    access.protocol === 'http' || access.protocol === 'wireguard' ? access.protocol : 'socks5'
  accessDraft.tls = access.tls === true
  accessDraft.note = access.note ?? ''
}

async function saveAccess(): Promise<void> {
  const host = accessDraft.public_host.trim()
  if (!host) {
    window.$message?.warning('未设置对外接入地址时无法生成客户端配置')
    return
  }
  const port = accessDraft.public_port
  if (port === null || Number.isNaN(port) || port < 1 || port > 65535) {
    window.$message?.warning('端口需在 1–65535')
    return
  }
  const payload: AccessConfig = {
    public_host: host,
    public_port: Number(port),
    protocol: accessDraft.protocol,
    tls: accessDraft.tls,
    note: accessDraft.note.trim(),
  }
  const saved = await run('access', () => api.saveAccessConfig(payload), '已保存')
  if (saved) {
    syncAccess(saved.access)
    accessSynced = true
  }
}

function openCreate(): void {
  clientDraft.name = ''
  clientDraft.platform = 'ios'
  clientDraft.protocol = accessDraft.protocol
  clientDraft.expires = null
  clientDraft.quotaGb = null
  clientDraft.note = ''
  showCreate.value = true
}

async function submitClient(): Promise<void> {
  const name = clientDraft.name.trim()
  if (!name) {
    window.$message?.warning('名称不能为空')
    return
  }
  const payload: Parameters<typeof api.createClient>[0] = {
    name,
    platform: clientDraft.platform,
    protocol: clientDraft.protocol,
  }
  const note = clientDraft.note.trim()
  if (note) payload.note = note
  if (clientDraft.expires) payload.expires_at = new Date(clientDraft.expires).toISOString()
  if (clientDraft.quotaGb !== null && clientDraft.quotaGb > 0) {
    payload.quota_bytes = Math.round(clientDraft.quotaGb * 1024 * 1024 * 1024)
  }

  const res = await run('create', () => api.createClient(payload))
  if (!res) return
  showCreate.value = false
  credential.value = res.credential
  credentialClient.value = res.client
  showCredential.value = true
  await load()
}

function rotateClient(client: ClientView): void {
  window.$dialog?.warning({
    title: '轮换凭据',
    content: `确认轮换「${client.name}」的凭据？旧凭据立即失效。`,
    positiveText: '轮换',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await run('rotate', () => api.rotateClient(client.id))
      if (!res) return
      credential.value = res.credential
      credentialClient.value = res.client
      showCredential.value = true
      await load()
    },
  })
}

async function toggleClient(client: ClientView): Promise<void> {
  const res = await run('toggle', () => api.updateClient(client.id, { enabled: !client.enabled }))
  if (res) await load()
}

function removeClient(client: ClientView): void {
  window.$dialog?.warning({
    title: '删除客户端',
    content: `确认删除「${client.name}」？`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await run('del', () => api.deleteClient(client.id))
      if (res) await load()
    },
  })
}

async function loadConfig(): Promise<void> {
  const client = drawerClient.value
  if (!client) return
  const wanted = format.value
  const res = await api.getClientConfig(client.id, wanted).catch(() => null)
  if (format.value !== wanted || drawerClient.value?.id !== client.id) return
  config.value = res
}

function openConfig(client: ClientView): void {
  drawerClient.value = client
  config.value = null
  showDrawer.value = true
  const allowed = formatsOf(client)
  if (!allowed.includes(format.value)) {
    format.value = allowed[0]
    return
  }
  void loadConfig()
}

watch(format, () => {
  void loadConfig()
})

function copyCredential(): void {
  void copyText(credential.value)
}

async function credentialConfig(): Promise<ClientConfigResult | null> {
  const client = credentialClient.value
  if (!client) return null
  return await api.getClientConfig(client.id, formatsOf(client)[0]).catch(() => null)
}

async function copySubscription(): Promise<void> {
  const cfg = await credentialConfig()
  if (!cfg) return
  if (!cfg.subscription_url) {
    window.$message?.warning('未生成订阅地址')
    return
  }
  await copyText(cfg.subscription_url)
}

async function copyConfigContent(): Promise<void> {
  const cfg = await credentialConfig()
  if (!cfg) return
  await copyText(cfg.content)
}

function copyDrawerContent(): void {
  void copyText(config.value?.content ?? '')
}

async function copySubscriptionUrl(): Promise<void> {
  const url = config.value?.subscription_url ?? ''
  if (!url) {
    window.$message?.warning('未生成订阅地址')
    return
  }
  await copyText(url)
}

function downloadConfig(): void {
  const cfg = config.value
  if (!cfg || !cfg.content) return
  const blob = new Blob([cfg.content], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = cfg.filename || `${drawerClient.value?.name ?? 'client'}.txt`
  a.click()
  URL.revokeObjectURL(url)
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="gw">
    <header class="gw-head">
      <h2 class="gw-title">客户端接入</h2>
      <div class="gw-head__ops">
        <NButton size="tiny" quaternary type="primary" :loading="loading" @click="load">刷新</NButton>
      </div>
    </header>

    <section class="gw-stats">
      <div v-for="item in stats" :key="item.label" class="gw-stat">
        <div class="gw-stat__label">{{ item.label }}</div>
        <div class="gw-stat__value" :title="item.value">{{ item.value }}</div>
      </div>
    </section>

    <section class="gw-card">
      <div class="gw-card__head">
        <h3 class="gw-card__title">接入设置</h3>
        <NButton size="tiny" type="primary" :loading="isBusy('access')" @click="saveAccess">
          保存
        </NButton>
      </div>
      <div class="grid">
        <div class="field span-2">
          <div class="label">对外地址</div>
          <NInput v-model:value="accessDraft.public_host" size="small" placeholder="vpn.example.com" />
        </div>
        <div class="field">
          <div class="label">端口</div>
          <NInputNumber v-model:value="accessDraft.public_port" size="small" :min="1" :max="65535" />
        </div>
        <div class="field">
          <div class="label">TLS</div>
          <div class="switch-row"><NSwitch v-model:value="accessDraft.tls" size="small" /></div>
        </div>
        <div class="field span-2">
          <div class="label">协议</div>
          <div class="seg" role="group" aria-label="接入协议">
            <button
              v-for="p in PROTOCOLS"
              :key="p.id"
              type="button"
              :class="{ 'is-active': accessDraft.protocol === p.id }"
              @click="accessDraft.protocol = p.id"
            >{{ p.label }}</button>
          </div>
        </div>
        <div class="field span-2">
          <div class="label">备注</div>
          <NInput v-model:value="accessDraft.note" size="small" :maxlength="128" />
        </div>
      </div>
    </section>

    <section class="gw-card">
      <div class="gw-card__head">
        <h3 class="gw-card__title">客户端</h3>
        <NButton v-if="clients.length" size="tiny" type="primary" @click="openCreate">+ 新增客户端</NButton>
      </div>
      <div v-if="!clients.length" class="empty">
        <NButton size="small" type="primary" @click="openCreate">+ 新增客户端</NButton>
      </div>
      <NDataTable
        v-else
        class="table"
        size="small"
        :columns="columns"
        :data="clients"
        :row-key="clientRowKey"
        :bordered="false"
        :pagination="false"
        :scroll-x="1040"
      />
    </section>

    <NModal
      v-model:show="showCreate"
      preset="card"
      title="新增客户端"
      class="modal"
      style="width: 460px"
      :bordered="false"
    >
      <div class="grid grid--2">
        <div class="field span-2">
          <div class="label">名称</div>
          <NInput v-model:value="clientDraft.name" size="small" :maxlength="64" />
        </div>
        <div class="field">
          <div class="label">平台</div>
          <NSelect v-model:value="clientDraft.platform" size="small" :options="PLATFORM_OPTIONS" />
        </div>
        <div class="field">
          <div class="label">协议</div>
          <NSelect v-model:value="clientDraft.protocol" size="small" :options="PROTOCOL_OPTIONS" />
        </div>
        <div class="field">
          <div class="label">过期时间</div>
          <NDatePicker
            v-model:value="clientDraft.expires"
            type="datetime"
            size="small"
            clearable
            style="width: 100%"
          />
        </div>
        <div class="field">
          <div class="label">流量配额 (GB)</div>
          <NInputNumber
            v-model:value="clientDraft.quotaGb"
            size="small"
            :min="0.1"
            :max="102400"
            :precision="1"
          />
        </div>
        <div class="field span-2">
          <div class="label">备注</div>
          <NInput v-model:value="clientDraft.note" size="small" :maxlength="128" />
        </div>
      </div>
      <div class="m-foot">
        <NButton size="small" @click="showCreate = false">取消</NButton>
        <NButton size="small" type="primary" :loading="isBusy('create')" @click="submitClient">
          创建
        </NButton>
      </div>
    </NModal>

    <NModal
      v-model:show="showCredential"
      preset="card"
      title="凭据（只显示一次）"
      class="modal"
      style="width: 480px"
      :bordered="false"
    >
      <pre class="code">{{ credential }}</pre>
      <div class="m-foot m-foot--start">
        <NButton size="tiny" type="primary" @click="copyCredential">复制凭据</NButton>
        <NButton size="tiny" @click="copySubscription">复制订阅地址</NButton>
        <NButton size="tiny" @click="copyConfigContent">复制配置内容</NButton>
      </div>
      <div class="m-foot">
        <NButton size="small" type="primary" @click="showCredential = false">关闭</NButton>
      </div>
    </NModal>

    <NDrawer v-model:show="showDrawer" :width="560" placement="right">
      <NDrawerContent closable :native-scrollbar="false" :title="drawerClient?.name || '配置'">
        <div class="dw-bar">
          <NSelect v-model:value="format" size="small" :options="formatOptions" style="width: 150px" />
          <NButton
            size="tiny"
            quaternary
            type="primary"
            :disabled="!config?.content"
            @click="copyDrawerContent"
          >复制</NButton>
          <NButton
            size="tiny"
            quaternary
            type="primary"
            :disabled="!config?.content"
            @click="downloadConfig"
          >下载</NButton>
        </div>
        <pre class="code code--tall">{{ config?.content || '' }}</pre>
        <div class="dw-sub">
          <span class="dw-sub__label">订阅地址</span>
          <span class="dw-sub__url mono" :title="config?.subscription_url || ''">
            {{ config?.subscription_url || '—' }}
          </span>
          <NButton
            size="tiny"
            quaternary
            type="primary"
            :disabled="!config?.subscription_url"
            @click="copySubscriptionUrl"
          >复制</NButton>
        </div>
      </NDrawerContent>
    </NDrawer>
  </div>
</template>

<style scoped>
.gw {
  font-family: -apple-system, BlinkMacSystemFont, 'SF Pro Text', 'PingFang SC',
    'Microsoft YaHei', sans-serif;
  background: #f5f5f7;
  border-radius: 12px;
  padding: 12px;
  color: #1d1d1f;
  font-size: 12px;
  min-height: calc(100vh - 120px);
}

.gw-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.gw-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  letter-spacing: -0.3px;
  line-height: 1.3;
}
.gw-head__ops {
  display: flex;
  align-items: center;
  gap: 6px;
}
.gw-meta {
  font-size: 10.5px;
  color: #6e6e73;
  font-variant-numeric: tabular-nums;
}

.gw-stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
  margin-bottom: 8px;
}
.gw-stat {
  background: #fff;
  border: 1px solid #e6e6eb;
  border-radius: 10px;
  padding: 8px 10px;
  min-width: 0;
}
.gw-stat__label {
  font-size: 10.5px;
  color: #6e6e73;
  font-weight: 500;
}
.gw-stat__value {
  font-size: 15px;
  font-weight: 600;
  letter-spacing: -0.02em;
  margin-top: 2px;
  line-height: 1.3;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.gw-card {
  background: #fff;
  border: 1px solid #e6e6eb;
  border-radius: 10px;
  padding: 10px 12px;
  margin-bottom: 8px;
}
.gw-card:last-of-type {
  margin-bottom: 0;
}
.gw-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 6px;
}
.gw-card__title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  letter-spacing: -0.01em;
}

.grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px 12px;
}
.grid--2 {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}
.field {
  min-width: 0;
}
.span-2 {
  grid-column: span 2;
}
.label {
  font-size: 10.5px;
  color: #6e6e73;
  font-weight: 500;
  margin-bottom: 4px;
}
.switch-row {
  display: flex;
  align-items: center;
  height: 28px;
}

.seg {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  background: #ececef;
  border-radius: 7px;
}
.seg button {
  appearance: none;
  border: 0;
  background: transparent;
  color: #6e6e73;
  font: inherit;
  font-size: 10.5px;
  padding: 3px 12px;
  border-radius: 5px;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.18s ease, color 0.18s ease, box-shadow 0.18s ease;
}
.seg button:hover {
  color: #1d1d1f;
}
.seg button.is-active {
  background: #fff;
  color: #1d1d1f;
  font-weight: 600;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.1);
}

.table :deep(.n-data-table-th) {
  font-size: 11px;
  font-weight: 600;
  padding: 6px 8px;
  white-space: nowrap;
}
.table :deep(.n-data-table-td) {
  font-size: 11.5px;
  padding: 6px 8px;
  line-height: 1.35;
}
.table :deep(.flow) {
  font-variant-numeric: tabular-nums;
}
.table :deep(.flow.is-over) {
  color: #e0245e;
}

.empty {
  display: flex;
  justify-content: center;
  padding: 18px 0 10px;
}

.mono {
  font-family: 'SF Mono Nerd Font', 'PingFang SC', 'Microsoft YaHei', monospace;
}

.code {
  margin: 0;
  padding: 8px 10px;
  background: #f5f5f7;
  border: 1px solid #ececf0;
  border-radius: 8px;
  font-family: 'SF Mono Nerd Font', 'PingFang SC', 'Microsoft YaHei', monospace;
  font-size: 11px;
  line-height: 1.55;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 220px;
  overflow: auto;
  color: #1d1d1f;
}
.code--tall {
  max-height: calc(100vh - 260px);
  min-height: 220px;
}

.m-foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 12px;
}
.m-foot--start {
  justify-content: flex-start;
  flex-wrap: wrap;
}

.modal :deep(.n-card-header__main) {
  font-size: 14px;
  font-weight: 600;
}

.dw-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 8px;
}
.dw-sub {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
}
.dw-sub__label {
  font-size: 10.5px;
  color: #6e6e73;
  font-weight: 500;
  flex: 0 0 auto;
}
.dw-sub__url {
  flex: 1 1 auto;
  min-width: 0;
  font-size: 11px;
  color: #3a3a3c;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 900px) {
  .gw-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 620px) {
  .grid {
    grid-template-columns: 1fr;
  }
  .span-2 {
    grid-column: 1 / -1;
  }
}
</style>
