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
import type { Rule } from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { makeColumns } from '../composables/tableColumns'
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

const dnsStatus = ref<unknown>(null)
const resolveForm = ref({ name: '', type: 'A' })
const resolveResult = ref<unknown>(null)
const hostsText = ref('{\n  "example.local": "127.0.0.1"\n}')
const blockText = ref('')
const dnsResult = ref<unknown>(null)

function buildRule(): Rule {
  return {
    id: ruleForm.value.id.trim(),
    type: ruleForm.value.type,
    value: ruleForm.value.value.trim(),
    action: ruleForm.value.action,
    core_id: ruleForm.value.core_id.trim() || undefined,
    chain: ruleForm.value.chain.trim() || undefined,
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

async function addRuleAction() {
  if (!ruleForm.value.id.trim() || !ruleForm.value.value.trim()) {
    window.$message?.warning('请填写规则 ID 与匹配值')
    return
  }
  const r = await run('add-rule', () => api.addRule(buildRule()), '规则已新增')
  if (r) rulesResult.value = r
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

async function refreshDnsStatus() {
  const d = await run('dns-status', () => api.getDnsStatus())
  if (d) dnsStatus.value = d
}

async function doResolve() {
  if (!resolveForm.value.name.trim()) {
    window.$message?.warning('请填写要解析的域名')
    return
  }
  const r = await run('resolve', () =>
    api.resolveDns(resolveForm.value.name.trim(), resolveForm.value.type.trim() || 'A'),
  )
  if (r) resolveResult.value = r
}

async function setHosts() {
  let hosts: Record<string, string>
  try {
    hosts = JSON.parse(hostsText.value)
  } catch {
    window.$message?.error('hosts 必须是合法的 JSON 对象')
    return
  }
  const block = blockText.value
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
  const r = await run(
    'hosts',
    () => api.setDnsHosts({ hosts, block }),
    'DNS hosts 已更新',
  )
  if (r) dnsResult.value = r
  await refreshDnsStatus()
}

const answers = computed(() =>
  toArray((resolveResult.value as Record<string, unknown> | null)?.answers ?? resolveResult.value),
)
const answerColumns = computed(() => makeColumns(answers.value))

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
  { title: '链', key: 'chain', minWidth: 110, render: (r: Rule) => r.chain ?? '—' },
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
    width: 100,
    fixed: 'right',
    render: (r: Rule) =>
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

onMounted(() => {
  refreshRules()
  refreshDnsStatus()
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">规则与 DNS</h2>
        <p class="page-subtitle">Stage 3 · 分流规则集、文件加载与本地 DNS</p>
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
        <NCard size="small" title="新增规则（POST /network/rules）">
          <NSpace vertical :size="10" style="width: 100%">
            <NGrid cols="2" :x-gap="10" :y-gap="8">
              <NGi>
                <div class="kv-label">ID（必填）</div>
                <NInput v-model:value="ruleForm.id" placeholder="rule-1" />
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
            <NButton size="small" type="primary" :loading="isBusy('add-rule')" @click="addRuleAction">
              新增规则
            </NButton>
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

    <NCard size="small" title="DNS 状态（/network/dns）" class="section-gap">
      <template #header-extra>
        <NButton size="small" :loading="isBusy('dns-status')" @click="refreshDnsStatus">刷新</NButton>
      </template>
      <pre class="mono output-block" style="max-height: 220px">{{ prettyJson(dnsStatus) }}</pre>
    </NCard>

    <NGrid cols="1 m:2" responsive="screen" :x-gap="14" :y-gap="14" class="section-gap">
      <NGi>
        <NCard size="small" title="解析测试（POST /network/dns/resolve）">
          <NSpace vertical :size="10" style="width: 100%">
            <NSpace :size="8" :wrap="true" align="center">
              <NInput v-model:value="resolveForm.name" style="max-width: 240px" placeholder="域名，例如 example.com" />
              <NInput v-model:value="resolveForm.type" style="width: 90px" placeholder="A" />
              <NButton size="small" type="primary" :loading="isBusy('resolve')" @click="doResolve">
                解析
              </NButton>
            </NSpace>
            <div v-if="resolveResult">
              <div class="hint">answers：</div>
              <NDataTable
                v-if="answers.length"
                :columns="answerColumns"
                :data="answers"
                :bordered="false"
                size="small"
                :pagination="false"
                :scroll-x="600"
              />
              <pre class="mono output-block" style="max-height: 200px">{{ prettyJson(resolveResult) }}</pre>
            </div>
          </NSpace>
        </NCard>
      </NGi>

      <NGi>
        <NCard size="small" title="Hosts / 拦截（POST /network/dns/hosts）">
          <NSpace vertical :size="10" style="width: 100%">
            <div class="hint">hosts（JSON 对象：域名 → IP）：</div>
            <NInput v-model:value="hostsText" type="textarea" class="code-editor" :rows="5" />
            <div class="hint">block（拦截域名，逗号或换行分隔）：</div>
            <NInput v-model:value="blockText" type="textarea" :rows="2" placeholder="ads.example.com" />
            <NButton size="small" type="primary" :loading="isBusy('hosts')" @click="setHosts">
              提交
            </NButton>
            <pre v-if="dnsResult" class="mono output-block" style="max-height: 160px">{{ prettyJson(dnsResult) }}</pre>
          </NSpace>
        </NCard>
      </NGi>
    </NGrid>
  </div>
</template>
