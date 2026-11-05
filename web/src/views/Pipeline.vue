<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  NAlert,
  NButton,
  NCard,
  NCheckbox,
  NCode,
  NInput,
  NInputNumber,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
  NTooltip,
} from 'naive-ui'
import * as api from '../api/ngg'
import type {
  HostView,
  PipelineConfig,
  PipelineStep,
  PipelineStepStatus,
} from '../api/ngg'

const router = useRouter()

const loading = ref(true)
const busy = ref('')
const view = ref<api.PipelineView | null>(null)
const steps = ref<PipelineStep[]>([])
const hosts = ref<HostView[]>([])
const ruleSets = ref<Array<{ id: string; name: string; enabled: boolean; rule_count: number }>>([])
const users = ref<Array<{ id: string; username: string; enabled: boolean }>>([])

const form = ref<PipelineConfig>({
  id: 'default',
  name: '默认链路',
  kernel: 'mihomo',
  protocol: 'mixed',
  inbound: { host_id: '', listen: '127.0.0.1', port: 7891, allow_lan: false },
  outbound: { host_ids: [], mode: 'select', direct_fallback: true },
  rule_set_ids: [],
  dns_enabled: false,
  user_ids: [],
  enabled: true,
  test_target: 'www.gstatic.com:80',
  updated_at: '',
})

const yaml = ref('')
const renderWarnings = ref<string[]>([])
const testResult = ref<api.PipelineTestResult | null>(null)
const applyError = ref('')
const applied = ref<api.PipelineApplyResult | null>(null)

const STEP_PAGE_LABEL: Record<string, string> = {
  protocol: '协议配置',
  inbound: '入口节点',
  rules: '规则配置',
  dns: 'DNS配置',
  outbound: '出口节点',
  users: '用户管理',
  kernel: '内核管理',
}

const statusText: Record<PipelineStepStatus, string> = {
  ok: '就绪',
  warn: '注意',
  fail: '阻断',
  skip: '跳过',
}

const hostOptions = computed(() =>
  hosts.value.map((h) => ({
    label: `${h.name || h.id} · ${h.address}:${h.svc_port || h.node_port}`,
    value: h.id,
    disabled: !h.enabled,
  })),
)
const entryOptions = computed(() => [
  { label: '不使用（本机直接监听）', value: '' },
  ...hostOptions.value,
])
const ruleOptions = computed(() =>
  ruleSets.value.map((r) => ({
    label: `${r.name}（${r.rule_count} 条${r.enabled ? '' : ' · 未启用'}）`,
    value: r.id,
    disabled: !r.enabled,
  })),
)
const userOptions = computed(() =>
  users.value.map((u) => ({ label: `${u.username}${u.enabled ? '' : '（已禁用）'}`, value: u.id })),
)
const modeOptions = [
  { label: '手动选择（select）', value: 'select' },
  { label: '自动测速（url-test）', value: 'url-test' },
  { label: '故障回退（fallback）', value: 'fallback' },
]
const kernelOptions = [{ label: 'mihomo（Clash.Meta）', value: 'mihomo' }]

function statusClass(s: PipelineStepStatus) {
  return `step--${s}`
}

function goto(page: string) {
  router.push({ name: page })
}

async function loadAll() {
  loading.value = true
  try {
    const [pv, hl, rl, ul] = await Promise.all([
      api.getPipeline(),
      api.listHosts('all'),
      api.listRuleSets(),
      api.listUsers('all'),
    ])
    view.value = pv
    steps.value = pv.steps ?? []
    form.value = JSON.parse(JSON.stringify(pv.config))
    hosts.value = hl.hosts ?? []
    ruleSets.value = (rl.rulesets ?? []).map((r: any) => ({
      id: r.id,
      name: r.name,
      enabled: !!r.enabled,
      rule_count: r.rule_count ?? 0,
    }))
    users.value = (ul.users ?? []).map((u: any) => ({
      id: u.id,
      username: u.username,
      enabled: !!u.enabled,
    }))
  } finally {
    loading.value = false
  }
}

async function save() {
  busy.value = 'save'
  applyError.value = ''
  try {
    const res = await api.savePipeline(form.value)
    view.value = res.view
    steps.value = res.view.steps ?? []
    form.value = JSON.parse(JSON.stringify(res.config))
    window.$message?.success('链路参数已保存')
  } catch (e) {
    applyError.value = e instanceof Error ? e.message : String(e)
  } finally {
    busy.value = ''
  }
}

async function runValidate() {
  busy.value = 'validate'
  try {
    await api.savePipeline(form.value)
    const res = await api.validatePipeline()
    steps.value = res.steps
    window.$message?.success(res.blocked ? '校验完成：存在阻断项' : '校验通过')
  } catch (e) {
    applyError.value = e instanceof Error ? e.message : String(e)
  } finally {
    busy.value = ''
  }
}

