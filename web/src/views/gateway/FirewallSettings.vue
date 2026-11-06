<script setup lang="ts">
import { computed, h, onMounted, reactive, ref } from 'vue'
import {
  NButton,
  NDataTable,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  NSwitch,
  NTag,
} from 'naive-ui'
import * as api from '../../api/ngg'
import type {
  FirewallRule,
  GatewayConfig,
  GatewayFirewall,
  GatewayStatus,
  PortForward,
} from '../../api/ngg'
import { useApiAction } from '../../composables/useApi'

const { isBusy, run } = useApiAction()

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

const ACTION_OPTIONS = [
  { label: '允许', value: 'accept' },
  { label: '丢弃', value: 'drop' },
  { label: '拒绝', value: 'reject' },
]
const RULE_PROTOCOL_OPTIONS = [
  { label: 'TCP', value: 'tcp' },
  { label: 'UDP', value: 'udp' },
  { label: '任意', value: 'any' },
]
const FORWARD_PROTOCOL_OPTIONS = [
  { label: 'TCP', value: 'tcp' },
  { label: 'UDP', value: 'udp' },
  { label: 'TCP+UDP', value: 'both' },
]
const ACTION_LABEL: Record<FirewallRule['action'], string> = {
  accept: '允许',
  drop: '丢弃',
  reject: '拒绝',
}

const status = ref<GatewayStatus | null>(null)
const config = ref<GatewayConfig | null>(null)
const loading = ref(false)

const rules = ref<FirewallRule[]>([])
const forwards = ref<PortForward[]>([])
const defaultInbound = ref<GatewayFirewall['default_inbound']>('drop')
const upnp = ref(false)
const conntrack = ref(true)

const showRule = ref(false)
const ruleEditingId = ref<string | null>(null)
const ruleForm = reactive({
  name: '',
  action: 'accept' as FirewallRule['action'],
  protocol: 'any' as FirewallRule['protocol'],
  src: '',
  dst: '',
  port: '',
  enabled: true,
})

const showForward = ref(false)
const forwardEditingId = ref<string | null>(null)
const forwardForm = reactive({
  name: '',
  protocol: 'both' as PortForward['protocol'],
  extPort: 8080 as number | null,
  intAddr: '',
  intPort: 80 as number | null,
  enabled: true,
})

const statRows = computed(() => [
  { label: '默认入站策略', value: defaultInbound.value === 'drop' ? '丢弃' : '允许' },
  { label: '规则数', value: String(rules.value.length) },
  { label: '端口转发数', value: String(forwards.value.length) },
  {
    label: '防火墙状态',
    value: status.value ? (status.value.firewall_active ? '已启用' : '未启用') : '—',
  },
])

function actionTagType(action: FirewallRule['action']): 'success' | 'error' | 'warning' {
  if (action === 'accept') return 'success'
  if (action === 'drop') return 'error'
  return 'warning'
}

function newId(prefix: string): string {
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
}

function isValidPort(value: number | null): boolean {
  return typeof value === 'number' && Number.isInteger(value) && value >= 1 && value <= 65535
}

function isValidPortSpec(spec: string): boolean {
  const text = spec.trim()
  if (!text || text === '*') return true
  return text
    .split(/[,，\s]+/)
    .filter(Boolean)
    .every((token) => {
      const m = /^(\d{1,5})(?:-(\d{1,5}))?$/.exec(token)
      if (!m) return false
      const start = Number(m[1])
      const end = m[2] ? Number(m[2]) : start
      return start >= 1 && start <= 65535 && end >= 1 && end <= 65535 && start <= end
    })
}

function warn(message: string): void {
  window.$message?.warning(message)
}

function syncFrom(cfg: GatewayConfig): void {
  const fw = cfg.firewall
  defaultInbound.value = fw.default_inbound === 'accept' ? 'accept' : 'drop'
  rules.value = Array.isArray(fw.rules) ? fw.rules.map((r) => ({ ...r })) : []
  forwards.value = Array.isArray(fw.port_forwards) ? fw.port_forwards.map((f) => ({ ...f })) : []
  upnp.value = fw.upnp === true
  conntrack.value = fw.conntrack === true
}

/* ---- rules ------------------------------------------------------- */

function ruleRowKey(row: FirewallRule): string {
  return row.id
}

