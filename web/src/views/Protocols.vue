<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NGi,
  NGrid,
  NInput,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type { Protocol, ProtocolMeta } from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { prettyJson, toArray } from '../utils/format'

const { isBusy, run } = useApiAction()

const GROUP_LABEL: Record<string, string> = {
  encrypted: '加密协议',
  plaintext: '普通未加密协议',
  'plain-proxy': '普通代理协议',
}
const GROUP_CLASS: Record<string, string> = {
  encrypted: 'proto-tag-encrypted',
  plaintext: 'proto-tag-plaintext',
  'plain-proxy': 'proto-tag-plain-proxy',
}
const CATEGORY_LABEL: Record<string, string> = {
  vpn: 'VPN',
  'encrypted-proxy': '加密代理',
  'post-quantum': '后量子',
  plaintext: '明文协议',
  'plain-proxy': '普通代理',
}
const KIND_LABEL: Record<string, string> = {
  outbound: '内核 outbound',
  policy: '规则动作',
  listener: '监听入口',
  helper: '外部 helper',
}

const KERNEL_OPTIONS = [
  { label: 'sing-box', value: 'sing-box' },
  { label: 'mihomo / Clash.Meta', value: 'mihomo' },
  { label: 'xray-core', value: 'xray-core' },
  { label: 'ngg-native（NGG 原生）', value: 'ngg-native' },
  { label: 'external-helper（外部程序）', value: 'external-helper' },
]
const FORMAT_OPTIONS = [
  { label: 'JSON', value: 'json' },
  { label: 'YAML', value: 'yaml' },
  { label: 'CONF / 文本', value: 'conf' },
]

/* ------------------------------------------------------------------ *
 * 目录与筛选
 * ------------------------------------------------------------------ */

const meta = ref<ProtocolMeta | null>(null)
const allProtocols = ref<Protocol[]>([])
const group = ref('')
const category = ref('')
const search = ref('')
const enabledFilter = ref('')
const checkedIds = ref<string[]>([])

const categoryOptions = computed(() => [
  { label: '全部分类', value: '' },
  ...Object.entries(CATEGORY_LABEL).map(([value, label]) => ({ label, value })),
])
const enabledOptions = [
  { label: '全部状态', value: '' },
  { label: '仅已启用', value: 'true' },
  { label: '仅已停用', value: 'false' },
]

const groupCounts = computed(() => {
  const c: Record<string, number> = {
    '': allProtocols.value.length,
    encrypted: 0,
    plaintext: 0,
    'plain-proxy': 0,
    enabled: 0,
    custom: 0,
  }
  for (const p of allProtocols.value) {
    if (c[p.group] !== undefined) c[p.group] += 1
    if (p.enabled) c.enabled += 1
    if (p.custom) c.custom += 1
  }
  return c
})

const tabs = computed(() => [
  { value: '', label: '全部' },
  ...Object.entries(GROUP_LABEL).map(([value, label]) => ({ value, label })),
])

const visibleProtocols = computed(() => {
  const q = search.value.trim().toLowerCase()
  return allProtocols.value.filter((p) => {
    if (group.value && p.group !== group.value) return false
    if (category.value && p.category !== category.value) return false
    if (enabledFilter.value === 'true' && !p.enabled) return false
    if (enabledFilter.value === 'false' && p.enabled) return false
    if (q) {
      const hay = [p.id, p.name, p.description, p.security, ...(p.kernels ?? [])]
        .join(' ')
        .toLowerCase()
      if (!hay.includes(q)) return false
    }
    return true
  })
})

async function refreshProtocols() {
  const data = await run('protocols', () => api.listProtocols())
  if (data) {
    allProtocols.value = toArray<Protocol>(data)
    // Drop selections that no longer exist (e.g. after deleting a custom one).
    const known = new Set(allProtocols.value.map((p) => p.id))
    checkedIds.value = checkedIds.value.filter((id) => known.has(id))
  }
}

