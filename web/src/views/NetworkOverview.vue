<script setup lang="ts">
/**
 * 网络概览 — Apple HIG 风格的网络网关仪表盘。
 *
 * 设计参考：neko-master（实时流量趋势 / 多时间窗口）、nx-migrate（网关总览）、
 * Surge Dashboard（连接与策略的清晰呈现）。配色为人工校正后的 Apple HIG：
 * #F5F5F7 底 / 白色大圆角卡片 / #0071E3 蓝 / #FF9F0A 橙 / SF 字体栈。
 *
 * 数据诚实性：所有数值来自 /network/metrics/history 的真实后端采样与
 * /network/status。网络引擎未运行时不绘制任何模拟曲线、不编造速率。
 */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { NAlert, NButton, NSpace, NTag } from 'naive-ui'
import * as api from '../api/ngg'
import type { MetricsHistoryResult, MetricsSample, NetworkComponent, NetworkStatus } from '../api/ngg'
import TrafficAreaChart from '../components/TrafficAreaChart.vue'
import { useApiAction } from '../composables/useApi'
import { formatBytes, formatRate, formatTime } from '../utils/format'

const history = ref<MetricsHistoryResult | null>(null)
const status = ref<NetworkStatus | null>(null)
const windowMin = ref(5)
const chartMode = ref<'rate' | 'total' | 'connections'>('rate')
const refreshing = ref(false)
const lastUpdated = ref('')
const loadError = ref('')
let timer: number | undefined

const router = useRouter()
const { isBusy, run } = useApiAction()

/* 网络组件 / 配置概览：每个数据源独立抓取，单接口失败不影响其它卡片。 */
const components = ref<NetworkComponent[]>([])
const liveMetrics = ref<Record<string, unknown>>({})
const engineRunning = ref<boolean | null>(null)
const nodeCount = ref<number | null>(null)
const ruleCount = ref<number | null>(null)
const ruleSetCount = ref<number | null>(null)
const userCount = ref<number | null>(null)

function countText(v: number | null): string {
  return v === null ? '—' : v.toLocaleString('zh-CN')
}
function go(name: string): void {
  void router.push({ name })
}

async function loadExtras(): Promise<void> {
  const results = await Promise.allSettled([
    api.getNetworkStatus(),
    api.listNodes(),
    api.getRules(),
    api.listRuleSets(),
    api.listUsers(),
  ])
  if (results[0].status === 'fulfilled') {
    const s = results[0].value
    components.value = Array.isArray(s.components) ? s.components : []
    engineRunning.value = s.running === true
    liveMetrics.value = (s.metrics ?? {}) as Record<string, unknown>
  }
  if (results[1].status === 'fulfilled') {
    const d = results[1].value
    nodeCount.value = Array.isArray(d.nodes)
      ? d.nodes.length
      : (typeof d.count === 'number' ? d.count : null)
  }
  if (results[2].status === 'fulfilled') {
    const d = results[2].value
    ruleCount.value = Array.isArray(d.rules) ? d.rules.length : null
  }
  if (results[3].status === 'fulfilled') {
    const d = results[3].value
    ruleSetCount.value = typeof d.count === 'number'
      ? d.count
      : (Array.isArray(d.rulesets) ? d.rulesets.length : null)
  }
  if (results[4].status === 'fulfilled') {
    const d = results[4].value
    userCount.value = typeof d.count === 'number'
      ? d.count
      : (Array.isArray(d.users) ? d.users.length : null)
  }
}

async function toggleEngine(action: 'start' | 'stop'): Promise<void> {
  const r = await run(
    'engine-' + action,
    () => (action === 'start' ? api.startNetwork() : api.stopNetwork()),
    action === 'start' ? '网络引擎启动命令已下发' : '网络引擎停止命令已下发',
  )
  if (r) {
    await refresh()
    await loadExtras()
  }
}

const samples = computed<MetricsSample[]>(() => history.value?.samples ?? [])
const running = computed(() => history.value?.running === true)
const latest = computed<MetricsSample | null>(() =>
  samples.value.length ? samples.value[samples.value.length - 1] : null,
)
/** 数据源：虚拟网卡在线时，图表读取网卡级真实收发字节（整机流量）。 */
const tunSource = computed(() => history.value?.source === 'tun' && history.value?.tun_open === true)
const sourceLabel = computed(() => (tunSource.value ? 'NGG 虚拟网卡' : '统一网络引擎'))
const counterHint = computed(() =>
  tunSource.value ? '虚拟网卡真实收发字节（整机流量）' : '网络引擎真实会话字节',
)