function openRule(row?: FirewallRule): void {
  ruleEditingId.value = row ? row.id : null
  ruleForm.name = row?.name ?? ''
  ruleForm.action = row?.action ?? 'accept'
  ruleForm.protocol = row?.protocol ?? 'any'
  ruleForm.src = row?.src ?? ''
  ruleForm.dst = row?.dst ?? ''
  ruleForm.port = row?.port ?? ''
  ruleForm.enabled = row?.enabled ?? true
  showRule.value = true
}

function submitRule(): void {
  const name = ruleForm.name.trim()
  if (!name) {
    warn('规则名称不能为空')
    return
  }
  if (!isValidPortSpec(ruleForm.port)) {
    warn('端口需在 1–65535 之间')
    return
  }
  const payload: FirewallRule = {
    id: ruleEditingId.value ?? newId('rule'),
    name,
    action: ruleForm.action,
    protocol: ruleForm.protocol,
    src: ruleForm.src.trim(),
    dst: ruleForm.dst.trim(),
    port: ruleForm.port.trim(),
    enabled: ruleForm.enabled,
  }
  const index = rules.value.findIndex((r) => r.id === payload.id)
  if (index >= 0) rules.value.splice(index, 1, payload)
  else rules.value.push(payload)
  showRule.value = false
}

function confirmDeleteRule(row: FirewallRule): void {
  window.$dialog?.warning({
    title: '删除规则',
    content: `确认删除「${row.name}」？`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: () => {
      rules.value = rules.value.filter((r) => r.id !== row.id)
    },
  })
}

const ruleColumns: any[] = [
  {
    title: '启用',
    key: 'enabled',
    width: 62,
    align: 'center',
    titleAlign: 'center',
    render: (row: FirewallRule) =>
      h(NSwitch, {
        size: 'small',
        value: row.enabled,
        'onUpdate:value': (value: boolean | string | number) => {
          row.enabled = Boolean(value)
        },
      }),
  },
  {
    title: '名称',
    key: 'name',
    minWidth: 130,
    render: (row: FirewallRule) =>
      h('span', { class: 'cell-clip', title: row.name }, row.name || '—'),
  },
  {
    title: '动作',
    key: 'action',
    width: 84,
    render: (row: FirewallRule) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: actionTagType(row.action) },
        { default: () => ACTION_LABEL[row.action] },
      ),
  },
  { title: '协议', key: 'protocol', width: 74 },
  {
    title: '来源',
    key: 'src',
    minWidth: 130,
    render: (row: FirewallRule) => h('span', { class: 'cell-clip' }, row.src || '—'),
  },
  {
    title: '目标',
    key: 'dst',
    minWidth: 130,
    render: (row: FirewallRule) => h('span', { class: 'cell-clip' }, row.dst || '—'),
  },
  {
    title: '端口',
    key: 'port',
    width: 110,
    render: (row: FirewallRule) => h('span', { class: 'mono' }, row.port || '—'),
  },
  {
    title: '操作',
    key: 'ops',
    width: 116,
    fixed: 'right',
    render: (row: FirewallRule) =>
      h('div', { class: 'row-actions' }, [
        h(
          NButton,
          { size: 'tiny', quaternary: true, type: 'primary', onClick: () => openRule(row) },
          { default: () => '编辑' },
        ),
        h(
          NButton,
          { size: 'tiny', quaternary: true, type: 'error', onClick: () => confirmDeleteRule(row) },
          { default: () => '删除' },
        ),
      ]),
  },
]

/* ---- port forwards ----------------------------------------------- */

function forwardRowKey(row: PortForward): string {
  return row.id
}

function openForward(row?: PortForward): void {
  forwardEditingId.value = row ? row.id : null
  forwardForm.name = row?.name ?? ''
  forwardForm.protocol = row?.protocol ?? 'both'
  forwardForm.extPort = row?.ext_port ?? 8080
  forwardForm.intAddr = row?.int_addr ?? ''
  forwardForm.intPort = row?.int_port ?? 80
  forwardForm.enabled = row?.enabled ?? true
  showForward.value = true
}

