<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NGi,
  NGrid,
  NInput,
  NInputNumber,
  NRadio,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type { Rule, RuleSetDef, RuleSetParsePreview, RuleSetSource } from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { prettyJson, toArray } from '../utils/format'

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
const selectedRuleId = ref('')

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
  selectedRuleId.value = r.id
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
  const r = rules.value.find((x) => x.id === selectedRuleId.value)
  if (!r) {
    window.$message?.warning('请先在规则表第一列选中一条规则，或直接点击行内「编辑」')
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
        if (selectedRuleId.value === id) selectedRuleId.value = ''
        if (editingId.value === id) cancelEdit()
      }
      await refreshRules()
    },
  })
}

function deleteSelectedRule() {
  if (!selectedRuleId.value) {
    window.$message?.warning('请先在规则表第一列选中一条规则，或直接点击行内「删除」')
    return
  }
  confirmDelete(selectedRuleId.value)
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
  {
    title: '选择',
    key: 'pick',
    width: 60,
    fixed: 'left',
    render: (r: Rule) =>
      h(NRadio, {
        checked: selectedRuleId.value === r.id,
        'onUpdate:checked': (v: boolean) => {
          if (v) selectedRuleId.value = r.id
        },
      }),
  },
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
    width: 80,
    render: (r: Rule) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: r.enabled ? 'success' : 'default' },
        { default: () => (r.enabled ? '是' : '否') },
      ),
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
  if (data) ruleSets.value = toArray<RuleSetDef>(data.rulesets ?? data)
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

const setColumns: any[] = [
  { title: 'ID', key: 'id', minWidth: 170, render: (s: RuleSetDef) => h('span', { class: 'mono' }, s.id) },
  { title: '名称', key: 'name', minWidth: 150 },
  {
    title: '来源',
    key: 'source',
    minWidth: 110,
    render: (s: RuleSetDef) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: sourceTagType(s.source) },
        { default: () => sourceLabel(s.source) },
      ),
  },
  { title: '格式', key: 'format', minWidth: 130, render: (s: RuleSetDef) => s.format || '—' },
  {
    title: '规则数',
    key: 'rule_count',
    width: 90,
    render: (s: RuleSetDef) => s.rule_count ?? (s.rules ? s.rules.length : 0),
  },
  {
    title: '启用',
    key: 'enabled',
    width: 80,
    render: (s: RuleSetDef) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: s.enabled ? 'success' : 'default' },
        { default: () => (s.enabled ? '是' : '否') },
      ),
  },
  { title: '更新时间', key: 'updated_at', minWidth: 160, render: (s: RuleSetDef) => shortTime(s.updated_at) },
  {
    title: '操作',
    key: 'actions',
    width: 250,
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

onMounted(() => {
  refreshRules()
  refreshRuleSets()
  refreshFormats()
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">规则</h2>
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
        顶部「修改 / 删除」针对第一列选中的规则；也可直接使用每行右侧的行内按钮。
      </div>
      <NDataTable
        :columns="ruleColumns"
        :data="rules"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="1620"
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

      <NDataTable
        :columns="setColumns"
        :data="ruleSets"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="1250"
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
