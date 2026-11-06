<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NDescriptions,
  NDescriptionsItem,
  NGi,
  NGrid,
  NInputNumber,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type {
  NetworkComponent,
  NetworkConfig,
  NetworkEndpoint,
  NetworkSession,
  NetworkStatus,
} from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { makeColumns } from '../composables/tableColumns'
import { prettyJson, toArray } from '../utils/format'

const { isBusy, run } = useApiAction()

const status = ref<NetworkStatus | null>(null)
const sessions = ref<NetworkSession[]>([])
const endpoints = ref<NetworkEndpoint[]>([])
const selftestResult = ref<unknown>(null)
const packettestResult = ref<unknown>(null)
const configResult = ref<unknown>(null)
const selftestCore = ref('')

const coreOptions = [
  { label: '自动选择（core_id 为空）', value: '' },
  { label: 'mihomo', value: 'mihomo' },
  { label: 'xray-core', value: 'xray-core' },
  { label: 'sing-box', value: 'sing-box' },
]

const defaultCoreOptions = ref<{ label: string; value: string }[]>([
  { label: '自动（按规则调度）', value: '' },
])

const POLICY_KEYS = ['fail_closed', 'failover', 'direct_on_failure', 'bypass_tun']
const POLICY_LABELS: Record<string, string> = {
  fail_closed: '故障保护（断连不泄漏）',
  failover: '内核故障自动切换',
  direct_on_failure: '直连兜底',
  bypass_tun: '绕过 TUN',
}

interface ComponentRow {
  key: string
  label: string
  enabled: boolean
  running: boolean
}

function statusFlag(key: string): boolean {
  const s = status.value
  if (!s) return false
  switch (key) {
    case 'fail_closed': return !!s.fail_closed
    case 'failover': return !!s.failover
    case 'direct_on_failure': return !!s.direct_on_failure
    case 'bypass_tun': return !!s.bypass_tun
    default: return false
  }
}

// 后端返回 components 时逐项渲染；缺失时降级为四个顶层策略开关，不伪造组件。
const componentRows = computed<ComponentRow[]>(() => {
  const s = status.value
  const list: NetworkComponent[] = s && Array.isArray(s.components) ? s.components : []
  if (list.length) {
    return list.map((c) => ({
      key: c.key,
      label: POLICY_LABELS[c.key] || c.label || c.key,
      enabled: !!c.enabled,
      running: !!c.running,
    }))
  }
  return POLICY_KEYS.map((key) => ({
    key,
    label: POLICY_LABELS[key],
    enabled: statusFlag(key),
    running: !!s?.running,
  }))
})

async function loadOptions() {
  const data = await run('options', () => api.getProfileOptions())
  if (data) {
    defaultCoreOptions.value = [
      { label: '自动（按规则调度）', value: '' },
      ...(data.kernels ?? []).map((k) => ({ label: k.label, value: k.kernel })),
    ]
  }
}

async function toggleComponent(key: string, enabled: boolean) {
  const r = await run('cmp:' + key, () => api.toggleNetworkComponent({ key, enabled }))
  if (r?.requires_restart) window.$message?.info('需重启网络引擎后生效')
  await refreshStatus()
}

// 表单数字字段允许 null，以匹配 NInputNumber 的 number | null 模型
const form = ref({
  fail_closed: false,
  failover: false,
  default_core: '',
  direct_on_failure: false,
  egress_mark: 0 as number | null,
  bypass_tun: false,
})

async function refreshStatus() {
  const data = await run('status', () => api.getNetworkStatus())
  if (data) status.value = data
}
async function refreshSessions() {
  const data = await run('sessions', () => api.getNetworkSessions())
  if (data) sessions.value = toArray<NetworkSession>(data)
}
async function refreshEndpoints() {
  const data = await run('endpoints', () => api.getNetworkEndpoints())
  if (data) endpoints.value = toArray<NetworkEndpoint>(data)
}
async function refreshConfig() {
  const data = await run('load-config', () => api.getNetworkConfig())
  if (data) {
    form.value = {
      fail_closed: !!data.fail_closed,
      failover: !!data.failover,
      default_core: String(data.default_core ?? ''),
      direct_on_failure: !!data.direct_on_failure,
      egress_mark: Number(data.egress_mark ?? 0),
      bypass_tun: !!data.bypass_tun,
    }
  }
}
function refreshAll() {
  refreshStatus()
  refreshSessions()
  refreshEndpoints()
  refreshConfig()
}