function rateText(v?: number): string {
  return v === undefined ? '—' : formatRate(v)
}
function byteText(v?: number): string {
  return v === undefined ? '—' : formatBytes(v)
}
function intText(v?: number): string {
  return v === undefined ? '—' : Math.round(v).toLocaleString('zh-CN')
}

const peakIn = computed(() => Math.max(0, ...samples.value.map((s) => s.rate_in)))
const peakOut = computed(() => Math.max(0, ...samples.value.map((s) => s.rate_out)))
const totalPeak = computed(() => Math.max(0, ...samples.value.map((s) => s.rate_in + s.rate_out)))

const rateInSeries = computed(() => samples.value.map((s) => s.rate_in))
const rateOutSeries = computed(() => samples.value.map((s) => s.rate_out))

/** Inline mini sparkline, same real samples, no extra dependency. */
function sparkPoints(values: number[], w = 160, h = 40): string {
  if (!values.length) return ''
  const max = Math.max(1, ...values)
  const denom = Math.max(1, values.length - 1)
  return values
    .map((v, i) => `${((i / denom) * w).toFixed(1)},${(h - 2 - (Math.max(0, v) / max) * (h - 6)).toFixed(1)}`)
    .join(' ')
}
function sparkArea(values: number[], w = 160, h = 40): string {
  const pts = sparkPoints(values, w, h)
  if (!pts) return ''
  return `M 0,${h} L ${pts.replace(/ /g, ' L ')} L ${w},${h} Z`
}
const inSpark = computed(() => sparkPoints(rateInSeries.value))
const outSpark = computed(() => sparkPoints(rateOutSeries.value))
const inSparkArea = computed(() => sparkArea(rateInSeries.value))
const outSparkArea = computed(() => sparkArea(rateOutSeries.value))

const statusMap = computed<Record<string, any>>(() => (status.value ?? {}) as Record<string, any>)
const endpointCount = computed(() => {
  const e = statusMap.value.endpoints
  return Array.isArray(e) ? e.length : 0
})
const sessionCount = computed(() => {
  const s = statusMap.value.sessions
  return typeof s === 'number' ? s : 0
})

const flowRows = computed(() => {
  const l = latest.value
  return [
    { label: '活跃连接', value: intText(l?.active), tone: 'blue' },
    { label: '累计连接', value: intText(l?.connections), tone: 'blue' },
    { label: '直连请求', value: intText(l?.direct), tone: 'green' },
    { label: '代理请求', value: intText(l?.proxied), tone: 'orange' },
    { label: '丢弃连接', value: intText(l?.dropped), tone: 'red' },
    { label: 'DNS 查询', value: intText(l?.dns_queries), tone: 'blue' },
    { label: '规则命中', value: intText(l?.rule_matches), tone: 'green' },
    { label: '故障切换', value: intText(l?.failovers), tone: 'orange' },
  ]
})

const DROP_LABELS: Array<{ key: string; label: string; tone: string }> = [
  { key: 'dial', label: '直连失败', tone: 'red' },
  { key: 'no_kernel', label: '无可用内核', tone: 'orange' },
  { key: 'blocked', label: '规则拦截', tone: 'red' },
  { key: 'loop', label: '环路拒绝', tone: 'orange' },
  { key: 'udp', label: 'UDP 不可代理', tone: 'blue' },
  { key: 'session', label: '会话创建失败', tone: 'red' },
  { key: 'rule', label: '规则错误', tone: 'red' },
]

/** 丢弃原因：只在有丢弃时展示，数值直接来自后端真实计数。 */
const dropReasons = computed(() => {
  const raw = (liveMetrics.value.drop_reasons ?? {}) as Record<string, number>
  return DROP_LABELS
    .map((r) => ({ ...r, value: Number(raw[r.key] ?? 0) }))
    .filter((r) => r.value > 0)
    .sort((a, b) => b.value - a.value)
})
const dropTotal = computed(() => dropReasons.value.reduce((sum, r) => sum + r.value, 0))

