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
  NInputNumber,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type {
  GitHubCatalog,
  GitHubMountResult,
  GitHubPreset,
  Rule,
  RuleSetDef,
  RuleSetParsePreview,
  RuleSetSource,
} from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { formatTime, prettyJson, toArray } from '../utils/format'

const { isBusy, run } = useApiAction()

const RULE_TYPES = [
  'DOMAIN', 'DOMAIN-SUFFIX', 'DOMAIN-KEYWORD', 'DOMAIN-REGEX', 'IP-CIDR',
  'DST-PORT', 'NETWORK', 'PROCESS-NAME', 'TENANT', 'MATCH',
].map((v) => ({ label: v, value: v }))
const RULE_ACTIONS = ['PROXY', 'DIRECT', 'REJECT', 'DNS-LOCAL'].map((v) => ({
  label: v,
  value: v,
}))
const SET_SOURCES: Array<{ label: string; value: RuleSetSource }> = [
  { label: '内联内容 inline', value: 'inline' },
  { label: 'URL 订阅链接', value: 'url' },
  { label: '上传附件（由上传区创建）', value: 'upload' },
]

/* ------------------------------------------------------------------ *
 * 分流规则表
 * ------------------------------------------------------------------ */

const rules = ref<Rule[]>([])
const defaultAction = ref('')
const defaultCore = ref('')
const rulesResult = ref<unknown>(null)
const loadPath = ref('')
const editingId = ref<string | null>(null)
// 多选：规则表 / 规则集栏都支持「单选、多选、全部」的启停操作。
const checkedRuleIds = ref<string[]>([])
const checkedSetIds = ref<string[]>([])
const ruleRowKey = (r: Rule) => r.id
const setRowKey = (s: RuleSetDef) => s.id

const ruleForm = ref({
  id: '',
  type: 'DOMAIN',
  value: '',
  action: 'PROXY',
  core_id: '',
  chain: '',
  tenant_id: '',
  priority: 0 as number | null,
  enabled: true,
  comment: '',
})

function buildRule(): Rule {
  return {
    id: ruleForm.value.id.trim(),
    type: ruleForm.value.type,
    value: ruleForm.value.value.trim(),
    action: ruleForm.value.action,
    core_id: ruleForm.value.core_id.trim() || undefined,
    chain: ruleForm.value.chain.trim()
      ? ruleForm.value.chain.split(/[,\s]+/).map((v) => v.trim()).filter(Boolean)
      : undefined,
    tenant_id: ruleForm.value.tenant_id.trim() || undefined,
    priority: ruleForm.value.priority ?? 0,
    enabled: ruleForm.value.enabled,
    comment: ruleForm.value.comment.trim() || undefined,
  }
}

async function refreshRules() {
  const data = await run('rules', () => api.getRules())
  if (data) {
    rules.value = toArray<Rule>(data.rules ?? data)
    defaultAction.value = String(data.default_action ?? '')
    defaultCore.value = String(data.default_core ?? '')
    // 刷新后丢弃已不存在的选中项，避免把不存在的 ID 发给后端。
    checkedRuleIds.value = checkedRuleIds.value.filter((id) =>
      rules.value.some((r) => r.id === id),
    )
  }
}

function chainToText(chain: unknown): string {
  if (Array.isArray(chain)) return chain.join(', ')
  return String(chain ?? '')
}

function scrollToRuleForm() {
  document.getElementById('rule-form-card')?.scrollIntoView({ behavior: 'smooth', block: 'center' })
}

function startEdit(r: Rule) {
  editingId.value = r.id
  if (!checkedRuleIds.value.includes(r.id)) checkedRuleIds.value = [...checkedRuleIds.value, r.id]
  ruleForm.value = {
    id: r.id,
    type: r.type,
    value: r.value,
    action: r.action,
    core_id: r.core_id ?? '',
    chain: chainToText(r.chain),
    tenant_id: r.tenant_id ?? '',
    priority: r.priority ?? 0,
    enabled: r.enabled,
    comment: r.comment ?? '',
  }
  window.$message?.info('已载入规则 ' + r.id + '，修改后点击「保存修改」')
  scrollToRuleForm()
}

function cancelEdit() {
  editingId.value = null
  ruleForm.value = {
    id: '', type: 'DOMAIN', value: '', action: 'PROXY', core_id: '',
    chain: '', tenant_id: '', priority: 0, enabled: true, comment: '',
  }
}

function addNewRule() {
  cancelEdit()
  scrollToRuleForm()
  window.$message?.info('请在下方表单填写新规则，填好后点击「新增规则」')
}

function editSelectedRule() {
  const r = rules.value.find((x) => x.id === checkedRuleIds.value[0])
  if (!r) {
    window.$message?.warning('请先在规则表第一列勾选一条规则，或直接点击行内「编辑」')
    return
  }
  startEdit(r)
}

async function saveRuleAction() {
  if (!ruleForm.value.id.trim() || !ruleForm.value.value.trim()) {
    window.$message?.warning('请填写规则 ID 与匹配值')
    return
  }
  const rule = buildRule()
  const editing = editingId.value
  const r = editing
    ? await run('update-rule', () => api.updateRule(editing, rule), '规则已修改')
    : await run('add-rule', () => api.addRule(rule), '规则已新增')
  if (r) {
    rulesResult.value = r
    cancelEdit()
  }
  await refreshRules()
}

async function replaceRulesAction() {
  const r = await run(
    'replace-rules',
    () =>
      api.replaceRules({
        rules: rules.value,
        default_action: defaultAction.value,
        default_core: defaultCore.value,
      }),
    '规则集已全量替换',
  )
  if (r) rulesResult.value = r
  await refreshRules()
}

