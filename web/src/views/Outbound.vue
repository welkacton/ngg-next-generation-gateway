<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
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
  EgressNodeConfig,
  NetworkConfigPatchResult,
  NetworkEndpoint,
  SdwanLink,
  SdwanStatus,
} from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { prettyJson, toArray } from '../utils/format'

const { isBusy, run } = useApiAction()

const endpoints = ref<NetworkEndpoint[]>([])
const sdwan = ref<SdwanStatus | null>(null)
const links = ref<SdwanLink[]>([])
const result = ref<NetworkConfigPatchResult | null>(null)
const actionResult = ref<unknown>(null)

const egress = ref<EgressNodeConfig>({
  interface: '', local_addr: '', mark: 20039, bypass_tun: true,
})
const policy = ref({
  fail_closed: true,
  failover: true,
  default_core: '',
  direct_on_failure: false,
})
const linkForm = ref<SdwanLink>({
  id: '', name: '', interface: '', gateway: '', probe_target: '',
  weight: 1, cost: 1, priority: 0, enabled: true,
})
const sdwanPolicy = ref('lowest-latency')
const policyOptions = [
  { label: '最低延迟 lowest-latency', value: 'lowest-latency' },
  { label: '加权 weighted', value: 'weighted' },
  { label: '优先级 priority', value: 'priority' },
  { label: '主备 failover', value: 'failover' },
]

function fillForms(cfg: unknown) {
  if (!cfg || typeof cfg !== 'object') return
  const c = cfg as Record<string, unknown>
  const e = c.egress as Partial<EgressNodeConfig> | undefined
  if (e) {
    egress.value = {
      interface: e.interface ?? '',
      local_addr: e.local_addr ?? '',
      mark: e.mark ?? 20039,
      bypass_tun: e.bypass_tun ?? true,
    }
  }
  policy.value = {
    fail_closed: c.fail_closed === undefined ? true : Boolean(c.fail_closed),
    failover: c.failover === undefined ? true : Boolean(c.failover),
    default_core: String(c.default_core ?? ''),
    direct_on_failure: Boolean(c.direct_on_failure ?? false),
  }
}

async function refreshAll() {
  const ep = await run('endpoints', () => api.getNetworkEndpoints())
  if (ep) endpoints.value = toArray<NetworkEndpoint>(ep)
  const cfg = await run('config', () => api.getNetworkConfig())
  if (cfg) fillForms(cfg)
  const st = await run('sdwan-status', () => api.getSdwanStatus())
  if (st) {
    sdwan.value = st
    sdwanPolicy.value = st.policy || sdwanPolicy.value
  }
  const ls = await run('links', () => api.listSdwanLinks())
  if (ls) links.value = toArray<SdwanLink>(ls)
}

async function saveEgress() {
  const r = await run('save-egress', () =>
    api.patchNetworkConfig({ egress: { ...egress.value } }), '出口配置已保存')
  if (r) result.value = r
  await refreshAll()
}

async function savePolicy() {
  const r = await run('save-policy', () => api.patchNetworkConfig({ ...policy.value }), '调度策略已保存')
  if (r) result.value = r
  await refreshAll()
}

async function addLink() {
  if (!linkForm.value.id.trim() || !linkForm.value.probe_target.trim()) {
    window.$message?.warning('请填写链路 ID 与探测目标')
    return
  }
  const r = await run('add-link', () => api.addSdwanLink({ ...linkForm.value }), '链路已新增')
  if (r) actionResult.value = r
  await refreshAll()
}

function confirmDelete(id: string) {
  window.$dialog?.warning({
    title: '删除链路',
    content: `确定删除链路 ${id} 吗？`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const r = await run('del:' + id, () => api.deleteSdwanLink(id), '链路已删除')
      if (r) actionResult.value = r
      await refreshAll()
    },
  })
}

async function sdwanAction(action: 'start' | 'stop' | 'failover') {
  const fns = {
    start: api.startSdwan,
    stop: api.stopSdwan,
    failover: api.failoverSdwan,
  }
  const r = await run('sdwan-' + action, fns[action], `SD-WAN ${action} 已执行`)
  if (r) actionResult.value = r
  await refreshAll()
}

async function applySdwanPolicy() {
  const r = await run('sdwan-policy', () => api.setSdwanPolicy(sdwanPolicy.value), '选路策略已更新')
  if (r) actionResult.value = r
  await refreshAll()
}

