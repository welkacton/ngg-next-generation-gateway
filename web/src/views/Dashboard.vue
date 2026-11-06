<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDescriptions,
  NDescriptionsItem,
  NEmpty,
  NGi,
  NGrid,
  NProgress,
  NSpace,
  NTag,
  NTimeline,
  NTimelineItem,
} from 'naive-ui'
import { useCoresStore } from '../stores/cores'
import {
  formatBytes,
  formatTime,
  formatUptime,
  stateTagType,
  STATE_LABELS,
} from '../utils/format'
import type { CoreStatus } from '../api/ngg'

const store = useCoresStore()
let statsTimer: number | undefined

const recentAudit = computed(() => store.audit.slice(0, 8))
const healthStatus = computed(() => store.health?.status || '—')
const uptime = computed(() => formatUptime(store.health?.uptime_sec))

function cpuPercent(core: CoreStatus): number {
  return Math.min(100, Math.max(0, Math.round(store.stats[core.id]?.cpu_percent ?? 0)))
}

function memoryText(core: CoreStatus): string {
  return formatBytes(store.stats[core.id]?.memory_rss)
}

function auditType(action: string): 'success' | 'error' | 'warning' | 'info' {
  const text = action.toLowerCase()
  if (text.includes('fail') || text.includes('error')) return 'error'
  if (text.includes('stop') || text.includes('remove') || text.includes('uninstall')) {
    return 'warning'
  }
  if (text.includes('start') || text.includes('install') || text.includes('save')) {
    return 'success'
  }
  return 'info'
}

onMounted(async () => {
  await store.ensureBase()
  store.pollStats()
  // CPU / 内存小指标通过轮询 /stats 获取
  statsTimer = window.setInterval(() => store.pollStats(), 5000)
})

onUnmounted(() => {
  if (statsTimer !== undefined) window.clearInterval(statsTimer)
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">系统概览</h2>
        <p class="page-subtitle">NGG Phase 1 统一管理中心 · 实时状态</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="store.baseLoading" @click="store.forceRefresh()">
          刷新数据
        </NButton>
      </NSpace>
    </div>

    
    <NGrid cols="1 s:2 m:4" responsive="screen" :x-gap="14" :y-gap="14" class="section-gap">
      <NGi>
        <NCard size="small" :bordered="true">
          <div class="kv-label">NGG 版本</div>
          <div class="stat-value">{{ store.system?.ngg_version || '—' }}</div>
          <div class="hint">Go {{ store.system?.go_version || '—' }} · {{ store.system?.stage || '—' }}</div>
        </NCard>
      </NGi>
      <NGi>
        <NCard size="small" :bordered="true">
          <div class="kv-label">服务健康</div>
          <div class="stat-value">{{ healthStatus }}</div>
          <div class="hint">运行时长 {{ uptime }}</div>
        </NCard>
      </NGi>
      <NGi>
        <NCard size="small" :bordered="true">
          <div class="kv-label">运行中内核</div>
          <div class="stat-value" style="color: #3fb984">
            {{ store.runningCount }} <span class="hint">/ {{ store.cores.length }}</span>
          </div>
          <div class="hint">已安装 {{ store.installedCount }} 个 · 运行模式 {{ store.system?.mode || '—' }}</div>
        </NCard>
      </NGi>
      <NGi>
        <NCard size="small" :bordered="true">
          <div class="kv-label">监听地址</div>
          <div class="stat-value mono" style="font-size: 16px">
            {{ store.system?.listen_addr || '—' }}
          </div>
          <div class="hint">仅本机访问 · Bearer Token 认证</div>
        </NCard>
      </NGi>
    </NGrid>

    <NCard title="系统信息" size="small" class="section-gap">
      <NDescriptions bordered :column="3" size="small" label-placement="left">
        <NDescriptionsItem label="操作系统">{{ store.system?.os || '—' }}</NDescriptionsItem>
        <NDescriptionsItem label="架构">{{ store.system?.arch || '—' }}</NDescriptionsItem>
        <NDescriptionsItem label="平台">{{ store.system?.platform || '—' }}</NDescriptionsItem>
        <NDescriptionsItem label="NGG 版本">{{ store.system?.ngg_version || '—' }}</NDescriptionsItem>
        <NDescriptionsItem label="Go 版本">{{ store.system?.go_version || '—' }}</NDescriptionsItem>
        <NDescriptionsItem label="阶段 / 模式">
          {{ store.system?.stage || '—' }} / {{ store.system?.mode || '—' }}
        </NDescriptionsItem>
        <NDescriptionsItem label="监听地址">{{ store.system?.listen_addr || '—' }}</NDescriptionsItem>
        <NDescriptionsItem label="数据目录">{{ store.system?.data_dir || '—' }}</NDescriptionsItem>
        <NDescriptionsItem label="启动时间">{{ formatTime(store.system?.started_at) }}</NDescriptionsItem>
      </NDescriptions>
    </NCard>

    <NCard title="内核状态与资源" size="small" class="section-gap">
      <NEmpty v-if="!store.cores.length" description="暂无已注册内核" />
      <NGrid v-else cols="1 s:2 m:3" responsive="screen" :x-gap="12" :y-gap="12">
        <NGi v-for="core in store.cores" :key="core.id">
          <NCard size="small" :bordered="true" :title="core.display_name || core.id">
            <template #header-extra>
              <NTag size="small" :type="stateTagType(core.state)" :bordered="false">
                {{ STATE_LABELS[core.state] || core.state }}
              </NTag>
            </template>
            <NSpace vertical :size="8" style="width: 100%">
              <div class="hint mono">{{ core.id }} · 版本 {{ core.version || '—' }}</div>
              <div class="hint">PID {{ core.pid || '—' }} · 端口 {{ core.ports?.length ? core.ports.join(', ') : '未分配' }}</div>
              <div>
                <div class="kv-label">CPU {{ store.stats[core.id]?.cpu_percent?.toFixed(1) ?? '0.0' }}%</div>
                <NProgress
                  type="line"
                  :percentage="cpuPercent(core)"
                  :height="6"
                  :show-indicator="false"
                  :border-radius="3"
                  :fill-border-radius="3"
                />
              </div>
              <div class="hint">
                内存 RSS {{ memoryText(core) }} · 线程 {{ store.stats[core.id]?.threads ?? '—' }}
              </div>
            </NSpace>
          </NCard>
        </NGi>
      </NGrid>
    </NCard>

    <NCard title="最近审计事件" size="small">
      <NTimeline v-if="recentAudit.length" :horizontal="false">
        <NTimelineItem
          v-for="item in recentAudit"
          :key="item.id"
          :type="auditType(item.action)"
          :title="`${item.action} · ${item.core_id || 'system'}`"
          :time="formatTime(item.created_at)"
        >
          {{ item.detail || '（无详情）' }}
        </NTimelineItem>
      </NTimeline>
      <NEmpty v-else description="暂无审计事件" />
    </NCard>
  </div>
</template>
