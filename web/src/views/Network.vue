<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
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
const globalStatus = ref<api.GlobalTakeoverResult | null>(null)
const tunStatus = ref<api.TunStatusResult | null>(null)
const tunRouteAll = ref(false)
const driverMsg = ref('')
const driverError = ref('')
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
async function loadGlobal() {
  const d = await run('global', () => api.getGlobalStatus())
  if (d) globalStatus.value = d
}
async function refreshTun() {
  try {
    tunStatus.value = await api.getTunStatus()
  } catch {
    // 网络引擎未初始化时保持上一次已知状态，不伪造
  }
}

async function installDriver() {
  driverMsg.value = ''
  driverError.value = ''
  const r = await run('tun-driver', () => api.installTunDriver())
  if (r) {
    driverMsg.value = r.driver?.message || '驱动已就绪'
    await refreshTun()
    await loadGlobal()
  }
}

async function attachTunAction() {
  driverError.value = ''
  const r = await run(
    'tun-attach',
    () => api.attachTun(tunRouteAll.value),
    tunRouteAll.value ? 'TUN 网卡已创建并接管默认路由' : 'TUN 网卡已创建（未改动默认路由）',
  )
  if (r) {
    await refreshTun()
    await loadGlobal()
  }
}

async function detachTunAction() {
  const r = await run('tun-detach', () => api.detachTun(), '已销毁 TUN 网卡并恢复路由')
  if (r) {
    await refreshTun()
    await loadGlobal()
  }
}

function tunBytes(v?: number): string {
  if (v === undefined || v === null) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let n = Number(v)
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i += 1
  }
  return `${i === 0 ? Math.round(n) : n.toFixed(2)} ${units[i]}`
}

async function attachGlobal() {
  const r = await run('attach', () => api.attachGlobalProxy(), '系统代理已接管，本机流量将走 NGG 入口')
  if (r) await loadGlobal()
}
async function detachGlobal() {
  const r = await run('detach', () => api.detachGlobalProxy(), '已解除接管并恢复原有代理设置')
  if (r) await loadGlobal()
}

