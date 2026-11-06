<script setup lang="ts">
/**
 * Apple-style dual-series SVG area chart.
 *
 * Pure SVG + math, no chart dependency. Every point comes from a REAL backend
 * sample (see /api/v1/network/metrics/history); there is no interpolation,
 * padding with zeroes, or synthetic baseline. Both series share one Y axis so
 * download and upload are visually comparable.
 */
import { computed, onUnmounted, ref, watch } from 'vue'
import type { MetricsSample } from '../api/ngg'
import { formatBytes, formatRate, rateParts } from '../utils/format'

const props = withDefaults(
  defineProps<{
    samples: MetricsSample[]
    mode?: 'rate' | 'total' | 'connections'
  }>(),
  { mode: 'rate' },
)

/**
 * The viewBox width follows the rendered aspect ratio (see ResizeObserver
 * below) so the chart fills its box exactly at a fixed, compact height — no
 * stretching, no letterboxing, and the axis text keeps its real size.
 */
const H = 170
const PAD_L = 54
const PAD_R = 14
const PAD_T = 12
const PAD_B = 24
const PLOT_H = H - PAD_T - PAD_B
const BOTTOM = PAD_T + PLOT_H

const canvasEl = ref<HTMLElement | null>(null)
const canvasW = ref(1000)
const plotW = computed(() => Math.max(200, canvasW.value - PAD_L - PAD_R))

let observer: ResizeObserver | null = null

function syncWidth(el: HTMLElement): void {
  const rect = el.getBoundingClientRect()
  if (rect.width < 1 || rect.height < 1) return
  canvasW.value = Math.max(360, Math.round((rect.width * H) / rect.height))
}

// The canvas only exists once there is at least one sample, so the observer has
// to follow the element instead of being attached once in onMounted. Without
// this the viewBox kept its 1000-unit default and the drawing was letterboxed
// inside the card (a wide empty gutter on both sides).
watch(canvasEl, (el) => {
  observer?.disconnect()
  observer = null
  if (!el || typeof ResizeObserver === 'undefined') return
  observer = new ResizeObserver(() => syncWidth(el))
  observer.observe(el)
  syncWidth(el)
})

onUnmounted(() => {
  observer?.disconnect()
  observer = null
})

const hoverIndex = ref<number | null>(null)

function valueOf(s: MetricsSample, which: 'in' | 'out'): number {
  if (props.mode === 'total') return which === 'in' ? s.bytes_in : s.bytes_out
  if (props.mode === 'connections') {
    return which === 'in' ? s.connections : s.active
  }
  return which === 'in' ? s.rate_in : s.rate_out
}

const downloadName = computed(() =>
  props.mode === 'total' ? '累计下载' : props.mode === 'connections' ? '累计连接' : '下载速率',
)
const uploadName = computed(() =>
  props.mode === 'total' ? '累计上传' : props.mode === 'connections' ? '活跃连接' : '上传速率',
)

const series = computed(() => {
  const list = props.samples ?? []
  return {
    in: list.map((s) => valueOf(s, 'in')),
    out: list.map((s) => valueOf(s, 'out')),
    list,
  }
})

/** Round the axis maximum up to a readable 1/2/5 x 10^n step. */
function niceMax(raw: number): number {
  if (!(raw > 0)) return 1
  const exp = Math.floor(Math.log10(raw))
  const base = Math.pow(10, exp)
  const n = raw / base
  const step = n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10
  return step * base
}

const yMax = computed(() => {
  const all = [...series.value.in, ...series.value.out]
  return niceMax(Math.max(0, ...all))
})

function xAt(i: number, n: number): number {
  if (n <= 1) return PAD_L + plotW.value / 2
  return PAD_L + (i / (n - 1)) * plotW.value
}
function yAt(v: number): number {
  return PAD_T + PLOT_H - (Math.max(0, v) / yMax.value) * PLOT_H
}

function linePath(values: number[]): string {
  if (!values.length) return ''
  const n = values.length
  return values.map((v, i) => `${i === 0 ? 'M' : 'L'} ${xAt(i, n).toFixed(2)} ${yAt(v).toFixed(2)}`).join(' ')
}
function areaPath(values: number[]): string {
  if (!values.length) return ''
  const n = values.length
  return `${linePath(values)} L ${xAt(n - 1, n).toFixed(2)} ${BOTTOM} L ${xAt(0, n).toFixed(2)} ${BOTTOM} Z`
}

const inPath = computed(() => linePath(series.value.in))
const outPath = computed(() => linePath(series.value.out))
const inArea = computed(() => areaPath(series.value.in))
const outArea = computed(() => areaPath(series.value.out))

const yTicks = computed(() => [0, 0.25, 0.5, 0.75, 1].map((f) => ({
  y: PAD_T + PLOT_H - f * PLOT_H,
  label: formatAxis(yMax.value * f),
})))

