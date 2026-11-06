<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDescriptions,
  NDescriptionsItem,
  NSpace,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type { PqStatus } from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { prettyJson } from '../utils/format'

const { isBusy, run } = useApiAction()

const status = ref<PqStatus | null>(null)
const selftestResult = ref<unknown>(null)

async function refresh() {
  const d = await run('status', () => api.getPqStatus())
  if (d) status.value = d
}

async function doSelftest() {
  const r = await run('selftest', () => api.pqSelftest(), '后量子握手自测已执行')
  if (r) selftestResult.value = r
}

onMounted(refresh)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">后量子密码</h2>
        <p class="page-subtitle">ML-KEM 混合密钥交换状态与真实握手自测</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="isBusy('status')" @click="refresh">刷新状态</NButton>
      </NSpace>
    </div>

    
    <NCard size="small" title="套件状态（/pq/status）" class="section-gap">
      <NDescriptions bordered :column="3" size="small" label-placement="left">
        <NDescriptionsItem label="套件 suite">
          <span class="mono">{{ status?.suite ?? '—' }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="ML-KEM">
          <span class="mono">{{ status?.mlkem ?? '—' }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="经典算法 classical">
          <span class="mono">{{ status?.classical ?? '—' }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="KDF">
          <span class="mono">{{ status?.kdf ?? '—' }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="AEAD">
          <span class="mono">{{ status?.aead ?? '—' }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="密钥长度 key_bytes">
          {{ status?.key_bytes ?? '—' }}
        </NDescriptionsItem>
        <NDescriptionsItem label="已实现 implemented">
          <NTag
            size="small"
            :bordered="false"
            :type="status?.implemented ? 'success' : 'warning'"
          >
            {{ status?.implemented ? '是' : '否' }}
          </NTag>
        </NDescriptionsItem>
        <NDescriptionsItem label="Go 版本">
          <span class="mono">{{ status?.go_version ?? '—' }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="备注 note" :span="3">
          {{ status?.note ?? '—' }}
        </NDescriptionsItem>
      </NDescriptions>
      <div class="hint" style="margin-top: 14px">完整响应：</div>
      <pre class="mono output-block" style="max-height: 260px">{{ prettyJson(status) }}</pre>
    </NCard>

    <NCard size="small" title="握手自测（POST /pq/selftest）" class="section-gap">
      <template #header-extra>
        <NButton size="small" type="primary" :loading="isBusy('selftest')" @click="doSelftest">
          执行自测
        </NButton>
      </template>
      <div class="hint">
        点击后由后端执行真实的密钥封装 / 解封装与握手流程，以下为接口原始返回：
      </div>
      <pre class="mono output-block" style="margin-top: 10px; max-height: 360px">{{ prettyJson(selftestResult) }}</pre>
    </NCard>
  </div>
</template>
