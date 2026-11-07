<script setup lang="ts">
/**
 * 模块架构 · 规划 2 ~ 10 的顺序工作台
 * ② 内核 ③ 协议组 ④ 规则 ⑤ DNS ⑥ 策略组 ⑦ 模块 ⑧ 用户 ⑨ 链接 ⑩ 总控
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NAlert, NButton, NCard, NCheckbox, NGrid, NGi, NInput, NSelect, NSpace, NSpin, NSwitch, NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type {
  MCatalogResponse, MKernel, MLink, MModuleSummary, ModuleNodeView,
} from '../api/ngg'

const route = useRoute()
const router = useRouter()
const step = ref<number>(Number(route.meta.step) || 2)
watch(() => route.meta.step, (v) => { if (v) step.value = Number(v) })
watch(step, (v) => router.push({ name: 'm' + v }))

const STEPS = [
  { n: 2, label: '内核管理' }, { n: 3, label: '协议管理' }, { n: 4, label: '规则管理' },
  { n: 5, label: 'DNS管理' }, { n: 6, label: '策略管理' }, { n: 7, label: '模块管理' },
  { n: 8, label: '用户管理' }, { n: 9, label: '链接管理' }, { n: 10, label: '总控' },
]

const data = ref<MCatalogResponse | null>(null)
const nodes = ref<ModuleNodeView[]>([])
const loading = ref(false)
const busy = ref(false)

type Field = {
  key: string; label: string
  type: 'text' | 'bool' | 'select' | 'multi' | 'lines'
  options?: { label: string; value: string }[]
  def?: unknown
}
type Def = { kind: string; key: keyof MCatalogResponse['catalog']; title: string; desc: string; fields: Field[] }

const kernelOptions = () => (data.value?.catalog.kernels ?? []).map((k) => ({ label: k.name, value: k.id }))
const rulesetOptions = () => (data.value?.catalog.rule_sets ?? []).map((r) => ({ label: r.name, value: r.id }))
const moduleOptions = () => (data.value?.modules ?? []).map((m) => ({ label: m.name, value: m.id }))
const nodeOptions = () => nodes.value.map((n) => ({ label: `${n.node.address}:${n.node.port}`, value: n.node.id }))

const DEFS: Record<number, Def[]> = {
  3: [{ kind: 'protocol-groups', key: 'protocol_groups', title: '协议组', desc: '一个协议组可以绑定 1~3 个内核；也可以建三个组各绑一个内核同时启用。', fields: [
    { key: 'name', label: '组名称', type: 'text' },
    { key: 'kernel_ids', label: '绑定内核（可多选）', type: 'multi', options: [] },
    { key: 'protocols', label: '协议条目（每行 type:tag）', type: 'lines', def: 'vless:in-1' },
    { key: 'enabled', label: '启用', type: 'bool', def: true },
  ] }],
  4: [
    { kind: 'rule-sets', key: 'rule_sets', title: '规则集', desc: '先建规则集，策略组才能绑定它。', fields: [
      { key: 'name', label: '规则集名称', type: 'text' },
      { key: 'source', label: '来源 / URL', type: 'text' },
      { key: 'enabled', label: '启用', type: 'bool', def: true },
    ] },
    { kind: 'rules', key: 'rules', title: '规则', desc: '新建规则、设置匹配与目标，可随时启停。', fields: [
      { key: 'name', label: '规则名称', type: 'text' },
      { key: 'type', label: '类型', type: 'select', options: [
        { label: '域名', value: 'domain' }, { label: 'IP', value: 'ip' }, { label: '端口', value: 'port' },
      ], def: 'domain' },
      { key: 'pattern', label: '匹配值', type: 'text' },
      { key: 'outbound', label: '目标出站', type: 'text', def: 'direct' },
      { key: 'enabled', label: '启用', type: 'bool', def: true },
    ] },
  ],
  5: [{ kind: 'dns', key: 'dns', title: 'DNS 策略', desc: '防泄密 + 加密上游；按国内/国外域名与 IP 自动选择解析器。', fields: [
    { key: 'name', label: '策略名称', type: 'text' },
    { key: 'leak_protection', label: 'DNS 防泄密', type: 'bool', def: true },
    { key: 'encrypt', label: '加密方式', type: 'select', options: [
      { label: '不加密', value: 'none' }, { label: 'DoH', value: 'doh' },
      { label: 'DoT', value: 'dot' }, { label: 'DoQ', value: 'doq' },
    ], def: 'doh' },
    { key: 'strategy', label: '分流策略', type: 'select', options: [
      { label: '按地域自动', value: 'geo' }, { label: '全部国内', value: 'all-domestic' }, { label: '全部国外', value: 'all-foreign' },
    ], def: 'geo' },
    { key: 'domestic', label: '国内上游（每行一个）', type: 'lines', def: 'https://dns.alidns.com/dns-query' },
    { key: 'foreign', label: '国外上游（每行一个）', type: 'lines', def: 'https://cloudflare-dns.com/dns-query' },
    { key: 'domestic_ip_cidrs', label: '额外国内 IP 段（每行一个 CIDR）', type: 'lines' },
    { key: 'enabled', label: '启用', type: 'bool', def: true },
  ] }],
  6: [{ kind: 'policy-groups', key: 'policy_groups', title: '策略组', desc: '策略组绑定规则集，供模块统一下发。', fields: [
    { key: 'name', label: '策略组名称', type: 'text' },
    { key: 'mode', label: '模式', type: 'select', options: [
      { label: 'select', value: 'select' }, { label: 'url-test', value: 'url-test' },
      { label: 'fallback', value: 'fallback' }, { label: 'load-balance', value: 'load-balance' },
    ], def: 'select' },
    { key: 'rule_set_ids', label: '绑定规则集（可多选）', type: 'multi', options: [] },
    { key: 'enabled', label: '启用', type: 'bool', def: true },
  ] }],
  8: [{ kind: 'users', key: 'users', title: '用户', desc: '新建用户时选择一个模块；保存后自动生成订阅 token。', fields: [
    { key: 'name', label: '用户名', type: 'text' },
    { key: 'module_id', label: '选择模块', type: 'select', options: [] },
  ] }],
}

// 动态选项在每次加载后刷新
function optionsFor(f: Field): { label: string; value: string }[] {
  if (f.key === 'kernel_ids') return kernelOptions()
  if (f.key === 'rule_set_ids') return rulesetOptions()
  if (f.key === 'module_id') return moduleOptions()
  return f.options ?? []
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const draft = ref<Record<string, Record<string, any>>>({})
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function form(def: Def): Record<string, any> {
  if (!draft.value[def.kind]) {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const init: Record<string, any> = {}
    for (const f of def.fields) init[f.key] = f.def ?? (f.type === 'bool' ? false : f.type === 'multi' || f.type === 'lines' ? [] : '')
    draft.value[def.kind] = init
  }
  return draft.value[def.kind]
}

async function load() {
  loading.value = true
  try {
    const [res, mn] = await Promise.all([api.getMCatalog(), api.listModuleNodes()])
    data.value = res
    nodes.value = mn.nodes ?? []
  } catch (e) {
    window.$message?.warning('读取模块库失败：' + errText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function save(def: Def) {
  const body: Record<string, unknown> = {}
  for (const f of def.fields) {
    let v = form(def)[f.key]
    if (f.type === 'lines') {
      v = String(v ?? '').split(/[\n,]/).map((s) => s.trim()).filter(Boolean)
      if (f.key === 'protocols') {
        v = (v as string[]).map((line) => {
          const [type, tag] = line.split(':')
          return { type: type?.trim() || 'vless', tag: tag?.trim() || type?.trim() || 'in' }
        })
      }
    }
    body[f.key] = v
  }
  if (!String(body.name ?? '').trim() && def.key !== 'users') {
    window.$message?.warning('请填写名称')
    return
  }
  busy.value = true
  try {
    await api.putMItem(def.kind, body)
    draft.value[def.kind] = {}
    await load()
    window.$message?.success('已保存')
  } catch (e) {
    window.$message?.error('保存失败：' + errText(e))
  } finally {
    busy.value = false
  }
}

async function remove(kind: string, id: string) {
  try {
    await api.deleteMItem(kind, id)
    await load()
  } catch (e) {
    window.$message?.error('删除失败：' + errText(e))
  }
}

function items(def: Def): Record<string, unknown>[] {
  const c = data.value?.catalog
  if (!c) return []
  return (c[def.key] as unknown as Record<string, unknown>[]) ?? []
}
function describe(def: Def, item: Record<string, unknown>): string {
  if (def.kind === 'protocol-groups') return `内核 ${(item.kernel_ids as string[] ?? []).join('、') || '未绑定'} · ${(item.protocols as unknown[] ?? []).length} 条协议`
  if (def.kind === 'dns') return `${item.strategy} / ${item.encrypt} · 防泄密 ${item.leak_protection ? '开' : '关'}`
  if (def.kind === 'policy-groups') return `${item.mode} · 规则集 ${(item.rule_set_ids as string[] ?? []).length} 个`
  if (def.kind === 'rules') return `${item.type}: ${item.pattern || '—'} → ${item.outbound || 'direct'}`
  if (def.kind === 'rule-sets') return String(item.source || '本地规则集')
  if (def.kind === 'users') return `模块 ${(data.value?.modules ?? []).find((m) => m.id === item.module_id)?.name || item.module_id}`
  return ''
}

/* ---------- ② 内核启停 ---------- */
const selectedKernels = ref<string[]>([])
const kernelMode = ref('multiple')
const kernelResults = ref<{ id: string; ok: boolean; message: string }[]>([])
async function runKernels(action: 'start' | 'stop') {
  busy.value = true
  try {
    const res = await api.mKernelAction(action, {
      mode: action === 'start' ? kernelMode.value : undefined,
      ids: kernelMode.value === 'all' && action === 'start' ? [] : selectedKernels.value,
    })
    kernelResults.value = res.results
    await load()
  } catch (e) {
    window.$message?.error('操作失败：' + errText(e))
  } finally {
    busy.value = false
  }
}

