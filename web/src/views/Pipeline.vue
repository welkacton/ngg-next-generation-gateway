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
const kernelOptions = ref<{ label: string; value: string }[]>([
  { label: 'mihomo（Clash.Meta）', value: 'mihomo' },
])

function statusClass(s: PipelineStepStatus) {
  return `step--${s}`
}

function goto(page: string) {
  router.push({ name: page })
}

async function loadAll() {
  loading.value = true
  try {
    const [pv, hl, rl, ul, opts] = await Promise.all([
      api.getPipeline(),
      api.listHosts('all'),
      api.listRuleSets(),
      api.listUsers('all'),
      api.getProfileOptions().catch(() => null),
    ])
    if (opts?.kernels?.length) {
      kernelOptions.value = [
        { label: '自动（按规则调度）', value: '' },
        ...opts.kernels.map((k) => ({ label: k.label || k.kernel, value: k.kernel })),
      ]
    }
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
    const warns = (res.steps ?? []).filter((s) => s.status === 'warn').length
    if (res.blocked) window.$message?.error('校验完成：存在阻断项')
    else if (warns) window.$message?.warning(`校验完成：${warns} 项注意（详见链路步骤）`)
    else window.$message?.success('校验通过')
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
    if (renderWarnings.value.length) {
      window.$message?.warning(`已生成 ${res.lines} 行内核配置，后端返回 ${renderWarnings.value.length} 条警告`)
    } else if (res.ok) {
      window.$message?.success(`已生成 ${res.lines} 行内核配置`)
    }
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

const showYaml = ref(false)

/** 阻断 / 注意计数与每段状态，供顶部状态条与编号步骤共用。 */
const blocked = computed(() => steps.value.some((s) => s.status === 'fail') || view.value?.blocked === true)
const warnCount = computed(() => steps.value.filter((s) => s.status === 'warn').length)
const fixSteps = computed(() => steps.value.filter((s) => s.fix_hint && s.status !== 'ok'))
const stepMap = computed<Record<string, PipelineStep>>(() =>
  Object.fromEntries(steps.value.map((s) => [s.key, s])),
)

/** 某一段的修复提示（只在非 ok 时返回），直接显示在对应的编号步骤下面。 */
function stepHint(key: string): string {
  const s = stepMap.value[key]
  if (!s || s.status === 'ok' || s.status === 'skip') return ''
  return s.fix_hint || s.detail || ''
}
function stepIsFail(key: string): boolean {
  return stepMap.value[key]?.status === 'fail'
}

onMounted(loadAll)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">链路编排</h2>
        <p class="page-subtitle">
          把配置引擎的六个页面串成一条真实可运行的链路：协议 → 节点 → 规则 → DNS → 用户
        </p>
      </div>
      <NSpace :size="8" align="center">
        <NTag :bordered="false" :type="blocked ? 'error' : (warnCount ? 'warning' : 'success')">
          {{ blocked ? '存在阻断项' : warnCount ? `${warnCount} 项待确认` : '链路就绪' }}
        </NTag>
        <NButton size="small" :loading="busy === 'validate'" @click="runValidate">校验</NButton>
        <NButton size="small" :loading="busy === 'render'" @click="runRender">预览配置</NButton>
        <NButton size="small" type="primary" :loading="busy === 'apply'" @click="runApply">
          应用并启动
        </NButton>
      </NSpace>
    </div>

    <!-- 链路一眼可见：六段状态条，点任意一段进入对应配置页 -->
    <div class="pl-steps">
      <button
        v-for="(s, i) in steps"
        :key="s.key"
        type="button"
        class="pl-step"
        :class="`is-${s.status}`"
        :title="`${s.detail}${s.fix_hint ? ' · ' + s.fix_hint : ''}`"
        @click="goto(s.page)"
      >
        <span class="pl-step__idx">{{ i + 1 }}</span>
        <span class="pl-step__body">
          <span class="pl-step__head">
            <i class="pl-step__dot" />
            {{ s.title }}
          </span>
          <span class="pl-step__value">{{ s.value || '—' }}</span>
        </span>
      </button>
      <div v-if="!steps.length" class="pl-steps__empty">正在读取链路状态…</div>
    </div>

    <NAlert v-if="blocked" type="error" :bordered="false" class="section-gap">
      <template #header>链路存在阻断项</template>
      标红的环节必须先修复才能应用；黄灯只是提醒，不阻止启动。
    </NAlert>

    <div class="pl-body section-gap">
      <!-- 左：参数，按「怎么进来 / 怎么出去 / 分流与记账」分组 -->
      <div class="pl-col">
        <NCard size="small" title="链路参数" class="pl-card">
          <p class="pl-lead">
            按 <b>1 → 6</b> 的顺序填，每一步都对应左侧链路里的一段；红字是该段当前的问题，点「去配置」直接跳转。
          </p>

          <div class="pl-seq">
            <section class="pl-seq__item" :class="{ 'is-bad': stepIsFail('protocol') }">
              <div class="pl-seq__head">
                <span class="pl-seq__no">1</span>
                <span class="pl-seq__title">协议</span>
                <span class="pl-seq__desc">用什么协议对外服务</span>
                <button class="pl-seq__go" type="button" @click="goto('protocols')">协议配置 →</button>
              </div>
              <div class="pl-grid">
                <label class="fld">
                  <span>承载内核</span>
                  <NSelect v-model:value="form.kernel" :options="kernelOptions" />
                </label>
                <label class="fld">
                  <span>接入协议</span>
                  <NSelect
                    v-model:value="form.protocol"
                    :options="[
                      { label: '混合端口（SOCKS5 + HTTP）', value: 'mixed' },
                      { label: 'SOCKS5', value: 'socks5' },
                      { label: 'HTTP 代理', value: 'http' },
                    ]"
                  />
                </label>
              </div>
              <div v-if="stepHint('protocol')" class="pl-seq__warn">{{ stepHint('protocol') }}</div>
            </section>

            <section class="pl-seq__item" :class="{ 'is-bad': stepIsFail('inbound') }">
              <div class="pl-seq__head">
                <span class="pl-seq__no">2</span>
                <span class="pl-seq__title">入口节点</span>
                <span class="pl-seq__desc">客户端先连到哪里</span>
                <button class="pl-seq__go" type="button" @click="goto('nodes')">节点管理 →</button>
              </div>
              <div class="pl-grid">
                <label class="fld pl-fld--wide">
                  <span>入口节点（第一跳，可留空 = 本机直接监听）</span>
                  <NSelect
                    v-model:value="form.inbound.host_id"
                    :options="entryOptions"
                    placeholder="不使用：本机直接监听"
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
              </div>
              <div v-if="stepHint('inbound')" class="pl-seq__warn">{{ stepHint('inbound') }}</div>
            </section>

            <section class="pl-seq__item" :class="{ 'is-bad': stepIsFail('rules') }">
              <div class="pl-seq__head">
                <span class="pl-seq__no">3</span>
                <span class="pl-seq__title">规则</span>
                <span class="pl-seq__desc">哪些流量直连、哪些走出口</span>
                <button class="pl-seq__go" type="button" @click="goto('rules')">规则配置 →</button>
              </div>
              <div class="pl-grid">
                <label class="fld pl-fld--wide">
                  <span>参与匹配的规则集（留空 = 所有已启用）</span>
                  <NSelect
                    v-model:value="form.rule_set_ids"
                    multiple
                    :options="ruleOptions"
                    placeholder="留空使用全部已启用规则集"
                  />
                </label>
              </div>
              <div v-if="stepHint('rules')" class="pl-seq__warn">{{ stepHint('rules') }}</div>
            </section>

            <section class="pl-seq__item" :class="{ 'is-bad': stepIsFail('dns') }">
              <div class="pl-seq__head">
                <span class="pl-seq__no">4</span>
                <span class="pl-seq__title">DNS</span>
                <span class="pl-seq__desc">域名在哪解析</span>
                <button class="pl-seq__go" type="button" @click="goto('dns')">DNS配置 →</button>
              </div>
              <div class="pl-grid">
                <label class="fld fld--switch pl-fld--wide">
                  <span>内核内置 DNS</span>
                  <NSwitch v-model:value="form.dns_enabled" />
                  <em>关闭时由 NGG DNS 与系统解析器处理，可避免端口冲突</em>
                </label>
              </div>
              <div v-if="stepHint('dns')" class="pl-seq__warn">{{ stepHint('dns') }}</div>
            </section>

            <section class="pl-seq__item" :class="{ 'is-bad': stepIsFail('outbound') }">
              <div class="pl-seq__head">
                <span class="pl-seq__no">5</span>
                <span class="pl-seq__title">出口节点</span>
                <span class="pl-seq__desc">流量从哪台 VPS 出去</span>
                <button class="pl-seq__go" type="button" @click="goto('nodes')">节点管理 →</button>
              </div>
              <div class="pl-grid">
                <label class="fld pl-fld--wide">
                  <span>出口节点（可多选，按顺序进入策略组；不选 = 直连）</span>
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
                  <em>仅手动选择模式生效，避免自动测速时绕过 VPS</em>
                </label>
              </div>
              <div v-if="stepHint('outbound')" class="pl-seq__warn">{{ stepHint('outbound') }}</div>
            </section>

            <section class="pl-seq__item" :class="{ 'is-bad': stepIsFail('users') }">
              <div class="pl-seq__head">
                <span class="pl-seq__no">6</span>
                <span class="pl-seq__title">用户</span>
                <span class="pl-seq__desc">哪些账号计入配额</span>
                <button class="pl-seq__go" type="button" @click="goto('users')">用户管理 →</button>
              </div>
              <div class="pl-grid">
                <label class="fld pl-fld--wide">
                  <span>计入配额的用户（仅记账，不参与握手）</span>
                  <NSelect
                    v-model:value="form.user_ids"
                    multiple
                    :options="userOptions"
                    placeholder="可不选"
                  />
                </label>
              </div>
              <div v-if="stepHint('users')" class="pl-seq__warn">{{ stepHint('users') }}</div>
            </section>
          </div>

          <div class="pl-actions">
            <NButton size="small" :loading="busy === 'save'" @click="save">保存参数</NButton>
            <span class="hint">保存不会影响运行中的内核；只有「应用并启动」才会下发配置。</span>
          </div>
          <div class="pl-actions">
            <label class="fld" style="flex: 1 1 220px">
              <span>链路名称 / 连通测试目标</span>
              <div class="pl-actions__row">
                <NInput v-model:value="form.name" placeholder="默认链路" />
                <NInput v-model:value="form.test_target" placeholder="www.gstatic.com:80" />
              </div>
            </label>
            <NButton size="small" :loading="busy === 'save'" @click="save">保存参数</NButton>
            <NButton size="small" type="primary" :loading="busy === 'apply'" @click="runApply">应用并启动</NButton>
          </div>
        </NCard>
      </div>

      <!-- 右：执行与结果 -->
      <div class="pl-col pl-col--side">
        <NCard size="small" title="执行" class="pl-card">
          <div class="pl-run">
            <div class="pl-run__row">
              <span class="pl-run__name">校验链路</span>
              <span class="pl-run__state" :class="blocked ? 'is-bad' : (warnCount ? 'is-warn' : 'is-ok')">
                {{ steps.length ? (blocked ? '阻断' : warnCount ? `${warnCount} 项注意` : '通过') : '未执行' }}
              </span>
              <NButton size="tiny" quaternary :loading="busy === 'validate'" @click="runValidate">重新校验</NButton>
            </div>
            <div class="pl-run__row">
              <span class="pl-run__name">生成配置</span>
              <span class="pl-run__state" :class="yaml ? 'is-ok' : 'is-idle'">
                {{ yaml ? `${yaml.split('\n').length} 行` : '未生成' }}
              </span>
              <NButton size="tiny" quaternary :loading="busy === 'render'" @click="runRender">
                {{ yaml ? '重新生成' : '预览' }}
              </NButton>
            </div>
            <div class="pl-run__row">
              <span class="pl-run__name">应用到内核</span>
              <span class="pl-run__state" :class="applied?.ok ? 'is-ok' : (applyError ? 'is-bad' : 'is-idle')">
                {{ applied?.ok ? '已生效' : applyError ? '失败' : '未应用' }}
              </span>
              <NButton size="tiny" quaternary :loading="busy === 'apply'" @click="runApply">应用</NButton>
            </div>
            <div class="pl-run__row">
              <span class="pl-run__name">端到端连通</span>
              <span class="pl-run__state" :class="testResult ? (testResult.ok ? 'is-ok' : 'is-bad') : 'is-idle'">
                <template v-if="testResult">
                  {{ testResult.ok ? `${(testResult.latency_ms ?? 0).toFixed(1)} ms` : '失败' }}
                </template>
                <template v-else>未测试</template>
              </span>
              <NButton size="tiny" quaternary :loading="busy === 'test'" @click="runTest(false)">测试</NButton>
            </div>
          </div>

          <div v-if="applyError" class="pl-note is-bad">{{ applyError }}</div>
          <div v-else-if="applied?.ok" class="pl-note is-ok">
            已应用到 {{ applied.kernel }}，入口 {{ applied.listen }}
          </div>
          <div v-else-if="testResult && !testResult.ok" class="pl-note is-bad">
            {{ testResult.message || testResult.error }}
          </div>
        </NCard>

        <NCard size="small" class="pl-card">
          <template #header>
            <div class="pl-card__head">
              <span>生成的配置</span>
              <NButton v-if="yaml" size="tiny" quaternary @click="showYaml = !showYaml">
                {{ showYaml ? '收起' : '展开' }}
              </NButton>
            </div>
          </template>
          <div v-if="renderWarnings.length" class="pl-note is-warn">
            {{ renderWarnings.length }} 条警告：{{ renderWarnings[0] }}
            <span v-if="renderWarnings.length > 1">…</span>
          </div>
          <NCode v-if="yaml && showYaml" :code="yaml" language="yaml" word-wrap class="yaml-box" />
          <div v-else-if="yaml" class="hint">已生成 {{ yaml.split('\n').length }} 行；点「展开」查看全文。</div>
          <div v-else class="hint">点「预览配置」生成 mihomo YAML。节点、规则与 DNS 全部来自上面六段。</div>
        </NCard>

        <NCard size="small" title="代理顺序" class="pl-card">
          <ol class="order-list">
            <li>客户端 → <span class="mono">{{ form.inbound.listen }}:{{ form.inbound.port }}</span></li>
            <li v-if="form.inbound.host_id">→ 入口节点（第一跳 VPS）</li>
            <li v-if="form.outbound.host_ids.length">
              → 出口节点（{{ form.outbound.host_ids.length }} 个 · {{ form.outbound.mode }}）
            </li>
            <li v-else>→ 直连（未选择出口节点）</li>
            <li>→ 目标；命中 DIRECT / REJECT 时提前分流</li>
          </ol>
        </NCard>
      </div>
    </div>

  </div>
</template>

<style scoped>
/* 六段状态条：一行看完，不再用六张卡片撑满首屏 */
.pl-steps {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.pl-step {
  flex: 1 1 150px;
  min-width: 132px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border: 1px solid #e8e8ed;
  border-radius: 10px;
  background: #fff;
  cursor: pointer;
  text-align: left;
  transition: border-color .15s ease, box-shadow .15s ease, transform .15s ease;
}
.pl-step:hover {
  border-color: #d7d7de;
  box-shadow: 0 4px 14px rgba(0,0,0,.05);
  transform: translateY(-1px);
}
.pl-step__idx {
  flex: 0 0 18px;
  height: 18px;
  border-radius: 50%;
  background: #f2f2f5;
  color: #8e8e93;
  font-size: 10.5px;
  line-height: 18px;
  text-align: center;
  font-variant-numeric: tabular-nums;
}
.pl-step__body { min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.pl-step__head {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 10.5px;
  color: #6e6e73;
}
.pl-step__dot { width: 6px; height: 6px; border-radius: 50%; background: #c7c7cc; }
.pl-step__value {
  font-size: 12px;
  color: #1d1d1f;
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.pl-step.is-ok .pl-step__dot { background: #2fb457; }
.pl-step.is-warn .pl-step__dot { background: #f0a020; }
.pl-step.is-fail .pl-step__dot { background: #ff3b30; }
.pl-step.is-fail { border-color: #f3c9c6; background: #fdf6f5; }
.pl-step.is-skip .pl-step__dot { background: #b0b0b8; }
.pl-steps__empty { color: #8e8e93; font-size: 12px; padding: 10px 0; }

/* 主体：左参数 3 组 / 右侧执行与结果 */
.pl-body {
  display: grid;
  grid-template-columns: minmax(0, 1.45fr) minmax(0, 1fr);
  gap: 12px;
  align-items: start;
}
@media (max-width: 1180px) {
  .pl-body { grid-template-columns: 1fr; }
}
.pl-col { display: flex; flex-direction: column; gap: 12px; min-width: 0; }
.pl-card { min-width: 0; }
.pl-card__head { display: flex; align-items: center; justify-content: space-between; gap: 10px; width: 100%; }

.pl-lead {
  margin: 0 0 12px;
  font-size: 11.5px;
  line-height: 1.6;
  color: #6e6e73;
}
.pl-lead b { color: #1d1d1f; }

/* 1 → 6 编号步骤：每一步对应链路里的一段，问题直接显示在对应步骤下面 */
.pl-seq { display: flex; flex-direction: column; gap: 10px; }
.pl-seq__item {
  border: 1px solid #eeeef2;
  border-radius: 10px;
  padding: 9px 11px 10px;
  background: #fcfcfd;
}
.pl-seq__item.is-bad { border-color: #f3c9c6; background: #fdf6f5; }
.pl-seq__head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}
.pl-seq__no {
  flex: 0 0 20px;
  height: 20px;
  border-radius: 50%;
  background: #0071e3;
  color: #fff;
  font-size: 11px;
  font-weight: 600;
  line-height: 20px;
  text-align: center;
}
.pl-seq__item.is-bad .pl-seq__no { background: #ff3b30; }
.pl-seq__title { font-size: 12.5px; font-weight: 600; color: #1d1d1f; }
.pl-seq__desc { flex: 1 1 auto; font-size: 10.5px; color: #8e8e93; }
.pl-seq__go {
  border: 0;
  background: none;
  color: #0071e3;
  font-size: 11px;
  cursor: pointer;
  padding: 0;
}
.pl-seq__warn {
  margin-top: 7px;
  font-size: 10.5px;
  line-height: 1.5;
  color: #b32442;
}
.pl-actions__row { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
@media (max-width: 720px) {
  .pl-actions__row { grid-template-columns: 1fr; }
}

.pl-group + .pl-group { margin-top: 16px; }
.pl-group__legend {
  font-size: 11px;
  font-weight: 600;
  color: #6e6e73;
  letter-spacing: .02em;
  margin-bottom: 8px;
}
.pl-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px 12px;
}
@media (max-width: 720px) {
  .pl-grid { grid-template-columns: 1fr; }
}
.pl-fld--wide { grid-column: 1 / -1; }
.fld { display: flex; flex-direction: column; gap: 5px; min-width: 0; }
.fld > span { font-size: 11.5px; color: #6e6e73; }
.fld--switch { display: grid; grid-template-columns: auto 1fr; align-items: center; gap: 4px 8px; }
.fld--switch > span { grid-column: 1 / -1; }
.fld--switch em { font-size: 10.5px; color: #8e8e93; font-style: normal; }
.pl-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 16px;
  padding-top: 12px;
  border-top: 1px solid #f0f0f2;
}

/* 执行状态：一行一件事，状态与动作都在右侧 */
.pl-run { display: flex; flex-direction: column; }
.pl-run__row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 0;
  border-bottom: 1px solid #f4f4f6;
  font-size: 12.5px;
}
.pl-run__row:last-child { border-bottom: 0; }
.pl-run__name { flex: 1 1 auto; color: #1d1d1f; }
.pl-run__state { font-variant-numeric: tabular-nums; font-size: 11.5px; }
.pl-run__state.is-ok { color: #1f7a3a; }
.pl-run__state.is-warn { color: #b26a00; }
.pl-run__state.is-bad { color: #b32442; }
.pl-run__state.is-idle { color: #8e8e93; }

.pl-note {
  margin-top: 10px;
  padding: 7px 9px;
  border-radius: 8px;
  font-size: 11.5px;
  line-height: 1.5;
}
.pl-note.is-ok { background: #eef8f1; color: #1f7a3a; }
.pl-note.is-warn { background: #fdf6e8; color: #8a5a00; }
.pl-note.is-bad { background: #fdeeee; color: #b32442; }

.yaml-box {
  max-height: 260px;
  overflow: auto;
  margin-top: 8px;
}

.order-list { margin: 0; padding-left: 18px; font-size: 12px; line-height: 1.9; color: #1d1d1f; }

.fix-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 0;
  border-bottom: 1px solid #f4f4f6;
  flex-wrap: wrap;
}
.fix-row:last-child { border-bottom: 0; }
.fix-text { flex: 1 1 320px; font-size: 12px; color: #4a4a4f; }
.chain-link {
  border: 0;
  background: none;
  color: #0071e3;
  font-size: 12px;
  cursor: pointer;
  padding: 0;
}
</style>