// The whole axis shares ONE unit. Mixing units per tick used to print
// "1 B/s / 1 B/s / 1 B/s" for a tiny range, because every value was rounded to
// whole bytes.
const axisUnit = computed(() => (props.mode === 'connections' ? null : rateParts(yMax.value)))

function axisDivisor(unit: string): number {
  if (unit === 'B') return 1
  const idx = ['KB', 'MB', 'GB', 'TB'].indexOf(unit)
  return idx < 0 ? 1 : Math.pow(1024, idx + 1)
}

function formatAxis(v: number): string {
  if (props.mode === 'connections') return Math.round(v).toLocaleString('zh-CN')
  const unit = axisUnit.value?.unit ?? 'B'
  const n = v / axisDivisor(unit)
  const digits = n === 0 ? 0 : n >= 100 ? 0 : n >= 10 ? 1 : 2
  const text = String(Number(n.toFixed(digits)))
  return props.mode === 'rate' ? `${text} ${unit}/s` : `${text} ${unit}`
}

function formatValue(v: number): string {
  if (props.mode === 'connections') return Math.round(v).toLocaleString('zh-CN')
  return props.mode === 'rate' ? formatRate(v) : formatBytes(v)
}
function timeLabel(ts: number): string {
  const d = new Date(ts)
  return Number.isNaN(d.getTime())
    ? '—'
    : d.toLocaleTimeString('zh-CN', { hour12: false })
}

const xLabels = computed(() => {
  const list = series.value.list
  if (list.length < 2) return []
  return [0, Math.floor((list.length - 1) / 2), list.length - 1].map((i) => ({
    x: xAt(i, list.length),
    label: timeLabel(list[i].ts),
    anchor: i === 0 ? 'start' : i === list.length - 1 ? 'end' : 'middle',
  }))
})

const hovered = computed(() => {
  const i = hoverIndex.value
  const list = series.value.list
  if (i === null || i < 0 || i >= list.length) return null
  return {
    x: xAt(i, list.length),
    time: timeLabel(list[i].ts),
    inVal: series.value.in[i],
    outVal: series.value.out[i],
    leftPct: (xAt(i, list.length) / canvasW.value) * 100,
  }
})

function onMove(e: MouseEvent) {
  const n = series.value.list.length
  if (n < 2) return
  const svg = e.currentTarget as SVGSVGElement
  const rect = svg.getBoundingClientRect()
  const vx = ((e.clientX - rect.left) / rect.width) * canvasW.value
  const ratio = (vx - PAD_L) / plotW.value
  hoverIndex.value = Math.max(0, Math.min(n - 1, Math.round(ratio * (n - 1))))
}
function onLeave() {
  hoverIndex.value = null
}

function peak(which: 'in' | 'out') {
  const values = which === 'in' ? series.value.in : series.value.out
  if (!values.length) return null
  let idx = 0
  for (let i = 1; i < values.length; i += 1) {
    if (values[i] > values[idx]) idx = i
  }
  return { x: xAt(idx, values.length), y: yAt(values[idx]), value: values[idx] }
}
const inPeak = computed(() => peak('in'))
const outPeak = computed(() => peak('out'))
</script>