/* ---------- ⑤ DNS 试算 ---------- */
const dnsTry = ref({ dns_id: '', domain: 'www.baidu.com', ip: '' })
const dnsResult = ref<{ region: string; upstream: string; reason: string; encrypted: boolean; leak_safe: boolean } | null>(null)
async function tryDNS() {
  try {
    dnsResult.value = await api.mDNSResolve(dnsTry.value)
  } catch (e) {
    window.$message?.error(errText(e))
  }
}

/* ---------- ⑦ 模块构建 ---------- */
const mod = ref({
  name: '', note: '', kernel_ids: [] as string[], protocol_group_ids: [] as string[],
  rule_ids: [] as string[], rule_set_ids: [] as string[], dns_id: '',
  policy_group_ids: [] as string[], node_ids: [] as string[],
  master_enabled: true, auto_regulate: true,
})
async function createModule() {
  if (!mod.value.name.trim()) { window.$message?.warning('填写模块名称'); return }
  const c = data.value?.catalog
  if (!c) return
  const selectedNode = mod.value.node_ids
  busy.value = true
  try {
    await api.mCreateModule({
      name: mod.value.name, note: mod.value.note,
      kernel_ids: mod.value.kernel_ids.length ? mod.value.kernel_ids : c.kernels.map((k) => k.id),
      protocol_group_ids: mod.value.protocol_group_ids, rule_ids: mod.value.rule_ids,
      rule_set_ids: mod.value.rule_set_ids, dns_id: mod.value.dns_id,
      policy_group_ids: mod.value.policy_group_ids, node_ids: selectedNode,
      master: { enabled: mod.value.master_enabled, mode: 'auto', auto_regulate: mod.value.auto_regulate,
        poll_interval_sec: 30, egress_node_id: selectedNode[0] ?? '' },
    })
    await load()
    window.$message?.success('模块已生成（含 NGG 主内核）')
  } catch (e) {
    window.$message?.error('生成失败：' + errText(e))
  } finally {
    busy.value = false
  }
}

