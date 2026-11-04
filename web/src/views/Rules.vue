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
  NSelect,
  NSpace,
  NSwitch,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type { Rule } from '../api/ngg'
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

const rules = ref<Rule[]>([])
const defaultAction = ref('')
const defaultCore = ref('')
const rulesResult = ref<unknown>(null)
const loadPath = ref('')
const editingId = ref<string | null>(null)

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

function startEdit(r: Rule) {
  editingId.value = r.id
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
}

function cancelEdit() {
  editingId.value = null
  ruleForm.value = {
    id: '', type: 'DOMAIN', value: '', action: 'PROXY', core_id: '',
    chain: '', tenant_id: '', priority: 0, enabled: true, comment: '',
  }
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
    content: `确定删除规则 ${id} 吗？`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const r = await run('del:' + id, () => api.deleteRule(id), '规则已删除')
      if (r) rulesResult.value = r
      await refreshRules()
    },
  })
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

onMounted(() => {
  refreshRules()
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">规则</h2>
        <p class="page-subtitle">配置引擎 · 分流规则集、优先级与规则文件加载</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="isBusy('rules')" @click="refreshRules">刷新规则</NButton>
      </NSpace>
    </div>

    <NAlert type="info" :bordered="false" class="section-gap">
      <template #header>规则说明</template>
      规则类型支持 DOMAIN / DOMAIN-SUFFIX / DOMAIN-KEYWORD / DOMAIN-REGEX / IP-CIDR /
      DST-PORT / NETWORK / PROCESS-NAME / TENANT / MATCH；动作为 PROXY / DIRECT / REJECT / DNS-LOCAL。
      「全量替换」会把当前表格中的全部规则连同默认动作、默认内核一次性提交。
    </NAlert>

    <NCard size="small" title="规则表（/network/rules）" class="section-gap">
      <template #header-extra>
        <NSpace :size="8" :wrap="true" align="center">
          <span class="hint">默认动作</span>
          <NInput v-model:value="defaultAction" size="small" style="width: 130px" placeholder="DIRECT" />
          <span class="hint">默认内核</span>
          <NInput v-model:value="defaultCore" size="small" style="width: 130px" placeholder="mihomo" />
          <NButton size="small" type="primary" :loading="isBusy('replace-rules')" @click="replaceRulesAction">
            全量替换
          </NButton>
        </NSpace>
      </template>
      <NDataTable
        :columns="ruleColumns"
        :data="rules"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="1500"
      />
    </NCard>

    <NGrid cols="1 m:2" responsive="screen" :x-gap="14" :y-gap="14" class="section-gap">
      <NGi>
        <NCard size="small" :title="editingId ? '编辑规则 ' + editingId + '（PUT /network/rules/{id}）' : '新增规则（POST /network/rules）'">
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
        </NCard>
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


  </div>
</template>