function submitForward(): void {
  const name = forwardForm.name.trim()
  if (!name) {
    warn('转发名称不能为空')
    return
  }
  if (!IPV4.test(forwardForm.intAddr.trim())) {
    warn('内部地址必须是 IPv4')
    return
  }
  if (!isValidPort(forwardForm.extPort)) {
    warn('外部端口需在 1–65535 之间')
    return
  }
  if (!isValidPort(forwardForm.intPort)) {
    warn('内部端口需在 1–65535 之间')
    return
  }
  const payload: PortForward = {
    id: forwardEditingId.value ?? newId('fwd'),
    name,
    protocol: forwardForm.protocol,
    ext_port: Number(forwardForm.extPort),
    int_addr: forwardForm.intAddr.trim(),
    int_port: Number(forwardForm.intPort),
    enabled: forwardForm.enabled,
  }
  const index = forwards.value.findIndex((f) => f.id === payload.id)
  if (index >= 0) forwards.value.splice(index, 1, payload)
  else forwards.value.push(payload)
  showForward.value = false
}

function confirmDeleteForward(row: PortForward): void {
  window.$dialog?.warning({
    title: '删除端口转发',
    content: `确认删除「${row.name}」？`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: () => {
      forwards.value = forwards.value.filter((f) => f.id !== row.id)
    },
  })
}

const forwardColumns: any[] = [
  {
    title: '启用',
    key: 'enabled',
    width: 62,
    align: 'center',
    titleAlign: 'center',
    render: (row: PortForward) =>
      h(NSwitch, {
        size: 'small',
        value: row.enabled,
        'onUpdate:value': (value: boolean | string | number) => {
          row.enabled = Boolean(value)
        },
      }),
  },
  {
    title: '名称',
    key: 'name',
    minWidth: 140,
    render: (row: PortForward) =>
      h('span', { class: 'cell-clip', title: row.name }, row.name || '—'),
  },
  { title: '协议', key: 'protocol', width: 90 },
  {
    title: '外部端口',
    key: 'ext_port',
    width: 100,
    render: (row: PortForward) => h('span', { class: 'mono' }, String(row.ext_port)),
  },
  {
    title: '内部地址',
    key: 'int_addr',
    minWidth: 140,
    render: (row: PortForward) => h('span', { class: 'mono' }, row.int_addr || '—'),
  },
  {
    title: '内部端口',
    key: 'int_port',
    width: 100,
    render: (row: PortForward) => h('span', { class: 'mono' }, String(row.int_port)),
  },
  {
    title: '操作',
    key: 'ops',
    width: 116,
    fixed: 'right',
    render: (row: PortForward) =>
      h('div', { class: 'row-actions' }, [
        h(
          NButton,
          { size: 'tiny', quaternary: true, type: 'primary', onClick: () => openForward(row) },
          { default: () => '编辑' },
        ),
        h(
          NButton,
          { size: 'tiny', quaternary: true, type: 'error', onClick: () => confirmDeleteForward(row) },
          { default: () => '删除' },
        ),
      ]),
  },
]

/* ---- load / save ------------------------------------------------- */

