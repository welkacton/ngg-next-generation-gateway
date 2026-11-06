<script setup lang="ts">
import { computed, h, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton,
  NCollapse,
  NCollapseItem,
  NDataTable,
  NInput,
  NInputNumber,
  NSelect,
  NSwitch,
  NTag,
  type DataTableColumns,
} from 'naive-ui'
import * as api from '../../api/ngg'
import type {
  GatewayMaster,
  GatewaySplitPolicy,
  GatewayUplinkPolicy,
  GatewayWAN,
  DatapathStatus,
} from '../../api/ngg'
import { useApiAction } from '../../composables/useApi'

const { isBusy, run } = useApiAction()
const router = useRouter()

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

const SPLIT_MODES: Array<{ id: GatewaySplitPolicy['mode']; label: string }> = [
  { id: 'smart', label: '智能分流' },
  { id: 'global-proxy', label: '全局代理' },
  { id: 'direct', label: '全局直连' },
]
const SPLIT_DEFAULTS: Array<{ id: GatewaySplitPolicy['default']; label: string }> = [
  { id: 'local', label: '本地直出' },
  { id: 'proxy', label: '国际代理' },
]
const UPLINK_MODES: Array<{ id: GatewayUplinkPolicy['mode']; label: string }> = [
  { id: 'gateway-wan', label: '走网关 WAN' },
  { id: 'pipeline', label: '走链路编排' },
]

interface Draft {
  enabled: boolean
  capture_all: boolean
  auto_start: boolean
  bypass_private: boolean
  adapter_name: string
  adapter_metric: number | null
  bypassText: string
  splitMode: GatewaySplitPolicy['mode']
  splitDefault: GatewaySplitPolicy['default']
  proxyDomainsText: string
  localDomainsText: string
  proxyCidrsText: string
  localCidrsText: string
  uplinkMode: GatewayUplinkPolicy['mode']
  wanId: string
  pipelineId: string
}

const draft = reactive<Draft>({
  enabled: false,
  capture_all: false,
  auto_start: false,
  bypass_private: true,
  adapter_name: 'NGG',
  adapter_metric: 5,
  bypassText: '',
  splitMode: 'smart',
  splitDefault: 'local',
  proxyDomainsText: '',
  localDomainsText: '',
  proxyCidrsText: '',
  localCidrsText: '',
  uplinkMode: 'gateway-wan',
  wanId: '',
  pipelineId: '',
})

const master = ref<GatewayMaster | null>(null)
const status = ref<DatapathStatus | null>(null)
const wans = ref<GatewayWAN[]>([])
const loading = ref(false)
const pendingCommands = ref<string[]>([])
const ruleCount = ref<number | null>(null)

const stats = computed(() => {
  const st = status.value
  return [
    {
      label: '数据面',
      value: !st ? '—' : st.capture_active ? '已接管' : '未接管',
      note: st ? (st.elevated ? '已提权' : '未提权') : '',
    },
    {
      label: '虚拟网卡',
      value: st?.adapter_name || '—',
      note: st ? (st.adapter_present ? '已创建' : '未创建') : '',
    },
    {
      label: '接口优先级',
      value: st && st.adapter_metric >= 0 ? String(st.adapter_metric) : '—',
      note: '',
    },
    {
      label: '默认路由',
      value: !st ? '—' : st.default_route_via_adapter ? '经虚拟网卡' : '系统默认',
      note: '',
    },
  ]
})

const wanOptions = computed(() =>
  wans.value
    .filter((w) => w.enabled)
    .map((w) => ({
      label: `${w.name || w.id.toUpperCase()} · ${w.route === 'proxy' ? '国际代理' : '本地直出'}`,
      value: w.id,
    })),
)

const wanColumns: DataTableColumns<GatewayWAN> = [
  {
    title: '编号',
    key: 'id',
    width: 76,
    render: (row) => h('span', { class: 'mono' }, row.id.toUpperCase()),
  },
  {
    title: '启用',
    key: 'enabled',
    width: 78,
    render: (row) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: row.enabled ? 'success' : 'default' },
        { default: () => (row.enabled ? '已启用' : '未启用') },
      ),
  },
  {
    title: '出口路由',
    key: 'route',
    width: 96,
    render: (row) => (row.route === 'proxy' ? '国际代理' : '本地直出'),
  },
  {
    title: '绑定链路',
    key: 'pipeline',
    minWidth: 150,
    render: (row) => {
      if (row.route !== 'proxy') return h('span', { class: 'dim' }, '—')
      const id = (row.pipeline_id ?? '').trim()
      if (!id) return h('span', { class: 'dim' }, '默认链路')
      return h('span', { class: 'mono' }, id)
    },
  },
]

