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
  NSelect,
  NSpace,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type { Protocol } from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { prettyJson, toArray } from '../utils/format'

const { isBusy, run } = useApiAction()

const category = ref('')
const categoryOptions = [
  { label: '全部', value: '' },
  { label: 'vpn', value: 'vpn' },
  { label: 'encrypted-proxy', value: 'encrypted-proxy' },
  { label: 'post-quantum', value: 'post-quantum' },
]

const protocols = ref<Protocol[]>([])
const detailId = ref('')
const detail = ref<unknown>(null)
const configTarget = ref('')
const kernel = ref('')
const paramsText = ref('{}')
const configResult = ref<unknown>(null)

async function refreshProtocols() {
  const data = await run('protocols', () =>
    api.listProtocols(category.value || undefined),
  )
  if (data) protocols.value = toArray<Protocol>(data)
}

async function queryDetail(id: string = detailId.value) {
  if (!id.trim()) {
    window.$message?.warning('请填写协议 ID')
    return
  }
  detailId.value = id
  const d = await run('detail', () => api.getProtocol(id.trim()))
  if (d) detail.value = d
}

async function toggle(row: Protocol) {
  const next = !row.enabled
  await run(
    'enable:' + row.id,
    () => api.enableProtocol(row.id, next),
    `协议 ${row.id} 已${next ? '启用' : '停用'}`,
  )
  await refreshProtocols()
}

function selectTarget(id: string) {
  configTarget.value = id
  if (!kernel.value) kernel.value = 'sing-box'
}

async function generateConfig() {
  if (!configTarget.value.trim()) {
    window.$message?.warning('请先选择或填写协议 ID')
    return
  }
  let params: Record<string, unknown>
  try {
    params = JSON.parse(paramsText.value || '{}')
  } catch {
    window.$message?.error('params 必须是合法的 JSON 对象')
    return
  }
  const r = await run('config-gen', () =>
    api.generateProtocolConfig(
      configTarget.value.trim(),
      kernel.value.trim(),
      params,
    ),
  )
  if (r) configResult.value = r
}

const configObj = computed<Record<string, unknown> | null>(() =>
  configResult.value && typeof configResult.value === 'object'
    ? (configResult.value as Record<string, unknown>)
    : null,
)
const configContent = computed(() =>
  configObj.value && typeof configObj.value.content === 'string'
    ? configObj.value.content
    : '',
)

const columns: any[] = [
  {
    title: 'ID',
    key: 'id',
    minWidth: 150,
    render: (r: Protocol) => h('span', { class: 'mono' }, r.id),
  },
  { title: '名称', key: 'name', minWidth: 150 },
  {
    title: '分类',
    key: 'category',
    minWidth: 160,
    render: (r: Protocol) =>
      h(NTag, { size: 'small', bordered: false }, { default: () => r.category }),
  },
  { title: '传输', key: 'transport', minWidth: 130, render: (r: Protocol) => r.transport ?? '—' },
  {
    title: '默认端口',
    key: 'default_port',
    width: 100,
    render: (r: Protocol) => r.default_port ?? '—',
  },
  {
    title: '适用内核',
    key: 'kernels',
    minWidth: 180,
    render: (r: Protocol) =>
      Array.isArray(r.kernels) ? r.kernels.join('、') : '—',
  },
  {
    title: '后量子',
    key: 'pq_capable',
    width: 100,
    render: (r: Protocol) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: r.pq_capable ? 'success' : 'default' },
        { default: () => (r.pq_capable ? '支持' : '否') },
      ),
  },
  {
    title: '启用',
    key: 'enabled',
    width: 80,
    render: (r: Protocol) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: r.enabled ? 'success' : 'default' },
        { default: () => (r.enabled ? '是' : '否') },
      ),
  },
  {
    title: '操作',
    key: 'actions',
    width: 210,
    fixed: 'right',
    render: (r: Protocol) =>
      h('div', { style: 'display: flex; gap: 4px; flex-wrap: wrap' }, [
        h(
          NButton,
          { size: 'tiny', onClick: () => queryDetail(r.id) },
          { default: () => '详情' },
        ),
        h(
          NButton,
          { size: 'tiny', onClick: () => selectTarget(r.id) },
          { default: () => '配置' },
        ),
        h(
          NButton,
          {
            size: 'tiny',
            type: r.enabled ? 'warning' : 'primary',
            loading: isBusy('enable:' + r.id),
            onClick: () => toggle(r),
          },
          { default: () => (r.enabled ? '停用' : '启用') },
        ),
      ]),
  },
]