function validateAll(): string | null {
  for (const rule of rules.value) {
    if (!rule.name.trim()) return '规则名称不能为空'
    if (!isValidPortSpec(rule.port)) return `规则「${rule.name}」端口无效`
  }
  for (const item of forwards.value) {
    if (!item.name.trim()) return '转发名称不能为空'
    if (!IPV4.test(item.int_addr.trim())) return `转发「${item.name}」内部地址格式无效`
    if (!isValidPort(item.ext_port)) return `转发「${item.name}」外部端口无效`
    if (!isValidPort(item.int_port)) return `转发「${item.name}」内部端口无效`
  }
  return null
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const [cfg, st] = await Promise.allSettled([api.getGatewayConfig(), api.getGatewayStatus()])
    if (cfg.status === 'fulfilled') {
      config.value = cfg.value.config
      syncFrom(cfg.value.config)
    }
    if (st.status === 'fulfilled') status.value = st.value
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  const err = validateAll()
  if (err) {
    warn(err)
    return
  }
  const latest = await api.getGatewayConfig().catch(() => null)
  const base = latest?.config ?? config.value
  if (!base) return

  const firewall: GatewayFirewall = {
    default_inbound: defaultInbound.value,
    rules: rules.value.map((r) => ({
      ...r,
      name: r.name.trim(),
      src: r.src.trim(),
      dst: r.dst.trim(),
      port: r.port.trim(),
    })),
    port_forwards: forwards.value.map((f) => ({
      ...f,
      name: f.name.trim(),
      int_addr: f.int_addr.trim(),
    })),
    upnp: upnp.value,
    conntrack: conntrack.value,
  }

  const saved = await run(
    'save',
    () => api.saveGatewayConfig({ ...base, firewall }),
    '已保存',
  )
  if (saved) {
    config.value = saved.config
    syncFrom(saved.config)
  }
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="gw">
    <div class="gw-head">
      <h2 class="page-title">防火墙设置</h2>
      <div class="gw-head__right">
        <button class="gw-refresh" type="button" :disabled="loading" @click="load">
          {{ loading ? '刷新中' : '刷新' }}
        </button>
      </div>
    </div>

    <section class="gw-stats">
      <div v-for="row in statRows" :key="row.label" class="gw-stat">
        <div class="gw-stat__label">{{ row.label }}</div>
        <div class="gw-stat__value" :title="row.value">{{ row.value }}</div>
      </div>
    </section>

    <section class="gw-card">
      <div class="gw-card__head">
        <h3 class="gw-card__title">默认入站策略</h3>
        <div class="gw-seg" role="group" aria-label="默认入站策略">
          <button
            type="button"
            :class="{ 'is-active': defaultInbound === 'drop' }"
            @click="defaultInbound = 'drop'"
          >丢弃</button>
          <button
            type="button"
            :class="{ 'is-active': defaultInbound === 'accept' }"
            @click="defaultInbound = 'accept'"
          >允许</button>
        </div>
      </div>
      <div class="gw-switches">
        <div class="gw-switch">
          <span class="gw-label">UPnP</span>
          <NSwitch v-model:value="upnp" size="small" />
        </div>
        <div class="gw-switch">
          <span class="gw-label">连接跟踪</span>
          <NSwitch v-model:value="conntrack" size="small" />
        </div>
      </div>
    </section>

    <section class="gw-card">
      <div class="gw-card__head">
        <h3 class="gw-card__title">规则</h3>
        <NButton size="tiny" type="primary" @click="openRule()">+ 新增规则</NButton>
      </div>
      <NDataTable
        :columns="ruleColumns"
        :data="rules"
        :row-key="ruleRowKey"
        :bordered="false"
        :pagination="false"
        :scroll-x="940"
        size="small"
      />
    </section>

    <section class="gw-card">
      <div class="gw-card__head">
        <h3 class="gw-card__title">端口转发</h3>
        <NButton size="tiny" type="primary" @click="openForward()">+ 新增转发</NButton>
      </div>
      <NDataTable
        :columns="forwardColumns"
        :data="forwards"
        :row-key="forwardRowKey"
        :bordered="false"
        :pagination="false"
        :scroll-x="820"
        size="small"
      />
    </section>

    <div class="gw-save">
      <NButton type="primary" size="small" :loading="isBusy('save')" @click="save">保存</NButton>
    </div>

    <NModal
      v-model:show="showRule"
      preset="card"
      :title="ruleEditingId ? '编辑规则' : '新增规则'"
      style="width: 520px"
      :bordered="false"
    >
      <div class="gw-form">
        <div class="gw-field span-2">
          <div class="gw-label">名称</div>
          <NInput v-model:value="ruleForm.name" size="small" />
        </div>
        <div class="gw-field">
          <div class="gw-label">动作</div>
          <NSelect v-model:value="ruleForm.action" size="small" :options="ACTION_OPTIONS" />
        </div>
        <div class="gw-field">
          <div class="gw-label">协议</div>
          <NSelect
            v-model:value="ruleForm.protocol"
            size="small"
            :options="RULE_PROTOCOL_OPTIONS"
          />
        </div>
        <div class="gw-field">
          <div class="gw-label">来源</div>
          <NInput v-model:value="ruleForm.src" size="small" placeholder="any" />
        </div>
        <div class="gw-field">
          <div class="gw-label">目标</div>
          <NInput v-model:value="ruleForm.dst" size="small" placeholder="any" />
        </div>
        <div class="gw-field">
          <div class="gw-label">端口</div>
          <NInput v-model:value="ruleForm.port" size="small" placeholder="any" />
        </div>
        <div class="gw-field">
          <div class="gw-label">启用</div>
          <NSwitch v-model:value="ruleForm.enabled" size="small" />
        </div>
      </div>
      <div class="m-foot">
        <NButton size="small" @click="showRule = false">取消</NButton>
        <NButton size="small" type="primary" @click="submitRule">确定</NButton>
      </div>
    </NModal>

    <NModal
      v-model:show="showForward"
      preset="card"
      :title="forwardEditingId ? '编辑端口转发' : '新增端口转发'"
      style="width: 520px"
      :bordered="false"
    >
      <div class="gw-form">
        <div class="gw-field span-2">
          <div class="gw-label">名称</div>
          <NInput v-model:value="forwardForm.name" size="small" />
        </div>
        <div class="gw-field">
          <div class="gw-label">协议</div>
          <NSelect
            v-model:value="forwardForm.protocol"
            size="small"
            :options="FORWARD_PROTOCOL_OPTIONS"
          />
        </div>
        <div class="gw-field">
          <div class="gw-label">外部端口</div>
          <NInputNumber v-model:value="forwardForm.extPort" size="small" :min="1" :max="65535" />
        </div>
        <div class="gw-field">
          <div class="gw-label">内部地址</div>
          <NInput v-model:value="forwardForm.intAddr" size="small" placeholder="192.168.10.20" />
        </div>
        <div class="gw-field">
          <div class="gw-label">内部端口</div>
          <NInputNumber v-model:value="forwardForm.intPort" size="small" :min="1" :max="65535" />
        </div>
        <div class="gw-field">
          <div class="gw-label">启用</div>
          <NSwitch v-model:value="forwardForm.enabled" size="small" />
        </div>
      </div>
      <div class="m-foot">
        <NButton size="small" @click="showForward = false">取消</NButton>
        <NButton size="small" type="primary" @click="submitForward">确定</NButton>
      </div>
    </NModal>
  </div>
</template>

<style scoped>
.gw {
  font-family: -apple-system, BlinkMacSystemFont, 'SF Pro Text', 'PingFang SC',
    'Microsoft YaHei', sans-serif;
  background: #f5f5f7;
  border-radius: 18px;
  padding: 16px;
  color: #1d1d1f;
  min-height: calc(100vh - 120px);
}

.gw-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.gw-head__right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.gw-refresh {
  appearance: none;
  border: 1px solid #e8e8ed;
  background: #fff;
  color: #0071e3;
  font: inherit;
  font-size: 12.5px;
  font-weight: 600;
  padding: 7px 14px;
  border-radius: 10px;
  cursor: pointer;
}
.gw-refresh:disabled {
  opacity: 0.55;
  cursor: default;
}

.gw-stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
  margin-bottom: 12px;
}
.gw-stat,
.gw-card {
  background: #fff;
  border: 1px solid #e8e8ed;
  border-radius: 13px;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.03);
}
.gw-stat {
  padding: 11px 13px 12px;
  min-width: 0;
}
.gw-stat__label {
  font-size: 12px;
  color: #6e6e73;
  font-weight: 500;
}
.gw-stat__value {
  font-size: 20px;
  font-weight: 650;
  letter-spacing: -0.02em;
  margin-top: 5px;
  line-height: 1.25;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.gw-card {
  padding: 14px 16px 16px;
  margin-bottom: 10px;
}
.gw-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.gw-card__title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  letter-spacing: -0.01em;
}