/* ---------- ⑨ 链接 ---------- */
const linkUser = ref('')
const linkClients = ref<string[]>([])
const linkBase = ref('')
const issued = ref<{ subscription: string; links: MLink[] } | null>(null)
async function issueLinks() {
  if (!linkUser.value) { window.$message?.warning('先选择用户'); return }
  try {
    const res = await api.mIssueLinks(linkUser.value, {
      base_url: linkBase.value || undefined,
      clients: linkClients.value.length ? linkClients.value : undefined,
    })
    issued.value = { subscription: res.subscription, links: res.links }
    await load()
  } catch (e) {
    window.$message?.error('生成失败：' + errText(e))
  }
}

/* ---------- ⑩ 总控 ---------- */
const masterModule = ref('')
const masterAuto = ref(true)
const masterResult = ref<{ actions: string[]; start_kernels: string[]; stop_kernels: string[] } | null>(null)
async function applyMaster() {
  try {
    const res = await api.mMasterApply({ module_id: masterModule.value || undefined, auto_regulate: masterAuto.value })
    masterResult.value = res.plan
    await load()
    window.$message?.success('主内核已按模块调控')
  } catch (e) {
    window.$message?.error('调控失败：' + errText(e))
  }
}

const modules = computed<MModuleSummary[]>(() => data.value?.modules ?? [])
const kernelList = computed<MKernel[]>(() => data.value?.catalog.kernels ?? [])
const clients = computed(() => data.value?.clients ?? [])