async function doStart() {
  await run('start', () => api.startNetwork(), '网络引擎启动命令已下发')
  await refreshStatus()
}
async function doStop() {
  await run('stop', () => api.stopNetwork(), '网络引擎停止命令已下发')
  await refreshStatus()
}
async function doSelftest() {
  const r = await run('selftest', () => api.networkSelftest(selftestCore.value))
  if (r) selftestResult.value = r
}
async function doPackettest() {
  const r = await run('packettest', () => api.networkPackettest())
  if (r) packettestResult.value = r
}
async function saveConfig() {
  const payload: NetworkConfig = {
    fail_closed: form.value.fail_closed,
    failover: form.value.failover,
    default_core: form.value.default_core,
    direct_on_failure: form.value.direct_on_failure,
    egress_mark: form.value.egress_mark ?? 0,
    bypass_tun: form.value.bypass_tun,
  }
  const r = await run('save-config', () => api.saveNetworkConfig(payload))
  if (!r) return
  configResult.value = r
  const parts: string[] = []
  if (r.changed !== undefined) {
    parts.push(
      Array.isArray(r.changed)
        ? `变更字段：${r.changed.length ? r.changed.join('、') : '无'}`
        : `变更：${prettyJson(r.changed)}`,
    )
  }
  if (r.requires_restart === true) parts.push('需要重启网络引擎后生效')
  window.$message?.success(
    parts.length ? `配置已保存 · ${parts.join(' · ')}` : '配置已保存',
  )
  await refreshConfig()
}

const sessionColumns = computed(() => makeColumns(sessions.value))
const endpointColumns = computed(() => makeColumns(endpoints.value))

// cores store 收到 network.* / session.rejected 事件时会派发该事件，触发静默刷新
function onNetworkEvent() {
  refreshStatus()
  refreshSessions()
  refreshEndpoints()
}

