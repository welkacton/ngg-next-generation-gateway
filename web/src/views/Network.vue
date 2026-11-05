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
  NInput,
  NInputNumber,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type {
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

    <NAlert type="info" :bordered="false" class="section-gap">
      <template #header>能力边界说明</template>
      「接管全局」提供两条真实路径：<strong>系统代理接管</strong>（Windows 用户态，立即生效，无需驱动与提权）与
      <strong>TUN 虚拟网卡</strong>（需要 wintun 驱动 + 管理员权限）。后端会如实上报本机能力与原因，
      无法创建时不会伪造网卡。页面所有数据均来自真实后端接口。
    </NAlert>

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
          <div class="proto-sec-title">TUN 虚拟网卡</div>
          <div class="hint" style="margin-top: 6px">
            创建虚拟网卡需要已签名的 wintun / TAP 驱动与管理员权限。NGG 只上报真实检测结果，
            不会伪造一个不存在的网卡。
          </div>
          <div class="hint" style="margin-top: 8px">
            平台 {{ globalStatus?.tun?.os || '—' }} ·
            管理员 {{ globalStatus?.tun?.admin ? '是' : '否' }} ·
            支持 TUN {{ globalStatus?.tun?.tun_supported ? '是' : '否' }}
          </div>
          <div class="hint">wintun.dll：{{ globalStatus?.tun?.wintun_dll || '未找到' }}</div>
          <div v-if="globalStatus?.tun?.reason" class="hint" style="color: #b32442">
            {{ globalStatus.tun.reason }}
          </div>
          <div v-if="globalStatus?.tun?.virtual_adapters?.length" class="hint">
            已检测到虚拟网卡：{{ globalStatus.tun.virtual_adapters.join('、') }}
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
        <NDescriptionsItem label="阶段">{{ status?.stage ?? '—' }}</NDescriptionsItem>
        <NDescriptionsItem label="失败关闭 fail_closed">
          <NTag size="small" :bordered="false" :type="status?.fail_closed ? 'warning' : 'default'">
            {{ status?.fail_closed ? '开启' : '关闭' }}
          </NTag>
        </NDescriptionsItem>
        <NDescriptionsItem label="故障转移 failover">
          <NTag size="small" :bordered="false" :type="status?.failover ? 'success' : 'default'">
            {{ status?.failover ? '开启' : '关闭' }}
          </NTag>
        </NDescriptionsItem>
      </NDescriptions>

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
          <NInput v-model:value="form.default_core" placeholder="例如 mihomo" />
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
