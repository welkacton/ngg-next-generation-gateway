<script setup lang="ts">
import { computed, h, nextTick, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
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
 * 五大类视图：按内核 / 用途 / 来源分门别类，一次只看一类，避免一屏几十行
 * ------------------------------------------------------------------ */

type ViewKey = 'mihomo' | 'sing-box' | 'xray-core' | 'vpn' | 'custom' | 'all'
interface ViewDef {
  key: ViewKey
  label: string
  hint: string
  match: (p: Protocol) => boolean
}

const VIEWS: ViewDef[] = [
  {
    key: 'mihomo', label: 'Mihomo 支持协议',
    hint: 'kernels 含 mihomo 的协议（多核共用的协议会同时出现在其它内核分类里）',
    match: (p) => (p.kernels ?? []).includes('mihomo'),
  },
  {
    key: 'sing-box', label: 'sing-box 支持协议',
    hint: 'kernels 含 sing-box 的协议',
    match: (p) => (p.kernels ?? []).includes('sing-box'),
  },
  {
    key: 'xray-core', label: 'Xray-core 支持协议',
    hint: 'kernels 含 xray-core 的协议',
    match: (p) => (p.kernels ?? []).includes('xray-core'),
  },
  {
    key: 'vpn', label: 'VPN 代理协议',
    hint: 'VPN、加密代理与后量子控制信道（category = vpn / encrypted-proxy / post-quantum）',
    match: (p) => ['vpn', 'encrypted-proxy', 'post-quantum'].includes(p.category),
  },
  {
    key: 'custom', label: '自定义协议',
    hint: '你在下方 JSON 编辑器里新建的协议，可完全自定义字段与配置模板',
    match: (p) => p.custom === true,
  },
  {
    key: 'all', label: '全部',
    hint: '全部内置与自定义协议',
    match: () => true,
  },
]

const view = ref<ViewKey>('mihomo')
const viewCounts = computed<Record<ViewKey, number>>(() => {
  const out = {} as Record<ViewKey, number>
  for (const v of VIEWS) out[v.key] = allProtocols.value.filter(v.match).length
  return out
})
const activeView = computed(() => VIEWS.find((v) => v.key === view.value) ?? VIEWS[0])

const meta = ref<ProtocolMeta | null>(null)
const allProtocols = ref<Protocol[]>([])
const search = ref('')
const enabledFilter = ref('')
const secondaryGroup = ref('')
const checkedIds = ref<string[]>([])
const expandedKeys = ref<string[]>([])

const enabledOptions = [
  { label: '全部状态', value: '' },
  { label: '仅已启用', value: 'true' },
  { label: '仅已停用', value: 'false' },
]
const groupOptions = [
  { label: '全部小类', value: '' },
  { label: '加密协议', value: 'encrypted' },
  { label: '普通未加密协议', value: 'plaintext' },
  { label: '普通代理协议', value: 'plain-proxy' },
]

const visibleProtocols = computed(() => {
  const q = search.value.trim().toLowerCase()
  const order: Record<string, number> = { encrypted: 0, plaintext: 1, 'plain-proxy': 2 }
  return allProtocols.value
    .filter(activeView.value.match)
    .filter((p) => {
      if (secondaryGroup.value && p.group !== secondaryGroup.value) return false
      if (enabledFilter.value === 'true' && !p.enabled) return false
      if (enabledFilter.value === 'false' && p.enabled) return false
      if (q) {
        const hay = [p.id, p.name, p.description, p.security, ...(p.kernels ?? [])]
          .join(' ').toLowerCase()
        if (!hay.includes(q)) return false
      }
      return true
    })
    .sort((a, b) =>
      (order[a.group] ?? 3) - (order[b.group] ?? 3) || a.id.localeCompare(b.id),
    )
})

async function refreshProtocols() {
  const data = await run('protocols', () => api.listProtocols())
  if (data) {
    allProtocols.value = toArray<Protocol>(data)
    const known = new Set(allProtocols.value.map((p) => p.id))
    checkedIds.value = checkedIds.value.filter((id) => known.has(id))
    expandedKeys.value = expandedKeys.value.filter((id) => known.has(id))
  }
}

function rowKey(r: Protocol) {
  return r.id
}

/* ------------------------------------------------------------------ *
 * 单条 / 多选 / 本类全部启停
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

function toggleViewAll(enabled: boolean) {
  const key = `toggle:view:${enabled ? 'on' : 'off'}`
  void run(key, () => api.toggleProtocols({
    ids: visibleProtocols.value.map((p) => p.id), enabled,
  })).then(async (r) => {
    if (r) {
      window.$message?.success(`已${enabled ? '启用' : '停用'} ${r.changed} 条协议`)
      await refreshProtocols()
    }
  })
}

/* ------------------------------------------------------------------ *
 * 配置生成 / 自定义编辑（params 已取消：直接编辑生成结果即可）
 * ------------------------------------------------------------------ */

const configCard = ref<HTMLElement | null>(null)
const configFlashing = ref(false)
const configTarget = ref('')
const kernel = ref('sing-box')
const configContent = ref('')
const configFormat = ref('json')
const pristineContent = ref('')
const validated = ref<boolean | null>(null)
const configNote = ref('')
const customized = ref(false)

async function generate(regenerate = false) {
  const id = configTarget.value.trim()
  if (!id) {
    window.$message?.warning('请先选择或填写协议 ID')
    return
  }
  const r = await run('config-gen', () =>
    api.generateProtocolConfig(id, kernel.value.trim(), {}, regenerate),
  )
  if (!r) return
  configContent.value = r.content
  pristineContent.value = r.content
  configFormat.value = r.format || 'json'
  validated.value = r.validated === true
  customized.value = r.customized === true
  configNote.value = customized.value
    ? '当前内容来自该协议已保存的自定义配置；点「重新生成模板」可回到内置模板。'
    : `已按 ${kernel.value} 生成 ${r.format} 配置。server / port / 密钥等参数已内置在协议定义里，直接在下方编辑即可。`
}

async function selectTarget(row: Protocol) {
  configTarget.value = row.id
  if (row.kernels?.length) kernel.value = row.kernels[0]
  if (!row.enabled) {
    configNote.value = `协议 ${row.id} 当前已停用，请先启用再生成配置。`
  }
  await generate(false)
  await nextTick()
  configCard.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  configFlashing.value = true
  window.setTimeout(() => { configFlashing.value = false }, 1400)
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

const specCard = ref<HTMLElement | null>(null)
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

function loadSpecForEdit(row: Protocol) {
  const copy: Record<string, unknown> = { ...row }
  delete copy.custom
  specText.value = JSON.stringify(copy, null, 2)
  specEditingId.value = row.id
  specNote.value = row.custom
    ? `正在编辑自定义协议 ${row.id}，可改任意字段。`
    : `正在编辑内置协议 ${row.id}：结构字段只读，仅启用状态 / params / 显示名 / 描述 / 自定义配置会被保存。需要改结构请另建自定义协议。`
  specCard.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
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
  if (!editing) view.value = 'custom'
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
 * 表格：列数大幅精简，所有列固定宽度 + 省略号，避免文字互相覆盖叠加
 * ------------------------------------------------------------------ */

function toggleDetail(row: Protocol) {
  expandedKeys.value = expandedKeys.value.includes(row.id)
    ? expandedKeys.value.filter((k) => k !== row.id)
    : [...expandedKeys.value, row.id]
}

const columns: any[] = [
  { type: 'selection', width: 44 },
  {
    type: 'expand',
    width: 34,
    renderExpand: (r: Protocol) =>
      h('div', { style: 'padding: 10px 14px; background:#fafafb' }, [
        h('div', { class: 'hint', style: 'margin-bottom:6px' }, `完整协议定义 · ${r.id}`),
        h('pre', { class: 'mono output-block', style: 'max-height:320px; word-break:normal' },
          prettyJson(r)),
      ]),
  },
  {
    title: '协议',
    key: 'id',
    width: 230,
    render: (r: Protocol) =>
      h('div', { class: 'proto-name-cell' }, [
        h('div', { class: 'mono proto-id' }, [
          r.id,
          r.custom
            ? h(NTag, { size: 'small', bordered: false, type: 'info', class: 'proto-custom-tag' },
              { default: () => '自定义' })
            : null,
        ]),
        h('div', { class: 'proto-sub' }, [
          h('span', { class: 'proto-name' }, r.name),
          h('span', { class: 'proto-dot' }, '·'),
          h('span', {}, CATEGORY_LABEL[r.category] ?? r.category),
        ]),
      ]),
  },
  {
    title: '小类 / 配置形态',
    key: 'group',
    width: 150,
    render: (r: Protocol) =>
      h('div', { class: 'proto-meta-cell' }, [
        h(NTag, { size: 'small', bordered: false, class: GROUP_CLASS[r.group] ?? '' },
          { default: () => GROUP_LABEL[r.group] ?? r.group }),
        h('div', { class: 'proto-kind' }, KIND_LABEL[r.config_kind] ?? r.config_kind ?? '—'),
      ]),
  },
  {
    title: '内核支持',
    key: 'kernels',
    width: 170,
    ellipsis: { tooltip: true },
    render: (r: Protocol) => (Array.isArray(r.kernels) ? r.kernels.join(' / ') : '—'),
  },
  {
    title: '加密方式',
    key: 'security',
    width: 170,
    ellipsis: { tooltip: true },
    render: (r: Protocol) => r.security || '—',
  },
  {
    title: '启用',
    key: 'enabled',
    width: 70,
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
    width: 180,
    render: (r: Protocol) =>
      h('div', { class: 'proto-actions' }, [
        h(NButton, { size: 'tiny', type: 'primary', onClick: () => selectTarget(r) },
          { default: () => '配置' }),
        h(NButton, { size: 'tiny', onClick: () => loadSpecForEdit(r) },
          { default: () => 'JSON' }),
        h(NButton, { size: 'tiny', tertiary: true, onClick: () => toggleDetail(r) },
          { default: () => '详情' }),
        r.custom
          ? h(NButton, { size: 'tiny', type: 'error', onClick: () => confirmDeleteProtocol(r) },
            { default: () => '删除' })
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
        <p class="page-subtitle">Stage 4 · 全内核协议目录 · 按内核与用途分门别类 · 配置生成与自定义编辑</p>
      </div>
      <NSpace align="center" :size="10" :wrap="true">
        <NButton size="small" :loading="isBusy('protocols')" @click="refreshProtocols">
          刷新目录
        </NButton>
        <NButton size="small" type="primary" @click="loadTemplate(); specCard?.scrollIntoView({ behavior: 'smooth', block: 'start' })">
          新建自定义协议
        </NButton>
      </NSpace>
    </div>

    <NAlert type="info" :bordered="false" class="section-gap">
      <template #header>五大类怎么分</template>
      <div>
        <strong>Mihomo / sing-box / Xray-core 支持协议</strong>按 <span class="mono">kernels</span> 字段筛选（多核共用的协议会同时出现在多个内核分类）；
        <strong>VPN 代理协议</strong>收录 VPN、加密代理与后量子控制信道；<strong>自定义协议</strong>只显示你在 JSON 编辑器里新建的条目。
      </div>
      <div style="margin-top: 6px">
        每条协议都标注真实内核支持与配置形态（内核 outbound / 规则动作 / 监听入口 / 外部 helper）。
        <strong>只有已启用的协议才能生成配置</strong>；生成结果可直接编辑、校验并保存为该协议的自定义配置。
        内置协议不可删除、结构字段只读；需要完全自定义请新建协议。完整 JSON 点行内「详情」展开查看。
      </div>
    </NAlert>

    <!-- ① 协议目录 -->
    <NCard size="small" class="section-gap major-card">
      <template #header>
        <div class="major-head">
          <span class="major-title">协议目录</span>
          <span class="major-sub">GET /protocols</span>
        </div>
      </template>
      <template #header-extra>
        <NSpace :size="8" :wrap="true" align="center">
          <NInput v-model:value="search" size="small" clearable placeholder="搜索 ID / 名称 / 内核" style="width: 210px" />
          <NSelect v-model:value="secondaryGroup" size="small" :options="groupOptions" style="width: 150px" />
          <NSelect v-model:value="enabledFilter" size="small" :options="enabledOptions" style="width: 125px" />
        </NSpace>
      </template>

      <div class="proto-tabs">
        <button
          v-for="v in VIEWS"
          :key="v.key"
          type="button"
          class="proto-tab"
          :class="{ active: view === v.key }"
          @click="view = v.key; checkedIds = []"
        >
          {{ v.label }}
          <span class="tab-count">{{ viewCounts[v.key] }}</span>
        </button>
      </div>
      <div class="hint" style="margin-top: 8px">{{ activeView.hint }} · 当前显示 {{ visibleProtocols.length }} 条</div>

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
        <NButton
          size="small"
          :disabled="!visibleProtocols.length"
          :loading="isBusy('toggle:view:on')"
          @click="toggleViewAll(true)"
        >本类全部启用</NButton>
        <NButton
          size="small"
          :disabled="!visibleProtocols.length"
          :loading="isBusy('toggle:view:off')"
          @click="toggleViewAll(false)"
        >本类全部停用</NButton>
      </div>

      <NDataTable
        v-model:checked-row-keys="checkedIds"
        v-model:expanded-row-keys="expandedKeys"
        :row-key="rowKey"
        :columns="columns"
        :data="visibleProtocols"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="1050"
      />
      <div v-if="!visibleProtocols.length" class="hint" style="margin-top: 10px">
        本类没有匹配的协议。可调整搜索/小类筛选，或点右上「新建自定义协议」。
      </div>
    </NCard>

    <!-- ② 配置生成与自定义编辑 -->
    <div ref="configCard">
      <NCard size="small" class="section-gap major-card" :class="{ 'flash-target': configFlashing }">
        <template #header>
          <div class="major-head">
            <span class="major-title">配置生成与自定义编辑</span>
            <span class="major-sub">POST /protocols/{id}/config</span>
          </div>
        </template>

        <NSpace vertical :size="12" style="width: 100%">
          <div class="hint">
            点上方「配置」会自动定位到这里。参数（server / port / 密钥等）已内置在协议定义中，
            <strong>直接编辑下方生成结果</strong>即可自定义，无需再单独填 params。
          </div>

          <NSpace :size="10" :wrap="true" align="end">
            <div>
              <div class="kv-label">目标协议 ID</div>
              <NInput v-model:value="configTarget" placeholder="点目录中的「配置」自动填入" style="width: 230px" />
            </div>
            <div>
              <div class="kv-label">目标内核 kernel</div>
              <NSelect v-model:value="kernel" :options="KERNEL_OPTIONS" filterable tag style="width: 210px" />
            </div>
            <div>
              <div class="kv-label">配置格式 format</div>
              <NSelect v-model:value="configFormat" :options="FORMAT_OPTIONS" style="width: 150px" />
            </div>
          </NSpace>

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
    </div>

    <!-- ③ 自定义协议 · JSON 编辑 -->
    <div ref="specCard">
      <NCard size="small" class="section-gap major-card">
        <template #header>
          <div class="major-head">
            <span class="major-title">自定义协议 · JSON 编辑</span>
            <span class="major-sub">POST / PUT / DELETE /protocols</span>
          </div>
        </template>

        <NSpace vertical :size="12" style="width: 100%">
          <NSpace :size="8" :wrap="true">
            <NButton size="small" @click="loadTemplate">载入模板</NButton>
            <NButton size="small" @click="formatSpec">格式化 / 检查 JSON</NButton>
            <NButton size="small" type="primary" :loading="isBusy('spec-create') || isBusy('spec-update')" @click="saveSpec">
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

          <div v-if="viewCounts.custom > 0" class="proto-sec">
            <span class="proto-sec-title">已有自定义协议（{{ viewCounts.custom }}）</span>
            <span class="hint">点名称编辑，或切到「自定义协议」分类删除</span>
          </div>
          <NSpace v-if="viewCounts.custom > 0" :size="8" :wrap="true">
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
    </div>
  </div>
</template>
