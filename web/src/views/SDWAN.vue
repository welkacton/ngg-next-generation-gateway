<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
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
import type { SdwanLink, SdwanStatus } from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { makeColumns } from '../composables/tableColumns'
import { prettyJson, toArray } from '../utils/format'

const { isBusy, run } = useApiAction()

const policyOptions = [
  { label: 'lowest-latency（最低时延）', value: 'lowest-latency' },
  { label: 'weighted（加权）', value: 'weighted' },
  { label: 'priority（优先级）', value: 'priority' },
  { label: 'failover（主备）', value: 'failover' },
]

const status = ref<SdwanStatus | null>(null)
const links = ref<SdwanLink[]>([])
const policy = ref('lowest-latency')
const actionResult = ref<unknown>(null)

const linkForm = ref({
  id: '',
  name: '',
  interface: '',
  gateway: '',
  probe_target: '',
  weight: 1 as number | null,
  cost: 0 as number | null,
  priority: 0 as number | null,
  enabled: true,
})

async function refreshStatus() {
  const d = await run('status', () => api.getSdwanStatus())
  if (d) status.value = d
}
async function refreshLinks() {
  const d = await run('links', () => api.listSdwanLinks())
  if (d) links.value = toArray<SdwanLink>(d)
}
function refreshAll() {
  refreshStatus()
  refreshLinks()
}

async function doStart() {
  const r = await run('start', () => api.startSdwan(), 'SD-WAN 启动命令已下发')
  if (r) actionResult.value = r
  await refreshStatus()
}
async function doStop() {
  const r = await run('stop', () => api.stopSdwan(), 'SD-WAN 停止命令已下发')
  if (r) actionResult.value = r
  await refreshStatus()
}
async function doFailover() {
  const r = await run('failover', () => api.failoverSdwan(), '已触发链路故障转移')
  if (r) actionResult.value = r
  await refreshStatus()
}
async function applyPolicy() {
  const r = await run('policy', () => api.setSdwanPolicy(policy.value), '调度策略已应用')
  if (r) actionResult.value = r
  await refreshStatus()
}

function buildLink(): SdwanLink {
  return {
    id: linkForm.value.id.trim(),
    name: linkForm.value.name.trim(),
    interface: linkForm.value.interface.trim(),
    gateway: linkForm.value.gateway.trim(),
    probe_target: linkForm.value.probe_target.trim(),
    weight: linkForm.value.weight ?? 0,
    cost: linkForm.value.cost ?? 0,
    priority: linkForm.value.priority ?? 0,
    enabled: linkForm.value.enabled,
  }
}

async function addLink() {
  if (!linkForm.value.id.trim()) {
    window.$message?.warning('请填写链路 ID')
    return
  }
  const r = await run('add-link', () => api.addSdwanLink(buildLink()), '链路已新增')
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

const healthLinks = computed(() => toArray<SdwanLink>(status.value?.links ?? []))
const healthColumns = computed(() => makeColumns(healthLinks.value))

const linkColumns: any[] = [
  {
    title: 'ID',
    key: 'id',
    minWidth: 140,
    render: (r: SdwanLink) => h('span', { class: 'mono' }, r.id),
  },
  { title: '名称', key: 'name', minWidth: 130 },
  { title: '接口', key: 'interface', minWidth: 130, render: (r: SdwanLink) => h('span', { class: 'mono' }, r.interface ?? '—') },
  { title: '网关', key: 'gateway', minWidth: 140, render: (r: SdwanLink) => h('span', { class: 'mono' }, r.gateway ?? '—') },
  { title: '探测目标', key: 'probe_target', minWidth: 150, render: (r: SdwanLink) => h('span', { class: 'mono' }, r.probe_target ?? '—') },
  { title: '权重', key: 'weight', width: 80, render: (r: SdwanLink) => r.weight ?? 0 },
  { title: '开销', key: 'cost', width: 80, render: (r: SdwanLink) => r.cost ?? 0 },
  { title: '优先级', key: 'priority', width: 90, render: (r: SdwanLink) => r.priority ?? 0 },
  {
    title: '启用',
    key: 'enabled',
    width: 80,
    render: (r: SdwanLink) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: r.enabled ? 'success' : 'default' },
        { default: () => (r.enabled ? '是' : '否') },
      ),
  },
  {
    title: '操作',
    key: 'actions',
    width: 90,
    fixed: 'right',
    render: (r: SdwanLink) =>
      h(
        NButton,
        {
          size: 'tiny',
          type: 'error',
          loading: isBusy('del:' + r.id),
          onClick: () => confirmDelete(r.id),
        },
        { default: () => '删除' },
      ),
  },
]