function confirmDelete(id: string) {
  window.$dialog?.warning({
    title: '删除规则',
    content: `确定删除规则 ${id} 吗？此操作立即生效。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const r = await run('del:' + id, () => api.deleteRule(id), '规则已删除')
      if (r) {
        rulesResult.value = r
        checkedRuleIds.value = checkedRuleIds.value.filter((x) => x !== id)
        if (editingId.value === id) cancelEdit()
      }
      await refreshRules()
    },
  })
}

function deleteSelectedRule() {
  const id = checkedRuleIds.value[0]
  if (!id) {
    window.$message?.warning('请先在规则表第一列勾选一条规则，或直接点击行内「删除」')
    return
  }
  confirmDelete(id)
}

/* ---- 启用 / 停用：单条、多选、全部 ---------------------------------- */

async function applyRuleToggle(ids: string[], all: boolean, enabled: boolean) {
  if (!all && ids.length === 0) {
    window.$message?.warning('请先勾选至少一条规则，或使用「全部启用 / 全部停用」')
    return
  }
  const key = `toggle-rules:${all ? 'all' : 'selected'}:${enabled ? 'on' : 'off'}`
  const r = await run(key, () =>
    api.toggleRules(all ? { all: true, enabled } : { ids, enabled }),
  )
  if (r) {
    rulesResult.value = r
    window.$message?.success(`已${enabled ? '启用' : '停用'} ${r.changed} 条规则`)
  }
  await refreshRules()
}

async function toggleSingleRule(id: string, enabled: boolean) {
  const r = await run('toggle-rule:' + id, () =>
    api.toggleRules({ ids: [id], enabled }),
  )
  if (r) rulesResult.value = r
  await refreshRules()
}

async function applySetToggle(ids: string[], all: boolean, enabled: boolean) {
  if (!all && ids.length === 0) {
    window.$message?.warning('请先勾选至少一条规则集，或使用「全部启用 / 全部停用」')
    return
  }
  const key = `toggle-sets:${all ? 'all' : 'selected'}:${enabled ? 'on' : 'off'}`
  const r = await run(key, () =>
    api.toggleRuleSets(all ? { all: true, enabled } : { ids, enabled }),
  )
  if (r) {
    setResult.value = r
    window.$message?.success(`已${enabled ? '启用' : '停用'} ${r.changed} 个规则集`)
  }
  await refreshRuleSets()
}

async function toggleSingleSet(id: string, enabled: boolean) {
  const r = await run('toggle-set:' + id, () =>
    api.toggleRuleSets({ ids: [id], enabled }),
  )
  if (r) setResult.value = r
  await refreshRuleSets()
}

async function loadFromFile() {
  if (!loadPath.value.trim()) {
    window.$message?.warning('请填写规则文件路径')
    return
  }
  const r = await run(
    'load-file',
    () => api.loadRulesFromFile(loadPath.value.trim()),
    '规则文件加载命令已下发',
  )
  if (r) rulesResult.value = r
  await refreshRules()
}

function actionTagType(action: string): 'success' | 'error' | 'warning' | 'info' | 'default' {
  switch (action) {
    case 'PROXY':
      return 'info'
    case 'DIRECT':
      return 'success'
    case 'REJECT':
      return 'error'
    case 'DNS-LOCAL':
      return 'warning'
    default:
      return 'default'
  }
}

const ruleColumns: any[] = [
  { type: 'selection', fixed: 'left', width: 48 },
  {
    title: 'ID',
    key: 'id',
    minWidth: 150,
    render: (r: Rule) => h('span', { class: 'mono' }, r.id),
  },
  {
    title: '类型',
    key: 'type',
    minWidth: 160,
    render: (r: Rule) =>
      h(NTag, { size: 'small', bordered: false }, { default: () => r.type }),
  },
  { title: '匹配值', key: 'value', minWidth: 180 },
  {
    title: '动作',
    key: 'action',
    minWidth: 120,
    render: (r: Rule) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: actionTagType(r.action) },
        { default: () => r.action },
      ),
  },
  { title: '内核', key: 'core_id', minWidth: 110, render: (r: Rule) => r.core_id ?? '—' },
  { title: '链', key: 'chain', minWidth: 130, render: (r: Rule) => Array.isArray(r.chain) ? r.chain.join(' → ') : (r.chain ?? '—') },
  { title: '租户', key: 'tenant_id', minWidth: 110, render: (r: Rule) => r.tenant_id ?? '—' },
  { title: '优先级', key: 'priority', width: 90, render: (r: Rule) => r.priority ?? 0 },
  {
    title: '启用',
    key: 'enabled',
    width: 88,
    render: (r: Rule) =>
      h(NSwitch, {
        value: !!r.enabled,
        size: 'small',
        loading: isBusy('toggle-rule:' + r.id),
        'onUpdate:value': (v: boolean) => toggleSingleRule(r.id, v),
      }),
  },
  { title: '备注', key: 'comment', minWidth: 150, render: (r: Rule) => r.comment ?? '—' },
  {
    title: '操作',
    key: 'actions',
    width: 140,
    fixed: 'right',
    render: (r: Rule) =>
      h(NSpace, { size: 6 }, { default: () => [
        h(
          NButton,
          { size: 'tiny', onClick: () => startEdit(r) },
          { default: () => '编辑' },
        ),
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
      ] }),
  },
]

/* ------------------------------------------------------------------ *
 * 自定义规则集（inline / URL 订阅 / 上传附件自动识别）
 * ------------------------------------------------------------------ */

const ruleSets = ref<RuleSetDef[]>([])
const formats = ref<Array<{ id: string; name: string }>>([])
const setResult = ref<unknown>(null)
const parsePreview = ref<RuleSetParsePreview | null>(null)
const uploadedSetName = ref('')
const uploadName = ref('')
const uploadDescription = ref('')
const applyCore = ref('')
const fileInput = ref<HTMLInputElement | null>(null)
const setEditingId = ref<string | null>(null)

const setForm = ref({
  id: '',
  name: '',
  description: '',
  source: 'inline' as RuleSetSource,
  url: '',
  content: '',
  format: 'auto',
  enabled: true,
  filename: '' as string | undefined,
})

const formatOptions = () => [
  { label: '自动识别（推荐）', value: 'auto' },
  ...formats.value.map((f) => ({ label: `${f.id} — ${f.name}`, value: f.id })),
]

async function refreshRuleSets() {
  const data = await run('rulesets', () => api.listRuleSets())
  if (data) {
    ruleSets.value = toArray<RuleSetDef>(data.rulesets ?? data)
    checkedSetIds.value = checkedSetIds.value.filter((id) =>
      ruleSets.value.some((s) => s.id === id),
    )
  }
}

async function refreshFormats() {
  const data = await run('formats', () => api.listRuleSetFormats())
  if (data) formats.value = toArray<{ id: string; name: string }>(data)
}

function resetSetForm() {
  setEditingId.value = null
  setForm.value = {
    id: '', name: '', description: '', source: 'inline', url: '',
    content: '', format: 'auto', enabled: true, filename: undefined,
  }
}

function startSetEdit(s: RuleSetDef) {
  setEditingId.value = s.id
  setForm.value = {
    id: s.id,
    name: s.name,
    description: s.description ?? '',
    source: s.source,
    url: s.url ?? '',
    content: s.content ?? '',
    format: s.format || 'auto',
    enabled: s.enabled,
    filename: s.filename,
  }
  window.$message?.info('已载入规则集 ' + s.id + '，修改后点击「保存修改」')
}

function buildRuleSetPayload(): Partial<RuleSetDef> {
  return {
    id: setForm.value.id.trim(),
    name: setForm.value.name.trim(),
    description: setForm.value.description.trim(),
    source: setForm.value.source,
    url: setForm.value.url.trim(),
    content: setForm.value.content,
    format: setForm.value.format,
    enabled: setForm.value.enabled,
    filename: setForm.value.filename,
  }
}

async function saveRuleSetAction() {
  if (!setForm.value.id.trim() || !setForm.value.name.trim()) {
    window.$message?.warning('请填写规则集 ID 与名称')
    return
  }
  if (setForm.value.source === 'url' && !setForm.value.url.trim()) {
    window.$message?.warning('URL 订阅规则集请填写 http/https 链接')
    return
  }
  if (setForm.value.source !== 'url' && !setForm.value.content.trim()) {
    window.$message?.warning('请填写规则集内容')
    return
  }
  const payload = buildRuleSetPayload()
  const editing = setEditingId.value
  const r = editing
    ? await run('update-ruleset', () => api.updateRuleSet(editing, payload), '规则集已修改')
    : await run('add-ruleset', () => api.addRuleSet(payload), '规则集已新增')
  if (r) {
    setResult.value = r
    resetSetForm()
  }
  await refreshRuleSets()
}

function confirmDeleteSet(s: RuleSetDef) {
  window.$dialog?.warning({
    title: '删除规则集',
    content: `确定删除规则集 ${s.id}（${s.name}）吗？已应用到规则表的规则不会自动撤销。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const r = await run('del-ruleset:' + s.id, () => api.deleteRuleSet(s.id), '规则集已删除')
      if (r) {
        setResult.value = r
        checkedSetIds.value = checkedSetIds.value.filter((x) => x !== s.id)
        if (setEditingId.value === s.id) resetSetForm()
      }
      await refreshRuleSets()
    },
  })
}