function rowKey(r: Protocol) {
  return r.id
}

/* ------------------------------------------------------------------ *
 * 单条 / 多选 / 全部启停
 * ------------------------------------------------------------------ */

async function toggleOne(row: Protocol) {
  const next = !row.enabled
  const r = await run('enable:' + row.id, () => api.enableProtocol(row.id, next))
  if (r) {
    window.$message?.success(`协议 ${row.id} 已${next ? '启用' : '停用'}`)
    await refreshProtocols()
  }
}

async function applyToggle(ids: string[], all: boolean, enabled: boolean) {
  const key = `toggle:${all ? 'all' : 'selected'}:${enabled ? 'on' : 'off'}`
  const r = await run(key, () => api.toggleProtocols({ ids, all, enabled }))
  if (r) {
    window.$message?.success(`已${enabled ? '启用' : '停用'} ${r.changed} 条协议`)
    await refreshProtocols()
  }
}

/* ------------------------------------------------------------------ *
 * 配置生成 / 自定义编辑 / JSON 编辑
 * ------------------------------------------------------------------ */

const configTarget = ref('')
const kernel = ref('sing-box')
const paramsText = ref('{}')
const configContent = ref('')
const configFormat = ref('json')
const pristineContent = ref('')
const validated = ref<boolean | null>(null)
const configNote = ref('')
const customized = ref(false)

function parseObject(text: string, label: string): Record<string, unknown> | null {
  try {
    const v = JSON.parse(text || '{}')
    if (v === null || typeof v !== 'object' || Array.isArray(v)) {
      window.$message?.error(`${label} 必须是 JSON 对象`)
      return null
    }
    return v as Record<string, unknown>
  } catch (e) {
    window.$message?.error(`${label} 不是合法 JSON：${(e as Error).message}`)
    return null
  }
}

async function generate(regenerate = false) {
  const id = configTarget.value.trim()
  if (!id) {
    window.$message?.warning('请先选择或填写协议 ID')
    return
  }
  const params = parseObject(paramsText.value, 'params')
  if (!params) return
  const r = await run('config-gen', () =>
    api.generateProtocolConfig(id, kernel.value.trim(), params, regenerate),
  )
  if (!r) return
  configContent.value = r.content
  pristineContent.value = r.content
  configFormat.value = r.format || 'json'
  validated.value = r.validated === true
  customized.value = r.customized === true
  configNote.value = customized.value
    ? '当前内容来自该协议已保存的自定义配置；点「重新生成模板」可回到内置模板。'
    : `已按 ${kernel.value} 生成 ${r.format} 模板，可直接编辑。`
  await refreshProtocols()
}

function selectTarget(row: Protocol) {
  configTarget.value = row.id
  if (row.kernels?.length) kernel.value = row.kernels[0]
  paramsText.value = JSON.stringify(
    row.params && Object.keys(row.params).length
      ? row.params
      : { server: '127.0.0.1', port: row.default_port || 443 },
    null,
    2,
  )
  if (!row.enabled) {
    configNote.value = `协议 ${row.id} 当前已停用，请先启用再生成配置。`
  }
  void generate(false)
}

function formatParams() {
  const v = parseObject(paramsText.value, 'params')
  if (v) paramsText.value = JSON.stringify(v, null, 2)
}

function formatContent() {
  if (configFormat.value === 'json') {
    try {
      configContent.value = JSON.stringify(JSON.parse(configContent.value), null, 2)
      configNote.value = 'JSON 已格式化。'
    } catch (e) {
      configNote.value = `JSON 格式化失败：${(e as Error).message}`
    }
  } else {
    configNote.value = `${configFormat.value.toUpperCase()} 保留原样；自动格式化仅支持 JSON。`
  }
}

