<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NDescriptions,
  NDescriptionsItem,
  NInput,
  NSelect,
  NSpace,
  NTag,
} from 'naive-ui'
import { useCoresStore } from '../stores/cores'
import { formatTime } from '../utils/format'
import type { VersionInfo } from '../api/ngg'

const store = useCoresStore()
const selected = ref('')
const targetVersion = ref('')

const repository = computed(() => {
  const descriptor = store.registry.find((r) => r.id === selected.value)
  return descriptor?.repository || '—'
})

const installedVersion = computed(() => {
  const core = store.cores.find((c) => c.id === selected.value)
  return core?.version || '—'
})

const upgradeBusy = computed(() => store.isBusy(`upgrade:${selected.value}`))
const rollbackBusy = computed(() =>
  store.isBusy(`rollback-version:${selected.value}`),
)
const versionsTitle = computed(() => `可用版本列表（${store.versions.length}）`)

function versionRowKey(row: any): string {
  return String(row.version || row.raw || Math.random())
}

function requireInput(): boolean {
  if (!selected.value || !targetVersion.value.trim()) {
    window.$message?.warning('请选择内核并填写目标版本号')
    return false
  }
  return true
}

function confirmUpgrade(): void {
  if (!requireInput()) return
  store.upgradeCoreAction(selected.value, targetVersion.value.trim())
}

function confirmRollbackVersion(): void {
  if (!requireInput()) return
  store.rollbackVersionAction(selected.value, targetVersion.value.trim())
}

const columns: any[] = [
  {
    title: '版本',
    key: 'version',
    minWidth: 140,
    render: (row: VersionInfo) =>
      h(NTag, { size: 'small', bordered: false, type: 'info' }, {
        default: () => row.version || row.raw || '—',
      }),
  },
  {
    title: '通道',
    key: 'channel',
    width: 110,
    render: (row: VersionInfo) => row.channel || '—',
  },
  {
    title: '原始标识',
    key: 'raw',
    minWidth: 140,
    render: (row: VersionInfo) => h('span', { class: 'mono' }, row.raw || '—'),
  },
  {
    title: '检测时间',
    key: 'detected_at',
    minWidth: 180,
    render: (row: VersionInfo) => formatTime(row.detected_at),
  },
  {
    title: '资源地址（asset_url）',
    key: 'asset_url',
    minWidth: 220,
    render: (row: VersionInfo) => h('span', { class: 'mono' }, row.asset_url || '—'),
  },
  {
    title: '摘要地址（digest_url）',
    key: 'digest_url',
    minWidth: 220,
    render: (row: VersionInfo) => h('span', { class: 'mono' }, row.digest_url || '—'),
  },
  {
    title: '操作',
    key: 'actions',
    width: 190,
    fixed: 'right',
    render: (row: VersionInfo) =>
      h(NSpace, { size: 4, wrap: false }, {
        default: () => [
          h(
            NButton,
            {
              size: 'tiny',
              type: 'primary',
              loading: store.isBusy(`upgrade:${selected.value}`),
              onClick: () => store.upgradeCoreAction(selected.value, row.version),
            },
            { default: () => '升级到此版本' },
          ),
          h(
            NButton,
            {
              size: 'tiny',
              loading: store.isBusy(`rollback-version:${selected.value}`),
              onClick: () => store.rollbackVersionAction(selected.value, row.version),
            },
            { default: () => '回滚到此版本' },
          ),
        ],
      }),
  },
]

watch(selected, (id) => {
  if (id) store.loadVersions(id)
})

onMounted(async () => {
  await store.ensureBase()
  const first = selected.value || store.coreOptions[0]?.value || ''
  if (first) selected.value = first
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">版本管理</h2>
        <p class="page-subtitle">已安装版本、可用版本与摘要校验</p>
      </div>
      <NSpace align="center" :size="10">
        <span class="hint">选择内核</span>
        <NSelect
          v-model:value="selected"
          :options="store.coreOptions"
          style="width: 240px"
          placeholder="请选择内核"
        />
        <NButton size="small" :disabled="!selected" @click="selected && store.loadVersions(selected)">
          刷新
        </NButton>
      </NSpace>
    </div>

    <NAlert type="info" :bordered="false" class="section-gap">
      可用版本列表来自 <span class="mono">GET /cores/{id}/versions</span>，由后端从上游仓库探测。
      安装 / 升级时会下载 <span class="mono">asset_url</span> 对应的产物，并用
      <span class="mono">digest_url</span> 提供的摘要做完整性校验，校验不通过则拒绝替换现有内核。
      仓库：<span class="mono">{{ repository }}</span>
    </NAlert>

    <NCard size="small" title="当前版本" class="section-gap">
      <NDescriptions bordered :column="3" size="small" label-placement="left">
        <NDescriptionsItem label="内核已安装版本">
          <span class="mono">{{ installedVersion }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="检测到的版本">
          <span class="mono">{{ store.currentVersion?.version || '—' }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="通道">{{ store.currentVersion?.channel || '—' }}</NDescriptionsItem>
        <NDescriptionsItem label="原始标识">
          <span class="mono">{{ store.currentVersion?.raw || '—' }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="检测时间">
          {{ formatTime(store.currentVersion?.detected_at) }}
        </NDescriptionsItem>
        <NDescriptionsItem label="仓库">
          <span class="mono">{{ repository }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="资源地址" :span="3">
          <span class="mono">{{ store.currentVersion?.asset_url || '—' }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="摘要地址" :span="3">
          <span class="mono">{{ store.currentVersion?.digest_url || '—' }}</span>
        </NDescriptionsItem>
      </NDescriptions>
    </NCard>

    <NCard size="small" title="升级 / 回滚到指定版本" class="section-gap">
      <NSpace align="center" :wrap="true" :size="10">
        <NInput
          v-model:value="targetVersion"
          style="max-width: 320px"
          placeholder="输入目标版本号，例如 v1.10.0"
        />
        <NButton
          size="small"
          type="primary"
          :loading="upgradeBusy"
          :disabled="!selected"
          @click="confirmUpgrade"
        >
          升级
        </NButton>
        <NButton
          size="small"
          :loading="rollbackBusy"
          :disabled="!selected"
          @click="confirmRollbackVersion"
        >
          回滚
        </NButton>
      </NSpace>
    </NCard>

    <NCard size="small" :title="versionsTitle">
      <NDataTable
        :columns="columns"
        :data="store.versions"
        :row-key="versionRowKey"
        :bordered="false"
        :scroll-x="1300"
        size="small"
        :pagination="false"
      />
    </NCard>
  </div>
</template>