async function refreshRuleSetAction(s: RuleSetDef) {
  const r = await run(
    'fetch-ruleset:' + s.id,
    () => api.fetchRuleSet(s.id),
    s.source === 'url' ? '已重新拉取订阅链接并解析' : '已重新解析规则集内容',
  )
  if (r) setResult.value = r
  await refreshRuleSets()
}

async function applyRuleSetAction(s: RuleSetDef) {
  const r = await run(
    'apply-ruleset:' + s.id,
    () => api.applyRuleSet(s.id, applyCore.value.trim() || undefined),
    `规则集 ${s.id} 已合并进实时规则表`,
  )
  if (r) setResult.value = r
  await refreshRules()
}

function pickFile() {
  fileInput.value?.click()
}

async function onFileChange(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  const r = await run(
    'upload-ruleset',
    () => api.uploadRuleSet(file, uploadName.value.trim() || undefined, uploadDescription.value.trim() || undefined),
  )
  // 允许重复选择同一文件
  input.value = ''
  if (r) {
    setResult.value = r
    parsePreview.value = r.parse ?? null
    uploadedSetName.value = `${r.ruleset.id}（${r.ruleset.name}）`
    uploadName.value = ''
    uploadDescription.value = ''
    window.$message?.success(
      `已自动识别为 ${r.parse?.format ?? '未知格式'}，识别 ${r.parse?.count ?? 0} 条规则，并附加到下方规则集栏`,
    )
    // 上传后端立即创建规则集，这里刷新列表把新规则集附加进「规则集栏」
    await refreshRuleSets()
  }
}

function sourceTagType(source: string): 'success' | 'warning' | 'info' | 'default' {
  switch (source) {
    case 'url': return 'info'
    case 'upload': return 'warning'
    case 'inline': return 'success'
    default: return 'default'
  }
}