function errText(e: unknown): string {
  const x = e as { response?: { data?: { error?: string } }; message?: string }
  return x?.response?.data?.error || x?.message || '未知错误'
}
async function validateModule(id: string) {
  try {
    await api.mValidateModule(id)
    window.$message?.success('模块校验通过')
  } catch (e) {
    window.$message?.error('模块校验未通过：' + errText(e))
  }
}
async function removeModule(id: string) {
  try {
    await api.mDeleteModule(id)
    await load()
  } catch (e) {
    window.$message?.error('删除失败：' + errText(e))
  }
}
async function copy(text: string) {
  try { await navigator.clipboard.writeText(text); window.$message?.success('已复制') }
  catch { window.$message?.warning('剪贴板不可用') }
}
</script>

<template>
  <div class="wb">
    <header class="wb__head">
      <div>
        <h1>模块架构 · 顺序开发 ② → ⑩</h1>
        <p>规划 1 在「① 节点管理」。以下按 ② 内核 → ③ 协议 → ④ 规则 → ⑤ DNS → ⑥ 策略 → ⑦ 模块 → ⑧ 用户 → ⑨ 链接 → ⑩ 总控 依次落地。</p>
      </div>
      <NButton size="small" @click="load">刷新</NButton>
    </header>

    <div class="wb__steps">
      <button v-for="s in STEPS" :key="s.n" :class="['wb__step', { on: step === s.n }]" @click="step = s.n">
        {{ s.n }}. {{ s.label }}
      </button>
    </div>

    <NSpin :show="loading">
      <!-- ② 内核管理 -->
      <section v-if="step === 2" class="wb__panel">
        <h2>规划 2 · 内核管理</h2>
        <p class="wb__desc">三个内核可同时启动、单独启动、或多个一起启动；NGG 主内核在 ⑩ 自动调控。</p>
        <NGrid cols="1 m:3" responsive="screen" :x-gap="12" :y-gap="12">
          <NGi v-for="k in kernelList" :key="k.id">
            <NCard size="small">
              <div class="wb__row"><b>{{ k.name }}</b><NTag size="small" :type="k.state === 'running' ? 'success' : 'default'">{{ k.state }}</NTag></div>
              <div class="wb__muted">{{ k.id }} · 启用 {{ k.enabled ? '是' : '否' }} · 自启 {{ k.auto_start ? '是' : '否' }}</div>
            </NCard>
          </NGi>
        </NGrid>
        <div class="wb__bar">
          <NSelect v-model:value="kernelMode" style="width: 140px" :options="[
            { label: '单独启动', value: 'single' }, { label: '多个启动', value: 'multiple' }, { label: '全部启动', value: 'all' },
          ]" />
          <NSelect v-model:value="selectedKernels" multiple style="min-width: 300px" placeholder="选择内核" :options="kernelOptions()" />
          <NButton type="primary" :loading="busy" @click="runKernels('start')">启动</NButton>
          <NButton :loading="busy" @click="runKernels('stop')">暂停选中</NButton>
        </div>
        <div v-for="r in kernelResults" :key="r.id" class="wb__line">
          <NTag size="small" :type="r.ok ? 'success' : 'error'">{{ r.ok ? 'OK' : '失败' }}</NTag>
          <b>{{ r.id }}</b> {{ r.message }}
        </div>
        <NAlert type="info" :bordered="false" style="margin-top: 12px">
          启动/暂停直接调用现有内核引擎的批量接口；引擎未初始化或内核未安装时，逐条返回真实原因而不是假成功。
        </NAlert>
      </section>

      <!-- ③④⑤⑥⑧ 通用库管理 -->
      <section v-else-if="DEFS[step]" class="wb__panel">
        <h2>规划 {{ step }} · {{ DEFS[step][0].title }}管理</h2>
        <p class="wb__desc">{{ DEFS[step][0].desc }}</p>
        <NGrid cols="1 m:2" responsive="screen" :x-gap="14" :y-gap="14">
          <NGi v-for="def in DEFS[step]" :key="def.kind">
            <NCard :title="def.title" size="small">
              <div v-for="f in def.fields" :key="f.key" class="wb__field">
                <label>{{ f.label }}</label>
                <NSwitch v-if="f.type === 'bool'" v-model:value="form(def)[f.key]" />
                <NSelect v-else-if="f.type === 'select'" v-model:value="form(def)[f.key]" :options="optionsFor(f)" />
                <NSelect v-else-if="f.type === 'multi'" v-model:value="form(def)[f.key]" multiple :options="optionsFor(f)" />
                <NInput v-else-if="f.type === 'lines'" v-model:value="form(def)[f.key]" type="textarea" :rows="2" />
                <NInput v-else v-model:value="form(def)[f.key]" />
              </div>
              <NButton size="small" type="primary" :loading="busy" style="margin-top: 10px" @click="save(def)">新建 / 保存</NButton>
            </NCard>
          </NGi>
        </NGrid>

        <div v-for="def in DEFS[step]" :key="def.kind + '-list'" class="wb__list">
          <h3>{{ def.title }}列表（{{ items(def).length }}）</h3>
          <div v-if="!items(def).length" class="wb__muted">暂无，先新建一个。</div>
          <div v-for="it in items(def)" :key="String(it.id)" class="wb__item">
            <div>
              <b>{{ it.name || it.id }}</b>
              <span class="wb__muted"> · {{ describe(def, it) }}</span>
            </div>
            <NButton size="tiny" quaternary type="error" @click="remove(def.kind, String(it.id))">删除</NButton>
          </div>
        </div>

        <!-- ⑤ DNS 分流试算 -->
        <NCard v-if="step === 5" title="分流试算" size="small" style="margin-top: 14px">
          <div class="wb__bar">
            <NSelect v-model:value="dnsTry.dns_id" style="width: 200px" placeholder="选择 DNS 策略"
              :options="(data?.catalog.dns ?? []).map((d) => ({ label: d.name, value: d.id }))" />
            <NInput v-model:value="dnsTry.domain" style="width: 220px" placeholder="域名" />
            <NInput v-model:value="dnsTry.ip" style="width: 160px" placeholder="解析出的 IP（可选）" />
            <NButton @click="tryDNS">试算</NButton>
          </div>
          <div v-if="dnsResult" class="wb__line">
            <NTag size="small" :type="dnsResult.leak_safe ? 'success' : 'error'">{{ dnsResult.region }}</NTag>
            <b>{{ dnsResult.upstream || '拒绝明文解析' }}</b> — {{ dnsResult.reason }}
          </div>
        </NCard>
      </section>

      <!-- ⑦ 模块管理 -->
      <section v-else-if="step === 7" class="wb__panel">
        <h2>规划 7 · 模块管理</h2>
        <p class="wb__desc">把节点 + 内核 + 协议组 + 规则/规则集 + DNS + 策略组，连同 NGG 主内核一起打包成一个模块。</p>
        <NCard size="small">
          <div class="wb__grid2">
            <div class="wb__field"><label>模块名称</label><NInput v-model:value="mod.name" /></div>
            <div class="wb__field"><label>备注</label><NInput v-model:value="mod.note" /></div>
            <div class="wb__field"><label>节点（规划1）</label><NSelect v-model:value="mod.node_ids" multiple :options="nodeOptions()" /></div>
            <div class="wb__field"><label>内核（留空＝全部）</label><NSelect v-model:value="mod.kernel_ids" multiple :options="kernelOptions()" /></div>
            <div class="wb__field"><label>协议组</label><NSelect v-model:value="mod.protocol_group_ids" multiple
              :options="(data?.catalog.protocol_groups ?? []).map((g) => ({ label: g.name, value: g.id }))" /></div>
            <div class="wb__field"><label>规则</label><NSelect v-model:value="mod.rule_ids" multiple
              :options="(data?.catalog.rules ?? []).map((r) => ({ label: r.name, value: r.id }))" /></div>
            <div class="wb__field"><label>规则集</label><NSelect v-model:value="mod.rule_set_ids" multiple :options="rulesetOptions()" /></div>
            <div class="wb__field"><label>DNS 策略</label><NSelect v-model:value="mod.dns_id" clearable
              :options="(data?.catalog.dns ?? []).map((d) => ({ label: d.name, value: d.id }))" /></div>
            <div class="wb__field"><label>策略组</label><NSelect v-model:value="mod.policy_group_ids" multiple
              :options="(data?.catalog.policy_groups ?? []).map((p) => ({ label: p.name, value: p.id }))" /></div>
            <div class="wb__field"><label>NGG 主内核</label><NSpace><NSwitch v-model:value="mod.master_enabled" /> 启用</NSpace></div>
          </div>
          <NCheckbox v-model:checked="mod.auto_regulate" style="margin-top: 8px">主内核自动调控</NCheckbox>
          <div style="margin-top: 12px"><NButton type="primary" :loading="busy" @click="createModule">生成模块</NButton></div>
        </NCard>
        <div class="wb__list">
          <h3>模块列表（{{ modules.length }}）</h3>
          <div v-for="m in modules" :key="m.id" class="wb__item">
            <div>
              <b>{{ m.name }}</b>
              <span class="wb__muted"> · 节点 {{ m.node_count }} / 内核 {{ m.active_kernels }}/{{ m.kernel_count }} /
                协议组 {{ m.protocol_group_count }} / 规则 {{ m.rule_count }} / 规则集 {{ m.rule_set_count }} /
                策略组 {{ m.policy_group_count }} / DNS {{ m.dns_enabled ? '开' : '关' }} / 主内核 {{ m.master_enabled ? '开' : '关' }}</span>
            </div>
            <NSpace>
              <NButton size="tiny" @click="validateModule(m.id)">校验</NButton>
              <NButton size="tiny" quaternary type="error" @click="removeModule(m.id)">删除</NButton>
            </NSpace>
          </div>
        </div>
      </section>

      <!-- ⑨ 链接管理 -->
      <section v-else-if="step === 9" class="wb__panel">
        <h2>规划 9 · 链接管理</h2>
        <p class="wb__desc">按客户端把订阅链接自动转成该客户端专属、可直接识别的导入链接。</p>
        <NCard size="small">
          <div class="wb__bar">
            <NSelect v-model:value="linkUser" style="width: 220px" placeholder="选择用户"
              :options="(data?.catalog.users ?? []).map((u) => ({ label: u.name, value: u.id }))" />
            <NSelect v-model:value="linkClients" multiple style="min-width: 260px" placeholder="客户端（可多选，留空＝全部）"
              :options="clients.map((c) => ({ label: c, value: c }))" />
            <NInput v-model:value="linkBase" style="width: 260px" placeholder="订阅基址（留空用当前面板地址）" />
            <NButton type="primary" @click="issueLinks">生成专属链接</NButton>
          </div>
        </NCard>
        <div v-if="issued" class="wb__list">
          <h3>通用订阅</h3>
          <div class="wb__item"><code>{{ issued.subscription }}</code><NButton size="tiny" @click="copy(issued!.subscription)">复制</NButton></div>
          <h3>客户端专属链接（{{ issued.links.length }}）</h3>
          <div v-for="l in issued.links" :key="l.id" class="wb__item">
            <div><NTag size="small">{{ l.client }}</NTag> <code class="wb__url">{{ l.url }}</code></div>
            <NButton size="tiny" @click="copy(l.url)">复制</NButton>
          </div>
        </div>
      </section>

      <!-- ⑩ 总控 -->
      <section v-else-if="step === 10" class="wb__panel">
        <h2>规划 10 · NGG 主内核总控</h2>
        <p class="wb__desc">用户订阅后识别模块信息，主内核统一调控节点、内核、协议、规则、DNS、策略组。</p>
        <NCard size="small">
          <div class="wb__bar">
            <NSelect v-model:value="masterModule" style="width: 280px" placeholder="选择要下发的模块" :options="moduleOptions()" />
            <NCheckbox v-model:checked="masterAuto">自动调控</NCheckbox>
            <NButton type="primary" :loading="busy" @click="applyMaster">按模块调控</NButton>
          </div>
          <div v-if="data?.catalog.master" class="wb__line" style="margin-top: 10px">
            <NTag size="small" :type="data.catalog.master.enabled ? 'success' : 'default'">
              {{ data.catalog.master.enabled ? '已启用' : '未启用' }}
            </NTag>
            调控次数 {{ data.catalog.master.regulate_count }} · 模块 {{ data.catalog.master.module_id || '—' }} ·
            出口 {{ data.catalog.master.egress_node_id || '—' }}
          </div>
        </NCard>
        <div v-if="masterResult" class="wb__list">
          <h3>本次调控动作</h3>
          <div v-for="(a, i) in masterResult.actions" :key="i" class="wb__line">{{ i + 1 }}. {{ a }}</div>
          <NAlert type="info" :bordered="false" style="margin-top: 10px">
            启动 {{ masterResult.start_kernels.join('、') || '无' }}，停止 {{ masterResult.stop_kernels.join('、') || '无' }}。
          </NAlert>
        </div>
      </section>
    </NSpin>
  </div>
