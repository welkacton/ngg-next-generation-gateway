<script setup lang="ts">
import { computed, h, nextTick, onMounted, reactive, ref } from 'vue'
import {
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
  KernelSchema,
  Protocol,
  ProtocolConfigResult,
  ProtocolMeta,
} from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { prettyJson, toArray } from '../utils/format'

const { isBusy, run } = useApiAction()

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
const KERNEL_KEYS = ['mihomo', 'sing-box', 'xray-core']

const KERNEL_OPTIONS = [
  { label: 'mihomo · YAML proxies', value: 'mihomo' },
  { label: 'sing-box · JSON outbound', value: 'sing-box' },
  { label: 'xray-core · JSON outbounds', value: 'xray-core' },
]

function hasKernel(p: Protocol, k: string) {
  return (p.kernels ?? []).includes(k)
}

/* ------------------------------------------------------------------ *
 * 目录：只按三个内核分类，普通未加密 / 普通代理协议不进入目录
 * ------------------------------------------------------------------ */

type ViewKey = 'mihomo' | 'sing-box' | 'xray-core' | 'custom' | 'all'
interface ViewDef {
  key: ViewKey
  label: string
  match: (p: Protocol) => boolean
}
const VIEWS: ViewDef[] = [
  { key: 'mihomo', label: 'Mihomo 支持协议', match: (p) => hasKernel(p, 'mihomo') },
  { key: 'sing-box', label: 'sing-box 支持协议', match: (p) => hasKernel(p, 'sing-box') },
  { key: 'xray-core', label: 'Xray-core 支持协议', match: (p) => hasKernel(p, 'xray-core') },
  { key: 'custom', label: '自定义协议', match: (p) => p.custom === true },
  {
    key: 'all', label: '全部',
    match: (p) => KERNEL_KEYS.some((k) => hasKernel(p, k)) || p.custom === true,
  },
]

const view = ref<ViewKey>('mihomo')
const activeView = computed(() => VIEWS.find((v) => v.key === view.value) ?? VIEWS[0])
const allProtocols = ref<Protocol[]>([])
const meta = ref<ProtocolMeta | null>(null)
const schemas = ref<KernelSchema[]>([])
const search = ref('')
const enabledFilter = ref('')
const checkedIds = ref<string[]>([])
const expandedKeys = ref<string[]>([])

const enabledOptions = [
  { label: '全部状态', value: '' },
  { label: '仅已启用', value: 'true' },
  { label: '仅已停用', value: 'false' },
]

const viewCounts = computed<Record<ViewKey, number>>(() => {
  const out = {} as Record<ViewKey, number>
  for (const v of VIEWS) out[v.key] = allProtocols.value.filter(v.match).length
  return out
})