onMounted(() => {
  refreshAll()
  loadOptions()
  window.addEventListener('ngg-network-event', onNetworkEvent)
})
onUnmounted(() => {
  window.removeEventListener('ngg-network-event', onNetworkEvent)
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">高级诊断</h2>
        <p class="page-subtitle">统一网络引擎的运行状态、会话表、自检与原始配置（接管操作在「控制中心」）</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="isBusy('status')" @click="refreshAll">
          刷新全部
        </NButton>
      </NSpace>
    </div>

    
    <NAlert type="info" :bordered="false" class="section-gap">
      <strong>接管全局已并入「控制中心」</strong>：虚拟网卡创建、全量流量接管、WAN 出口选择都在
      网关中心 → 控制中心 里按顺序完成。本页保留运行状态、会话与自检等诊断能力。</NAlert>

    <NCard size="small" title="运行状态（/network/status）" class="section-gap">
      <template #header-extra>
        <NSpace :size="8" :wrap="true">
          <NButton size="small" type="primary" :loading="isBusy('start')" @click="doStart">
            启动
          </NButton>
          <NButton size="small" :loading="isBusy('stop')" @click="doStop">停止</NButton>
          <NButton size="small" :loading="isBusy('status')" @click="refreshStatus">
            刷新状态
          </NButton>
        </NSpace>
      </template>

      <NDescriptions bordered :column="4" size="small" label-placement="left">
        <NDescriptionsItem label="运行状态">
          <NTag size="small" :bordered="false" :type="status?.running ? 'success' : 'default'">
            {{ status?.running ? '运行中' : '未运行' }}
          </NTag>
        </NDescriptionsItem>
        <NDescriptionsItem label="能力集">Stage {{ status?.stage }}</NDescriptionsItem>
        <NDescriptionsItem label="故障保护（断连不泄漏）">
          <NTag size="small" :bordered="false" :type="status?.fail_closed ? 'success' : 'default'">
            {{ status?.fail_closed ? '开启' : '关闭' }}
          </NTag>
        </NDescriptionsItem>
        <NDescriptionsItem label="内核故障自动切换">
          <NTag size="small" :bordered="false" :type="status?.failover ? 'success' : 'default'">
            {{ status?.failover ? '开启' : '关闭' }}
          </NTag>
        </NDescriptionsItem>
      </NDescriptions>

      <NGrid cols="1 s:2 m:3" responsive="screen" :x-gap="12" :y-gap="12" style="margin-top: 14px">
        <NGi v-for="row in componentRows" :key="row.key">
          <div class="kv-label">{{ row.label }}</div>
          <NSpace align="center" :size="8" style="margin-top: 6px">
            <NSwitch
              :value="row.enabled"
              :loading="isBusy('cmp:' + row.key)"
              @update:value="(v) => toggleComponent(row.key, !!v)"
            />
            <NTag
              size="small"
              :bordered="false"
              :type="row.running ? 'success' : row.enabled ? 'info' : 'default'"
            >
              {{ row.running ? 'running' : row.enabled ? 'enabled' : 'stopped' }}
            </NTag>
          </NSpace>
        </NGi>
      </NGrid>

      <NGrid cols="1 s:2" responsive="screen" :x-gap="12" :y-gap="12" style="margin-top: 14px">
        <NGi>
          <NCard size="small" title="Ingress 入口" :bordered="true">
            <div class="hint">启用：{{ status?.ingress?.enabled ? '是' : '否' }}</div>
            <div class="hint mono">地址：{{ status?.ingress?.address ?? '—' }}</div>
            <div class="hint">协议：{{ status?.ingress?.protocols?.join('、') || '—' }}</div>
          </NCard>
        </NGi>
        <NGi>
          <NCard size="small" title="TUN 设备" :bordered="true">
            <div class="hint">
              已配置：{{ status?.tun?.configured ? '是' : '否' }} ·
              激活：{{ status?.tun?.active ? '是' : '否' }}
            </div>
            <div class="hint mono">
              名称：{{ status?.tun?.name ?? '—' }} · MTU：{{ status?.tun?.mtu ?? '—' }}
            </div>
            <pre class="mono output-block" style="margin-top: 8px; max-height: 120px">{{ prettyJson(status?.tun?.stats) }}</pre>
          </NCard>
        </NGi>
      </NGrid>

      <div class="hint" style="margin-top: 14px">
        完整状态响应（dns / rules / sessions / endpoints / healthy / resources / metrics / plugins 等全字段）：
      </div>
      <pre class="mono output-block" style="max-height: 320px">{{ prettyJson(status) }}</pre>
    </NCard>

    <NCard size="small" title="自检" class="section-gap">
      <NSpace vertical :size="12" style="width: 100%">
        <NSpace align="center" :size="10" :wrap="true">
          <span class="hint">端到端自检内核：</span>
          <NSelect v-model:value="selftestCore" :options="coreOptions" style="width: 230px" />
          <NButton size="small" type="primary" :loading="isBusy('selftest')" @click="doSelftest">
            端到端自检
          </NButton>
          <NButton size="small" :loading="isBusy('packettest')" @click="doPackettest">
            报文分类自检
          </NButton>
        </NSpace>
        <div v-if="selftestResult">
          <div class="hint">端到端自检结果（path / latency_ms / ok / echo）：</div>
          <pre class="mono output-block">{{ prettyJson(selftestResult) }}</pre>
        </div>
        <div v-if="packettestResult">
          <div class="hint">报文分类自检结果：</div>
          <pre class="mono output-block">{{ prettyJson(packettestResult) }}</pre>
        </div>
      </NSpace>
    </NCard>

    <NCard size="small" title="会话表（/network/sessions）" class="section-gap">
      <template #header-extra>
        <NButton size="small" :loading="isBusy('sessions')" @click="refreshSessions">刷新</NButton>
      </template>
      <NDataTable
        :columns="sessionColumns"
        :data="sessions"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="900"
      />
    </NCard>

    <NCard size="small" title="健康端点表（/network/endpoints）" class="section-gap">
      <template #header-extra>
        <NButton size="small" :loading="isBusy('endpoints')" @click="refreshEndpoints">刷新</NButton>
      </template>
      <NDataTable
        :columns="endpointColumns"
        :data="endpoints"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="900"
      />
    </NCard>

    <NCard size="small" title="引擎配置（/network/config）" class="section-gap">
      <template #header-extra>
        <NSpace :size="8">
          <NButton size="small" :loading="isBusy('load-config')" @click="refreshConfig">
            重新加载
          </NButton>
          <NButton size="small" type="primary" :loading="isBusy('save-config')" @click="saveConfig">
            保存
          </NButton>
        </NSpace>
      </template>
      <NGrid cols="1 s:2 m:3" responsive="screen" :x-gap="14" :y-gap="12">
        <NGi>
          <div class="kv-label">失败关闭 fail_closed</div>
          <NSwitch v-model:value="form.fail_closed" />
        </NGi>
        <NGi>
          <div class="kv-label">故障转移 failover</div>
          <NSwitch v-model:value="form.failover" />
        </NGi>
        <NGi>
          <div class="kv-label">直连兜底 direct_on_failure</div>
          <NSwitch v-model:value="form.direct_on_failure" />
        </NGi>
        <NGi>
          <div class="kv-label">绕过 TUN bypass_tun</div>
          <NSwitch v-model:value="form.bypass_tun" />
        </NGi>
        <NGi>
          <div class="kv-label">默认内核 default_core</div>
          <NSelect v-model:value="form.default_core" :options="defaultCoreOptions" />
        </NGi>
        <NGi>
          <div class="kv-label">出口标记 egress_mark</div>
          <NInputNumber v-model:value="form.egress_mark" style="width: 100%" :min="0" />
        </NGi>
      </NGrid>
      <div v-if="configResult" style="margin-top: 14px">
        <div class="hint">保存返回（changed / requires_restart）：</div>
        <pre class="mono output-block">{{ prettyJson(configResult) }}</pre>
      </div>
    </NCard>
  </div>
</template>
