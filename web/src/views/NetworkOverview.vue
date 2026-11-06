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
import { formatBytes, formatTime } from '../utils/format'

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

function rateText(v?: number): string {
  return v === undefined ? '—' : `${formatBytes(v)}/s`
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
const ingressText = computed(() => {
  const i = statusMap.value.ingress
  return i && typeof i === 'object' ? String(i.address ?? '—') : '—'
})
const tunText = computed(() => {
  const t = statusMap.value.tun
  if (!t || typeof t !== 'object') return '未配置'
  return t.active ? `已启用 · ${t.name ?? 'ngg0'}` : t.configured ? '已配置 · 未激活' : '未启用'
})
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

    <!-- Hero -->
    <section class="netov-hero">
      <div class="netov-hero__left">
        <div class="netov-hero__label">当前总速率</div>
        <div class="netov-hero__value">
          {{ rateText((latest?.rate_in ?? 0) + (latest?.rate_out ?? 0)) }}
        </div>
        <div class="netov-hero__meta">
          <span><i class="netov-dot netov-dot--in" />下载 {{ rateText(latest?.rate_in) }}</span>
          <span><i class="netov-dot netov-dot--out" />上传 {{ rateText(latest?.rate_out) }}</span>
        </div>
        <div class="netov-hero__hint">
          峰值 {{ rateText(totalPeak) }} · 采样 {{ samples.length }} 点 / {{ windowMin }} 分钟 ·
          后端每 {{ history?.interval_sec ?? 2 }} 秒真实采集一次
        </div>
      </div>
      <div class="netov-hero__right">
        <div class="netov-spark">
          <div class="netov-spark__head"><i class="netov-dot netov-dot--in" />下载速率</div>
          <svg viewBox="0 0 160 40" preserveAspectRatio="none" aria-label="下载速率迷你趋势图">
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
          <div class="netov-spark__head"><i class="netov-dot netov-dot--out" />上传速率</div>
          <svg viewBox="0 0 160 40" preserveAspectRatio="none" aria-label="上传速率迷你趋势图">
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
    </section>

    <!-- KPI row -->
    <section class="netov-grid netov-grid--4">
      <div class="netov-kpi">
        <div class="netov-kpi__label"><i class="netov-dot netov-dot--in" />累计下载</div>
        <div class="netov-kpi__value">{{ byteText(latest?.bytes_in) }}</div>
        <div class="netov-kpi__sub">自网络引擎启动以来的真实字节数</div>
      </div>
      <div class="netov-kpi">
        <div class="netov-kpi__label"><i class="netov-dot netov-dot--out" />累计上传</div>
        <div class="netov-kpi__value">{{ byteText(latest?.bytes_out) }}</div>
        <div class="netov-kpi__sub">自网络引擎启动以来的真实字节数</div>
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

    <!-- Main chart -->
    <section class="netov-card netov-chart-card">
      <div class="netov-card__head">
        <div>
          <h3 class="netov-card__title">流量趋势</h3>
          <p class="netov-card__sub">下载与上传共享纵轴，数据点来自真实计数器差分</p>
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
        <span>每 2 秒自动刷新 · 未运行时不绘制模拟曲线</span>
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
          <NTag size="small" :bordered="false">{{ running ? '实时' : '停止' }}</NTag>
        </div>
        <div class="netov-flow">
          <div v-for="row in flowRows" :key="row.label" class="netov-flow__row">
            <span class="netov-flow__label">{{ row.label }}</span>
            <span class="netov-flow__value" :class="`tone-${row.tone}`">{{ row.value }}</span>
          </div>
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
          <div class="netov-engine__row"><span>统一入口</span><b class="mono">{{ ingressText }}</b></div>
          <div class="netov-engine__row"><span>TUN</span><b>{{ tunText }}</b></div>
          <div class="netov-engine__row"><span>会话数</span><b>{{ intText(sessionCount) }}</b></div>
          <div class="netov-engine__row"><span>内核端点</span><b>{{ intText(endpointCount) }}</b></div>
          <div class="netov-engine__row">
            <span>安全策略</span>
            <b>{{ statusMap.fail_closed ? 'Fail-Closed' : 'Fail-Open' }} · {{ statusMap.failover ? 'Failover 开' : 'Failover 关' }}</b>
          </div>
        </div>
        <div class="netov-engine__note">
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
            <p class="netov-card__sub">GET /network/status · components</p>
          </div>
          <NSpace :size="6" align="center" :wrap="true">
            <NTag size="small" :bordered="false" :type="engineRunning === true ? 'success' : 'default'">
              {{ engineRunning === null ? '状态未知' : (engineRunning ? '运行中' : '未运行') }}
            </NTag>
            <NButton size="tiny" :loading="isBusy('engine-start')" @click="toggleEngine('start')">启动引擎</NButton>
            <NButton size="tiny" :loading="isBusy('engine-stop')" @click="toggleEngine('stop')">停止引擎</NButton>
            <NButton size="tiny" quaternary @click="go('network')">前往网络引擎 →</NButton>
          </NSpace>
        </div>
        <div class="netov-components">
          <div v-for="c in components" :key="c.key" class="netov-component">
            <span class="netov-component__label">{{ c.label }}</span>
            <NSpace :size="6" align="center">
              <NTag size="small" :bordered="false" :type="c.enabled ? 'info' : 'default'">
                {{ c.enabled ? '已启用' : '未启用' }}
              </NTag>
              <NTag size="small" :bordered="false" :type="c.running ? 'success' : 'default'">
                {{ c.running ? '运行中' : '未运行' }}
              </NTag>
            </NSpace>
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
  border-radius: 18px;
  padding: 16px;
  color: #1d1d1f;
  min-height: calc(100vh - 120px);
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
  margin-bottom: 12px;
}
.netov-eyebrow {
  font-size: 10.5px;
  letter-spacing: 0.14em;
  color: #8e8e93;
  font-weight: 600;
}
.netov-title {
  margin: 4px 0 2px;
  font-size: 22px;
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
.netov-alert { margin-bottom: 10px; border-radius: 14px !important; }

.netov-hero {
  display: flex;
  justify-content: space-between;
  gap: 28px;
  flex-wrap: wrap;
  background: linear-gradient(135deg, #ffffff 0%, #f2f7ff 100%);
  border: 1px solid #e8e8ed;
  border-radius: 16px;
  padding: 16px 18px;
  box-shadow: 0 1px 2px rgba(0,0,0,.03), 0 12px 32px rgba(0,0,0,.04);
  margin-bottom: 12px;
}
.netov-hero__label { font-size: 12.5px; color: #6e6e73; font-weight: 500; }
.netov-hero__value {
  font-size: 30px;
  font-weight: 650;
  letter-spacing: -0.035em;
  line-height: 1.15;
  margin: 6px 0 8px;
  font-variant-numeric: tabular-nums;
}
.netov-hero__meta { display: flex; gap: 22px; flex-wrap: wrap; font-size: 13px; color: #1d1d1f; }
.netov-hero__meta span { display: inline-flex; align-items: center; gap: 7px; }
.netov-hero__hint { margin-top: 14px; font-size: 11.5px; color: #8e8e93; }
.netov-hero__right {
  display: flex;
  gap: 14px;
  flex-wrap: wrap;
  align-items: center;
}
.netov-spark {
  width: 132px;
  background: rgba(255,255,255,.8);
  border: 1px solid #e8e8ed;
  border-radius: 12px;
  padding: 8px 10px 6px;
}
.netov-spark__head {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11.5px;
  color: #6e6e73;
  margin-bottom: 6px;
}
.netov-spark svg { width: 100%; height: 26px; display: block; }
.netov-spark__line { fill: none; stroke-width: 2; vector-effect: non-scaling-stroke; }
.netov-spark__line--in { stroke: #0071e3; }
.netov-spark__line--out { stroke: #ff9f0a; }

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
  border-radius: 14px;
  box-shadow: 0 1px 2px rgba(0,0,0,.03), 0 8px 24px rgba(0,0,0,.035);
}
.netov-kpi { padding: 12px 14px 14px; }
.netov-kpi__label {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 12.5px;
  color: #6e6e73;
  font-weight: 500;
}
.netov-kpi__value {
  font-size: 20px;
  font-weight: 650;
  letter-spacing: -0.025em;
  margin: 6px 0 4px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.netov-kpi__value--sm { font-size: 18px; }
.netov-kpi__sub { font-size: 11.5px; color: #8e8e93; line-height: 1.5; }

.netov-card { padding: 14px 16px; }
.netov-card__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}
.netov-card__title { margin: 0; font-size: 14.5px; font-weight: 600; letter-spacing: -.01em; }
.netov-card__sub { margin: 5px 0 0; font-size: 11.5px; color: #8e8e93; }
.netov-chart-card { margin-bottom: 12px; }
.netov-chart-foot {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid #f0f0f2;
  font-size: 11.5px;
  color: #8e8e93;
}

.netov-flow { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0 22px; }
.netov-flow__row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 7px 0;
  border-bottom: 1px solid #f2f2f4;
  font-size: 12.5px;
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
  padding: 7px 0;
  border-bottom: 1px solid #f2f2f4;
  font-size: 12.5px;
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
.netov-component__label { color: #1d1d1f; font-weight: 500; }
.netov-component__empty { padding: 9px 0; color: #8e8e93; font-size: 12.5px; }

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
  .netov-hero__value { font-size: 36px; }
  .netov-hero__right { width: 100%; }
  .netov-spark { flex: 1 1 140px; width: auto; }
  .netov-config { grid-template-columns: 1fr; }
}
</style>