onMounted(refreshProtocols)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">协议中心</h2>
        <p class="page-subtitle">Stage 4 · 协议注册、启用与内核配置生成</p>
      </div>
      <NSpace align="center" :size="10" :wrap="true">
        <span class="hint">分类</span>
        <NSelect v-model:value="category" :options="categoryOptions" style="width: 200px" />
        <NButton size="small" :loading="isBusy('protocols')" @click="refreshProtocols">
          刷新列表
        </NButton>
      </NSpace>
    </div>

    <NAlert type="info" :bordered="false" class="section-gap">
      <template #header>协议分类</template>
      category 可取 <span class="mono">vpn</span> / <span class="mono">encrypted-proxy</span> /
      <span class="mono">post-quantum</span>；「配置」会把该协议设为下方配置生成的目标。
    </NAlert>

    <NCard size="small" title="协议列表（/protocols）" class="section-gap">
      <NDataTable
        :columns="columns"
        :data="protocols"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="1400"
      />
    </NCard>

    <NCard size="small" title="单条详情（GET /protocols/{id}）" class="section-gap">
      <template #header-extra>
        <NSpace :size="8" :wrap="true" align="center">
          <NInput v-model:value="detailId" size="small" style="width: 220px" placeholder="协议 ID" />
          <NButton size="small" :loading="isBusy('detail')" @click="queryDetail()">查询</NButton>
        </NSpace>
      </template>
      <pre class="mono output-block" style="max-height: 260px">{{ prettyJson(detail) }}</pre>
    </NCard>

    <NCard size="small" title="配置生成（POST /protocols/{id}/config）" class="section-gap">
      <NSpace vertical :size="10" style="width: 100%">
        <NGrid cols="1 m:2" responsive="screen" :x-gap="12" :y-gap="10">
          <NGi>
            <div class="kv-label">目标协议 ID</div>
            <NInput v-model:value="configTarget" placeholder="点击列表中的「配置」自动填入" />
          </NGi>
          <NGi>
            <div class="kv-label">内核 kernel</div>
            <NInput v-model:value="kernel" placeholder="例如 sing-box / mihomo / xray-core" />
          </NGi>
        </NGrid>
        <div>
          <div class="kv-label">params（JSON 对象）</div>
          <NInput v-model:value="paramsText" type="textarea" class="code-editor" :rows="5" />
        </div>
        <NButton size="small" type="primary" :loading="isBusy('config-gen')" @click="generateConfig">
          生成配置
        </NButton>

        <template v-if="configObj">
          <NDescriptions bordered :column="2" size="small" label-placement="left">
            <NDescriptionsItem label="format">{{ configObj.format ?? '—' }}</NDescriptionsItem>
            <NDescriptionsItem label="validated">
              <NTag
                size="small"
                :bordered="false"
                :type="configObj.validated === true ? 'success' : 'error'"
              >
                {{ configObj.validated === true ? '通过' : '未通过' }}
              </NTag>
            </NDescriptionsItem>
          </NDescriptions>
          <div class="hint">content（{{ configObj.format ?? 'text' }}）：</div>
          <pre class="mono output-block" style="max-height: 320px">{{ configContent || prettyJson(configResult) }}</pre>
        </template>
      </NSpace>
    </NCard>
  </div>
</template>