const endpointColumns: any[] = [
  { title: '内核', key: 'core_id', minWidth: 150 },
  { title: '本地端点地址', key: 'address', minWidth: 200 },
  { title: '接入类型', key: 'kind', minWidth: 130 },
  {
    title: '健康', key: 'healthy', minWidth: 100,
    render: (r: NetworkEndpoint) => (r.healthy ? '健康' : '不可达'),
  },
]
const linkColumns: any[] = [
  { title: 'ID', key: 'id', minWidth: 120 },
  { title: '名称', key: 'name', minWidth: 140 },
  { title: '探测目标', key: 'probe_target', minWidth: 190 },
  { title: '健康', key: 'healthy', minWidth: 90 },
  { title: 'RTT (ms)', key: 'rtt_ms', minWidth: 110 },
  { title: '探测次数', key: 'probes', minWidth: 110 },
  {
    title: '操作', key: 'actions', width: 100, fixed: 'right',
    render: (r: SdwanLink) => String(r.id ?? ''),
  },
]

onMounted(refreshAll)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">出口节点</h2>
        <p class="page-subtitle">配置引擎 · 内核出口端点、物理出口绑定与 SD-WAN 链路调度</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="isBusy('endpoints')" @click="refreshAll">刷新</NButton>
      </NSpace>
    </div>

    <NAlert type="info" :bordered="false" class="section-gap">
      <template #header>NGG 统一出口调度</template>
      每条连接的执行路径由 NGG 独立决定：PROXY 经 Core Bridge 连到内核的隔离本地端点；
      DIRECT 由 NGG 直接拨号并绑定指定物理出口。Linux 下用 SO_MARK 让内核远程传输绕过 NGG TUN，
      防止回环。Fail-Closed 默认开启，内核不可用时丢弃连接而不是意外直连。
    </NAlert>

    <NCard size="small" title="内核出口端点（GET /network/endpoints · 真实拨号探测）" class="section-gap">
      <NDataTable
        :columns="endpointColumns"
        :data="endpoints"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="760"
      />
    </NCard>

    <NGrid cols="1 m:2" responsive="screen" :x-gap="14" :y-gap="14" class="section-gap">
      <NGi>
        <NCard size="small" title="出口绑定（PUT /network/config · egress）">
          <template #header-extra>
            <NButton size="small" type="primary" :loading="isBusy('save-egress')" @click="saveEgress">
              保存
            </NButton>
          </template>
          <NSpace vertical :size="10" style="width: 100%">
            <NGrid cols="2" :x-gap="10" :y-gap="8">
              <NGi>
                <div class="kv-label">物理接口 interface</div>
                <NInput v-model:value="egress.interface" placeholder="eth0（留空 = 自动）" />
              </NGi>
              <NGi>
                <div class="kv-label">绑定源地址 local_addr</div>
                <NInput v-model:value="egress.local_addr" placeholder="192.168.1.10" />
              </NGi>
              <NGi>
                <div class="kv-label">SO_MARK（Linux）</div>
                <NInputNumber v-model:value="egress.mark" style="width: 100%" />
              </NGi>
              <NGi>
                <div class="kv-label">绕过 NGG TUN</div>
                <NSwitch v-model:value="egress.bypass_tun" />
              </NGi>
            </NGrid>
            <div class="hint">修改出口绑定后需重启网络引擎，新的拨号器才会带上标记与源地址。</div>
          </NSpace>
        </NCard>
      </NGi>

      <NGi>
        <NCard size="small" title="调度策略（PUT /network/config）">
          <template #header-extra>
            <NButton size="small" type="primary" :loading="isBusy('save-policy')" @click="savePolicy">
              保存
            </NButton>
          </template>
          <NSpace vertical :size="10" style="width: 100%">
            <NGrid cols="2" :x-gap="10" :y-gap="8">
              <NGi>
                <div class="kv-label">Fail-Closed（禁止意外 DIRECT 泄漏）</div>
                <NSwitch v-model:value="policy.fail_closed" />
              </NGi>
              <NGi>
                <div class="kv-label">内核故障自动切换 Failover</div>
                <NSwitch v-model:value="policy.failover" />
              </NGi>
              <NGi>
                <div class="kv-label">默认内核 default_core</div>
                <NInput v-model:value="policy.default_core" placeholder="留空 = 按规则决定" />
              </NGi>
              <NGi>
                <div class="kv-label">全部内核失败时允许直连</div>
                <NSwitch v-model:value="policy.direct_on_failure" />
              </NGi>
            </NGrid>
            <NAlert type="warning" :bordered="false" :show-icon="false">
              仅在显式关闭 Fail-Closed 且开启「允许直连」时，故障才会回落到 DIRECT。默认两者均关闭。
            </NAlert>
          </NSpace>
        </NCard>
      </NGi>
    </NGrid>

    <NCard size="small" title="SD-WAN 链路" class="section-gap">
      <template #header-extra>
        <NSpace :size="8" :wrap="true" align="center">
          <NTag size="small" :bordered="false" :type="sdwan?.running ? 'success' : 'default'">
            {{ sdwan?.running ? '探测运行中' : '探测未启动' }}
          </NTag>
          <NTag size="small" :bordered="false" type="info">
            当前主链路 {{ sdwan?.active || '未选定' }}
          </NTag>
        </NSpace>
      </template>
      <NSpace :size="8" :wrap="true" style="margin-bottom: 12px">
        <NButton size="small" :loading="isBusy('sdwan-start')" @click="sdwanAction('start')">启动探测</NButton>
        <NButton size="small" :loading="isBusy('sdwan-stop')" @click="sdwanAction('stop')">停止探测</NButton>
        <NButton size="small" :loading="isBusy('sdwan-failover')" @click="sdwanAction('failover')">强制切换</NButton>
        <NSelect v-model:value="sdwanPolicy" :options="policyOptions" style="width: 210px" />
        <NButton size="small" :loading="isBusy('sdwan-policy')" @click="applySdwanPolicy">应用策略</NButton>
      </NSpace>
      <NDataTable
        :columns="linkColumns"
        :data="links"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="900"
      />
      <div v-if="!links.length" class="hint" style="margin-top: 8px">尚未配置链路。</div>

      <NGrid cols="2 m:4" responsive="screen" :x-gap="10" :y-gap="8" style="margin-top: 14px">
        <NGi><div class="kv-label">链路 ID</div><NInput v-model:value="linkForm.id" placeholder="wan1" /></NGi>
        <NGi><div class="kv-label">名称</div><NInput v-model:value="linkForm.name" placeholder="主线路" /></NGi>
        <NGi><div class="kv-label">探测目标 host:port</div><NInput v-model:value="linkForm.probe_target" placeholder="223.5.5.5:53" /></NGi>
        <NGi><div class="kv-label">物理接口</div><NInput v-model:value="linkForm.interface" placeholder="eth0" /></NGi>
        <NGi><div class="kv-label">网关</div><NInput v-model:value="linkForm.gateway" placeholder="192.168.1.1" /></NGi>
        <NGi><div class="kv-label">权重</div><NInputNumber v-model:value="linkForm.weight" style="width: 100%" /></NGi>
        <NGi><div class="kv-label">开销</div><NInputNumber v-model:value="linkForm.cost" style="width: 100%" /></NGi>
        <NGi><div class="kv-label">启用</div><NSwitch v-model:value="linkForm.enabled" /></NGi>
      </NGrid>
      <NSpace style="margin-top: 10px">
        <NButton size="small" type="primary" :loading="isBusy('add-link')" @click="addLink">新增链路</NButton>
      </NSpace>

      <div v-if="links.length" style="margin-top: 10px">
        <div class="kv-label">删除链路</div>
        <NSpace :size="6" :wrap="true">
          <NButton
            v-for="l in links"
            :key="l.id"
            size="tiny"
            type="error"
            :loading="isBusy('del:' + l.id)"
            @click="confirmDelete(l.id)"
          >
            删除 {{ l.id }}
          </NButton>
        </NSpace>
      </div>
      <pre class="mono output-block" style="max-height: 180px; margin-top: 10px">{{ prettyJson(sdwan) }}</pre>
    </NCard>

    <NCard size="small" title="操作返回（如实展示）" class="section-gap">
      <div v-if="result">
        <NSpace :size="6" :wrap="true">
          <NTag size="small" :bordered="false" type="success">
            已变更 {{ (result.changed || []).join(', ') || '无' }}
          </NTag>
          <NTag v-if="(result.requires_restart || []).length" size="small" :bordered="false" type="warning">
            需重启：{{ (result.requires_restart || []).join(', ') }}
          </NTag>
          <NTag size="small" :bordered="false" :type="result.persisted ? 'info' : 'default'">
            {{ result.persisted ? '已写入配置文件' : '仅内存生效' }}
          </NTag>
        </NSpace>
      </div>
      <pre class="mono output-block" style="max-height: 200px; margin-top: 10px">{{ prettyJson(actionResult ?? result) }}</pre>
    </NCard>
  </div>
</template>
