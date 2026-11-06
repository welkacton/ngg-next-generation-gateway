<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDescriptions,
  NDescriptionsItem,
  NEmpty,
  NGi,
  NGrid,
  NSpace,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type { StageInfo } from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { prettyJson, toArray } from '../utils/format'

const { isBusy, run } = useApiAction()

const stages = ref<StageInfo[]>([])

async function refresh() {
  const d = await run('stages', () => api.getStages())
  if (d) stages.value = toArray<StageInfo>(d)
}

function statusTagType(status: string): 'success' | 'info' | 'warning' | 'default' {
  const s = (status || '').toLowerCase()
  if (s.includes('done') || s.includes('complete') || s.includes('已') || s.includes('ok')) {
    return 'success'
  }
  if (s.includes('running') || s.includes('active') || s.includes('进行')) return 'info'
  if (s.includes('plan') || s.includes('todo') || s.includes('预留')) return 'warning'
  return 'default'
}

/** 提取除展示字段以外的数字计数，供标签化展示。 */
function numericFields(stage: StageInfo): Array<[string, number]> {
  return Object.entries(stage).filter(
    (entry): entry is [string, number] =>
      typeof entry[1] === 'number' &&
      !['id', 'stage', 'priority'].includes(entry[0]),
  )
}

function stageLabel(stage: StageInfo, index: number): string {
  return String(stage.stage ?? stage.id ?? `stage${index + 1}`)
}

onMounted(refresh)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">阶段总览</h2>
        <p class="page-subtitle">Stage 1 ~ Stage 5 的交付状态与能力计数</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="isBusy('stages')" @click="refresh">刷新阶段</NButton>
      </NSpace>
    </div>

    
    <NEmpty v-if="!stages.length" description="暂无阶段数据" class="section-gap" />

    <NGrid v-else cols="1 m:2" responsive="screen" :x-gap="14" :y-gap="14">
      <NGi v-for="(s, i) in stages" :key="stageLabel(s, i)">
        <NCard size="small" :title="`Stage ${stageLabel(s, i)} · ${s.name ?? '—'}`" :bordered="true">
          <template #header-extra>
            <NTag size="small" :bordered="false" :type="statusTagType(String(s.status ?? ''))">
              {{ s.status ?? '—' }}
            </NTag>
          </template>
          <NSpace vertical :size="10" style="width: 100%">
            <NDescriptions bordered :column="1" size="small" label-placement="left">
              <NDescriptionsItem label="名称 name">{{ s.name ?? '—' }}</NDescriptionsItem>
              <NDescriptionsItem label="状态 status">{{ s.status ?? '—' }}</NDescriptionsItem>
              <NDescriptionsItem label="说明 note">{{ s.note ?? '—' }}</NDescriptionsItem>
            </NDescriptions>

            <div v-if="numericFields(s).length">
              <div class="kv-label">各计数：</div>
              <div class="cap-tags">
                <NTag
                  v-for="[k, v] in numericFields(s)"
                  :key="k"
                  size="small"
                  :bordered="false"
                  type="info"
                >
                  {{ k }}：{{ v }}
                </NTag>
              </div>
            </div>

            <div>
              <div class="hint">完整阶段数据（含全部计数与扩展字段）：</div>
              <pre class="mono output-block" style="max-height: 220px">{{ prettyJson(s) }}</pre>
            </div>
          </NSpace>
        </NCard>
      </NGi>
    </NGrid>
  </div>
</template>