function sourceLabel(source: string): string {
  switch (source) {
    case 'url': return 'URL 订阅'
    case 'upload': return '上传附件'
    case 'inline': return '内联'
    default: return source || '—'
  }
}

function shortTime(v?: string): string {
  if (!v) return '—'
  return String(v).replace('T', ' ').slice(0, 19)
}

// Fixed widths + per-cell clipping: long GitHub rule-set ids used to paint over
// the pinned 操作 column when the table was narrower than its scroll width.
const setColumns: any[] = [
  { type: 'selection', fixed: 'left', width: 44 },
  {
    title: 'ID',
    key: 'id',
    width: 210,
    ellipsis: { tooltip: true },
    render: (s: RuleSetDef) => h('span', { class: 'mono' }, s.id),
  },
  { title: '名称', key: 'name', width: 140, ellipsis: { tooltip: true } },
  {
    title: '来源',
    key: 'source',
    width: 96,
    render: (s: RuleSetDef) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: sourceTagType(s.source) },
        { default: () => sourceLabel(s.source) },
      ),
  },
  { title: '格式', key: 'format', width: 104, ellipsis: { tooltip: true }, render: (s: RuleSetDef) => s.format || '—' },
  {
    title: '规则数',
    key: 'rule_count',
    width: 84,
    render: (s: RuleSetDef) => s.rule_count ?? (s.rules ? s.rules.length : 0),
  },
  {
    title: '启用',
    key: 'enabled',
    width: 76,
    render: (s: RuleSetDef) =>
      h(NSwitch, {
        value: !!s.enabled,
        size: 'small',
        loading: isBusy('toggle-set:' + s.id),
        'onUpdate:value': (v: boolean) => toggleSingleSet(s.id, v),
      }),
  },
  {
    title: '更新时间',
    key: 'updated_at',
    width: 156,
    ellipsis: { tooltip: true },
    render: (s: RuleSetDef) => shortTime(s.updated_at),
  },
  {
    title: '操作',
    key: 'actions',
    width: 232,
    fixed: 'right',
    render: (s: RuleSetDef) =>
      h(NSpace, { size: 6 }, { default: () => [
        h(NButton, { size: 'tiny', onClick: () => startSetEdit(s) }, { default: () => '编辑' }),
        h(NButton, {
          size: 'tiny',
          loading: isBusy('fetch-ruleset:' + s.id),
          onClick: () => refreshRuleSetAction(s),
        }, { default: () => s.source === 'url' ? '重新拉取' : '重新解析' }),
        h(NButton, {
          size: 'tiny',
          type: 'primary',
          loading: isBusy('apply-ruleset:' + s.id),
          onClick: () => applyRuleSetAction(s),
        }, { default: () => '应用' }),
        h(NButton, {
          size: 'tiny',
          type: 'error',
          loading: isBusy('del-ruleset:' + s.id),
          onClick: () => confirmDeleteSet(s),
        }, { default: () => '删除' }),
      ] }),
  },
]

/* ------------------------------------------------------------------ *
 * GitHub 规则集：自动识别仓库里所有分流规则分类，批量挂载 / 导入
 * ------------------------------------------------------------------ */
const ghPresets = ref<GitHubPreset[]>([])
const ghRepo = ref('blackmatrix7/ios_rule_script')
const ghBranch = ref('master')
const ghFormat = ref('Clash')
const ghCatalog = ref<GitHubCatalog | null>(null)
const ghSearch = ref('')
const ghSelected = ref<string[]>([])
const ghMode = ref<'mount' | 'import' | 'both'>('mount')
const ghAction = ref('DIRECT')
const ghCore = ref('mihomo')
const ghResult = ref<GitHubMountResult | null>(null)
const ghError = ref('')

const ghFormatOptions = computed(() =>
  (ghPresets.value.find((p) => p.repo === ghRepo.value)?.formats ?? ['Clash']).map((f) => ({
    label: f,
    value: f,
  })),
)
const ghPresetOptions = computed(() =>
  ghPresets.value.map((p) => ({ label: p.label, value: p.repo })),
)
const ghModeOptions = [
  { label: '侧挂载为规则集（可随时重新拉取）', value: 'mount' },
  { label: '直接导入规则表（自动添加全部分流规则）', value: 'import' },
  { label: '两者都做', value: 'both' },
]
const ghActionOptions = ['PROXY', 'DIRECT', 'REJECT'].map((v) => ({ label: v, value: v }))

const ghShowAll = ref(false)
const ghVisible = computed(() => {
  const q = ghSearch.value.trim().toLowerCase()
  const all = ghCatalog.value?.categories ?? []
  if (!q) return all
  return all.filter((c) => c.name.toLowerCase().includes(q))
})
// Default to a bounded list so 668 chips don't stall the page; the user can
// expand to the full catalogue with one click and still select every item.
const ghShown = computed(() =>
  ghShowAll.value ? ghVisible.value : ghVisible.value.slice(0, 150),
)

function toggleGhCategory(name: string) {
  const i = ghSelected.value.indexOf(name)
  if (i >= 0) ghSelected.value.splice(i, 1)
  else ghSelected.value.push(name)
}
function selectAllGhAll() {
  ghSelected.value = (ghCatalog.value?.categories ?? []).map((c) => c.name)
  window.$message?.success(`已全选 ${ghSelected.value.length} 个分类；挂载时会自动分批执行`)
}
function selectAllGhVisible() {
  const names = new Set(ghVisible.value.map((c) => c.name))
  ghSelected.value = (ghCatalog.value?.categories ?? [])
    .filter((c) => names.has(c.name))
    .map((c) => c.name)
}
function clearGhSelection() {
  ghSelected.value = []
}