async function runRender() {
  busy.value = 'render'
  try {
    await api.savePipeline(form.value)
    const res = await api.renderPipeline()
    yaml.value = res.content
    renderWarnings.value = res.warnings ?? []
    window.$message?.success(`已生成 ${res.lines} 行内核配置`)
  } catch (e) {
    applyError.value = e instanceof Error ? e.message : String(e)
  } finally {
    busy.value = ''
  }
}

async function runApply() {
  window.$dialog?.warning({
    title: '应用链路并启动内核',
    content:
      '将把生成的配置写入 mihomo（先做原生校验并备份旧配置），然后重启内核。正在走代理的连接会被重置。',
    positiveText: '应用并启动',
    negativeText: '取消',
    onPositiveClick: () => {
      void doApply()
    },
  })
}

async function doApply() {
  busy.value = 'apply'
  applyError.value = ''
  applied.value = null
  try {
    await api.savePipeline(form.value)
    const res = await api.applyPipeline()
    applied.value = res
    steps.value = res.steps ?? steps.value
    if (res.ok) {
      window.$message?.success('链路已应用，内核已启动')
      await runTest(true)
    } else {
      applyError.value = res.error || '应用失败'
    }
  } catch (e) {
    applyError.value = e instanceof Error ? e.message : String(e)
  } finally {
    busy.value = ''
  }
}

async function runTest(silent = false) {
  busy.value = 'test'
  testResult.value = null
  try {
    const res = await api.testPipeline(form.value.test_target)
    testResult.value = res
    if (!silent) {
      window.$message?.success(res.ok ? '端到端连通正常' : '端到端测试失败')
    }
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e)
    testResult.value = { ok: false, listen: '', target: form.value.test_target, message: msg }
  } finally {
    busy.value = ''
  }
}

