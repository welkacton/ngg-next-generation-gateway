<script setup lang="ts">
import { h, onMounted, ref, watch } from 'vue'
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
} from 'naive-ui'
import { useCoresStore } from '../stores/cores'
import { formatBytes, formatTime, shortSha } from '../utils/format'
import type { ConfigBackup } from '../api/ngg'

const store = useCoresStore()
const selected = ref('')
const saveReason = ref('')
const fileInput = ref<HTMLInputElement | null>(null)

function backupRowKey(row: any): string {
  return String(row.id)
}

function triggerImport(): void {
  fileInput.value?.click()
}

function onFileChange(event: Event): void {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (file && selected.value) {
    store.importConfigAction(selected.value, file)
  }
  input.value = ''
}

function confirmRollback(backupId: string): void {
  window.$dialog?.warning({
    title: '回滚配置',
    content: `确定要把 ${selected.value} 的配置回滚到备份 ${backupId} 吗？回滚前会先备份当前配置。`,
    positiveText: '确认回滚',
    negativeText: '取消',
    onPositiveClick: () => store.rollbackConfigAction(selected.value, backupId),
  })
}

const backupColumns: any[] = [
  {
    title: '备份 ID',
    key: 'id',
    minWidth: 170,
    render: (row: ConfigBackup) => h('span', { class: 'mono' }, row.id),
  },
  {
    title: 'SHA256',
    key: 'sha256',
    minWidth: 170,
    render: (row: ConfigBackup) => h('span', { class: 'mono' }, shortSha(row.sha256)),
  },
  {
    title: '大小',
    key: 'size',
    width: 100,
    render: (row: ConfigBackup) => formatBytes(row.size),
  },
  { title: '原因', key: 'reason', minWidth: 160 },
  {
    title: '创建时间',
    key: 'created_at',
    minWidth: 180,
    render: (row: ConfigBackup) => formatTime(row.created_at),
  },
  {
    title: '操作',
    key: 'actions',
    width: 90,
    fixed: 'right',
    render: (row: ConfigBackup) =>
      h(
        NButton,
        {
          size: 'tiny',
          loading: store.isBusy(`rollback-config:${row.id}`),
          onClick: () => confirmRollback(row.id),
        },
        { default: () => '回滚' },
      ),
  },
]

watch(selected, (id) => {
  if (id) store.loadConfig(id)
})

onMounted(async () => {
  await store.ensureBase()
  const first = store.configCoreId || store.coreOptions[0]?.value || ''
  if (first) selected.value = first
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">配置管理</h2>
        <p class="page-subtitle">编辑、原生校验、导入导出与备份回滚</p>
      </div>
      <NSpace align="center" :size="10">
        <span class="hint">选择内核</span>
        <NSelect
          v-model:value="selected"
          :options="store.coreOptions"
          style="width: 260px"
          placeholder="请选择内核"
        />
      </NSpace>
    </div>

    <NAlert type="warning" :bordered="false" class="section-gap">
      <template #header>配置修改的事务顺序</template>
      点击「保存」后，后端严格按
      <strong>① 备份当前配置 → ② 调用内核原生校验 → ③ 校验通过后原子替换</strong>
      的顺序执行；任一步失败都会保留原配置并生成可追溯的备份。建议先「校验」再「保存」。
    </NAlert>

    <NDescriptions
      v-if="store.config"
      bordered
      :column="4"
      size="small"
      label-placement="left"
      class="section-gap"
    >
      <NDescriptionsItem label="配置路径">
        <span class="mono">{{ store.config.path }}</span>
      </NDescriptionsItem>
      <NDescriptionsItem label="SHA256">
        <span class="mono">{{ shortSha(store.config.sha256) }}</span>
      </NDescriptionsItem>
      <NDescriptionsItem label="大小">{{ formatBytes(store.config.size) }}</NDescriptionsItem>
      <NDescriptionsItem label="更新时间">{{ formatTime(store.config.updated_at) }}</NDescriptionsItem>
    </NDescriptions>

    <NCard size="small" title="配置编辑（等宽字体）" class="section-gap">
      <template #header-extra>
        <NSpace :size="8" :wrap="true" justify="end">
          <NButton
            size="small"
            :loading="store.isBusy('validate')"
            :disabled="!selected"
            @click="store.validateConfigAction(selected, store.configDraft)"
          >
            校验
          </NButton>
          <NButton
            size="small"
            type="primary"
            :loading="store.isBusy('save-config')"
            :disabled="!selected"
            @click="store.saveConfigAction(selected, store.configDraft, saveReason.trim() || undefined)"
          >
            保存
          </NButton>
          <NButton
            size="small"
            :loading="store.isBusy('export-config')"
            :disabled="!selected"
            @click="store.exportConfigAction(selected)"
          >
            导出
          </NButton>
          <NButton
            size="small"
            :loading="store.isBusy('import-config')"
            :disabled="!selected"
            @click="triggerImport"
          >
            导入
          </NButton>
        </NSpace>
      </template>

      <NSpace vertical :size="10" style="width: 100%">
        <NInput
          v-model:value="store.configDraft"
          type="textarea"
          class="code-editor"
          :rows="20"
          :disabled="!selected"
          placeholder="请选择内核以加载配置文件内容"
        />
        <NSpace align="center" :size="8">
          <span class="hint">保存说明（可选）：</span>
          <NInput
            v-model:value="saveReason"
            size="small"
            style="max-width: 360px"
            placeholder="记录本次修改原因，便于备份追溯"
          />
        </NSpace>
      </NSpace>
    </NCard>

    <NCard v-if="store.validateOutput" size="small" title="内核原生校验输出" class="section-gap">
      <pre class="output-block">{{ store.validateOutput }}</pre>
    </NCard>

    <NCard size="small" title="配置备份与一键回滚" class="section-gap">
      <template #header-extra>
        <NButton
          size="small"
          :disabled="!selected"
          @click="selected && store.loadConfig(selected)"
        >
          刷新
        </NButton>
      </template>
      <NDataTable
        :columns="backupColumns"
        :data="store.backups"
        :row-key="backupRowKey"
        :bordered="false"
        :scroll-x="900"
        size="small"
        :pagination="false"
      />
    </NCard>

    <input
      ref="fileInput"
      type="file"
      accept=".json,.yaml,.yml,.toml,.conf,.ini,*"
      style="display: none"
      @change="onFileChange"
    />
  </div>
</template>