const WINDOWS = [5, 15, 30]
const MODES: Array<{ id: 'rate' | 'total' | 'connections'; label: string }> = [
  { id: 'rate', label: '实时速率' },
  { id: 'total', label: '累计流量' },
  { id: 'connections', label: '连接数' },
]

async function refresh(): Promise<void> {
  const results = await Promise.allSettled([
    api.getNetworkMetricsHistory(windowMin.value),
    api.getNetworkStatus(),
  ])
  if (results[0].status === 'fulfilled') {
    history.value = results[0].value
    loadError.value = ''
  }
  if (results[1].status === 'fulfilled') status.value = results[1].value
  if (results[0].status === 'rejected' && results[1].status === 'rejected') {
    loadError.value = '无法读取网络指标，请检查 API Token 与后端服务'
  }
  lastUpdated.value = formatTime(new Date().toISOString())
}

async function manualRefresh(): Promise<void> {
  refreshing.value = true
  try {
    await refresh()
  } finally {
    refreshing.value = false
  }
}

function setWindow(min: number): void {
  if (windowMin.value === min) return
  windowMin.value = min
  void refresh()
}

watch(running, (now, before) => {
  // Engine state changed: pull a fresh snapshot immediately.
  if (now !== before) void refresh()
})

onMounted(() => {
  void refresh()
  void loadExtras()
  timer = window.setInterval(() => void refresh(), 2000)
})
onUnmounted(() => {
  if (timer !== undefined) window.clearInterval(timer)
})
</script>