onMounted(loadAll)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">链路编排</h2>
        <p class="page-subtitle">
          配置引擎的六个二级页面在这里串成一条真实可运行的代理链路：协议 → 入口 → 规则 → DNS → 出口 → 用户
        </p>
      </div>
      <NSpace :size="8">
        <NButton size="small" :loading="busy === 'validate'" @click="runValidate">校验链路</NButton>
        <NButton size="small" :loading="busy === 'render'" @click="runRender">预览配置</NButton>
        <NButton
          size="small"
          type="primary"
          :loading="busy === 'apply'"
          @click="runApply"
        >应用并启动</NButton>
        <NButton size="small" tertiary :loading="busy === 'test'" @click="runTest(false)">连通测试</NButton>
      </NSpace>
    </div>

    <!-- 链路顺序 -->
    <div class="chain">
      <template v-for="(s, i) in steps" :key="s.key">
        <div class="chain-node" :class="statusClass(s.status)">
          <div class="chain-node__top">
            <span class="chain-index">{{ i + 1 }}</span>
            <span class="chain-title">{{ s.title }}</span>
            <span class="chain-status">{{ statusText[s.status] }}</span>
          </div>
          <div class="chain-value" :title="s.value">{{ s.value || '—' }}</div>
          <div class="chain-detail" :title="s.detail">{{ s.detail }}</div>
          <button class="chain-link" type="button" @click="goto(s.page)">
            {{ STEP_PAGE_LABEL[s.key] || s.page }} →
          </button>
        </div>
        <div v-if="i < steps.length - 1" class="chain-arrow" aria-hidden="true">›</div>
      </template>
      <div v-if="!steps.length" class="chain-empty">正在读取链路状态…</div>
    </div>

    <NAlert v-if="view?.blocked" type="error" :bordered="false" class="section-gap">
      <template #header>链路存在阻断项</template>
      标红的环节必须修复后才能应用；其余环节的黄灯只是提醒，不会阻止启动。
    </NAlert>

    <div class="pipeline-grid section-gap">
      <!-- 左：参数 -->
      <NCard size="small" title="链路参数" class="pl-card">
        <div class="form-grid">
          <label class="fld">
            <span>链路名称</span>
            <NInput v-model:value="form.name" placeholder="默认链路" />
          </label>
          <label class="fld">
            <span>承载内核</span>
            <NSelect v-model:value="form.kernel" :options="kernelOptions" />
          </label>

          <label class="fld">
            <span>入口节点（第一跳，可空）</span>
            <NSelect
              v-model:value="form.inbound.host_id"
              :options="entryOptions"
              placeholder="不使用：本机直接监听"
            />
          </label>
          <label class="fld">
            <span>客户端接入协议</span>
            <NSelect
              v-model:value="form.protocol"
              :options="[
                { label: '混合端口（SOCKS5 + HTTP）', value: 'mixed' },
                { label: 'SOCKS5', value: 'socks5' },
                { label: 'HTTP 代理', value: 'http' },
              ]"
            />
          </label>

          <label class="fld">
            <span>监听地址</span>
            <NSelect
              v-model:value="form.inbound.listen"
              :options="[
                { label: '127.0.0.1（仅本机）', value: '127.0.0.1' },
                { label: '0.0.0.0（局域网）', value: '0.0.0.0' },
              ]"
            />
          </label>
          <label class="fld">
            <span>监听端口</span>
            <NInputNumber v-model:value="form.inbound.port" :min="1" :max="65535" style="width: 100%" />
          </label>

          <label class="fld fld--wide">
            <span>出口节点（可多选，按顺序参与策略组）</span>
            <NSelect
              v-model:value="form.outbound.host_ids"
              multiple
              :options="hostOptions"
              placeholder="未选择时链路直连"
            />
          </label>
          <label class="fld">
            <span>出口策略</span>
            <NSelect v-model:value="form.outbound.mode" :options="modeOptions" />
          </label>
          <label class="fld fld--switch">
            <span>保留 DIRECT 选项</span>
            <NSwitch v-model:value="form.outbound.direct_fallback" />
            <em>仅手动选择模式下生效，避免自动测速时绕过 VPS</em>
          </label>

          <label class="fld fld--wide">
            <span>参与匹配的规则集（留空＝所有已启用规则集）</span>
            <NSelect v-model:value="form.rule_set_ids" multiple :options="ruleOptions" placeholder="留空使用全部已启用规则集" />
          </label>

          <label class="fld fld--wide">
            <span>计入配额的用户（仅记账，不参与握手）</span>
            <NSelect v-model:value="form.user_ids" multiple :options="userOptions" placeholder="可不选" />
          </label>

          <label class="fld">
            <span>测试目标（host:port）</span>
            <NInput v-model:value="form.test_target" placeholder="www.gstatic.com:80" />
          </label>
          <label class="fld fld--switch">
            <span>内核内置 DNS</span>
            <NSwitch v-model:value="form.dns_enabled" />
            <em>关闭时由 NGG DNS 与系统解析器处理，可避免端口冲突</em>
          </label>
        </div>

        <NSpace :size="8" style="margin-top: 14px">
          <NButton size="small" :loading="busy === 'save'" @click="save">保存参数</NButton>
          <NButton size="small" type="primary" :loading="busy === 'apply'" @click="runApply">
            应用并启动
          </NButton>
          <span class="hint">保存不会影响正在运行的内核，只有「应用并启动」才会下发配置。</span>
        </NSpace>

        <NAlert v-if="applyError" type="error" :bordered="false" style="margin-top: 12px">
          {{ applyError }}
        </NAlert>
        <NAlert v-if="applied?.ok" type="success" :bordered="false" style="margin-top: 12px">
          已应用到 {{ applied.kernel }}，入口 {{ applied.listen }}
          {{ applied.restarted ? '（内核已重启以加载新配置）' : '' }}
        </NAlert>
      </NCard>

      <!-- 右：结果 -->
      <NCard size="small" title="执行结果" class="pl-card">
        <div class="result-block">
          <div class="result-title">端到端连通测试</div>
          <template v-if="testResult">
            <div class="result-row">
              <NTag :type="testResult.ok ? 'success' : 'error'" :bordered="false" size="small">
                {{ testResult.ok ? '通过' : '失败' }}
              </NTag>
              <span class="mono">{{ testResult.listen || '—' }} → {{ testResult.target }}</span>
              <span v-if="testResult.latency_ms" class="hint">
                {{ testResult.latency_ms.toFixed(1) }} ms
              </span>
            </div>
            <div class="result-detail">{{ testResult.message || testResult.error }}</div>
          </template>
          <div v-else class="hint">
            尚未测试。测试会真实完成一次 SOCKS5 握手：连接入口 → 由出口节点连到目标 → 返回耗时。
          </div>
        </div>

        <div class="result-block">
          <div class="result-title">生成的内核配置</div>
          <div v-if="renderWarnings.length" class="result-warnings">
            <div v-for="(w, i) in renderWarnings" :key="i">· {{ w }}</div>
          </div>
          <NCode v-if="yaml" :code="yaml" language="yaml" word-wrap class="yaml-box" />
          <div v-else class="hint">点击「预览配置」生成 mihomo YAML。所有节点、规则与 DNS 都来自上面六个页面。</div>
        </div>

        <div class="result-block">
          <div class="result-title">代理顺序</div>
          <ol class="order-list">
            <li>客户端 → <span class="mono">{{ form.inbound.listen }}:{{ form.inbound.port }}</span>（混合端口）</li>
            <li v-if="form.inbound.host_id">→ 入口节点（第一跳 VPS，dialer-proxy）</li>
            <li v-if="form.outbound.host_ids.length">→ 出口节点（{{ form.outbound.host_ids.length }} 个，{{ form.outbound.mode }}）</li>
            <li v-else>→ 直连（未选择出口节点）</li>
            <li>→ 目标站点；规则命中 DIRECT / REJECT 时提前分流</li>
          </ol>
        </div>
      </NCard>
    </div>

    <NCard v-if="steps.some((s) => s.fix_hint)" size="small" title="待处理提示" class="section-gap">
      <div v-for="s in steps.filter((x) => x.fix_hint)" :key="s.key" class="fix-row">
        <NTooltip trigger="hover">
          <template #trigger>
            <NTag :bordered="false" size="small" :type="s.status === 'fail' ? 'error' : 'warning'">
              {{ s.title }}
            </NTag>
          </template>
          {{ s.detail }}
        </NTooltip>
        <span class="fix-text">{{ s.fix_hint }}</span>
        <button class="chain-link" type="button" @click="goto(s.page)">去处理 →</button>
      </div>
    </NCard>
  </div>
