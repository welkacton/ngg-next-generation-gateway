<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NInputNumber,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
} from 'naive-ui'
import { useCoresStore } from '../stores/cores'
import { formatTime, logClass } from '../utils/format'
import type { LogEntry } from '../api/ngg'

const store = useCoresStore()
const selected = ref('')
const stream = ref('all')
const autoScroll = ref(true)
const after = ref<number | null>(0)
const limit = ref<number | null>(200)
const logView = ref<HTMLElement | null>(null)

const streamOptions = [
  { label: '全部', value: 'all' },
  { label: 'stdout', value: 'stdout' },
  { label: 'stderr', value: 'stderr' },
  { label: 'ngg', value: 'ngg' },
]

const filteredLogs = computed<LogEntry[]>(() =>
  stream.value === 'all'
    ? store.logs
    : store.logs.filter((entry) => entry.stream === stream.value),
)

const logTitle = computed(() => `日志输出 · 当前显示 ${filteredLogs.value.length} 行`)

function scrollToBottom(): void {
  const el = logView.value
  if (el) el.scrollTop = el.scrollHeight
}

watch(
  () => filteredLogs.value.length,
  async () => {
    if (!autoScroll.value) return
    await nextTick()
    scrollToBottom()
  },
)

watch(autoScroll, (enabled) => {
  if (enabled) scrollToBottom()
})

watch(selected, (id) => {
  if (id) store.setLogCore(id)
})

async function loadHistory(): Promise<void> {
  if (!selected.value) {
    window.$message?.warning('请先选择内核')
    return
  }
  await store.loadHistory(
    selected.value,
    Number(after.value ?? 0),
    Number(limit.value ?? 200),
  )
  const last = store.logs[store.logs.length - 1]
  if (last && last.seq >= Number(after.value ?? 0)) {
    after.value = last.seq + 1
  }
}

function resetAndLoad(): void {
  after.value = 0
  loadHistory()
}

function confirmClear(): void {
  if (!selected.value) return
  window.$dialog?.warning({
    title: '清空日志',
    content: `确定要清空内核 ${selected.value} 的全部日志吗？`,
    positiveText: '确定清空',
    negativeText: '取消',
    onPositiveClick: () => store.clearLogsAction(selected.value),
  })
}

onMounted(async () => {
  await store.ensureBase()
  const first = store.activeLogCore || store.coreOptions[0]?.value || ''
  if (first) selected.value = first
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">实时日志</h2>
        <p class="page-subtitle">WebSocket 实时推送 + 历史拉取</p>
      </div>
      <NTag :bordered="false" size="small" :type="store.wsConnected ? 'success' : 'error'">
        <span :class="['ws-dot', store.wsConnected ? 'ws-on' : 'ws-off']" />
        {{ store.wsConnected ? 'WS 已连接' : 'WS 未连接（自动重连中）' }}
      </NTag>
    </div>

    <NAlert type="info" :bordered="false" class="section-gap">
      日志通过 <span class="mono">/api/v1/ws</span> 的
      <span class="mono">core.log</span> 事件实时推送，断线按指数退避自动重连；
      也可按 <span class="mono">after / limit</span> 拉取历史。
      颜色区分：<span class="log-stdout">stdout</span> /
      <span class="log-stderr">stderr</span> / <span class="log-ngg">ngg</span>。
    </NAlert>

    <NCard size="small" class="section-gap">
      <NSpace align="center" :wrap="true" :size="10">
        <span class="hint">内核</span>
        <NSelect
          v-model:value="selected"
          :options="store.coreOptions"
          style="width: 220px"
          placeholder="选择内核"
        />
        <span class="hint">流</span>
        <NSelect v-model:value="stream" :options="streamOptions" style="width: 130px" />
        <span class="hint">after</span>
        <NInputNumber v-model:value="after" :min="0" size="small" style="width: 110px" />
        <span class="hint">limit</span>
        <NInputNumber v-model:value="limit" :min="1" :max="2000" size="small" style="width: 110px" />
        <NButton size="small" @click="loadHistory">拉取历史</NButton>
        <NButton size="small" quaternary @click="resetAndLoad">从头加载</NButton>
        <NSwitch v-model:value="autoScroll" size="small">
          <template #checked>自动滚动</template>
          <template #unchecked>自动滚动</template>
        </NSwitch>
        <NButton
          size="small"
          type="error"
          :loading="store.isBusy('clear-logs')"
          :disabled="!selected"
          @click="confirmClear"
        >
          清空日志
        </NButton>
      </NSpace>
    </NCard>

    <NCard size="small" :title="logTitle">
      <div ref="logView" class="log-view">
        <div
          v-if="!filteredLogs.length"
          class="hint"
          style="text-align: center; padding-top: 48px"
        >
          暂无日志，请选择内核并拉取历史，或等待 WebSocket 推送。
        </div>
        <div v-for="log in filteredLogs" :key="log.seq" class="log-line">
          <span class="log-meta">#{{ log.seq }} {{ formatTime(log.time) }}</span>
          <span :class="['log-stream', logClass(log.stream)]">[{{ log.stream }}]</span>
          <span :class="logClass(log.stream)">{{ log.line }}</span>
        </div>
      </div>
    </NCard>
  </div>
</template>
