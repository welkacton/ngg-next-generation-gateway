<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NDescriptions,
  NDescriptionsItem,
  NInput,
  NSpace,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type { PlatformBuildResult, PlatformInfo } from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { prettyJson, toArray } from '../utils/format'

const { isBusy, run } = useApiAction()

const info = ref<PlatformInfo | null>(null)
const buildTargets = ref('')
const results = ref<PlatformBuildResult[]>([])
const buildRaw = ref<unknown>(null)

async function refreshPlatforms() {
  const d = await run('platforms', () => api.getPlatforms())
  if (d) {
    info.value = d
    if (!buildTargets.value.trim() && Array.isArray(d.targets)) {
      buildTargets.value = d.targets.join('\n')
    }
  }
}

function parseTargets(): string[] {
  return buildTargets.value
    .split(/[\n,;]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

async function doBuild() {
  const targets = parseTargets()
  if (!targets.length) {
    window.$message?.warning('请至少填写一个构建目标，例如 linux/amd64')
    return
  }
  const r = await run('build', () => api.buildPlatforms(targets), '跨平台构建任务已下发')
  if (r) {
    buildRaw.value = r
    results.value = toArray<PlatformBuildResult>(r)
  }
}

const resultColumns: any[] = [
  {
    title: 'target',
    key: 'target',
    minWidth: 180,
    render: (r: PlatformBuildResult) =>
      h('span', { class: 'mono' }, r.target ?? '—'),
  },
  {
    title: 'ok',
    key: 'ok',
    width: 90,
    render: (r: PlatformBuildResult) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: r.ok ? 'success' : 'error' },
        { default: () => (r.ok ? '成功' : '失败') },
      ),
  },
  {
    title: 'output',
    key: 'output',
    minWidth: 280,
    render: (r: PlatformBuildResult) =>
      h('span', { class: 'mono' }, r.output ?? '—'),
  },
  {
    title: 'path',
    key: 'path',
    minWidth: 240,
    render: (r: PlatformBuildResult) =>
      h('span', { class: 'mono' }, r.path ?? '—'),
  },
]

onMounted(refreshPlatforms)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">跨平台构建</h2>
        <p class="page-subtitle">主机 / Go 工具链、构建目标与客户端产物</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="isBusy('platforms')" @click="refreshPlatforms">
          刷新信息
        </NButton>
      </NSpace>
    </div>

    <NAlert type="info" :bordered="false" class="section-gap">
      <template #header>说明</template>
      <span class="mono">POST /platforms/build</span> 的 body 为
      <span class="mono">{"targets": ["linux/amd64", ...]}</span>，返回每个 target 的
      ok / output / path。所有结果均来自后端真实构建，不做模拟。
    </NAlert>

    <NCard size="small" title="平台信息（/platforms）" class="section-gap">
      <NDescriptions bordered :column="2" size="small" label-placement="left">
        <NDescriptionsItem label="主机 host">
          <span class="mono">{{ prettyJson(info?.host) }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="Go 工具链">
          <span class="mono">{{ info?.go ?? '—' }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="构建目标 targets">
          <span class="mono">{{ prettyJson(info?.targets) }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="客户端 clients">
          <span class="mono">{{ prettyJson(info?.clients) }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="备注 note" :span="2">
          {{ info?.note ?? '—' }}
        </NDescriptionsItem>
      </NDescriptions>
      <div class="hint" style="margin-top: 14px">完整响应：</div>
      <pre class="mono output-block" style="max-height: 220px">{{ prettyJson(info) }}</pre>
    </NCard>

    <NCard size="small" title="构建（POST /platforms/build）" class="section-gap">
      <NSpace vertical :size="10" style="width: 100%">
        <div class="hint">targets（每行一个，或用逗号分隔）：</div>
        <NInput
          v-model:value="buildTargets"
          type="textarea"
          class="code-editor"
          :rows="5"
          placeholder="linux/amd64"
        />
        <NButton size="small" type="primary" :loading="isBusy('build')" @click="doBuild">
          开始构建
        </NButton>
        <NDataTable
          v-if="results.length"
          :columns="resultColumns"
          :data="results"
          :bordered="false"
          size="small"
          :pagination="false"
          :scroll-x="900"
        />
        <pre v-if="buildRaw" class="mono output-block" style="max-height: 260px">{{ prettyJson(buildRaw) }}</pre>
      </NSpace>
    </NCard>
  </div>
</template>