async function validateContent() {
  const r = await run('config-validate', () =>
    api.validateProtocolConfig(configContent.value, configFormat.value),
  )
  if (!r) return
  validated.value = r.valid
  configNote.value = r.valid
    ? `校验通过：${configFormat.value.toUpperCase()} 语法正确，可保存。`
    : `校验未通过：${r.error ?? '语法错误'}`
}

async function saveContent() {
  const id = configTarget.value.trim()
  if (!id) {
    window.$message?.warning('请先选择协议 ID')
    return
  }
  if (!configContent.value.trim()) {
    window.$message?.warning('配置内容为空')
    return
  }
  const check = await run('config-validate', () =>
    api.validateProtocolConfig(configContent.value, configFormat.value),
  )
  if (!check) return
  if (!check.valid) {
    validated.value = false
    configNote.value = `未保存：${check.error ?? '语法错误'}`
    window.$message?.error('配置语法不合法，未保存')
    return
  }
  const r = await run('config-save', () =>
    api.saveProtocolConfig(id, configContent.value, configFormat.value),
  )
  if (!r) return
  validated.value = true
  customized.value = true
  pristineContent.value = configContent.value
  configNote.value = `已保存为 ${id} 的自定义配置；此后生成配置会返回这份内容。`
  window.$message?.success('自定义配置已保存')
  await refreshProtocols()
}

async function clearSaved() {
  const id = configTarget.value.trim()
  if (!id) return
  const r = await run('config-clear', () => api.clearProtocolConfig(id))
  if (!r) return
  customized.value = false
  configNote.value = '已清除自定义配置，恢复使用内置模板。'
  await refreshProtocols()
  await generate(true)
}

function restoreTemplate() {
  if (!pristineContent.value) return
  configContent.value = pristineContent.value
  configNote.value = '已还原为本次生成的内容（未保存的编辑已撤销）。'
  validated.value = null
}

async function copyContent() {
  try {
    await navigator.clipboard.writeText(configContent.value)
    window.$message?.success('配置内容已复制')
  } catch {
    window.$message?.warning('浏览器拒绝了剪贴板访问，请手动选中复制')
  }
}

function downloadContent() {
  const ext = configFormat.value === 'yaml' ? 'yaml' : configFormat.value === 'conf' ? 'conf' : 'json'
  const blob = new Blob([configContent.value], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${configTarget.value || 'ngg-protocol'}.${ext}`
  a.click()
  URL.revokeObjectURL(url)
}

/* ------------------------------------------------------------------ *
 * 自定义协议（原始 JSON 编辑）
 * ------------------------------------------------------------------ */

const specText = ref('')
const specEditingId = ref('')
const specNote = ref('')

const SPEC_TEMPLATE: Partial<Protocol> = {
  id: 'my-custom-protocol',
  name: '我的自定义协议',
  category: 'plain-proxy',
  group: 'plain-proxy',
  security: '自定义 / 由上游决定',
  transport: 'TCP',
  config_kind: 'outbound',
  default_port: 9000,
  kernels: ['mihomo'],
  features: ['custom'],
  description: '在 JSON 编辑器中按需修改字段后保存',
  enabled: true,
  params: { server: '127.0.0.1', port: 9000 },
}

function loadTemplate() {
  specText.value = JSON.stringify(SPEC_TEMPLATE, null, 2)
  specEditingId.value = ''
  specNote.value = '已载入模板。修改 id、name、kernels 后点「新建协议」。'
}

function loadSpecForEdit(row: Protocol) {
  const copy: Record<string, unknown> = { ...row }
  delete copy.custom
  specText.value = JSON.stringify(copy, null, 2)
  specEditingId.value = row.id
  specNote.value = row.custom
    ? `正在编辑自定义协议 ${row.id}，可改任意字段。`
    : `正在编辑内置协议 ${row.id}：结构字段只读，仅启用状态 / params / 显示名 / 描述 / 自定义配置会被保存。需要改结构请另建自定义协议。`
}

function formatSpec() {
  const v = parseObject(specText.value, '协议 JSON')
  if (v) {
    specText.value = JSON.stringify(v, null, 2)
    specNote.value = 'JSON 已格式化。'
  }
}

async function saveSpec() {
  const v = parseObject(specText.value, '协议 JSON')
  if (!v) return
  const id = v.id
  if (typeof id !== 'string' || !id.trim()) {
    specNote.value = '缺少 id。'
    window.$message?.error('协议 JSON 缺少 id')
    return
  }
  const kernels = v.kernels
  if (!Array.isArray(kernels) || kernels.length === 0) {
    specNote.value = 'kernels 必须是非空数组，例如 ["mihomo"]。'
    window.$message?.error('请填写至少一个内核')
    return
  }
  const payload = v as unknown as Partial<Protocol>
  const editing = specEditingId.value
  const r = editing
    ? await run('spec-update', () => api.updateProtocol(editing, payload))
    : await run('spec-create', () => api.createProtocol(payload))
  if (!r) return
  window.$message?.success(editing ? `协议 ${editing} 已更新` : `协议 ${id} 已创建`)
  specNote.value = editing ? `已保存 ${editing}。` : `已新建 ${id}。`
  specEditingId.value = ''
  await refreshProtocols()
}

function confirmDeleteProtocol(row: Protocol) {
  window.$dialog?.warning({
    title: '删除自定义协议',
    content: `确定删除 ${row.id} 吗？内置协议不可删除，只能停用。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const r = await run('spec-del:' + row.id, () => api.deleteProtocol(row.id))
      if (!r) return
      window.$message?.success(`协议 ${row.id} 已删除`)
      if (specEditingId.value === row.id) loadTemplate()
      if (configTarget.value === row.id) configTarget.value = ''
      await refreshProtocols()
    },
  })
}