.gw-seg {
  display: inline-flex;
  gap: 2px;
  padding: 3px;
  background: #e9e9ed;
  border-radius: 10px;
}
.gw-seg button {
  appearance: none;
  border: 0;
  background: transparent;
  color: #6e6e73;
  font: inherit;
  font-size: 12.5px;
  padding: 5px 14px;
  border-radius: 7px;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.18s ease, color 0.18s ease, box-shadow 0.18s ease;
}
.gw-seg button:hover {
  color: #1d1d1f;
}
.gw-seg button.is-active {
  background: #fff;
  color: #1d1d1f;
  font-weight: 600;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

.gw-switches {
  display: flex;
  align-items: center;
  gap: 28px;
  flex-wrap: wrap;
  border-top: 1px solid #f0f0f2;
  padding-top: 12px;
}
.gw-switch {
  display: inline-flex;
  align-items: center;
  gap: 9px;
}

.gw-form {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 16px;
}
.gw-field {
  min-width: 0;
}
.gw-field.span-2 {
  grid-column: 1 / -1;
}
.gw-label {
  font-size: 12.5px;
  color: #6e6e73;
  margin-bottom: 5px;
  font-weight: 500;
}

.gw-save {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 12px;
}

.m-foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
}

@media (max-width: 1080px) {
  .gw-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 720px) {
  .gw-form,
  .gw-stats {
    grid-template-columns: 1fr;
  }
}
</style>