<template>
  <div class="netov">
    <!-- Header -->
    <div class="netov-head">
      <div>
        <div class="netov-eyebrow">NETWORK OVERVIEW</div>
        <h2 class="netov-title">网络概览</h2>
        <p class="netov-subtitle">统一网络引擎的真实流量、速率与连接状态</p>
      </div>
      <div class="netov-head__right">
        <div class="netov-status" :class="running ? 'is-on' : 'is-off'">
          <span class="netov-status__dot" />
          {{ running ? '运行中' : '未运行' }}
        </div>
        <div class="netov-seg" role="group" aria-label="时间窗口">
          <button
            v-for="w in WINDOWS"
            :key="w"
            type="button"
            :class="{ 'is-active': windowMin === w }"
            @click="setWindow(w)"
          >{{ w }} 分钟</button>
        </div>
        <button class="netov-refresh" type="button" :disabled="refreshing" @click="manualRefresh">
          {{ refreshing ? '刷新中' : '刷新' }}
        </button>
      </div>
    </div>

    <NAlert v-if="loadError" type="error" :bordered="false" class="netov-alert">
      {{ loadError }}。页面不会用模拟数据填充。
    </NAlert>
    <NAlert v-else-if="history && !running" type="warning" :bordered="false" class="netov-alert">
      {{ history.note || '网络引擎未运行，当前没有真实流量数据。' }}
      可在「后端管理 → 网络引擎」启动后查看实时曲线。
    </NAlert>

    <!-- 速率 + 趋势：1:1 两等分（规格：每张卡高 208px，间距 10px，各占 50%） -->
    <section class="netov-split">
      <div class="netov-card netov-rate-card">
        <div class="netov-card__head">
          <div>
            <h3 class="netov-card__title">当前总速率</h3>
            <p class="netov-card__sub">数据源 {{ sourceLabel }}</p>
          </div>
          <NTag size="small" :bordered="false" :type="running ? 'success' : 'default'">
            {{ running ? '实时' : '停止' }}
          </NTag>
        </div>

        <div class="netov-rate">
          <div class="netov-rate__main">
            <div class="netov-rate__value">
              {{ rateText((latest?.rate_in ?? 0) + (latest?.rate_out ?? 0)) }}
            </div>
            <div class="netov-rate__meta">
              <span><i class="netov-dot netov-dot--in" />下载 {{ rateText(latest?.rate_in) }}</span>
              <span><i class="netov-dot netov-dot--out" />上传 {{ rateText(latest?.rate_out) }}</span>
            </div>
          </div>

          <div class="netov-rate__sparks">
            <div class="netov-spark">
              <div class="netov-spark__head">
                <span><i class="netov-dot netov-dot--in" />下载速率</span>
                <b>{{ rateText(latest?.rate_in) }}</b>
              </div>
              <svg viewBox="0 0 180 44" preserveAspectRatio="none" aria-label="下载速率迷你趋势图">
                <defs>
                  <linearGradient id="spark-in" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stop-color="#0071E3" stop-opacity="0.3" />
                    <stop offset="100%" stop-color="#0071E3" stop-opacity="0" />
                  </linearGradient>
                </defs>
                <path v-if="inSparkArea" :d="inSparkArea" fill="url(#spark-in)" />
                <polyline v-if="inSpark" :points="inSpark" class="netov-spark__line netov-spark__line--in" />
              </svg>
            </div>
            <div class="netov-spark">
              <div class="netov-spark__head">
                <span><i class="netov-dot netov-dot--out" />上传速率</span>
                <b>{{ rateText(latest?.rate_out) }}</b>
              </div>
              <svg viewBox="0 0 180 44" preserveAspectRatio="none" aria-label="上传速率迷你趋势图">
                <defs>
                  <linearGradient id="spark-out" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stop-color="#FF9F0A" stop-opacity="0.28" />
                    <stop offset="100%" stop-color="#FF9F0A" stop-opacity="0" />
                  </linearGradient>
                </defs>
                <path v-if="outSparkArea" :d="outSparkArea" fill="url(#spark-out)" />
                <polyline v-if="outSpark" :points="outSpark" class="netov-spark__line netov-spark__line--out" />
              </svg>
            </div>
          </div>

          <div class="netov-rate__hint">
            峰值 {{ rateText(totalPeak) }} · {{ samples.length }} 点 / {{ windowMin }} 分钟 ·
            数据源 <b class="netov-source">{{ sourceLabel }}</b>
          </div>
        </div>
      </div>

      <div class="netov-card netov-chart-card">
        <div class="netov-card__head">
          <div>
            <h3 class="netov-card__title">流量趋势</h3>
            <p class="netov-card__sub">共享纵轴 · 数据源 {{ sourceLabel }}</p>
          </div>
          <div class="netov-seg" role="group" aria-label="图表指标">
            <button
              v-for="m in MODES"
              :key="m.id"
              type="button"
              :class="{ 'is-active': chartMode === m.id }"
              @click="chartMode = m.id"
            >{{ m.label }}</button>
          </div>
        </div>
        <TrafficAreaChart :samples="samples" :mode="chartMode" />
        <div class="netov-chart-foot">
          <span>最后更新 {{ lastUpdated || '—' }}</span>
          <span>每 2 秒刷新</span>
        </div>
      </div>
    </section>

    <!-- KPI row -->
    <section class="netov-grid netov-grid--4">
      <div class="netov-kpi">
        <div class="netov-kpi__label"><i class="netov-dot netov-dot--in" />累计下载</div>
        <div class="netov-kpi__value">{{ byteText(latest?.bytes_in) }}</div>
        <div class="netov-kpi__sub">{{ counterHint }}</div>
      </div>
      <div class="netov-kpi">
        <div class="netov-kpi__label"><i class="netov-dot netov-dot--out" />累计上传</div>
        <div class="netov-kpi__value">{{ byteText(latest?.bytes_out) }}</div>
        <div class="netov-kpi__sub">{{ counterHint }}</div>
      </div>
      <div class="netov-kpi">
        <div class="netov-kpi__label">峰值下载 / 上传</div>
        <div class="netov-kpi__value netov-kpi__value--sm">{{ rateText(peakIn) }}</div>
        <div class="netov-kpi__sub">上传峰值 {{ rateText(peakOut) }}</div>
      </div>
      <div class="netov-kpi">
        <div class="netov-kpi__label">活跃连接</div>
        <div class="netov-kpi__value">{{ intText(latest?.active) }}</div>
        <div class="netov-kpi__sub">累计连接 {{ intText(latest?.connections) }}</div>
      </div>
    </section>

    <!-- Detail row -->
    <section class="netov-grid netov-grid--2">
      <div class="netov-card">
        <div class="netov-card__head">
          <div>
            <h3 class="netov-card__title">连接与策略</h3>
            <p class="netov-card__sub">最新一次真实采样的累计计数</p>
          </div>
          <NTag size="small" :bordered="false" :type="tunSource ? 'success' : 'default'">
            {{ running ? sourceLabel : '停止' }}
          </NTag>
        </div>
        <div class="netov-flow">
          <div v-for="row in flowRows" :key="row.label" class="netov-flow__row">
            <span class="netov-flow__label">{{ row.label }}</span>
            <span class="netov-flow__value" :class="`tone-${row.tone}`">{{ row.value }}</span>
          </div>
        </div>
        <div v-if="dropTotal > 0" class="netov-drop">
          <span class="netov-drop__title">丢弃原因</span>
          <span v-for="r in dropReasons" :key="r.key" class="netov-drop__item">
            {{ r.label }}<b :class="`tone-${r.tone}`">{{ r.value.toLocaleString('zh-CN') }}</b>
          </span>
        </div>
      </div>

      <div class="netov-card">
        <div class="netov-card__head">
          <div>
            <h3 class="netov-card__title">统一网络引擎</h3>
            <p class="netov-card__sub">入口、TUN 与内核端点状态</p>
          </div>
        </div>
        <div class="netov-engine">
          <div class="netov-engine__row">
            <span>运行状态</span>
            <b :class="running ? 'is-on' : 'is-off'">{{ running ? '运行中' : '未运行' }}</b>
          </div>
          <div class="netov-engine__row"><span>会话数</span><b>{{ intText(sessionCount) }}</b></div>
          <div class="netov-engine__row"><span>内核端点</span><b>{{ intText(endpointCount) }}</b></div>
          <div class="netov-engine__row">
            <span>安全策略</span>
            <b>{{ statusMap.fail_closed ? 'Fail-Closed' : 'Fail-Open' }} · {{ statusMap.failover ? 'Failover 开' : 'Failover 关' }}</b>
          </div>
        </div>
        <div class="netov-engine__note">
          本卡只保留引擎级状态；统一入口与 TUN 的明细在下方「网络组件」。
          内核端点为 NGG Core Adapter Bridge 的真实连接目标；CPU / 内存等进程指标在「内核管理」查看。
        </div>
      </div>
    </section>

    <!-- 网络组件 / 配置概览 -->
    <section class="netov-grid netov-grid--2">
      <div class="netov-card">
        <div class="netov-card__head">
          <div>
            <h3 class="netov-card__title">网络组件</h3>
            <p class="netov-card__sub">可独立观测的 4 个数据面组件 · 点状态可刷新</p>
          </div>
          <div class="netov-controls">
            <button
              type="button"
              class="netov-state"
              :class="{ 'is-on': engineRunning === true }"
              title="点击刷新状态"
              @click="refresh()"
            >
              <i class="netov-dot" />
              {{ engineRunning === null ? '状态未知' : (engineRunning ? '运行中' : '未运行') }}
            </button>
            <NButton
              size="tiny"
              type="primary"
              :disabled="engineRunning !== false"
              :loading="isBusy('engine-start')"
              :title="engineRunning === true ? '引擎已在运行' : '启动统一网络引擎'"
              @click="toggleEngine('start')"
            >启动</NButton>
            <NButton
              size="tiny"
              type="error"
              secondary
              :disabled="engineRunning !== true"
              :loading="isBusy('engine-stop')"
              :title="engineRunning === true ? '停止统一网络引擎' : '引擎未运行'"
              @click="toggleEngine('stop')"
            >停止</NButton>
            <NButton size="tiny" quaternary @click="go('network')">前往网络引擎 →</NButton>
          </div>
        </div>
        <div class="netov-components">
          <div v-for="c in components" :key="c.key" class="netov-component">
            <div class="netov-component__main">
              <span class="netov-component__label">{{ c.label }}</span>
              <span class="netov-component__desc">{{ c.description }}</span>
            </div>
            <NTag
              size="small"
              :bordered="false"
              :type="c.running ? 'success' : (c.enabled ? 'info' : 'default')"
            >
              {{ c.running ? '运行中' : (c.enabled ? '已启用' : '未启用') }}
            </NTag>
          </div>
          <div v-if="!components.length" class="netov-component__empty">—</div>
        </div>
      </div>

      <div class="netov-card">
        <div class="netov-card__head">
          <div>
            <h3 class="netov-card__title">配置概览</h3>
            <p class="netov-card__sub">各接口独立读取 · 失败项显示「—」</p>
          </div>
        </div>
        <div class="netov-config">
          <button type="button" class="netov-config__item" @click="go('inbound')">
            <span class="netov-config__label">节点数</span>
            <span class="netov-config__value">{{ countText(nodeCount) }}</span>
            <span class="netov-config__go">入口节点 →</span>
          </button>
          <button type="button" class="netov-config__item" @click="go('rules')">
            <span class="netov-config__label">规则数</span>
            <span class="netov-config__value">{{ countText(ruleCount) }}</span>
            <span class="netov-config__go">规则配置 →</span>
          </button>
          <button type="button" class="netov-config__item" @click="go('dns')">
            <span class="netov-config__label">规则集数</span>
            <span class="netov-config__value">{{ countText(ruleSetCount) }}</span>
            <span class="netov-config__go">DNS 配置 →</span>
          </button>
          <button type="button" class="netov-config__item" @click="go('users')">
            <span class="netov-config__label">用户数</span>
            <span class="netov-config__value">{{ countText(userCount) }}</span>
            <span class="netov-config__go">用户管理 →</span>
          </button>
        </div>
      </div>
    </section>

    <p class="netov-foot">
      数据来源：<span class="mono">GET /api/v1/network/metrics/history</span>（后端每 2 秒采样统一网络引擎计数器）与
      <span class="mono">GET /api/v1/network/status</span>。速率 = 相邻真实计数差 ÷ 真实时间差；
      引擎重启导致计数归零时按 0 处理，不生成尖峰。所有未采集字段显示「—」。
    </p>
  </div>