/* ------------------------------------------------------------------ *
 * 单条详情
 * ------------------------------------------------------------------ */

const detailId = ref('')
const detail = ref<unknown>(null)

async function queryDetail(id = detailId.value) {
  if (!id.trim()) {
    window.$message?.warning('请填写协议 ID')
    return
  }
  detailId.value = id
  const d = await run('detail', () => api.getProtocol(id.trim()))
  if (d) detail.value = d
}

/* ------------------------------------------------------------------ *
 * 表格
 * ------------------------------------------------------------------ */

const columns: any[] = [
  { type: 'selection', width: 46, fixed: 'left' },
  {
    title: 'ID',
    key: 'id',
    minWidth: 160,
    fixed: 'left',
    render: (r: Protocol) =>
      h('span', { class: 'mono' }, [
        r.id,
        r.custom
          ? h(NTag, { size: 'small', bordered: false, type: 'info', style: 'margin-left:6px' }, { default: () => '自定义' })
          : null,
      ]),
  },
  { title: '名称', key: 'name', minWidth: 190 },
  {
    title: '大类',
    key: 'group',
    minWidth: 150,
    render: (r: Protocol) =>
      h(
        NTag,
        { size: 'small', bordered: false, class: GROUP_CLASS[r.group] ?? '' },
        { default: () => GROUP_LABEL[r.group] ?? r.group },
      ),
  },
  {
    title: '分类',
    key: 'category',
    minWidth: 120,
    render: (r: Protocol) =>
      h('span', { class: 'mono' }, CATEGORY_LABEL[r.category] ?? r.category),
  },
  { title: '加密方式', key: 'security', minWidth: 220, render: (r: Protocol) => r.security || '—' },
  {
    title: '配置形态',
    key: 'config_kind',
    width: 130,
    render: (r: Protocol) => KIND_LABEL[r.config_kind] ?? r.config_kind ?? '—',
  },
  { title: '端口', key: 'default_port', width: 85, render: (r: Protocol) => r.default_port || '—' },
  {
    title: '适用内核',
    key: 'kernels',
    minWidth: 210,
    render: (r: Protocol) => (Array.isArray(r.kernels) ? r.kernels.join(' / ') : '—'),
  },
  {
    title: '启用',
    key: 'enabled',
    width: 86,
    render: (r: Protocol) =>
      h(NSwitch, {
        size: 'small',
        value: !!r.enabled,
        loading: isBusy('enable:' + r.id),
        'onUpdate:value': () => toggleOne(r),
      }),
  },
  {
    title: '操作',
    key: 'actions',
    width: 236,
    fixed: 'right',
    render: (r: Protocol) =>
      h('div', { style: 'display:flex;gap:4px;flex-wrap:wrap' }, [
        h(NButton, { size: 'tiny', onClick: () => selectTarget(r) }, { default: () => '配置' }),
        h(NButton, { size: 'tiny', onClick: () => loadSpecForEdit(r) }, { default: () => 'JSON' }),
        h(NButton, { size: 'tiny', onClick: () => queryDetail(r.id) }, { default: () => '详情' }),
        r.custom
          ? h(
              NButton,
              { size: 'tiny', type: 'error', onClick: () => confirmDeleteProtocol(r) },
              { default: () => '删除' },
            )
          : null,
      ]),
  },
]