</template>

<style scoped>
.wb { padding: 20px 24px 48px; max-width: 1400px; }
.wb__head { display: flex; justify-content: space-between; gap: 16px; align-items: flex-start; }
.wb h1 { font-size: 20px; margin: 0 0 6px; font-weight: 650; }
.wb__head p, .wb__desc { color: #6b7280; font-size: 13px; line-height: 1.7; margin: 0; }
.wb__steps { display: flex; flex-wrap: wrap; gap: 6px; margin: 16px 0; }
.wb__step { border: 1px solid #d1d5db; background: #fff; border-radius: 6px; padding: 6px 12px; font-size: 12px; cursor: pointer; }
.wb__step.on { background: #2080f0; border-color: #2080f0; color: #fff; }
.wb__panel h2 { font-size: 16px; margin: 0 0 4px; }
.wb__panel h3 { font-size: 13px; margin: 18px 0 8px; }
.wb__desc { margin-bottom: 14px !important; }
.wb__row { display: flex; justify-content: space-between; align-items: center; gap: 8px; }
.wb__muted { color: #9ca3af; font-size: 12px; }
.wb__bar { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin: 14px 0; }
.wb__line { font-size: 12px; padding: 6px 0; border-bottom: 1px solid #f3f4f6; display: flex; gap: 8px; align-items: center; }
.wb__field { margin-bottom: 10px; }
.wb__field label { display: block; font-size: 12px; color: #6b7280; margin-bottom: 4px; }
.wb__grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 0 16px; }
.wb__list { margin-top: 16px; }
.wb__item { display: flex; justify-content: space-between; align-items: center; gap: 10px; padding: 8px 10px; border: 1px solid #f0f1f3; border-radius: 6px; margin-bottom: 6px; font-size: 12px; }
.wb__url { font-size: 11px; word-break: break-all; }
@media (max-width: 800px) { .wb__grid2 { grid-template-columns: 1fr; } }
</style>