</template>

<style scoped>
.chain {
  display: flex;
  align-items: stretch;
  gap: 5px;
  flex-wrap: nowrap;
  overflow-x: auto;
  padding-bottom: 6px;
}
.chain-empty {
  color: #8e8e93;
  font-size: 13px;
  padding: 18px 0;
}
.chain-node {
  flex: 1 1 0;
  min-width: 148px;
  background: #fff;
  border: 1px solid #e8e8ed;
  border-radius: 14px;
  padding: 10px 11px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  transition: border-color 0.18s ease, box-shadow 0.18s ease;
}
.chain-node:hover {
  border-color: #d2d2d7;
  box-shadow: 0 6px 18px rgba(0, 0, 0, 0.05);
}
.chain-node__top {
  display: flex;
  align-items: center;
  gap: 7px;
}
.chain-index {
  width: 18px;
  height: 18px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  font-size: 10.5px;
  font-weight: 600;
  color: #fff;
  background: #0071e3;
}
.chain-title {
  font-size: 12.5px;
  font-weight: 600;
  color: #1d1d1f;
}
.chain-status {
  margin-left: auto;
  font-size: 10.5px;
  color: #8e8e93;
}
.chain-value {
  font-size: 12.5px;
  color: #1d1d1f;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.chain-detail {
  font-size: 11px;
  line-height: 1.5;
  color: #8e8e93;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  min-height: 32px;
}
.chain-link {
  border: 0;
  background: none;
  padding: 0;
  font-size: 11px;
  color: #0071e3;
  cursor: pointer;
  text-align: left;
}
.chain-link:hover {
  text-decoration: underline;
}
.step--ok .chain-index {
  background: #30a14e;
}
.step--warn {
  border-color: #f2d09a;
  background: #fffbf2;
}
.step--warn .chain-index {
  background: #ff9f0a;
}
.step--fail {
  border-color: #f0b6bb;
  background: #fff6f6;
}
.step--fail .chain-index {
  background: #ff453a;
}
.step--skip .chain-index {
  background: #c7c7cc;
}
.chain-arrow {
  display: grid;
  place-items: center;
  color: #c7c7cc;
  font-size: 18px;
  flex: 0 0 9px;
}

.pipeline-grid {
  display: grid;
  grid-template-columns: minmax(420px, 1.05fr) minmax(380px, 0.95fr);
  gap: 14px;
  align-items: start;
}
@media (max-width: 1180px) {
  .pipeline-grid {
    grid-template-columns: 1fr;
  }
}
.form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px 14px;
}
.fld {
  display: grid;
  gap: 5px;
  font-size: 12px;
  color: #6e6e73;
}
.fld--wide {
  grid-column: 1 / -1;
}
.fld--switch {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.fld--switch em {
  font-style: normal;
  font-size: 10.5px;
  color: #a1a1a6;
}
.result-block + .result-block {
  margin-top: 16px;
  padding-top: 14px;
  border-top: 1px solid #f0f0f2;
}
.result-title {
  font-size: 12px;
  font-weight: 600;
  color: #1d1d1f;
  margin-bottom: 8px;
}
.result-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 12px;
}
.result-detail {
  margin-top: 6px;
  font-size: 11.5px;
  color: #6e6e73;
  line-height: 1.6;
}
.result-warnings {
  font-size: 11.5px;
  color: #b26a00;
  margin-bottom: 6px;
}
.yaml-box {
  max-height: 320px;
  overflow: auto;
  border-radius: 10px;
  font-size: 11.5px;
}
.order-list {
  margin: 0;
  padding-left: 18px;
  font-size: 11.5px;
  line-height: 1.9;
  color: #6e6e73;
}
.fix-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 0;
  font-size: 12px;
  color: #1d1d1f;
}
.fix-text {
  color: #6e6e73;
}
.hint {
  font-size: 11.5px;
  color: #8e8e93;
  line-height: 1.7;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
</style>
