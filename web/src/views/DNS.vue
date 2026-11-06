<script setup lang="ts">
import ChainStrip from '../components/ChainStrip.vue'
import { computed, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NGi,
  NGrid,
  NInput,
  NInputNumber,
  NSpace,
  NSwitch,
  NTabPane,
  NTabs,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type {
  DnsEngineConfig,
  KernelConfigField,
  KernelConfigSchema,
  NetworkConfigPatchResult,
} from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { makeColumns } from '../composables/tableColumns'
import { prettyJson, toArray } from '../utils/format'

const { isBusy, run } = useApiAction()

/* ---------- 内核 DNS 配置（三内核官方 schema） ---------- */
const schemas = ref<KernelConfigSchema[]>([])
const activeKernel = ref<string>('')
const editors = ref<Record<string, string>>({})
const validateState = ref<Record<string, { ok: boolean; message: string }>>({})

const fieldColumns: any[] = [
  { title: 'Key', key: 'key', minWidth: 150 },
  { title: 'Label', key: 'label', minWidth: 150 },
  { title: 'Type', key: 'type', minWidth: 90 },
  {
    title: 'Default', key: 'default', minWidth: 160,
    render: (r: KernelConfigField) => {
      const d = r.default as unknown
      return typeof d === 'string' ? d : prettyJson(d)
    },
  },
  {
    title: 'Required', key: 'required', minWidth: 90,
    render: (r: KernelConfigField) => (r.required ? '是' : '否'),
  },
]

function setEditor(kernel: string, value: string) {
  editors.value = { ...editors.value, [kernel]: value }
}

async function loadTemplate(s: KernelConfigSchema) {
  const p = await run('template:' + s.kernel, () => api.getProfileTemplate(s.kernel, 'DNS'))
  if (!p) return
  const sec = (p.sections || []).find((x) => x.key === 'dns')
  setEditor(s.kernel, sec?.content ?? p.sections?.[0]?.content ?? s.example ?? '')
}

async function validateDns(s: KernelConfigSchema) {
  const r = await run('validate:' + s.kernel, () =>
    api.validateProfile({
      name: 'dns-preview',
      kernel: s.kernel,
      format: s.format,
      sections: [{
        key: 'dns',
        label: 'DNS',
        hint: '',
        doc_url: s.doc_url,
        content: editors.value[s.kernel] ?? '',
      }],
    }),
  )
  if (r) {
    validateState.value = {
      ...validateState.value,
      [s.kernel]: { ok: r.ok && r.validated, message: r.message },
    }
  }
}

const status = ref<unknown>(null)
const configResult = ref<NetworkConfigPatchResult | null>(null)
const dnsResult = ref<unknown>(null)
const resolveForm = ref({ name: '', type: 'A' })
const resolveResult = ref<unknown>(null)

const emptyDns = (): DnsEngineConfig => ({
  enabled: true,
  listen: '127.0.0.1',
  port: 18053,
  upstreams: [],
  cache_ttl: 60,
  hosts: {},
  block: [],
})
const form = ref<DnsEngineConfig>(emptyDns())
const upstreamsText = ref('')
const hostsText = ref('{}')
const blockText = ref('')
const quickHostsText = ref('{}')
const quickBlockText = ref('')

function fillForm(dns: unknown) {
  if (!dns || typeof dns !== 'object') return
  const d = dns as Partial<DnsEngineConfig>
  form.value = {
    enabled: d.enabled ?? true,
    listen: d.listen ?? '127.0.0.1',
    port: d.port ?? 18053,
    upstreams: Array.isArray(d.upstreams) ? [...d.upstreams] : [],
    cache_ttl: d.cache_ttl ?? 60,
    hosts: d.hosts && typeof d.hosts === 'object' ? { ...d.hosts } : {},
    block: Array.isArray(d.block) ? [...d.block] : [],
  }
  upstreamsText.value = form.value.upstreams.join('\n')
  hostsText.value = JSON.stringify(form.value.hosts, null, 2)
  blockText.value = form.value.block.join('\n')
}

async function refreshAll() {
  const s = await run('status', () => api.getDnsStatus())
  if (s) status.value = s
  const c = await run('config', () => api.getNetworkConfig())
  if (c) fillForm((c as unknown as Record<string, unknown>).dns)
}

async function saveDnsConfig() {
  let hosts: Record<string, string>
  try {
    hosts = JSON.parse(hostsText.value || '{}')
  } catch {
    window.$message?.error('hosts 必须是合法的 JSON 对象')
    return
  }
  if (form.value.port < 1 || form.value.port > 65535) {
    window.$message?.error('监听端口必须在 1 ~ 65535 之间')
    return
  }
  const dns: DnsEngineConfig = {
    enabled: form.value.enabled,
    listen: form.value.listen.trim() || '127.0.0.1',
    port: form.value.port,
    upstreams: upstreamsText.value.split(/[\n,]+/).map((s) => s.trim()).filter(Boolean),
    cache_ttl: form.value.cache_ttl,
    hosts,
    block: blockText.value.split(/[\n,]+/).map((s) => s.trim()).filter(Boolean),
  }
  const r = await run('save', () => api.patchNetworkConfig({ dns }), 'DNS 引擎配置已保存')
  if (r) configResult.value = r
  await refreshAll()
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

async function applyQuickHosts() {
  let hosts: Record<string, string>
  try {
    hosts = JSON.parse(quickHostsText.value || '{}')
  } catch {
    window.$message?.error('hosts 必须是合法的 JSON 对象')
    return
  }
  const block = quickBlockText.value.split(/[\n,]+/).map((s) => s.trim()).filter(Boolean)
  const r = await run('hosts', () => api.setDnsHosts({ hosts, block }), 'Hosts / 拦截策略已即时生效')
  if (r) dnsResult.value = r
  await refreshAll()
}

const answers = computed(() =>
  toArray((resolveResult.value as Record<string, unknown> | null)?.answers ?? resolveResult.value),
)
const answerColumns = computed(() => makeColumns(answers.value))

const statusRows = computed(() => {
  if (!status.value || typeof status.value !== 'object') return []
  return Object.entries(status.value as Record<string, unknown>).map(([k, v]) => ({
    field: k,
    value: typeof v === 'object' ? JSON.stringify(v) : String(v),
  }))
})
const statusColumns: any[] = [
  { title: '字段', key: 'field', minWidth: 180 },
  { title: '当前值', key: 'value', minWidth: 260 },
]

onMounted(async () => {
  await refreshAll()
  const r = await run('schemas', () => api.getDnsSchemas())
  const list = r?.schemas ?? []
  schemas.value = list
  if (list.length) {
    activeKernel.value = list[0].kernel
    const ed: Record<string, string> = {}
    for (const s of list) ed[s.kernel] = s.example ?? ''
    editors.value = ed
    await Promise.all(list.map(loadTemplate))
  }
})
</script>

<template>
  <div class="page">
    <ChainStrip current="dns" />
    <div class="page-head">
      <div>
        <h2 class="page-title">DNS配置</h2>
        <p class="page-subtitle">配置引擎 · NGG 自有 DNS 引擎、解析策略与 Hosts 管理</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="isBusy('status') || isBusy('config')" @click="refreshAll">
          刷新
        </NButton>
      </NSpace>
    </div>

    
    <NCard size="small" title="内核 DNS 配置" class="section-gap">
      <NTabs v-model:value="activeKernel" type="line" size="small">
        <NTabPane
          v-for="s in schemas"
          :key="s.kernel"
          :name="s.kernel"
          :tab="s.label || s.kernel"
        >
          <NSpace vertical :size="12" style="width: 100%">
            <NDataTable
              :columns="fieldColumns"
              :data="s.fields"
              :bordered="false"
              size="small"
              :pagination="false"
              :scroll-x="640"
            />
            <a :href="s.doc_url" target="_blank" rel="noopener noreferrer" class="mono">{{ s.doc_name }}</a>
            <pre class="mono output-block" style="max-height: 200px">{{ s.example }}</pre>
            <NInput
              :value="editors[s.kernel] ?? ''"
              :rows="8"
              type="textarea"
              class="code-editor"
              @update:value="(v: any) => setEditor(s.kernel, String(v))"
            />
            <NSpace :size="8" align="center" :wrap="true">
              <NButton size="small" :loading="isBusy('template:' + s.kernel)" @click="loadTemplate(s)">载入模板</NButton>
              <NButton size="small" type="primary" :loading="isBusy('validate:' + s.kernel)" @click="validateDns(s)">校验</NButton>
              <template v-if="validateState[s.kernel]">
                <NTag size="small" :bordered="false" :type="validateState[s.kernel]?.ok ? 'success' : 'error'">
                  {{ validateState[s.kernel]?.ok ? '通过' : '未通过' }}
                </NTag>
                <span class="mono">{{ validateState[s.kernel]?.message }}</span>
              </template>
            </NSpace>
          </NSpace>
        </NTabPane>
      </NTabs>
    </NCard>

    <NGrid cols="1 m:2" responsive="screen" :x-gap="14" :y-gap="14" class="section-gap">
      <NGi>
        <NCard size="small" title="运行状态（GET /network/dns）">
          <template #header-extra>
            <NTag size="small" :bordered="false" :type="status ? 'success' : 'default'">
              {{ status ? '已取得真实状态' : '未取得' }}
            </NTag>
          </template>
          <NDataTable
            :columns="statusColumns"
            :data="statusRows"
            :bordered="false"
            size="small"
            :pagination="false"
            :scroll-x="480"
          />
          <pre class="mono output-block" style="max-height: 200px; margin-top: 10px">{{ prettyJson(status) }}</pre>
        </NCard>
      </NGi>

      <NGi>
        <NCard size="small" title="解析测试（POST /network/dns/resolve）">
          <NSpace vertical :size="10" style="width: 100%">
            <NSpace :size="8" :wrap="true" align="center">
              <NInput v-model:value="resolveForm.name" style="max-width: 240px" placeholder="域名，例如 ngg.local." />
              <NInput v-model:value="resolveForm.type" style="width: 90px" placeholder="A" />
              <NButton size="small" type="primary" :loading="isBusy('resolve')" @click="doResolve">
                解析
              </NButton>
            </NSpace>
            <div v-if="resolveResult">
              <div class="hint">返回的 answers：</div>
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
    </NGrid>

    <NCard size="small" title="DNS 引擎配置（PUT /network/config · dns）" class="section-gap">
      <template #header-extra>
        <NButton size="small" type="primary" :loading="isBusy('save')" @click="saveDnsConfig">
          保存配置
        </NButton>
      </template>
      <NGrid cols="1 m:2" :x-gap="14" :y-gap="10">
        <NGi>
          <div class="kv-label">启用</div>
          <NSwitch v-model:value="form.enabled" />
        </NGi>
        <NGi>
          <div class="kv-label">监听地址 listen</div>
          <NInput v-model:value="form.listen" placeholder="127.0.0.1" />
        </NGi>
        <NGi>
          <div class="kv-label">监听端口 port</div>
          <NInputNumber v-model:value="form.port" style="width: 100%" :min="1" :max="65535" />
        </NGi>
        <NGi>
          <div class="kv-label">缓存 TTL（秒）</div>
          <NInputNumber v-model:value="form.cache_ttl" style="width: 100%" :min="0" />
        </NGi>
        <NGi>
          <div class="kv-label">上游 upstreams（每行一个，支持 https://.../dns-query 的 DoH）</div>
          <NInput v-model:value="upstreamsText" type="textarea" class="code-editor" :rows="4" />
        </NGi>
        <NGi>
          <div class="kv-label">拦截域名 block（每行一个）</div>
          <NInput v-model:value="blockText" type="textarea" class="code-editor" :rows="4" />
        </NGi>
        <NGi :span="2">
          <div class="kv-label">静态 hosts（JSON 对象：域名 → IP）</div>
          <NInput v-model:value="hostsText" type="textarea" class="code-editor" :rows="5" />
        </NGi>
      </NGrid>
      <div v-if="configResult" style="margin-top: 12px">
        <NSpace :size="6" :wrap="true">
          <NTag size="small" :bordered="false" type="success">
            已变更 {{ (configResult.changed || []).join(', ') || '无' }}
          </NTag>
          <NTag v-if="(configResult.requires_restart || []).length" size="small" :bordered="false" type="warning">
            需重启网络引擎：{{ (configResult.requires_restart || []).join(', ') }}
          </NTag>
          <NTag size="small" :bordered="false" :type="configResult.persisted ? 'info' : 'default'">
            {{ configResult.persisted ? '已写入配置文件' : '仅内存生效（未配置文件路径）' }}
          </NTag>
        </NSpace>
        <NAlert
          v-for="(w, i) in configResult.warnings || []"
          :key="i"
          type="warning"
          :bordered="false"
          style="margin-top: 8px"
        >
          {{ String(w) }}
        </NAlert>
      </div>
    </NCard>

    <NCard size="small" title="Hosts / 拦截即时策略（POST /network/dns/hosts）" class="section-gap">
      <NGrid cols="1 m:2" :x-gap="14" :y-gap="10">
        <NGi>
          <div class="kv-label">hosts（JSON 对象）</div>
          <NInput v-model:value="quickHostsText" type="textarea" class="code-editor" :rows="5" />
        </NGi>
        <NGi>
          <div class="kv-label">新增拦截域名（逗号或换行分隔）</div>
          <NInput v-model:value="quickBlockText" type="textarea" class="code-editor" :rows="5" />
        </NGi>
      </NGrid>
      <NSpace style="margin-top: 10px">
        <NButton size="small" type="primary" :loading="isBusy('hosts')" @click="applyQuickHosts">
          立即应用
        </NButton>
      </NSpace>
      <pre v-if="dnsResult" class="mono output-block" style="max-height: 160px; margin-top: 10px">{{ prettyJson(dnsResult) }}</pre>
    </NCard>
  </div>
</template>