function lines(text: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of text.split(/\r?\n/)) {
    const value = raw.trim().toLowerCase()
    if (!value || seen.has(value)) continue
    seen.add(value)
    out.push(value)
  }
  return out
}

function splitList(text: string): string[] {
  return text
    .split(/[,，\s]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

function isDomain(value: string): boolean {
  let text = value.trim().toLowerCase()
  if (text.startsWith('*.')) text = text.slice(2)
  text = text.replace(/\.$/, '')
  if (!text || text.length > 253) return false
  return text.split('.').every((label) => {
    if (!label || label.length > 63) return false
    if (label.startsWith('-') || label.endsWith('-')) return false
    return /^[a-z0-9_-]+$/.test(label)
  })
}

function isCIDR(value: string): boolean {
  const parts = value.split('/')
  if (parts.length !== 2) return false
  if (!IPV4.test(parts[0])) return false
  if (!/^\d{1,2}$/.test(parts[1])) return false
  const mask = Number(parts[1])
  return mask >= 0 && mask <= 32
}

function syncFrom(m: GatewayMaster): void {
  draft.enabled = m.enabled === true
  draft.capture_all = m.capture_all === true
  draft.auto_start = m.auto_start === true
  draft.bypass_private = m.bypass_private === true
  draft.adapter_name = m.adapter_name || 'NGG'
  draft.adapter_metric = typeof m.adapter_metric === 'number' ? m.adapter_metric : 5
  draft.bypassText = Array.isArray(m.bypass_cidrs) ? m.bypass_cidrs.join('\n') : ''
  const split = m.split ?? ({} as GatewaySplitPolicy)
  draft.splitMode = split.mode === 'global-proxy' || split.mode === 'direct' ? split.mode : 'smart'
  draft.splitDefault = split.default === 'proxy' ? 'proxy' : 'local'
  draft.proxyDomainsText = Array.isArray(split.proxy_domains) ? split.proxy_domains.join('\n') : ''
  draft.localDomainsText = Array.isArray(split.local_domains) ? split.local_domains.join('\n') : ''
  draft.proxyCidrsText = Array.isArray(split.proxy_cidrs) ? split.proxy_cidrs.join('\n') : ''
  draft.localCidrsText = Array.isArray(split.local_cidrs) ? split.local_cidrs.join('\n') : ''
  const uplink = m.uplink ?? ({} as GatewayUplinkPolicy)
  draft.uplinkMode = uplink.mode === 'pipeline' ? 'pipeline' : 'gateway-wan'
  draft.wanId = uplink.wan_id ?? ''
  draft.pipelineId = uplink.pipeline_id ?? ''
}

function validateLists(): string | null {
  for (const domain of lines(draft.proxyDomainsText)) {
    if (!isDomain(domain)) return `代理域名无效：${domain}`
  }
  for (const domain of lines(draft.localDomainsText)) {
    if (!isDomain(domain)) return `本地域名无效：${domain}`
  }
  for (const cidr of lines(draft.proxyCidrsText)) {
    if (!isCIDR(cidr)) return `代理网段无效：${cidr}`
  }
  for (const cidr of lines(draft.localCidrsText)) {
    if (!isCIDR(cidr)) return `本地网段无效：${cidr}`
  }
  return null
}

function validate(): string | null {
  const name = draft.adapter_name.trim()
  if (!name) return '虚拟网卡名称不能为空'
  if (name.length > 32) return '虚拟网卡名称最多 32 个字符'
  const metric = draft.adapter_metric
  if (metric === null || Number.isNaN(metric)) return '接口优先级不能为空'
  if (metric < 0 || metric > 9999) return '接口优先级需在 0–9999'
  for (const cidr of splitList(draft.bypassText)) {
    if (!isCIDR(cidr)) return `例外网段无效：${cidr}`
  }
  const listErr = validateLists()
  if (listErr) return listErr
  const pipeline = draft.pipelineId.trim()
  if (pipeline && !pipeline.startsWith('pl-')) return '链路编排 ID 需以 pl- 开头'
  if (draft.uplinkMode === 'gateway-wan' && draft.wanId && !wanOptions.value.some((o) => o.value === draft.wanId))
    return '所选 WAN 口未启用'
  return null
}

function buildMaster(base: GatewayMaster): GatewayMaster {
  return {
    ...base,
    enabled: draft.enabled,
    capture_all: draft.capture_all,
    auto_start: draft.auto_start,
    bypass_private: draft.bypass_private,
    adapter_name: draft.adapter_name.trim(),
    adapter_metric: Number(draft.adapter_metric),
    bypass_cidrs: lines(draft.bypassText),
    split: {
      mode: draft.splitMode,
      default: draft.splitDefault,
      proxy_domains: lines(draft.proxyDomainsText),
      local_domains: lines(draft.localDomainsText),
      proxy_cidrs: lines(draft.proxyCidrsText),
      local_cidrs: lines(draft.localCidrsText),
    },
    uplink: {
      mode: draft.uplinkMode,
      wan_id: draft.wanId,
      pipeline_id: draft.pipelineId.trim(),
    },
  }
}

function confirmRun(title: string, content: string, positiveText: string): Promise<boolean> {
  return new Promise((resolve) => {
    let settled = false
    const finish = (value: boolean): void => {
      if (settled) return
      settled = true
      resolve(value)
    }
    window.$dialog?.warning({
      title,
      content,
      positiveText,
      negativeText: '取消',
      onPositiveClick: () => finish(true),
      onNegativeClick: () => finish(false),
      onClose: () => finish(false),
      onMaskClick: () => finish(false),
    })
  })
}

async function refreshStatus(): Promise<void> {
  const st = await api.getDatapathStatus().catch(() => null)
  if (st) status.value = st
}

async function datapath(kind: 'apply' | 'revert'): Promise<void> {
  const ok = await confirmRun(
    kind === 'apply' ? '应用接管' : '恢复原状',
    kind === 'apply'
      ? '接管后会修改系统默认路由，全部流量交由虚拟网卡处理。确认继续？'
      : '将删除虚拟网卡相关路由并恢复原默认路由。确认继续？',
    kind === 'apply' ? '接管' : '恢复',
  )
  if (!ok) return

  const res = await run(kind, () => (kind === 'apply' ? api.applyDatapath() : api.revertDatapath()))
  if (!res) return

  status.value = res.status
  pendingCommands.value = Array.isArray(res.commands) ? res.commands : []

  if (!res.executed) {
    window.$message?.warning(res.reason || '命令未执行', { duration: 8000 })
    return
  }
  if (res.applied) {
    pendingCommands.value = []
    window.$message?.success(kind === 'apply' ? '已接管' : '已恢复原状')
    await refreshStatus()
    return
  }
  window.$message?.warning(res.reason || '未生效', { duration: 8000 })
}

async function writeRules(): Promise<void> {
  const err = validateLists()
  if (err) {
    window.$message?.warning(err)
    return
  }
  const res = await run('rules', () => api.applySplitRules())
  if (!res) return
  ruleCount.value = Array.isArray(res.rules) ? res.rules.length : 0
  window.$message?.success(`已写入 ${res.added} 条，跳过 ${res.skipped} 条`)
  window.$message?.info('规则需内核重载后生效', { duration: 6000 })
}

function openPipeline(): void {
  void router.push('/pipeline')
}

function wanRowKey(row: GatewayWAN): string {
  return row.id
}

async function copyCommands(): Promise<void> {
  const text = pendingCommands.value.join('\n')
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    window.$message?.success('已复制到剪贴板')
  } catch {
    window.$message?.warning('浏览器拒绝了剪贴板访问，请手动全选复制')
  }
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const [m, cfg] = await Promise.allSettled([api.getGatewayMaster(), api.getGatewayConfig()])
    if (m.status === 'fulfilled') {
      master.value = m.value.master
      status.value = m.value.status
      syncFrom(m.value.master)
    }
    if (cfg.status === 'fulfilled') {
      wans.value = Array.isArray(cfg.value.config.wans) ? cfg.value.config.wans : []
    }
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  const err = validate()
  if (err) {
    window.$message?.warning(err)
    return
  }
  const latest = await api.getGatewayMaster().catch(() => null)
  const base = latest?.master ?? master.value
  if (!base) {
    window.$message?.warning('未获取到总控配置')
    return
  }
  const saved = await run('save', () => api.saveGatewayMaster(buildMaster(base)), '已保存')
  if (saved) {
    master.value = saved.master
    syncFrom(saved.master)
    if (saved.warnings?.length) {
      window.$message?.info(saved.warnings.slice(0, 2).join('；'), { duration: 6000 })
    }
  }
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="gw">
    <header class="gw-head">
      <h2 class="gw-title">网关总控</h2>
      <div class="gw-head__ops">
        <NButton size="tiny" quaternary type="primary" :loading="loading" @click="load">刷新</NButton>
      </div>
    </header>

    <section class="gw-stats">
      <div v-for="item in stats" :key="item.label" class="gw-stat">
        <div class="gw-stat__label">{{ item.label }}</div>
        <div class="gw-stat__value" :title="item.value">{{ item.value }}</div>
        <div v-if="item.note" class="gw-stat__note">{{ item.note }}</div>
      </div>
      <div class="gw-stats__ops">
        <NButton size="small" type="primary" :loading="isBusy('apply')" @click="datapath('apply')">
          应用接管
        </NButton>
        <NButton size="small" :loading="isBusy('revert')" @click="datapath('revert')">恢复原状</NButton>
      </div>
    </section>

    <section v-if="pendingCommands.length" class="gw-card">
      <NCollapse :default-expanded-names="['cmd']">
        <NCollapseItem title="命令未执行" name="cmd">
          <template #header-extra>
            <NButton size="tiny" quaternary type="primary" @click.stop="copyCommands">复制</NButton>
          </template>
          <pre class="cmd">{{ pendingCommands.join('\n') }}</pre>
        </NCollapseItem>
      </NCollapse>
    </section>

    <section class="gw-card">
      <div class="gw-card__head">
        <h3 class="gw-card__title">总控开关</h3>
        <NTag
          v-if="status"
          size="small"
          :bordered="false"
          :type="status.capture_active ? 'success' : 'default'"
        >
          {{ status.capture_active ? '已接管' : '未接管' }}
        </NTag>
      </div>
      <div class="grid">
        <div class="field">
          <div class="label">启用总控</div>
          <div class="switch-row"><NSwitch v-model:value="draft.enabled" size="small" /></div>
        </div>
        <div class="field">
          <div class="label">接管全部流量</div>
          <div class="switch-row"><NSwitch v-model:value="draft.capture_all" size="small" /></div>
        </div>
        <div class="field">
          <div class="label">开机自动接管</div>
          <div class="switch-row"><NSwitch v-model:value="draft.auto_start" size="small" /></div>
        </div>
        <div class="field">
          <div class="label">局域网与保留地址直连</div>
          <div class="switch-row"><NSwitch v-model:value="draft.bypass_private" size="small" /></div>
        </div>
        <div class="field">
          <div class="label">虚拟网卡</div>
          <NInput v-model:value="draft.adapter_name" size="small" :maxlength="32" placeholder="NGG" />
        </div>
        <div class="field">
          <div class="label">接口优先级</div>
          <NInputNumber v-model:value="draft.adapter_metric" size="small" :min="0" :max="9999" />
        </div>
        <div class="field span-2">
          <div class="label">例外网段</div>
          <NInput
            v-model:value="draft.bypassText"
            size="small"
            placeholder="10.0.0.0/8, 172.16.0.0/12"
          />
        </div>
      </div>
    </section>

    <section class="gw-card">
      <div class="gw-card__head">
        <h3 class="gw-card__title">智能分流</h3>
      </div>
      <div class="policy">
        <div class="inline">
          <span class="label">分流模式</span>
          <div class="seg" role="group" aria-label="分流模式">
            <button
              v-for="m in SPLIT_MODES"
              :key="m.id"
              type="button"
              :class="{ 'is-active': draft.splitMode === m.id }"
              @click="draft.splitMode = m.id"
            >{{ m.label }}</button>
          </div>
        </div>
        <div class="inline">
          <span class="label">默认策略</span>
          <div class="seg" role="group" aria-label="默认策略">
            <button
              v-for="d in SPLIT_DEFAULTS"
              :key="d.id"
              type="button"
              :class="{ 'is-active': draft.splitDefault === d.id }"
              @click="draft.splitDefault = d.id"
            >{{ d.label }}</button>
          </div>
        </div>
      </div>
      <div class="grid grid--2">
        <div class="field">
          <div class="label">代理域名</div>
          <NInput v-model:value="draft.proxyDomainsText" type="textarea" :rows="3" size="small" />
        </div>
        <div class="field">
          <div class="label">本地域名</div>
          <NInput v-model:value="draft.localDomainsText" type="textarea" :rows="3" size="small" />
        </div>
        <div class="field">
          <div class="label">代理网段</div>
          <NInput v-model:value="draft.proxyCidrsText" type="textarea" :rows="3" size="small" />
        </div>
        <div class="field">
          <div class="label">本地网段</div>
          <NInput v-model:value="draft.localCidrsText" type="textarea" :rows="3" size="small" />
        </div>
      </div>
      <div class="gw-card__foot">
        <NButton size="tiny" type="primary" :loading="isBusy('rules')" @click="writeRules">
          写入规则引擎
        </NButton>
        <span v-if="ruleCount !== null" class="gw-meta">已写入 {{ ruleCount }} 条</span>
      </div>
    </section>

    <section class="gw-card">
      <div class="gw-card__head">
        <h3 class="gw-card__title">出口绑定</h3>
      </div>
      <div class="policy">
        <div class="inline">
          <span class="label">上行模式</span>
          <div class="seg" role="group" aria-label="上行模式">
            <button
              v-for="m in UPLINK_MODES"
              :key="m.id"
              type="button"
              :class="{ 'is-active': draft.uplinkMode === m.id }"
              @click="draft.uplinkMode = m.id"
            >{{ m.label }}</button>
          </div>
        </div>
        <div v-if="draft.uplinkMode === 'gateway-wan'" class="inline">
          <span class="label">WAN 口</span>
          <NSelect
            v-model:value="draft.wanId"
            size="small"
            :options="wanOptions"
            placeholder="选择 WAN 口"
            style="width: 220px"
          />
        </div>
        <template v-else>
          <div class="inline">
            <span class="label">链路编排</span>
            <NInput
              v-model:value="draft.pipelineId"
              size="small"
              placeholder="pl-..."
              style="width: 200px"
            />
          </div>
          <NButton size="tiny" quaternary type="primary" @click="openPipeline">打开链路编排</NButton>
        </template>
      </div>
      <NDataTable
        class="table"
        size="small"
        :columns="wanColumns"
        :data="wans"
        :row-key="wanRowKey"
        :bordered="false"
        :pagination="false"
        :scroll-x="520"
      />
    </section>

    <div class="gw-foot">
      <NButton size="small" type="primary" :loading="isBusy('save')" @click="save">保存</NButton>
    </div>
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
  grid-template-columns: repeat(4, minmax(0, 1fr)) auto;
  gap: 8px;
  margin-bottom: 8px;
  align-items: stretch;
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
.gw-stat__note {
  font-size: 10px;
  color: #8e8e93;
  line-height: 1.3;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.gw-stats__ops {
  display: flex;
  align-items: center;
  gap: 6px;
  padding-left: 2px;
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
.gw-card__foot {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
}

.policy {
  display: flex;
  align-items: center;
  gap: 20px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.inline {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.label {
  font-size: 10.5px;
  color: #6e6e73;
  font-weight: 500;
  margin-bottom: 4px;
}
.inline .label {
  margin-bottom: 0;
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

.table {
  margin-top: 2px;
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
.table :deep(.mono) {
  font-family: 'SF Mono Nerd Font', 'PingFang SC', 'Microsoft YaHei', monospace;
  font-variant-numeric: tabular-nums;
}
.table :deep(.dim) {
  color: #8e8e93;
}

.cmd {
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

.gw-card :deep(.n-collapse-item__header) {
  min-height: 26px;
  padding: 0;
  font-size: 12px;
  font-weight: 600;
}
.gw-card :deep(.n-collapse-item__content-inner) {
  padding-top: 6px;
}

.gw-foot {
  display: flex;
  justify-content: flex-end;
  margin-top: 8px;
}

@media (max-width: 1180px) {
  .gw-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .gw-stats__ops {
    grid-column: 1 / -1;
  }
}
@media (max-width: 900px) {
  .grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .grid--2 {
    grid-template-columns: 1fr;
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