<template>
  <div class="tac-wrap">
    <div class="tac-legend">
      <span class="tac-legend__item"><i class="tac-dot tac-dot--in" />{{ downloadName }}</span>
      <span class="tac-legend__item"><i class="tac-dot tac-dot--out" />{{ uploadName }}</span>
      <span class="tac-legend__hint">真实采样 · {{ samples.length }} 点 · 共享纵轴</span>
    </div>

    <div v-if="!samples.length" class="tac-empty">
      暂无真实采样数据。启动统一网络引擎后，后端会每 2 秒采集一次计数器。
    </div>

    <div v-else ref="canvasEl" class="tac-canvas">
      <svg
        :viewBox="`0 0 ${canvasW} ${H}`"
        preserveAspectRatio="xMidYMid meet"
        role="img"
        aria-label="下载与上传流量趋势图"
        @mousemove="onMove"
        @mouseleave="onLeave"
      >
        <defs>
          <linearGradient id="tac-grad-in" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#0071E3" stop-opacity="0.28" />
            <stop offset="100%" stop-color="#0071E3" stop-opacity="0.015" />
          </linearGradient>
          <linearGradient id="tac-grad-out" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#FF9F0A" stop-opacity="0.22" />
            <stop offset="100%" stop-color="#FF9F0A" stop-opacity="0.012" />
          </linearGradient>
        </defs>

        <g class="tac-grid">
          <line
            v-for="t in yTicks"
            :key="t.y"
            :x1="PAD_L"
            :x2="canvasW - PAD_R"
            :y1="t.y"
            :y2="t.y"
          />
        </g>
        <g class="tac-axis">
          <text
            v-for="t in yTicks"
            :key="'l' + t.y"
            :x="PAD_L - 10"
            :y="t.y + 4"
            text-anchor="end"
          >{{ t.label }}</text>
          <text
            v-for="t in xLabels"
            :key="'t' + t.x"
            :x="t.x"
            :y="H - 8"
            :text-anchor="t.anchor"
          >{{ t.label }}</text>
        </g>

        <path :d="inArea" fill="url(#tac-grad-in)" />
        <path :d="outArea" fill="url(#tac-grad-out)" />
        <path :d="inPath" class="tac-line tac-line--in" />
        <path :d="outPath" class="tac-line tac-line--out" />

        <g v-if="inPeak && inPeak.value > 0" class="tac-peak">
          <circle :cx="inPeak.x" :cy="inPeak.y" r="3.5" fill="#0071E3" />
        </g>
        <g v-if="outPeak && outPeak.value > 0" class="tac-peak">
          <circle :cx="outPeak.x" :cy="outPeak.y" r="3.5" fill="#FF9F0A" />
        </g>

        <g v-if="hovered">
          <line
            :x1="hovered.x"
            :x2="hovered.x"
            :y1="PAD_T"
            :y2="BOTTOM"
            class="tac-cursor"
          />
          <circle :cx="hovered.x" :cy="yAt(hovered.inVal)" r="4" fill="#0071E3" />
          <circle :cx="hovered.x" :cy="yAt(hovered.outVal)" r="4" fill="#FF9F0A" />
        </g>
      </svg>

      <div
        v-if="hovered"
        class="tac-tooltip"
        :style="{ left: Math.min(82, Math.max(8, hovered.leftPct)) + '%' }"
      >
        <div class="tac-tooltip__time">{{ hovered.time }}</div>
        <div><i class="tac-dot tac-dot--in" />{{ downloadName }} <b>{{ formatValue(hovered.inVal) }}</b></div>
        <div><i class="tac-dot tac-dot--out" />{{ uploadName }} <b>{{ formatValue(hovered.outVal) }}</b></div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.tac-wrap {
  width: 100%;
}
.tac-legend {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 6px;
  font-size: 11px;
  color: #6e6e73;
}
.tac-legend__item {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-weight: 500;
  color: #1d1d1f;
}
.tac-legend__hint {
  margin-left: auto;
  font-size: 10.5px;
  color: #8e8e93;
}
.tac-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex: 0 0 auto;
}
.tac-dot--in { background: #0071e3; }
.tac-dot--out { background: #ff9f0a; }
.tac-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 110px;
  color: #8e8e93;
  font-size: 11.5px;
  background: #fafafa;
  border-radius: 14px;
  text-align: center;
  padding: 24px;
}
.tac-canvas {
  position: relative;
  width: 100%;
  /* Compact fixed height: the viewBox width is derived from the measured
     aspect ratio, so the drawing fills the box without distortion. */
  height: 148px;
}
@media (max-width: 900px) {
  .tac-canvas {
    height: 126px;
  }
}
.tac-canvas svg {
  width: 100%;
  height: 100%;
  display: block;
  overflow: visible;
  cursor: crosshair;
}
.tac-grid line {
  stroke: #ececf1;
  stroke-width: 1;
}
.tac-axis text {
  fill: #8e8e93;
  font-size: 9.5px;
  font-variant-numeric: tabular-nums;
}
.tac-line {
  fill: none;
  stroke-width: 2.2;
  stroke-linecap: round;
  stroke-linejoin: round;
  vector-effect: non-scaling-stroke;
}
.tac-line--in { stroke: #0071e3; }
.tac-line--out { stroke: #ff9f0a; }
.tac-cursor {
  stroke: #c7c7cc;
  stroke-width: 1;
  stroke-dasharray: 4 4;
}
.tac-tooltip {
  position: absolute;
  top: 8px;
  transform: translateX(-50%);
  background: rgba(255, 255, 255, 0.94);
  backdrop-filter: saturate(180%) blur(14px);
  border: 1px solid #e8e8ed;
  box-shadow: 0 8px 28px rgba(0, 0, 0, 0.1);
  border-radius: 12px;
  padding: 9px 12px;
  font-size: 12px;
  color: #1d1d1f;
  white-space: nowrap;
  pointer-events: none;
  z-index: 3;
}
.tac-tooltip__time {
  color: #8e8e93;
  margin-bottom: 5px;
  font-variant-numeric: tabular-nums;
}
.tac-tooltip div {
  display: flex;
  align-items: center;
  gap: 6px;
  line-height: 1.7;
}
.tac-tooltip b {
  margin-left: auto;
  padding-left: 12px;
  font-variant-numeric: tabular-nums;
}
</style>