</template>

<style scoped>
.netov {
  font-family: -apple-system, BlinkMacSystemFont, 'SF Pro Display', 'SF Pro Text',
    'PingFang SC', 'Microsoft YaHei', sans-serif;
  background: #f5f5f7;
  border-radius: 12px;
  padding: 12px;
  color: #1d1d1f;
}
.netov :deep(.mono) {
  font-family: 'SF Mono Nerd Font', ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.92em;
}

.netov-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 18px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.netov-eyebrow {
  font-size: 10.5px;
  letter-spacing: 0.14em;
  color: #8e8e93;
  font-weight: 600;
}
.netov-title {
  margin: 4px 0 2px;
  font-size: 18px;
  font-weight: 650;
  letter-spacing: -0.025em;
  line-height: 1.2;
}
.netov-subtitle {
  margin: 0;
  font-size: 13.5px;
  color: #6e6e73;
}
.netov-head__right {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.netov-status {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-size: 12.5px;
  font-weight: 600;
  padding: 7px 13px;
  border-radius: 999px;
  background: #fff;
  border: 1px solid #e8e8ed;
}
.netov-status__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #8e8e93;
}
.netov-status.is-on .netov-status__dot { background: #34c759; box-shadow: 0 0 0 3px rgba(52,199,89,.15); }
.netov-status.is-off .netov-status__dot { background: #ff9f0a; box-shadow: 0 0 0 3px rgba(255,159,10,.14); }

.netov-seg {
  display: inline-flex;
  gap: 2px;
  padding: 3px;
  background: #e9e9ed;
  border-radius: 11px;
}
.netov-seg button {
  appearance: none;
  border: 0;
  background: transparent;
  color: #6e6e73;
  font: inherit;
  font-size: 12.5px;
  padding: 6px 13px;
  border-radius: 8px;
  cursor: pointer;
  transition: background .18s ease, color .18s ease, box-shadow .18s ease;
}
.netov-seg button:hover { color: #1d1d1f; }
.netov-seg button.is-active {
  background: #fff;
  color: #1d1d1f;
  font-weight: 600;
  box-shadow: 0 1px 3px rgba(0,0,0,.1);
}
.netov-refresh {
  appearance: none;
  border: 1px solid #e8e8ed;
  background: #fff;
  color: #0071e3;
  font: inherit;
  font-size: 12.5px;
  font-weight: 600;
  padding: 8px 16px;
  border-radius: 11px;
  cursor: pointer;
}
.netov-refresh:disabled { opacity: .55; cursor: default; }
.netov-alert { margin-bottom: 8px; border-radius: 14px !important; }

/* 布局规格（v2.7.4）：速率卡与趋势卡 1:1 等分（各占 50%），间距 10px，
   桌面端统一高度 208px；≤1100px 自动堆叠为单列。卡内高度分配：
   标题行 34px + 主体 150px + 底部说明 24px。 */
.netov-split {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  align-items: stretch;
  margin-bottom: 10px;
}
.netov-rate-card {
  min-height: 208px;
  display: flex;
  flex-direction: column;
}
.netov-rate__main {
  display: flex;
  align-items: baseline;
  gap: 14px;
  flex-wrap: wrap;
}
.netov-rate__main {
  display: flex;
  align-items: baseline;
  gap: 14px;
  flex-wrap: wrap;
}
.netov-rate {
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
  min-width: 0;
}
.netov-rate__main {
  min-width: 0;
}
.netov-rate__value {
  font-size: 24px;
  font-weight: 650;
  letter-spacing: -0.03em;
  line-height: 1.1;
  margin: 2px 0 4px;
  font-variant-numeric: tabular-nums;
}
.netov-rate__meta {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  font-size: 11.5px;
  color: #1d1d1f;
}
.netov-rate__meta span {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.netov-rate__hint {
  margin-top: 6px;
  font-size: 10.5px;
  color: #8e8e93;
}
.netov-rate__sparks {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
  flex: 1 1 auto;
  min-height: 62px;
}
.netov-spark {
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  min-width: 0;
  background: #fbfbfd;
  border: 1px solid #eeeef2;
  border-radius: 8px;
  padding: 5px 8px 4px;
}
.netov-spark__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 5px;
  font-size: 10.5px;
  color: #6e6e73;
}
.netov-spark__head span {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.netov-spark__head b {
  font-size: 11px;
  font-weight: 600;
  color: #1d1d1f;
  font-variant-numeric: tabular-nums;
}
.netov-spark svg {
  width: 100%;
  height: 32px;
  display: block;
  margin-top: 4px;
}
.netov-spark__line {
  fill: none;
  stroke-width: 2;
  vector-effect: non-scaling-stroke;
}
.netov-spark__line--in { stroke: #0071e3; }
.netov-spark__line--out { stroke: #ff9f0a; }
.netov-source { color: #0071e3; font-weight: 600; }

.netov-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: 0 0 auto;
}
.netov-dot--in { background: #0071e3; }
.netov-dot--out { background: #ff9f0a; }

.netov-grid { display: grid; gap: 10px; margin-bottom: 12px; }
.netov-grid--4 { grid-template-columns: repeat(4, minmax(0, 1fr)); }
.netov-grid--2 { grid-template-columns: repeat(2, minmax(0, 1fr)); }

.netov-kpi,
.netov-card {
  background: #fff;
  border: 1px solid #e8e8ed;
  border-radius: 10px;
  box-shadow: 0 1px 2px rgba(0,0,0,.03), 0 8px 24px rgba(0,0,0,.035);
}
.netov-kpi { padding: 6px 9px 7px; }
.netov-kpi__label {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11.5px;
  color: #6e6e73;
  font-weight: 500;
}
.netov-kpi__value {
  font-size: 14.5px;
  font-weight: 650;
  letter-spacing: -0.025em;
  margin: 2px 0 2px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.netov-kpi__value--sm { font-size: 13.5px; }
.netov-kpi__sub { font-size: 10.5px; color: #8e8e93; line-height: 1.45; }

.netov-card { padding: 8px 10px; }
.netov-card__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.netov-card__title { margin: 0; font-size: 12.5px; font-weight: 600; letter-spacing: -.01em; }
.netov-card__sub { margin: 3px 0 0; font-size: 10.5px; color: #8e8e93; }
.netov-chart-card {
  margin-bottom: 0;
  display: flex;
  flex-direction: column;
  min-height: 208px;
}
/* 图表画布在等分卡里固定 118px（原为整行 148px） */
.netov-chart-card :deep(.tac-canvas) {
  height: 118px;
}
.netov-chart-foot {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: auto;
  padding-top: 6px;
  border-top: 1px solid #f0f0f2;
  font-size: 10.5px;
  color: #8e8e93;
}

.netov-flow { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0 22px; }
.netov-flow__row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 5px 0;
  border-bottom: 1px solid #f2f2f4;
  font-size: 11.5px;
}
.netov-flow__label { color: #6e6e73; }
.netov-flow__value { font-weight: 600; font-variant-numeric: tabular-nums; }
.tone-blue { color: #0071e3; }
.tone-green { color: #34a853; }
.tone-orange { color: #d97706; }
.tone-red { color: #ff3b30; }

.netov-engine__row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 5px 0;
  border-bottom: 1px solid #f2f2f4;
  font-size: 11.5px;
}
.netov-engine__row span { color: #6e6e73; }
.netov-engine__row b { font-weight: 600; text-align: right; }
.netov-engine__row b.is-on { color: #34a853; }
.netov-engine__row b.is-off { color: #ff9f0a; }
.netov-engine__note { margin-top: 14px; font-size: 11.5px; color: #8e8e93; line-height: 1.6; }

.netov-foot {
  margin: 18px 4px 0;
  font-size: 11.5px;
  color: #8e8e93;
  line-height: 1.7;
}

.netov-components { display: grid; gap: 0; }
.netov-component {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 9px 0;
  border-bottom: 1px solid #f2f2f4;
  font-size: 12.5px;
}
.netov-component:last-child { border-bottom: 0; }
.netov-component__main { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.netov-component__label { color: #1d1d1f; font-weight: 500; }
.netov-component__desc { color: #8e8e93; font-size: 10.5px; line-height: 1.35; }
.netov-component__empty { padding: 9px 0; color: #8e8e93; font-size: 12.5px; }

/* 组件卡右上角的可点击操作区：状态徽标可点刷新，启动/停止是真实按钮 */
.netov-controls {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.netov-state {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 9px;
  border-radius: 999px;
  border: 1px solid #e8e8ed;
  background: #f5f5f7;
  color: #6e6e73;
  font-size: 11.5px;
  font-weight: 500;
  line-height: 1.5;
  cursor: pointer;
  transition: background .15s ease, border-color .15s ease, color .15s ease;
}
.netov-state:hover { background: #ededf0; border-color: #dcdce2; }
.netov-state .netov-dot { width: 7px; height: 7px; background: #b0b0b8; }
.netov-state.is-on {
  background: #eaf7ef;
  border-color: #cdebd8;
  color: #1f7a3a;
}
.netov-state.is-on .netov-dot { background: #2fb457; }

/* 丢弃原因：一句话把「丢包」拆开，数值全部来自后端真实计数 */
.netov-drop {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 8px;
  padding-top: 7px;
  border-top: 1px dashed #f0f0f2;
  font-size: 10.5px;
  color: #8e8e93;
}
.netov-drop__title { font-weight: 600; color: #6e6e73; }
.netov-drop__item { display: inline-flex; align-items: center; gap: 4px; }
.netov-drop__item b { font-weight: 600; font-variant-numeric: tabular-nums; }

.netov-config { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.netov-config__item {
  appearance: none;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  padding: 14px 16px;
  border: 1px solid #e8e8ed;
  border-radius: 14px;
  background: #fff;
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition: border-color .18s ease, box-shadow .18s ease;
}
.netov-config__item:hover { border-color: #0071e3; box-shadow: 0 2px 10px rgba(0,113,227,.1); }
.netov-config__label { font-size: 12px; color: #6e6e73; }
.netov-config__value {
  font-size: 24px;
  font-weight: 650;
  letter-spacing: -.02em;
  font-variant-numeric: tabular-nums;
  color: #1d1d1f;
}
.netov-config__go { font-size: 11px; color: #0071e3; font-weight: 600; }

@media (max-width: 1100px) {
  .netov-grid--4 { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 760px) {
  .netov { padding: 16px; border-radius: 20px; }
  .netov-grid--4,
  .netov-grid--2 { grid-template-columns: 1fr; }
  .netov-split { grid-template-columns: 1fr; }
  .netov-rate__value { font-size: 20px; }
  .netov-rate__sparks { width: 100%; }
  .netov-config { grid-template-columns: 1fr; }
}
</style>