async function loadGhPresets() {
  const data = await run('gh-presets', () => api.getGitHubPresets())
  if (data) ghPresets.value = data.presets ?? []
}

async function loadGhCatalog(force = false) {
  ghError.value = ''
  const data = force
    ? await run('gh-catalog', () => api.refreshGitHubCatalog(ghRepo.value, ghBranch.value, ghFormat.value), '已从 GitHub 刷新规则分类')
    : await run('gh-catalog', () => api.getGitHubCatalog(ghRepo.value, ghBranch.value, ghFormat.value))
  if (data) {
    ghCatalog.value = data
    ghSelected.value = ghSelected.value.filter((n) => data.categories.some((c) => c.name === n))
  } else if (!force) {
    ghError.value = '未能读取规则分类目录；请点「从 GitHub 刷新」重试。'
  }
}

function choosePreset(repo: string) {
  const p = ghPresets.value.find((x) => x.repo === repo)
  if (p) {
    ghBranch.value = p.branch
    if (!p.formats.includes(ghFormat.value)) ghFormat.value = p.formats[0] ?? 'Clash'
  }
  ghCatalog.value = null
  ghSelected.value = []
  ghResult.value = null
  loadGhCatalog(false)
}

const ghBatchSize = 50 // backend hard cap per request
const ghProgress = ref('')

async function mountGh() {
  if (!ghSelected.value.length) {
    window.$message?.warning('请先勾选要挂载的分流规则分类')
    return
  }
  ghResult.value = null
  const batches: string[][] = []
  for (let i = 0; i < ghSelected.value.length; i += ghBatchSize) {
    batches.push(ghSelected.value.slice(i, i + ghBatchSize))
  }
  const combined: GitHubMountResult = {
    ok: true, mode: ghMode.value, results: [], categories: 0,
    recognised: 0, rules_added: 0, mounted: 0, failed: 0, message: '',
  }
  let done = 0
  for (const batch of batches) {
    ghProgress.value = `正在处理第 ${++done} / ${batches.length} 批（${batch.length} 个分类）…`
    const data = await run('gh-mount', () =>
      api.mountGitHubRuleSets({
        repo: ghRepo.value,
        branch: ghBranch.value,
        format: ghFormat.value,
        categories: batch,
        mode: ghMode.value,
        action: ghAction.value,
        core_id: ghCore.value,
        enabled: true,
      }),
    )
    if (!data) {
      ghProgress.value = `第 ${done} 批失败，已停止；前面批次的结果保留。`
      ghResult.value = combined
      await Promise.all([refreshRules(), refreshRuleSets()])
      return
    }
    combined.results.push(...data.results)
    combined.categories += data.categories
    combined.recognised += data.recognised
    combined.rules_added += data.rules_added
    combined.mounted += data.mounted
    combined.failed += data.failed
    ghResult.value = { ...combined, results: [...combined.results] }
  }
  combined.message = `全选全部加载完成：${batches.length} 批 · ${combined.categories} 个分类 · ` +
    `识别 ${combined.recognised} 条规则 · 新增 ${combined.rules_added} 条 · ` +
    `挂载 ${combined.mounted} 个规则集 · 失败 ${combined.failed} 个。`
  ghResult.value = combined
  ghProgress.value = ''
  window.$message?.success(combined.message)
  await Promise.all([refreshRules(), refreshRuleSets()])
}