function refreshAll() {
  loadGlobal()
  refreshTun()
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
        <h2 class="page-title">接管全局</h2>
        <p class="page-subtitle">接管本机全局流量 · 系统代理一键接管与 TUN 真实能力检测</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="isBusy('status')" @click="refreshAll">
          刷新全部
        </NButton>
      </NSpace>
    </div>

    
    <NCard size="small" title="接管全局" class="section-gap">
      <NGrid cols="1 s:2" responsive="screen" :x-gap="14" :y-gap="14">
        <NGi>
          <div class="proto-sec-title">系统代理（用户态 · 立即生效）</div>
          <div class="hint" style="margin-top: 6px">
            接管后本机 WinINET 应用（浏览器等）的流量会走 NGG 入口；解除时恢复原有代理设置，
            全程不需要驱动或管理员权限。
          </div>
          <NSpace align="center" :size="10" :wrap="true" style="margin-top: 10px">
            <NTag :type="globalStatus?.proxy?.attached ? 'success' : 'default'" :bordered="false" size="small">
              {{ globalStatus?.proxy?.attached ? '已接管' : '未接管' }}
            </NTag>
            <span class="hint mono">{{ globalStatus?.proxy?.server || '—' }}</span>
            <NButton
              size="small"
              type="primary"
              :disabled="!globalStatus?.proxy?.supported || globalStatus?.proxy?.attached"
              :loading="isBusy('attach')"
              @click="attachGlobal"
            >一键接管</NButton>
            <NButton
              size="small"
              :disabled="!globalStatus?.proxy?.attached"
              :loading="isBusy('detach')"
              @click="detachGlobal"
            >解除接管</NButton>
          </NSpace>
          <div v-if="globalStatus?.proxy?.error" class="hint" style="margin-top: 6px">
            {{ globalStatus.proxy.error }}
          </div>
        </NGi>
        <NGi>
          <div class="proto-sec-title">TUN 虚拟网卡（NGG 独占）</div>
          <div class="hint" style="margin-top: 6px">
            由 NGG 自己创建并驱动 wintun 网卡，TCP 流量经用户态协议栈进入统一调度器，
            与 SOCKS5 入口共用同一套规则与出口选择。
          </div>

          <div class="tun-grid">
            <div class="tun-cell">
              <span class="tun-label">平台 / 管理员</span>
              <span class="tun-value">
                {{ tunStatus?.capabilities?.os || globalStatus?.tun?.os || '—' }} ·
                {{ (tunStatus?.capabilities?.admin ?? globalStatus?.tun?.admin) ? '已提权' : '标准用户' }}
              </span>
            </div>
            <div class="tun-cell">
              <span class="tun-label">wintun 驱动</span>
              <span class="tun-value">
                <NTag
                  size="tiny"
                  :bordered="false"
                  :type="(tunStatus?.capabilities?.driver?.present ?? !!globalStatus?.tun?.wintun_dll) ? 'success' : 'warning'"
                >
                  {{ (tunStatus?.capabilities?.driver?.present ?? !!globalStatus?.tun?.wintun_dll) ? '已就位' : '未安装' }}
                </NTag>
                <span class="mono" style="margin-left: 6px">
                  {{ tunStatus?.capabilities?.driver?.version ? 'v' + tunStatus.capabilities.driver.version : '' }}
                </span>
                <NTag
                  v-if="tunStatus?.capabilities?.driver?.present"
                  size="tiny"
                  :bordered="false"
                  :type="tunStatus.capabilities.driver.signed ? 'success' : 'error'"
                  style="margin-left: 6px"
                >
                  {{ tunStatus.capabilities.driver.signed ? '签名有效' : '签名无效' }}
                </NTag>
              </span>
            </div>
            <div class="tun-cell">
              <span class="tun-label">网卡状态</span>
              <span class="tun-value">
                <NTag
                  size="tiny"
                  :bordered="false"
                  :type="(tunStatus?.adapter?.open ?? globalStatus?.tun?.adapter?.open) ? 'success' : 'default'"
                >
                  {{ (tunStatus?.adapter?.open ?? globalStatus?.tun?.adapter?.open) ? '已创建' : '未创建' }}
                </NTag>
                <span v-if="tunStatus?.adapter?.open" class="mono" style="margin-left: 6px">
                  {{ tunStatus.adapter.adapter }} · {{ tunStatus.adapter.ipv4 }}
                </span>
              </span>
            </div>
            <div class="tun-cell">
              <span class="tun-label">默认路由</span>
              <span class="tun-value">
                {{ (tunStatus?.adapter?.route_all ?? globalStatus?.tun?.adapter?.route_all) ? '已接管 0.0.0.0/0' : '未接管（仅网卡内路由）' }}
              </span>
            </div>
          </div>

          <NSpace align="center" :size="8" :wrap="true" style="margin-top: 10px">
            <NButton
              size="small"
              :type="tunStatus?.capabilities?.driver?.present ? 'default' : 'primary'"
              :loading="isBusy('tun-driver')"
              :disabled="isBusy('tun-attach')"
              @click="installDriver"
            >
              {{ tunStatus?.capabilities?.driver?.present ? '重新校验驱动' : '下载并安装驱动' }}
            </NButton>
            <NButton
              size="small"
              type="primary"
              :loading="isBusy('tun-attach')"
              :disabled="isBusy('tun-driver')"
              @click="attachTunAction"
            >
              创建网卡
            </NButton>
            <NButton
              size="small"
              :loading="isBusy('tun-detach')"
              :disabled="!(tunStatus?.adapter?.open ?? globalStatus?.tun?.adapter?.open)"
              @click="detachTunAction"
            >
              销毁网卡
            </NButton>
            <span class="tun-switch">
              <NSwitch v-model:value="tunRouteAll" size="small" />
              <span>同时接管默认路由</span>
            </span>
          </NSpace>

          <div v-if="driverMsg" class="hint" style="margin-top: 8px; color: #1f7a3a">{{ driverMsg }}</div>
          <div v-if="driverError" class="hint" style="margin-top: 8px; color: #b32442">{{ driverError }}</div>
          <div
            v-if="tunStatus?.capabilities?.reason || globalStatus?.tun?.reason"
            class="hint"
            style="margin-top: 8px; color: #b26a00"
          >
            {{ tunStatus?.capabilities?.reason || globalStatus?.tun?.reason }}
          </div>

          <div v-if="tunStatus?.adapter?.open" class="tun-stats">
            <div><span>协议栈</span><strong>{{ tunStatus.adapter.stack }}</strong></div>
            <div><span>MTU</span><strong>{{ tunStatus.adapter.mtu }}</strong></div>
            <div><span>活动会话</span><strong>{{ tunStatus.adapter.stats?.sessions ?? 0 }}</strong></div>
            <div><span>下行 / 上行</span><strong>{{ tunBytes(tunStatus.adapter.stats?.bytes_in) }} / {{ tunBytes(tunStatus.adapter.stats?.bytes_out) }}</strong></div>
            <div><span>收 / 发 包</span><strong>{{ tunStatus.adapter.stats?.packets_in ?? 0 }} / {{ tunStatus.adapter.stats?.packets_out ?? 0 }}</strong></div>
            <div><span>创建时间</span><strong>{{ tunStatus.adapter.since ? new Date(tunStatus.adapter.since).toLocaleString('zh-CN') : '—' }}</strong></div>
          </div>

          <div v-if="tunStatus?.capabilities?.virtual_adapters?.length || globalStatus?.tun?.virtual_adapters?.length" class="hint" style="margin-top: 8px">
            本机其它虚拟网卡（仅供参考，不会被 NGG 接管）：
            {{ (tunStatus?.capabilities?.virtual_adapters || globalStatus?.tun?.virtual_adapters || []).join('、') }}
          </div>
        </NGi>
      </NGrid>
    </NCard>

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