const visibleProtocols = computed(() => {
  const q = search.value.trim().toLowerCase()
  return allProtocols.value
    .filter(activeView.value.match)
    .filter((p) => {
      if (enabledFilter.value === 'true' && !p.enabled) return false
      if (enabledFilter.value === 'false' && p.enabled) return false
      if (q) {
        const hay = [p.id, p.name, p.description, p.security, ...(p.kernels ?? [])]
          .join(' ').toLowerCase()
        if (!hay.includes(q)) return false
      }
      return true
    })
    .sort((a, b) => a.id.localeCompare(b.id))
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

async function toggleOne(row: Protocol) {
  const next = !row.enabled
  const r = await run('enable:' + row.id, () => api.enableProtocol(row.id, next))
  if (r) {
    window.$message?.success(`协议 ${row.id} 已${next ? '启用' : '停用'}`)
    await refreshProtocols()
  }
}

async function applyToggle(ids: string[], enabled: boolean, scope: 'selected' | 'view' = 'selected') {
  const key = `toggle:${scope}:${enabled ? 'on' : 'off'}`
  const r = await run(key, () => api.toggleProtocols({ ids, enabled }))
  if (r) {
    window.$message?.success(`已${enabled ? '启用' : '停用'} ${r.changed} 条协议`)
    await refreshProtocols()
  }
}

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
    title: '协议', key: 'id', width: 230,
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
    title: '配置形态', key: 'config_kind', width: 140,
    render: (r: Protocol) => KIND_LABEL[r.config_kind] ?? r.config_kind ?? '—',
  },
  {
    title: '内核支持', key: 'kernels', width: 190, ellipsis: { tooltip: true },
    render: (r: Protocol) => (Array.isArray(r.kernels) ? r.kernels.join(' / ') : '—'),
  },
  {
    title: '加密方式', key: 'security', width: 190, ellipsis: { tooltip: true },
    render: (r: Protocol) => r.security || '—',
  },
  {
    title: '启用', key: 'enabled', width: 70,
    render: (r: Protocol) =>
      h(NSwitch, {
        size: 'small', value: !!r.enabled, loading: isBusy('enable:' + r.id),
        'onUpdate:value': () => toggleOne(r),
      }),
  },
  {
    title: '操作', key: 'actions', width: 180,
    render: (r: Protocol) =>
      h('div', { class: 'proto-actions' }, [
        h(NButton, { size: 'tiny', type: 'primary', onClick: () => openEditor(r, 'existing') },
          { default: () => '配置' }),
        h(NButton, { size: 'tiny', onClick: () => openEditor(r, 'spec') },
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

/* ------------------------------------------------------------------ *
 * 合并后的编辑区：可视化表单（上半）+ 带注释代码编辑（下半）
 * ------------------------------------------------------------------ */

const editorCard = ref<HTMLElement | null>(null)
const editorFlashing = ref(false)
const editMode = ref<'existing' | 'new'>('existing')
const configTarget = ref('')
const kernel = ref('mihomo')
const fieldValues = ref<Record<string, unknown>>({})
const currentSchema = computed<KernelSchema | null>(() =>
  schemas.value.find((s) => s.kernel === kernel.value) ?? schemas.value[0] ?? null,
)
const docUrl = ref('')

const newSpec = reactive({
  id: '', name: '', category: 'encrypted-proxy', group: 'encrypted',
  kernels: 'mihomo', security: '自定义 / 由上游决定', transport: 'TCP',
  config_kind: 'outbound', default_port: 443, description: '',
})

const codeMode = ref<'config' | 'spec'>('config')
const showComments = ref(true)
const configText = ref('')
const specText = ref('')
const plainConfig = ref('')
const configFormat = ref('yaml')
const validated = ref<boolean | null>(null)
const note = ref('')
const customized = ref(false)

const codeText = computed({
  get: () => (codeMode.value === 'config' ? configText.value : specText.value),
  set: (v: string) => {
    if (codeMode.value === 'config') configText.value = v
    else specText.value = v
  },
})

const protocolOptions = computed(() =>
  allProtocols.value.map((p) => ({ label: `${p.id} · ${p.name}`, value: p.id })),
)

const SPEC_COMMENT_HEADER = [
  '// ── 协议定义 Spec JSON ──',
  '// id：协议唯一标识；name：界面显示名称；kernels：支持的内核列表',
  '// category / group：协议分类；config_kind：生成物形态（outbound / policy / listener / helper）',
  '// params：可视化表单里的字段，生成配置时按内核语法映射',
  '// config_content / config_format：保存的手写配置模板（可选）',
  '// enabled：是否启用；停用后不能生成配置；custom：是否为自定义协议',
  '// 「校验 / 保存」时会自动去掉这些注释行。',
  '',
].join('\n')

function stripWholeLineComments(text: string) {
  return text.split('\n')
    .filter((l) => {
      const t = l.trim()
      return !(t.startsWith('//') || t.startsWith('#'))
    })
    .join('\n')
    .trimStart()
}

function setField(key: string, v: unknown) {
  fieldValues.value = { ...fieldValues.value, [key]: v }
}

function resetFields(p?: Protocol | null) {
  const v: Record<string, unknown> = {}
  for (const f of currentSchema.value?.fields ?? []) {
    v[f.key] = f.type === 'bool' ? f.default === 'true' : (f.default ?? '')
  }
  if (p) {
    if (p.params) Object.assign(v, p.params)
    if (!v.name) v.name = p.id
    if (!v.server && !v.address) v.server = '127.0.0.1'
    if (p.default_port > 0) {
      if (kernel.value === 'sing-box' && !v.server_port) v.server_port = p.default_port
      else if (!v.port) v.port = p.default_port
    }
  }
  fieldValues.value = v
}

function buildParams(): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(fieldValues.value)) {
    if (typeof v === 'boolean') { out[k] = v; continue }
    if (v === '' || v === null || v === undefined) continue
    out[k] = v
  }
  return out
}

function buildDraftSpec(): Partial<Protocol> {
  if (editMode.value === 'new') {
    return {
      id: newSpec.id.trim(), name: newSpec.name.trim() || newSpec.id.trim(),
      category: newSpec.category, group: newSpec.group,
      security: newSpec.security, transport: newSpec.transport,
      config_kind: newSpec.config_kind, default_port: Number(newSpec.default_port) || 443,
      kernels: newSpec.kernels.split(/[\s,，]+/).filter(Boolean),
      features: ['custom'], description: newSpec.description || '在可视化编辑器中新建',
      enabled: true, params: buildParams(),
    }
  }
  const p = allProtocols.value.find((x) => x.id === configTarget.value)
  return p
    ? { ...p, params: buildParams() }
    : { id: configTarget.value, kernels: [kernel.value], params: buildParams() }
}

function refreshSpecText() {
  const plain = JSON.stringify(buildDraftSpec(), null, 2)
  specText.value = showComments.value ? SPEC_COMMENT_HEADER + plain : plain
}

function applyConfigResult(r: ProtocolConfigResult) {
  configFormat.value = r.format || 'json'
  docUrl.value = r.doc_url || currentSchema.value?.doc_url || ''
  plainConfig.value = r.content
  configText.value = showComments.value && r.annotated_content ? r.annotated_content : r.content
  validated.value = r.validated === true
  customized.value = r.customized === true
  note.value = `已按 ${currentSchema.value?.label ?? kernel.value} 的官方语法生成 ${r.format} 配置；下方每个字段上方都有中文注释。`
  refreshSpecText()
}

async function generate() {
  const params = buildParams()
  let r: ProtocolConfigResult | null
  if (editMode.value === 'new') {
    if (!newSpec.id.trim()) {
      window.$message?.warning('请先填写新协议 ID')
      return
    }
    r = await run('config-gen', () => api.previewProtocolConfig({
      spec: buildDraftSpec(), kernel: kernel.value, params,
    }))
  } else {
    if (!configTarget.value.trim()) {
      window.$message?.warning('请先选择协议')
      return
    }
    r = await run('config-gen', () =>
      api.generateProtocolConfig(configTarget.value.trim(), kernel.value, params, false))
  }
  if (!r) return
  applyConfigResult(r)
  codeMode.value = 'config'
}

async function changeKernel(k: string) {
  kernel.value = k
  docUrl.value = currentSchema.value?.doc_url || ''
  const p = editMode.value === 'existing'
    ? allProtocols.value.find((x) => x.id === configTarget.value) ?? null
    : null
  resetFields(p)
  refreshSpecText()
  await generate()
}

async function openEditor(row: Protocol, mode: 'existing' | 'spec') {
  configTarget.value = row.id
  editMode.value = 'existing'
  kernel.value = KERNEL_KEYS.find((k) => hasKernel(row, k)) ?? 'mihomo'
  resetFields(row)
  refreshSpecText()
  if (mode === 'spec') {
    codeMode.value = 'spec'
    note.value = `正在查看 ${row.id} 的协议定义 JSON；下方字段释义表说明每个字段的作用。`
  } else {
    codeMode.value = 'config'
    await generate()
  }
  await nextTick()
  editorCard.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  editorFlashing.value = true
  window.setTimeout(() => { editorFlashing.value = false }, 1400)
}

function startNew() {
  editMode.value = 'new'
  codeMode.value = 'spec'
  newSpec.id = 'my-protocol'
  newSpec.name = '我的自定义协议'
  newSpec.kernels = kernel.value
  resetFields(null)
  refreshSpecText()
  note.value = '填写左侧可视化字段后点「生成配置」预览；点「新建协议」把配置与参数一起保存。'
  editorCard.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

async function toggleComments(on: boolean) {
  showComments.value = on
  if (codeMode.value === 'spec') {
    const plain = stripWholeLineComments(specText.value)
    specText.value = on ? SPEC_COMMENT_HEADER + plain : plain
    return
  }
  const r = await run('annotate', () => api.annotateProtocolConfig({
    content: codeText.value, format: configFormat.value, kernel: kernel.value, on,
  }))
  if (!r) return
  configText.value = r.content
  plainConfig.value = r.plain
}

async function formatCode() {
  const plain = stripWholeLineComments(codeText.value)
  try {
    if (configFormat.value === 'json' || codeMode.value === 'spec') {
      const pretty = JSON.stringify(JSON.parse(plain), null, 2)
      if (codeMode.value === 'config') {
        const r = await run('annotate', () => api.annotateProtocolConfig({
          content: pretty, format: configFormat.value, kernel: kernel.value, on: showComments.value,
        }))
        if (r) {
          configText.value = r.content
          plainConfig.value = r.plain
        }
      } else {
        specText.value = showComments.value ? SPEC_COMMENT_HEADER + pretty : pretty
      }
      note.value = 'JSON 已格式化。'
    } else {
      note.value = `${configFormat.value.toUpperCase()} 保留原样；自动格式化仅支持 JSON。`
    }
  } catch (e) {
    note.value = `格式化失败：${(e as Error).message}`
  }
}

async function validateCode() {
  const fmt = codeMode.value === 'spec' ? 'json' : configFormat.value
  const r = await run('config-validate', () =>
    api.validateProtocolConfig(codeText.value, fmt))
  if (!r) return
  validated.value = r.valid
  note.value = r.valid
    ? `校验通过：${fmt.toUpperCase()} 语法正确（注释行已自动忽略）。`
    : `校验未通过：${r.error ?? '语法错误'}`
}

async function saveCode() {
  const plain = stripWholeLineComments(codeText.value)
  if (!plain.trim()) {
    window.$message?.warning('内容为空')
    return
  }
  if (codeMode.value === 'config') {
    const id = editMode.value === 'new' ? newSpec.id.trim() : configTarget.value.trim()
    if (!id) { window.$message?.warning('请先选择或填写协议 ID'); return }
    if (editMode.value === 'new') {
      window.$message?.warning('新协议请先点「新建协议」，配置会随协议一起保存')
      return
    }
    const r = await run('config-save', () => api.saveProtocolConfig(id, plain, configFormat.value))
    if (!r) return
    customized.value = true
    plainConfig.value = plain
    note.value = `已保存为 ${id} 的自定义配置（注释已自动剥离）。`
    window.$message?.success('自定义配置已保存')
    await refreshProtocols()
    return
  }
  let spec: Record<string, unknown>
  try {
    spec = JSON.parse(plain) as Record<string, unknown>
  } catch (e) {
    window.$message?.error(`协议定义不是合法 JSON：${(e as Error).message}`)
    return
  }
  const id = String(spec.id ?? '')
  if (!id) { window.$message?.error('缺少 id'); return }
  const editing = editMode.value === 'existing'
  const r = editing
    ? await run('spec-update', () => api.updateProtocol(id, spec))
    : await run('spec-create', () => api.createProtocol(spec))
  if (!r) return
  window.$message?.success(editing ? `协议 ${id} 已更新` : `协议 ${id} 已创建`)
  await refreshProtocols()
  if (!editing) view.value = 'custom'
}

/** The main visual-editor action: create a custom protocol OR save changes to
 *  an existing one, carrying the generated config as its template. */
async function saveProtocolFromForm() {
  const spec = buildDraftSpec()
  const plain = plainConfig.value || stripWholeLineComments(configText.value)
  if (plain.trim()) {
    spec.config_content = plain
    spec.config_format = configFormat.value
  }
  spec.params = buildParams()
  const creating = editMode.value === 'new'
  if (creating && !String(spec.id ?? '').trim()) {
    window.$message?.warning('请先填写新协议 ID')
    return
  }
  const r = creating
    ? await run('spec-create', () => api.createProtocol(spec))
    : await run('spec-update', () => api.updateProtocol(configTarget.value, spec))
  if (!r) return
  window.$message?.success(creating ? `协议 ${spec.id} 已创建` : `协议 ${configTarget.value} 已保存`)
  await refreshProtocols()
  if (creating) {
    editMode.value = 'existing'
    configTarget.value = String(spec.id)
    view.value = 'custom'
  }
  refreshSpecText()
}

async function clearSaved() {
  const id = configTarget.value.trim()
  if (!id) return
  const r = await run('config-clear', () => api.clearProtocolConfig(id))
  if (!r) return
  customized.value = false
  note.value = '已清除自定义配置，恢复使用内置模板。'
  await refreshProtocols()
  await generate()
}

async function copyCode() {
  try {
    await navigator.clipboard.writeText(stripWholeLineComments(codeText.value))
    window.$message?.success('已复制（不含注释行）')
  } catch {
    window.$message?.warning('浏览器拒绝了剪贴板访问')
  }
}

function downloadCode() {
  const fmt = codeMode.value === 'spec' ? 'json' : configFormat.value
  const ext = fmt === 'yaml' ? 'yaml' : fmt === 'conf' ? 'conf' : 'json'
  const blob = new Blob([stripWholeLineComments(codeText.value)], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${configTarget.value || newSpec.id || 'ngg-protocol'}.${ext}`
  a.click()
  URL.revokeObjectURL(url)
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
      if (configTarget.value === row.id) configTarget.value = ''
      await refreshProtocols()
    },
  })
}

onMounted(async () => {
  const m = await run('meta', () => api.getProtocolMeta())
  if (m) meta.value = m
  const s = await run('schemas', () => api.getProtocolSchemas())
  if (s) schemas.value = s.schemas
  await refreshProtocols()
  docUrl.value = currentSchema.value?.doc_url || ''
  resetFields(allProtocols.value.find((p) => p.id === 'vmess') ?? null)
  configTarget.value = allProtocols.value.find((p) => p.id === 'vmess')?.id ?? ''
  refreshSpecText()
  await generate()
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">协议中心</h2>
        <p class="page-subtitle">Stage 4 · 按内核分类 · 可视化配置编辑</p>
      </div>
      <NSpace align="center" :size="10" :wrap="true">
        <NButton size="small" :loading="isBusy('protocols')" @click="refreshProtocols">刷新目录</NButton>
        <NButton
          size="small"
          type="primary"
          @click="startNew(); editorCard?.scrollIntoView({ behavior: 'smooth', block: 'start' })"
        >
          新建自定义协议
        </NButton>
      </NSpace>
    </div>

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
          {{ v.label }} <span class="tab-count">{{ viewCounts[v.key] }}</span>
        </button>
      </div>

      <div class="bulk-bar" style="margin-top: 12px">
        <span class="bulk-count">已选 {{ checkedIds.length }} / {{ visibleProtocols.length }}</span>
        <NButton size="small" :disabled="!checkedIds.length" :loading="isBusy('toggle:selected:on')"
          @click="applyToggle(checkedIds, true)">启用选中</NButton>
        <NButton size="small" :disabled="!checkedIds.length" :loading="isBusy('toggle:selected:off')"
          @click="applyToggle(checkedIds, false)">停用选中</NButton>
        <NButton size="small" :disabled="!visibleProtocols.length" :loading="isBusy('toggle:view:on')"
          @click="applyToggle(visibleProtocols.map((p) => p.id), true, 'view')">本类全部启用</NButton>
        <NButton size="small" :disabled="!visibleProtocols.length" :loading="isBusy('toggle:view:off')"
          @click="applyToggle(visibleProtocols.map((p) => p.id), false, 'view')">本类全部停用</NButton>
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
        :scroll-x="1080"
      />
      <div v-if="!visibleProtocols.length" class="hint" style="margin-top: 10px">
        本类没有匹配的协议。可调整搜索条件，或点右上「新建自定义协议」。
      </div>
    </NCard>

    <!-- ② 合并后的编辑区 -->
    <div ref="editorCard">
      <NCard size="small" class="section-gap major-card" :class="{ 'flash-target': editorFlashing }">
        <template #header>
          <div class="major-head">
            <span class="major-title">配置与协议编辑</span>
            <span class="major-sub">GET /protocols/schema · POST /protocols/{id}/config · POST / PUT /protocols</span>
          </div>
        </template>

        <!-- ── 上半：可视化（点击式）编辑 ── -->
        <div class="edit-block">
          <div class="edit-block-head">
            <span class="edit-block-title">① 可视化编辑</span>
            <span class="hint">点选式填写字段；同一组字段会按所选内核的官方语法生成不同配置</span>
          </div>

          <NSpace :size="10" :wrap="true" align="end" style="margin-bottom: 10px">
            <div>
              <div class="kv-label">编辑模式</div>
              <NSpace :size="4">
                <NButton size="small" :type="editMode === 'existing' ? 'primary' : 'default'"
                  @click="editMode = 'existing'; refreshSpecText()">修改现有协议</NButton>
                <NButton size="small" :type="editMode === 'new' ? 'primary' : 'default'" @click="startNew">新建自定义协议</NButton>
              </NSpace>
            </div>
            <div v-if="editMode === 'existing'">
              <div class="kv-label">协议</div>
              <NSelect
                :value="configTarget"
                :options="protocolOptions"
                filterable
                style="width: 240px"
                @update:value="(v: string) => { const p = allProtocols.find(x => x.id === v); if (p) void openEditor(p, 'existing') }"
              />
            </div>
            <div>
              <div class="kv-label">目标内核（语法不同）</div>
              <NSelect :value="kernel" :options="KERNEL_OPTIONS" style="width: 230px"
                @update:value="(v: string) => void changeKernel(v)" />
            </div>
          </NSpace>

          <div v-if="editMode === 'new'" class="new-spec-grid">
            <div><div class="kv-label">协议 ID</div><NInput v-model:value="newSpec.id" size="small" @update:value="refreshSpecText" /></div>
            <div><div class="kv-label">显示名称</div><NInput v-model:value="newSpec.name" size="small" @update:value="refreshSpecText" /></div>
            <div><div class="kv-label">适用内核（空格或逗号分隔）</div><NInput v-model:value="newSpec.kernels" size="small" @update:value="refreshSpecText" /></div>
            <div><div class="kv-label">默认端口</div><NInputNumber v-model:value="newSpec.default_port" size="small" /></div>
            <div><div class="kv-label">分类 category</div>
              <NSelect v-model:value="newSpec.category" size="small"
                :options="Object.entries(CATEGORY_LABEL).map(([value, label]) => ({ label: label + ' (' + value + ')', value }))" />
            </div>
            <div><div class="kv-label">大类 group</div>
              <NSelect v-model:value="newSpec.group" size="small"
                :options="[{label:'加密协议 (encrypted)',value:'encrypted'},{label:'普通未加密 (plaintext)',value:'plaintext'},{label:'普通代理 (plain-proxy)',value:'plain-proxy'}]" />
            </div>
          </div>

          <div class="syntax-note">
            <strong>{{ currentSchema?.label ?? kernel }}</strong> · {{ currentSchema?.syntax }}。
            {{ currentSchema?.description }}
            <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener">{{ currentSchema?.doc_name }} ↗</a>
          </div>

          <NGrid cols="1 m:2" responsive="screen" :x-gap="14" :y-gap="10" class="field-grid">
            <NGi v-for="f in currentSchema?.fields ?? []" :key="f.key">
              <div class="proto-field">
                <div class="proto-field-head">
                  <span class="proto-field-label">{{ f.label }}</span>
                  <span class="mono proto-field-key">{{ f.key }}</span>
                  <span v-if="f.required" class="proto-required">必填</span>
                </div>
                <NInput v-if="f.type === 'text'" :value="String(fieldValues[f.key] ?? '')" size="small"
                  @update:value="(v: string) => setField(f.key, v)" />
                <NInputNumber v-else-if="f.type === 'number'" :value="Number(fieldValues[f.key] ?? 0)" size="small"
                  @update:value="(v: number | null) => setField(f.key, v ?? 0)" />
                <NSwitch v-else-if="f.type === 'bool'" :value="!!fieldValues[f.key]" size="small"
                  @update:value="(v: boolean) => setField(f.key, v)" />
                <NSelect v-else-if="f.type === 'select'" :value="String(fieldValues[f.key] ?? '')" size="small"
                  :options="(f.options ?? []).map((o) => ({ label: o, value: o }))"
                  @update:value="(v: string) => setField(f.key, v)" />
                <div class="proto-field-desc">{{ f.description }}</div>
              </div>
            </NGi>
          </NGrid>

          <NSpace :size="8" :wrap="true" style="margin-top: 12px">
            <NButton size="small" type="primary" :loading="isBusy('config-gen')" @click="generate">生成配置</NButton>
            <NButton size="small" type="primary" :loading="isBusy('spec-create') || isBusy('spec-update')"
              @click="saveProtocolFromForm">
              {{ editMode === 'new' ? '新建协议（含配置）' : '保存协议与配置' }}
            </NButton>
            <NButton size="small" :disabled="editMode === 'new'" :loading="isBusy('config-clear')" @click="clearSaved">清除已存配置</NButton>
            <NButton size="small" @click="resetFields(allProtocols.find((p) => p.id === configTarget) ?? null)">重置字段</NButton>
          </NSpace>
        </div>

        <!-- ── 下半：代码编辑（带注释） ── -->
        <div class="edit-block">
          <div class="edit-block-head">
            <span class="edit-block-title">② 代码编辑</span>
            <span class="hint">每个字段上方都有中文注释；校验 / 保存时自动去掉注释行</span>
          </div>

          <NSpace :size="10" :wrap="true" align="center" style="margin-bottom: 10px">
            <NButton size="small" :type="codeMode === 'config' ? 'primary' : 'default'"
              @click="codeMode = 'config'">配置源码 ({{ configFormat }})</NButton>
            <NButton size="small" :type="codeMode === 'spec' ? 'primary' : 'default'"
              @click="codeMode = 'spec'; refreshSpecText()">协议定义 JSON</NButton>
            <span class="upd-switch" style="margin-left: 8px">
              显示字段注释
              <NSwitch size="small" :value="showComments"
                @update:value="(v: boolean) => void toggleComments(v)" />
            </span>
            <NTag v-if="validated === true" size="small" type="success" :bordered="false">校验通过</NTag>
            <NTag v-else-if="validated === false" size="small" type="error" :bordered="false">校验未通过</NTag>
            <NTag v-if="customized" size="small" type="info" :bordered="false">已保存自定义配置</NTag>
          </NSpace>

          <NInput
            v-model:value="codeText"
            type="textarea"
            class="code-editor-large"
            :rows="16"
            placeholder="点「生成配置」后，这里会出现带中文注释的配置源码"
          />

          <NSpace :size="8" :wrap="true" style="margin-top: 10px">
            <NButton size="small" @click="formatCode">格式化</NButton>
            <NButton size="small" :loading="isBusy('config-validate')" @click="validateCode">校验</NButton>
            <NButton size="small" type="primary" :loading="isBusy('config-save') || isBusy('spec-create') || isBusy('spec-update')"
              @click="saveCode">
              {{ codeMode === 'config' ? '保存配置' : (editMode === 'new' ? '新建协议' : '保存协议定义') }}
            </NButton>
            <NButton size="small" @click="copyCode">复制</NButton>
            <NButton size="small" @click="downloadCode">下载</NButton>
          </NSpace>

          <div class="hint" style="margin-top: 8px">{{ note || '配置源码可直接编辑；注释只是阅读辅助，不会写入已保存的配置。' }}</div>

          <div class="doc-head">
            <span class="proto-sec-title">字段释义 · {{ currentSchema?.label }}（{{ currentSchema?.syntax }}）</span>
            <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener">{{ currentSchema?.doc_name }} ↗</a>
          </div>
          <div class="hint" style="margin-bottom: 8px">
            三个内核的字段名与层级结构各不相同（例如端口在 sing-box 是 <span class="mono">server_port</span>、
            在 Xray 是 <span class="mono">settings.vnext[].port</span>）；上表按当前内核列出，切换内核会自动换一套字段。
            {{ meta ? `服务端支持的配置格式：${meta.formats.join(' / ')}。` : '' }}
          </div>
          <table class="proto-doc-table">
            <thead><tr><th>字段</th><th>名称</th><th>含义 / 作用</th></tr></thead>
            <tbody>
              <tr v-for="f in currentSchema?.fields ?? []" :key="f.key">
                <td class="mono">{{ f.key }}</td>
                <td>{{ f.label }}</td>
                <td>{{ f.description }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </NCard>
    </div>
  </div>
</template>