onMounted(() => {
  refreshRules()
  refreshRuleSets()
  refreshFormats()
  loadGhPresets().then(() => loadGhCatalog(false))
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">规则配置</h2>
        <p class="page-subtitle">配置引擎 · 分流规则、自定义规则集、订阅链接与文档附件自动识别</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="isBusy('rules')" @click="refreshRules">刷新规则</NButton>
        <NButton size="small" :loading="isBusy('rulesets')" @click="refreshRuleSets">刷新规则集</NButton>
      </NSpace>
    </div>

    <NAlert type="info" :bordered="false" class="section-gap">
      <template #header>规则说明</template>
      规则类型支持 DOMAIN / DOMAIN-SUFFIX / DOMAIN-KEYWORD / DOMAIN-REGEX / IP-CIDR /
      DST-PORT / NETWORK / PROCESS-NAME / TENANT / MATCH；动作为 PROXY / DIRECT / REJECT / DNS-LOCAL。
      「全量替换」会把当前表格中的全部规则连同默认动作、默认内核一次性提交。
      规则集支持内联、URL 订阅与上传附件；上传后由后端自动识别格式、解析规则并直接附加到规则集栏。
    </NAlert>

    <NCard size="small" class="section-gap">
      <template #header>
        <div class="major-head">
          <span class="major-title">GitHub 规则集</span>
          <span class="major-sub">自动识别仓库内所有分流规则 · 批量挂载或导入</span>
        </div>
      </template>

      <div class="gh-bar">
        <NSelect
          :value="ghRepo"
          :options="ghPresetOptions"
          size="small"
          style="width: 250px"
          @update:value="choosePreset"
        />
        <NInput v-model:value="ghRepo" size="small" placeholder="owner/name" style="width: 220px" />
        <NInput v-model:value="ghBranch" size="small" placeholder="分支" style="width: 100px" />
        <NSelect v-model:value="ghFormat" :options="ghFormatOptions" size="small" style="width: 120px" />
        <NButton size="small" :loading="isBusy('gh-catalog')" @click="loadGhCatalog(true)">
          从 GitHub 刷新
        </NButton>
        <div class="pc-toolbar-spacer" />
        <span class="hint">
          {{ ghCatalog ? `${ghCatalog.total} 个分类 · ${ghCatalog.source === 'cache' ? '本地缓存' : 'GitHub 已同步'} · ${formatTime(ghCatalog.synced_at)}` : '尚未加载' }}
        </span>
      </div>

      <NAlert v-if="ghError" type="warning" :bordered="false" style="margin-bottom: 10px">
        {{ ghError }}
      </NAlert>
      <div class="hint" style="margin-bottom: 10px">
        {{ ghCatalog?.message || '选择仓库后点「从 GitHub 刷新」，会自动读取 rule/&lt;格式&gt;/ 下全部分流规则分类。' }}
        分类名来自仓库目录，可直接搜索；勾选后选择挂载方式即可一次性把成百上千条规则加入 NGG。
      </div>

      <div class="gh-bar">
        <NInput v-model:value="ghSearch" size="small" placeholder="搜索分类（Google / OpenAI / YouTube …）" clearable style="width: 260px" />
        <NButton size="small" quaternary @click="selectAllGhVisible">全选搜索结果</NButton>
        <NButton size="small" type="primary" tertiary @click="selectAllGhAll">
          全选全部（{{ ghCatalog?.total ?? 0 }}）
        </NButton>
        <NButton size="small" quaternary @click="clearGhSelection">清空</NButton>
        <NButton size="small" quaternary @click="ghShowAll = !ghShowAll">
          {{ ghShowAll ? '收起列表' : `显示全部 ${ghCatalog?.total ?? 0} 个` }}
        </NButton>
        <span class="gh-count">已选 <strong>{{ ghSelected.length }}</strong> / {{ ghCatalog?.total ?? 0 }}</span>
      </div>
      <div v-if="ghProgress" class="gh-progress">{{ ghProgress }}</div>

      <div class="gh-cats">
        <button
          v-for="c in ghShown"
          :key="c.name"
          type="button"
          class="gh-chip"
          :class="{ on: ghSelected.includes(c.name) }"
          @click="toggleGhCategory(c.name)"
        >
          {{ c.name }}
        </button>
        <div v-if="!ghShown.length" class="hint">没有匹配的分类，或目录尚未加载。</div>
      </div>
      <div v-if="ghVisible.length > ghShown.length" class="hint" style="margin-top: 8px">
        仅显示前 {{ ghShown.length }} 个（共 {{ ghVisible.length }} 个）；点「显示全部」可展开全部，或用搜索框缩小范围。
      </div>

      <div class="gh-bar gh-mount">
        <span class="kv-label">挂载方式</span>
        <NSelect v-model:value="ghMode" :options="ghModeOptions" size="small" style="width: 300px" />
        <span class="kv-label">动作</span>
        <NSelect v-model:value="ghAction" :options="ghActionOptions" size="small" style="width: 110px" />
        <span class="kv-label">执行内核</span>
        <NInput v-model:value="ghCore" size="small" placeholder="mihomo" style="width: 120px" />
        <NButton size="small" type="primary" :loading="isBusy('gh-mount')" :disabled="!ghSelected.length" @click="mountGh">
          加载所选（{{ ghSelected.length }}）
        </NButton>
      </div>

      <div v-if="ghResult" class="gh-result">
        <div class="gh-result-sum">{{ ghResult.message }}</div>
        <div class="gh-result-grid">
          <div v-for="r in ghResult.results" :key="r.category" class="gh-result-item" :class="{ bad: !!r.error }">
            <strong>{{ r.category }}</strong>
            <span v-if="r.error">{{ r.error }}</span>
            <span v-else>识别 {{ r.recognised }} 条<span v-if="r.rules_added"> · 导入 {{ r.rules_added }} 条</span><span v-if="r.mounted"> · 已挂载</span></span>
          </div>
        </div>
      </div>
    </NCard>

    <NCard size="small" title="规则表（/network/rules）" class="section-gap">
      <template #header-extra>
        <NSpace :size="8" :wrap="true" align="center">
          <NButton size="small" type="primary" @click="addNewRule">新增规则</NButton>
          <NButton size="small" @click="editSelectedRule">修改规则</NButton>
          <NButton size="small" type="error" @click="deleteSelectedRule">删除规则</NButton>
          <span class="hint">默认动作</span>
          <NInput v-model:value="defaultAction" size="small" style="width: 120px" placeholder="DIRECT" />
          <span class="hint">默认内核</span>
          <NInput v-model:value="defaultCore" size="small" style="width: 120px" placeholder="mihomo" />
          <NButton size="small" :loading="isBusy('replace-rules')" @click="replaceRulesAction">
            全量替换
          </NButton>
        </NSpace>
      </template>
      <div class="hint" style="margin-bottom: 8px">
        第一列支持单选 / 多选；「启用」列可单独开关一条规则，下方工具条支持批量与全部操作。
        顶部「修改 / 删除」针对当前选中的第一条规则，也可直接使用行内按钮。
      </div>
      <div class="bulk-bar">
        <span class="bulk-count">已选 {{ checkedRuleIds.length }} / {{ rules.length }}</span>
        <NButton size="small" :disabled="!checkedRuleIds.length" :loading="isBusy('toggle-rules:selected:on')" @click="applyRuleToggle(checkedRuleIds, false, true)">启用选中</NButton>
        <NButton size="small" :disabled="!checkedRuleIds.length" :loading="isBusy('toggle-rules:selected:off')" @click="applyRuleToggle(checkedRuleIds, false, false)">停用选中</NButton>
        <NButton size="small" :loading="isBusy('toggle-rules:all:on')" @click="applyRuleToggle([], true, true)">全部启用</NButton>
        <NButton size="small" :loading="isBusy('toggle-rules:all:off')" @click="applyRuleToggle([], true, false)">全部停用</NButton>
      </div>
      <NDataTable
        v-model:checked-row-keys="checkedRuleIds"
        :row-key="ruleRowKey"
        :columns="ruleColumns"
        :data="rules"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="1660"
      />
    </NCard>

    <NCard
      id="rule-form-card"
      size="small"
      :title="editingId ? '编辑规则 ' + editingId + '（PUT /network/rules/{id}）' : '新增规则（POST /network/rules）'"
      class="section-gap"
    >
      <NGrid cols="1 m:2" responsive="screen" :x-gap="14" :y-gap="14">
        <NGi>
          <NSpace vertical :size="10" style="width: 100%">
            <NGrid cols="2" :x-gap="10" :y-gap="8">
              <NGi>
                <div class="kv-label">ID（必填）</div>
                <NInput v-model:value="ruleForm.id" placeholder="rule-1" :disabled="!!editingId" />
              </NGi>
              <NGi>
                <div class="kv-label">类型</div>
                <NSelect v-model:value="ruleForm.type" :options="RULE_TYPES" />
              </NGi>
              <NGi :span="2">
                <div class="kv-label">匹配值（必填）</div>
                <NInput v-model:value="ruleForm.value" placeholder="example.com" />
              </NGi>
              <NGi>
                <div class="kv-label">动作</div>
                <NSelect v-model:value="ruleForm.action" :options="RULE_ACTIONS" />
              </NGi>
              <NGi>
                <div class="kv-label">优先级</div>
                <NInputNumber v-model:value="ruleForm.priority" style="width: 100%" />
              </NGi>
              <NGi>
                <div class="kv-label">内核 core_id</div>
                <NInput v-model:value="ruleForm.core_id" placeholder="留空 = 默认" />
              </NGi>
              <NGi>
                <div class="kv-label">链 chain</div>
                <NInput v-model:value="ruleForm.chain" placeholder="可选" />
              </NGi>
              <NGi>
                <div class="kv-label">租户 tenant_id</div>
                <NInput v-model:value="ruleForm.tenant_id" placeholder="可选" />
              </NGi>
              <NGi>
                <div class="kv-label">启用</div>
                <NSwitch v-model:value="ruleForm.enabled" />
              </NGi>
              <NGi :span="2">
                <div class="kv-label">备注 comment</div>
                <NInput v-model:value="ruleForm.comment" placeholder="可选" />
              </NGi>
            </NGrid>
            <NSpace :size="8">
              <NButton
                size="small"
                type="primary"
                :loading="isBusy('add-rule') || isBusy('update-rule')"
                @click="saveRuleAction"
              >
                {{ editingId ? '保存修改' : '新增规则' }}
              </NButton>
              <NButton v-if="editingId" size="small" @click="cancelEdit">取消编辑</NButton>
            </NSpace>
          </NSpace>
        </NGi>

        <NGi>
          <NCard size="small" title="从文件加载 / 操作返回">
            <NSpace vertical :size="10" style="width: 100%">
              <div class="hint">POST /network/rules/load · body {"path": "..."}</div>
              <NInput v-model:value="loadPath" placeholder="规则文件路径，例如 /etc/ngg/rules.yaml" />
              <NButton size="small" :loading="isBusy('load-file')" @click="loadFromFile">
                从文件加载
              </NButton>
              <div class="hint">最近一次操作返回（如实展示）：</div>
              <pre class="mono output-block" style="max-height: 220px">{{ prettyJson(rulesResult) }}</pre>
            </NSpace>
          </NCard>
        </NGi>
      </NGrid>
    </NCard>

    <!-- ---------------------------------------------------------------- *
     * 规则集栏：上传 / 链接 / 内联统一在这里展示与管理
     * ---------------------------------------------------------------- -->
    <NCard size="small" class="section-gap">
      <template #header>
        <NSpace :size="8" align="center">
          <span style="font-weight: 600">规则集栏（/rulesets）</span>
          <NTag size="small" :bordered="false" type="info">{{ ruleSets.length }} 个规则集</NTag>
        </NSpace>
      </template>
      <template #header-extra>
        <NSpace :size="8" :wrap="true" align="center">
          <NButton size="small" type="primary" @click="pickFile">上传文档附件（自动识别）</NButton>
          <NButton size="small" @click="resetSetForm">新增规则集</NButton>
          <NButton v-if="setEditingId" size="small" @click="resetSetForm">取消编辑</NButton>
          <span class="hint">应用执行内核</span>
          <NInput v-model:value="applyCore" size="small" style="width: 130px" placeholder="留空 = 规则表默认内核" />
        </NSpace>
      </template>

      <input
        ref="fileInput"
        type="file"
        style="display: none"
        accept=".txt,.yaml,.yml,.json,.list,.conf,.rules,.adg,.csv,.dat"
        @change="onFileChange"
      />

      <NAlert type="default" :bordered="false" style="margin-bottom: 12px">
        <template #header>自动识别说明</template>
        上传 <span class="mono">.yaml / .json / .txt / .list</span> 等文档后，后端会自动识别
        Clash/Mihomo <span class="mono">payload:</span> / <span class="mono">rules:</span> YAML、
        JSON 规则数组、逗号分隔规则行、纯域名列表、IP/CIDR 列表与 AdGuard
        <span class="mono">||domain^</span> 语法；识别成功的规则集会<strong>直接附加到下方规则集栏</strong>，
        无法识别的行只记为 warning，绝不伪装成规则。
        <div v-if="formats.length" class="hint" style="margin-top: 6px">
          支持的格式：{{ formats.map((f) => f.id).join(' / ') }}
        </div>
      </NAlert>

      <NAlert
        v-if="parsePreview"
        type="success"
        :bordered="false"
        style="margin-bottom: 12px"
        closable
        @close="parsePreview = null"
      >
        <template #header>上传已自动识别并附加：{{ uploadedSetName }}</template>
        <div>
          格式 <strong class="mono">{{ parsePreview.format }}</strong> ·
          识别 {{ parsePreview.count }} 条规则 ·
          warning {{ parsePreview.warnings?.length ?? 0 }} 条
        </div>
        <div v-if="parsePreview.warnings?.length" class="hint">
          {{ parsePreview.warnings.join('；') }}
        </div>
        <pre v-if="parsePreview.sample?.length" class="mono output-block" style="max-height: 130px; margin-top: 6px">{{
          parsePreview.sample.map((p) => `${p.type},${p.value},${p.action}`).join('\n')
        }}</pre>
      </NAlert>

      <div class="bulk-bar">
        <span class="bulk-count">已选 {{ checkedSetIds.length }} / {{ ruleSets.length }}</span>
        <NButton size="small" :disabled="!checkedSetIds.length" :loading="isBusy('toggle-sets:selected:on')" @click="applySetToggle(checkedSetIds, false, true)">启用选中</NButton>
        <NButton size="small" :disabled="!checkedSetIds.length" :loading="isBusy('toggle-sets:selected:off')" @click="applySetToggle(checkedSetIds, false, false)">停用选中</NButton>
        <NButton size="small" :loading="isBusy('toggle-sets:all:on')" @click="applySetToggle([], true, true)">全部启用</NButton>
        <NButton size="small" :loading="isBusy('toggle-sets:all:off')" @click="applySetToggle([], true, false)">全部停用</NButton>
      </div>
      <NDataTable
        v-model:checked-row-keys="checkedSetIds"
        :row-key="setRowKey"
        :columns="setColumns"
        :data="ruleSets"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="1142"
      />
      <div v-if="!ruleSets.length" class="hint" style="margin-top: 10px">
        暂无规则集。可以上传文档、填写订阅链接，或在下方新增内联规则集。
      </div>
    </NCard>

    <NGrid cols="1 m:2" responsive="screen" :x-gap="14" :y-gap="14" class="section-gap">
      <NGi>
        <NCard
          size="small"
          :title="setEditingId ? '编辑规则集 ' + setEditingId + '（PUT /rulesets/{id}）' : '新增规则集（POST /rulesets）'"
        >
          <NSpace vertical :size="10" style="width: 100%">
            <NGrid cols="2" :x-gap="10" :y-gap="8">
              <NGi>
                <div class="kv-label">ID（必填）</div>
                <NInput v-model:value="setForm.id" placeholder="rs-my-list" :disabled="!!setEditingId" />
              </NGi>
              <NGi>
                <div class="kv-label">名称（必填）</div>
                <NInput v-model:value="setForm.name" placeholder="我的规则集" />
              </NGi>
              <NGi>
                <div class="kv-label">来源</div>
                <NSelect v-model:value="setForm.source" :options="SET_SOURCES" />
              </NGi>
              <NGi>
                <div class="kv-label">格式</div>
                <NSelect v-model:value="setForm.format" :options="formatOptions()" />
              </NGi>
              <NGi v-if="setForm.source === 'url'" :span="2">
                <div class="kv-label">订阅链接（http / https，保存时真实拉取）</div>
                <NInput v-model:value="setForm.url" placeholder="https://example.com/rules.yaml" />
              </NGi>
              <NGi :span="2">
                <div class="kv-label">
                  {{ setForm.source === 'url' ? '内容（保存时由链接覆盖，可留空）' : '规则内容（必填）' }}
                </div>
                <NInput
                  v-model:value="setForm.content"
                  type="textarea"
                  :autosize="{ minRows: 5, maxRows: 12 }"
                  placeholder="DOMAIN-SUFFIX,example.com,PROXY"
                />
              </NGi>
              <NGi>
                <div class="kv-label">描述</div>
                <NInput v-model:value="setForm.description" placeholder="可选" />
              </NGi>
              <NGi>
                <div class="kv-label">启用</div>
                <NSwitch v-model:value="setForm.enabled" />
              </NGi>
            </NGrid>
            <NSpace :size="8">
              <NButton
                size="small"
                type="primary"
                :loading="isBusy('add-ruleset') || isBusy('update-ruleset')"
                @click="saveRuleSetAction"
              >
                {{ setEditingId ? '保存修改' : '新增规则集' }}
              </NButton>
              <NButton v-if="setEditingId" size="small" @click="resetSetForm">取消编辑</NButton>
            </NSpace>
          </NSpace>
        </NCard>
      </NGi>

      <NGi>
        <NCard size="small" title="上传附件（可选命名）/ 最近一次操作返回">
          <NSpace vertical :size="10" style="width: 100%">
            <div class="hint">POST /rulesets/upload · multipart form-data: file, name, description</div>
            <NInput v-model:value="uploadName" placeholder="规则集名称（留空则使用文件名）" />
            <NInput v-model:value="uploadDescription" placeholder="描述（可选）" />
            <NButton size="small" :loading="isBusy('upload-ruleset')" @click="pickFile">
              选择文档并自动识别
            </NButton>
            <div class="hint">
              上传成功后立即生成一条 <span class="mono">source=upload</span> 的规则集并附加到规则集栏，
              可再点击该行的「应用」把规则合并进实时规则表。
            </div>
            <div class="hint">最近一次操作返回（如实展示）：</div>
            <pre class="mono output-block" style="max-height: 260px">{{ prettyJson(setResult) }}</pre>
          </NSpace>
        </NCard>
      </NGi>
    </NGrid>
  </div>
</template>