onMounted(async () => {
  loadTemplate()
  const m = await run('meta', () => api.getProtocolMeta())
  if (m) meta.value = m
  await refreshProtocols()
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">协议中心</h2>
        <p class="page-subtitle">
          Stage 4 · 全内核协议目录（加密 / 未加密 / 普通代理）· 配置生成与自定义编辑
        </p>
      </div>
      <NSpace align="center" :size="10" :wrap="true">
        <NButton size="small" :loading="isBusy('protocols')" @click="refreshProtocols">
          刷新目录
        </NButton>
        <NButton size="small" type="primary" @click="loadTemplate">新建自定义协议</NButton>
      </NSpace>
    </div>

    <NAlert type="info" :bordered="false" class="section-gap">
      <template #header>协议分类与使用方式</template>
      <div>
        <strong>加密协议</strong>：VPN、TLS/QUIC/AEAD 加密代理与后量子控制信道；
        <strong>普通未加密协议</strong>：DIRECT / SOCKS5 / HTTP / 端口转发等明文本身；
        <strong>普通代理协议</strong>：CONNECT 隧道、上游代理链、PAC、系统代理入口等标准代理编排。
      </div>
      <div style="margin-top: 6px">
        每条协议都标注了真实的内核支持与配置形态（内核 outbound / 规则动作 / 监听入口 / 外部 helper）。
        <strong>只有已启用的协议才能生成配置</strong>；生成结果可以直接编辑、校验并保存为该协议的自定义配置。
        内置协议不可删除、结构字段只读；需要完全自定义请新建协议。
      </div>
    </NAlert>

    <div class="proto-stats">
      <div>
        <div class="proto-stat-label">协议总数</div>
        <div class="proto-stat-value">{{ groupCounts[''] }}</div>
      </div>
      <div>
        <div class="proto-stat-label">加密协议</div>
        <div class="proto-stat-value">{{ groupCounts.encrypted }}</div>
      </div>
      <div>
        <div class="proto-stat-label">普通未加密协议</div>
        <div class="proto-stat-value">{{ groupCounts.plaintext }}</div>
      </div>
      <div>
        <div class="proto-stat-label">普通代理协议</div>
        <div class="proto-stat-value">{{ groupCounts['plain-proxy'] }}</div>
      </div>
      <div>
        <div class="proto-stat-label">已启用</div>
        <div class="proto-stat-value">{{ groupCounts.enabled }}</div>
      </div>
      <div>
        <div class="proto-stat-label">自定义</div>
        <div class="proto-stat-value">{{ groupCounts.custom }}</div>
      </div>
    </div>

    <NCard size="small" title="协议目录（GET /protocols）" class="section-gap">
      <template #header-extra>
        <NSpace :size="8" :wrap="true" align="center">
          <NInput
            v-model:value="search"
            size="small"
            clearable
            placeholder="搜索 ID / 名称 / 内核"
            style="width: 220px"
          />
          <NSelect v-model:value="category" size="small" :options="categoryOptions" style="width: 150px" />
          <NSelect v-model:value="enabledFilter" size="small" :options="enabledOptions" style="width: 130px" />
        </NSpace>
      </template>

      <div class="proto-tabs">
        <button
          v-for="t in tabs"
          :key="t.value"
          type="button"
          class="proto-tab"
          :class="{ active: group === t.value }"
          @click="group = t.value"
        >
          {{ t.label }}
          <span class="tab-count">{{ groupCounts[t.value] ?? 0 }}</span>
        </button>
      </div>

      <div class="bulk-bar" style="margin-top: 12px">
        <span class="bulk-count">已选 {{ checkedIds.length }} / {{ visibleProtocols.length }}</span>
        <NButton
          size="small"
          :disabled="!checkedIds.length"
          :loading="isBusy('toggle:selected:on')"
          @click="applyToggle(checkedIds, false, true)"
        >启用选中</NButton>
        <NButton
          size="small"
          :disabled="!checkedIds.length"
          :loading="isBusy('toggle:selected:off')"
          @click="applyToggle(checkedIds, false, false)"
        >停用选中</NButton>
        <NButton size="small" :loading="isBusy('toggle:all:on')" @click="applyToggle([], true, true)">全部启用</NButton>
        <NButton size="small" :loading="isBusy('toggle:all:off')" @click="applyToggle([], true, false)">全部停用</NButton>
      </div>

      <NDataTable
        v-model:checked-row-keys="checkedIds"
        :row-key="rowKey"
        :columns="columns"
        :data="visibleProtocols"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="1860"
      />
      <div v-if="!visibleProtocols.length" class="hint" style="margin-top: 10px">
        没有匹配的协议，请调整分类 / 搜索条件，或新建自定义协议。
      </div>
    </NCard>

    <NCard size="small" title="配置生成与自定义编辑（POST /protocols/{id}/config）" class="section-gap">
      <NSpace vertical :size="12" style="width: 100%">
        <NGrid cols="1 m:3" responsive="screen" :x-gap="12" :y-gap="10">
          <NGi>
            <div class="kv-label">目标协议 ID（点目录中的「配置」自动填入）</div>
            <NInput v-model:value="configTarget" placeholder="例如 wireguard" />
          </NGi>
          <NGi>
            <div class="kv-label">目标内核 kernel</div>
            <NSelect v-model:value="kernel" :options="KERNEL_OPTIONS" filterable tag />
          </NGi>
          <NGi>
            <div class="kv-label">配置格式 format</div>
            <NSelect v-model:value="configFormat" :options="FORMAT_OPTIONS" />
          </NGi>
        </NGrid>

        <div>
          <div class="proto-sec">
            <span class="proto-sec-title">params（JSON 对象，可自定义编辑）</span>
            <NButton size="tiny" @click="formatParams">格式化 JSON</NButton>
          </div>
          <NInput v-model:value="paramsText" type="textarea" class="code-editor" :rows="4" />
        </div>

        <NSpace :size="8" :wrap="true">
          <NButton size="small" type="primary" :loading="isBusy('config-gen')" @click="generate(false)">
            生成配置
          </NButton>
          <NButton size="small" :loading="isBusy('config-gen')" @click="generate(true)">重新生成模板</NButton>
          <NButton size="small" @click="restoreTemplate">还原本次生成</NButton>
          <NButton size="small" @click="formatContent">格式化内容</NButton>
          <NButton size="small" :loading="isBusy('config-validate')" @click="validateContent">校验</NButton>
          <NButton size="small" type="primary" :loading="isBusy('config-save')" @click="saveContent">
            保存为该协议配置
          </NButton>
          <NButton size="small" :loading="isBusy('config-clear')" @click="clearSaved">清除已保存</NButton>
          <NButton size="small" @click="copyContent">复制</NButton>
          <NButton size="small" @click="downloadContent">下载</NButton>
        </NSpace>

        <div>
          <div class="proto-sec">
            <span class="proto-sec-title">配置内容（可直接编辑）</span>
            <NSpace :size="8" align="center">
              <NTag v-if="validated === true" size="small" type="success" :bordered="false">校验通过</NTag>
              <NTag v-else-if="validated === false" size="small" type="error" :bordered="false">校验未通过</NTag>
              <NTag v-if="customized" size="small" type="info" :bordered="false">已保存自定义配置</NTag>
            </NSpace>
          </div>
          <NInput
            v-model:value="configContent"
            type="textarea"
            class="code-editor-large"
            :rows="14"
            placeholder="点「生成配置」后，这里会出现可直接编辑的配置内容"
          />
        </div>

        <div class="hint">
          {{ configNote || '生成结果可直接编辑；保存前会强制按所选格式做真实语法校验，语法错误不会写入。' }}
        </div>
      </NSpace>
    </NCard>

    <NCard size="small" title="自定义协议 · JSON 编辑（POST / PUT / DELETE /protocols）" class="section-gap">
      <NSpace vertical :size="12" style="width: 100%">
        <NSpace :size="8" :wrap="true">
          <NButton size="small" @click="loadTemplate">载入模板</NButton>
          <NButton size="small" @click="formatSpec">格式化 / 检查 JSON</NButton>
          <NButton size="small" type="primary" :loading="isBusy('spec-create')" @click="saveSpec">
            {{ specEditingId ? '保存修改' : '新建协议' }}
          </NButton>
          <NButton v-if="specEditingId" size="small" @click="loadTemplate">取消编辑</NButton>
        </NSpace>

        <div class="hint">
          可填字段：<span class="mono">id / name / category / group / security / transport / config_kind /
          default_port / kernels / features / description / enabled / params / config_content / config_format</span>。
          <span class="mono">group</span> 取
          <span class="mono">encrypted | plaintext | plain-proxy</span>；
          <span class="mono">config_kind</span> 取
          <span class="mono">outbound | policy | listener | helper</span>；
          <span class="mono">config_content</span> 可直接写一份自定义 JSON / YAML / CONF 配置模板。
          {{ meta ? `服务端当前支持 ${meta.formats.join(' / ')} 格式。` : '' }}
        </div>

        <NInput
          v-model:value="specText"
          type="textarea"
          class="code-editor-large"
          :rows="14"
          placeholder="在此粘贴或编辑协议 JSON"
        />

        <div class="hint">{{ specNote || '在目录中点「JSON」可载入任意一条协议进行修改。' }}</div>

        <div v-if="groupCounts.custom > 0" class="proto-sec">
          <span class="proto-sec-title">已有自定义协议</span>
          <span class="hint">点 JSON 编辑，或直接在目录中删除</span>
        </div>
        <NSpace v-if="groupCounts.custom > 0" :size="8" :wrap="true">
          <NButton
            v-for="p in allProtocols.filter((x) => x.custom)"
            :key="p.id"
            size="tiny"
            @click="loadSpecForEdit(p)"
          >
            {{ p.id }}
          </NButton>
        </NSpace>
      </NSpace>
    </NCard>

    <NCard size="small" title="单条详情（GET /protocols/{id}）" class="section-gap">
      <template #header-extra>
        <NSpace :size="8" :wrap="true" align="center">
          <NInput v-model:value="detailId" size="small" style="width: 220px" placeholder="协议 ID" />
          <NButton size="small" :loading="isBusy('detail')" @click="queryDetail()">查询</NButton>
        </NSpace>
      </template>
      <pre class="mono output-block" style="max-height: 300px">{{ prettyJson(detail) }}</pre>
    </NCard>
  </div>
</template>