onMounted(refreshAll)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">SD-WAN 链路调度</h2>
        <p class="page-subtitle">Stage 5 · 链路健康、调度策略与故障转移</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="isBusy('status')" @click="refreshAll">刷新全部</NButton>
      </NSpace>
    </div>

    
    <NCard size="small" title="运行状态（/sdwan/status）" class="section-gap">
      <template #header-extra>
        <NSpace :size="8" :wrap="true">
          <NButton size="small" type="primary" :loading="isBusy('start')" @click="doStart">启动</NButton>
          <NButton size="small" :loading="isBusy('stop')" @click="doStop">停止</NButton>
          <NButton size="small" type="warning" :loading="isBusy('failover')" @click="doFailover">
            故障转移
          </NButton>
        </NSpace>
      </template>

      <NDescriptions bordered :column="4" size="small" label-placement="left">
        <NDescriptionsItem label="当前策略 policy">
          <span class="mono">{{ status?.policy ?? '—' }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="活动链路 active">
          <span class="mono">{{ status?.active ?? '—' }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="运行中 running">
          <NTag size="small" :bordered="false" :type="status?.running ? 'success' : 'default'">
            {{ status?.running ? '是' : '否' }}
          </NTag>
        </NDescriptionsItem>
        <NDescriptionsItem label="切换次数 switches">
          {{ status?.switches ?? '—' }}
        </NDescriptionsItem>
      </NDescriptions>

      <NSpace :size="8" :wrap="true" align="center" style="margin-top: 14px">
        <span class="hint">设置策略</span>
        <NSelect v-model:value="policy" :options="policyOptions" style="width: 230px" />
        <NButton size="small" :loading="isBusy('policy')" @click="applyPolicy">应用策略</NButton>
      </NSpace>

      <div class="hint" style="margin-top: 14px">完整状态响应：</div>
      <pre class="mono output-block" style="max-height: 240px">{{ prettyJson(status) }}</pre>
    </NCard>

    <NCard size="small" title="链路健康（/sdwan/status.links）" class="section-gap">
      <template #header-extra>
        <NButton size="small" :loading="isBusy('status')" @click="refreshStatus">刷新</NButton>
      </template>
      <NDataTable
        :columns="healthColumns"
        :data="healthLinks"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="1100"
      />
    </NCard>

    <NCard size="small" title="链路管理（/sdwan/links）" class="section-gap">
      <template #header-extra>
        <NButton size="small" :loading="isBusy('links')" @click="refreshLinks">刷新</NButton>
      </template>
      <NDataTable
        :columns="linkColumns"
        :data="links"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="1250"
      />
    </NCard>

    <NCard size="small" title="新增链路（POST /sdwan/links）" class="section-gap">
      <NSpace vertical :size="10" style="width: 100%">
        <NGrid cols="1 s:2 m:3" responsive="screen" :x-gap="12" :y-gap="10">
          <NGi>
            <div class="kv-label">ID（必填）</div>
            <NInput v-model:value="linkForm.id" placeholder="link-1" />
          </NGi>
          <NGi>
            <div class="kv-label">名称</div>
            <NInput v-model:value="linkForm.name" placeholder="主线路" />
          </NGi>
          <NGi>
            <div class="kv-label">接口 interface</div>
            <NInput v-model:value="linkForm.interface" placeholder="eth0" />
          </NGi>
          <NGi>
            <div class="kv-label">网关 gateway</div>
            <NInput v-model:value="linkForm.gateway" placeholder="192.168.1.1" />
          </NGi>
          <NGi>
            <div class="kv-label">探测目标 probe_target</div>
            <NInput v-model:value="linkForm.probe_target" placeholder="1.1.1.1" />
          </NGi>
          <NGi>
            <div class="kv-label">启用</div>
            <NSwitch v-model:value="linkForm.enabled" />
          </NGi>
          <NGi>
            <div class="kv-label">权重 weight</div>
            <NInputNumber v-model:value="linkForm.weight" style="width: 100%" :min="0" />
          </NGi>
          <NGi>
            <div class="kv-label">开销 cost</div>
            <NInputNumber v-model:value="linkForm.cost" style="width: 100%" :min="0" />
          </NGi>
          <NGi>
            <div class="kv-label">优先级 priority</div>
            <NInputNumber v-model:value="linkForm.priority" style="width: 100%" :min="0" />
          </NGi>
        </NGrid>
        <NButton size="small" type="primary" :loading="isBusy('add-link')" @click="addLink">
          新增链路
        </NButton>
        <pre v-if="actionResult" class="mono output-block" style="max-height: 180px">{{ prettyJson(actionResult) }}</pre>
      </NSpace>
    </NCard>
  </div>
</template>
