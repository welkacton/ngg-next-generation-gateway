<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NInput,
  NModal,
  NSpace,
  NTag,
} from 'naive-ui'
import { useCoresStore } from '../stores/cores'
import { stateTagType, STATE_LABELS } from '../utils/format'
import type { CoreStatus } from '../api/ngg'

const store = useCoresStore()
const selectedKeys = ref<Array<string | number>>([])

const showInstall = ref(false)
const installTarget = ref('')
const installVersion = ref('')

const showVersionOp = ref(false)
const versionOpMode = ref<'upgrade' | 'rollback'>('upgrade')
const versionOpTarget = ref('')
const versionOpValue = ref('')

const installBusy = computed(() => store.isBusy(`install:${installTarget.value}`))
const versionOpBusy = computed(() => {
  const prefix = versionOpMode.value === 'upgrade' ? 'upgrade' : 'rollback-version'
  return store.isBusy(`${prefix}:${versionOpTarget.value}`)
})
const progressEntries = computed(() => Object.entries(store.installProgress))

function rowKey(row: any): string {
  return String(row.id)
}

function openInstall(id: string): void {
  installTarget.value = id
  installVersion.value = ''
  showInstall.value = true
}

function openVersionOp(mode: 'upgrade' | 'rollback', id: string, preset = ''): void {
  versionOpMode.value = mode
  versionOpTarget.value = id
  versionOpValue.value = preset
  showVersionOp.value = true
}

function confirmInstall(): void {
  store.installCoreAction(installTarget.value, installVersion.value.trim() || undefined)
  showInstall.value = false
}

function confirmVersionOp(): void {
  const version = versionOpValue.value.trim()
  if (!version) {
    window.$message?.warning('请填写目标版本号')
    return
  }
  if (versionOpMode.value === 'upgrade') {
    store.upgradeCoreAction(versionOpTarget.value, version)
  } else {
    store.rollbackVersionAction(versionOpTarget.value, version)
  }
  showVersionOp.value = false
}

function confirmUninstall(id: string): void {
  window.$dialog?.warning({
    title: '卸载内核',
    content: `确定要卸载内核 ${id} 吗？该操作会移除内核二进制及相关文件。`,
    positiveText: '确定卸载',
    negativeText: '取消',
    onPositiveClick: () => store.uninstallCoreAction(id),
  })
}

function sourceOf(id: string): string {
  const descriptor = store.registry.find((r) => r.id === id)
  if (!descriptor) return '未登记'
  return descriptor.builtin ? '内置（builtin）' : `描述文件 · ${descriptor.source || '—'}`
}

function selectedIds(fallback: string[]): string[] {
  return selectedKeys.value.length
    ? selectedKeys.value.map(String)
    : fallback
}

function batchStart(exclusive: boolean): void {
  const ids = selectedIds(store.cores.filter((c) => c.installed).map((c) => c.id))
  if (!ids.length) {
    window.$message?.warning('请先选择内核，或等待内核注册')
    return
  }
  store.batchStartAction(ids, exclusive)
}

function batchStop(): void {
  const ids = selectedIds(store.cores.map((c) => c.id))
  if (!ids.length) {
    window.$message?.warning('没有可停止的内核')
    return
  }
  store.batchStopAction(ids)
}

function opButton(
  text: string,
  busyKey: string,
  onClick: () => void,
  disabled = false,
  type: 'default' | 'primary' | 'info' | 'error' = 'default',
) {
  return h(
    NButton,
    {
      size: 'tiny',
      type,
      disabled,
      loading: store.isBusy(busyKey),
      onClick,
      style: 'margin: 2px 4px 2px 0',
    },
    { default: () => text },
  )
}

const columns = computed<any[]>(() => [
  { type: 'selection' },
  {
    title: '内核',
    key: 'id',
    minWidth: 160,
    render: (row: CoreStatus) =>
      h('div', null, [
        h('div', { style: 'font-weight: 600' }, row.display_name || row.id),
        h('div', { class: 'hint mono' }, row.id),
      ]),
  },
  {
    title: '状态',
    key: 'state',
    width: 100,
    render: (row: CoreStatus) =>
      h(
        NTag,
        { size: 'small', type: stateTagType(row.state), bordered: false },
        { default: () => STATE_LABELS[row.state] || row.state },
      ),
  },
  {
    title: '版本',
    key: 'version',
    width: 110,
    render: (row: CoreStatus) => row.version || '—',
  },
  {
    title: 'PID',
    key: 'pid',
    width: 80,
    render: (row: CoreStatus) => row.pid || '—',
  },
  {
    title: '端口',
    key: 'ports',
    minWidth: 120,
    render: (row: CoreStatus) =>
      row.ports && row.ports.length ? row.ports.join(', ') : '未分配',
  },
  {
    title: '能力',
    key: 'capabilities',
    minWidth: 220,
    render: (row: CoreStatus) => {
      const cap: any = row.capabilities || {}
      const tags: string[] = []
      if (Array.isArray(cap.protocols)) tags.push(...cap.protocols.slice(0, 3))
      if (Array.isArray(cap.features)) tags.push(...cap.features.slice(0, 2))
      if (cap.worker_mode) tags.push('Worker Mode')
      if (!tags.length) return h('span', { class: 'hint' }, '—')
      return h(
        'div',
        { class: 'cap-tags' },
        tags.map((t) =>
          h(NTag, { size: 'small', bordered: false }, { default: () => t }),
        ),
      )
    },
  },
  {
    title: '注册来源',
    key: 'source',
    minWidth: 150,
    render: (row: CoreStatus) => sourceOf(row.id),
  },
  {
    title: '操作',
    key: 'actions',
    width: 330,
    fixed: 'right',
    render: (row: CoreStatus) => {
      const notInstalled = !row.installed || row.state === 'not_installed'
      return h('div', null, [
        opButton('启动', `start:${row.id}`, () => store.startCoreAction(row.id), notInstalled, 'primary'),
        opButton('停止', `stop:${row.id}`, () => store.stopCoreAction(row.id), notInstalled),
        opButton('重启', `restart:${row.id}`, () => store.restartCoreAction(row.id), notInstalled),
        opButton('安装', `install:${row.id}`, () => openInstall(row.id), false, 'info'),
        opButton('卸载', `uninstall:${row.id}`, () => confirmUninstall(row.id), false, 'error'),
        opButton('升级', `upgrade:${row.id}`, () => openVersionOp('upgrade', row.id, row.version), notInstalled),
        opButton('回滚', `rollback-version:${row.id}`, () => openVersionOp('rollback', row.id), notInstalled),
      ])
    },
  },
])

onMounted(() => {
  store.ensureBase()
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">内核管理</h2>
        <p class="page-subtitle">内核生命周期、批量编排与注册来源</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="store.baseLoading" @click="store.refreshCores()">
          刷新列表
        </NButton>
      </NSpace>
    </div>

    <NAlert type="info" :bordered="false" class="section-gap">
      注册表已加载 <strong>{{ store.registry.length }}</strong> 个内核描述，来源分为
      <strong>builtin（内置）</strong> 与 <strong>descriptor（描述文件）</strong>。
      支持通过描述文件新增内核，新增后到「系统设置」执行注册表重载即可生效。
      「单核独占启动」会先停止其他内核再启动所选内核；「多核并行启动」允许多个内核同时运行。
    </NAlert>

    <NCard size="small" class="section-gap">
      <NSpace align="center" :wrap="true" :size="10">
        <NTag :bordered="false" size="small">已选 {{ selectedKeys.length }} 个</NTag>
        <NButton
          size="small"
          type="primary"
          :loading="store.isBusy('batch-start')"
          @click="batchStart(false)"
        >
          多核并行启动
        </NButton>
        <NButton
          size="small"
          type="info"
          :loading="store.isBusy('batch-start')"
          @click="batchStart(true)"
        >
          单核独占启动
        </NButton>
        <NButton size="small" :loading="store.isBusy('batch-stop')" @click="batchStop">
          全部停止
        </NButton>
        <NButton size="small" quaternary @click="selectedKeys = []">清空选择</NButton>
        <span class="hint">未选择时，批量操作作用于全部已安装内核 / 全部内核。</span>
      </NSpace>
    </NCard>

    <NCard
      v-if="progressEntries.length"
      size="small"
      title="安装进度（WebSocket 实时推送）"
      class="section-gap"
    >
      <NSpace vertical :size="6">
        <div v-for="[id, text] in progressEntries" :key="id" class="hint">
          <NTag size="small" :bordered="false" type="info">{{ id }}</NTag>
          <span style="margin-left: 8px">{{ text }}</span>
        </div>
      </NSpace>
    </NCard>

    <NDataTable
      v-model:checked-row-keys="selectedKeys"
      :columns="columns"
      :data="store.cores"
      :row-key="rowKey"
      :loading="store.baseLoading"
      :bordered="false"
      :scroll-x="1560"
      size="small"
      :pagination="false"
    />

    <NModal
      v-model:show="showInstall"
      preset="card"
      title="安装内核"
      style="width: 480px"
      :bordered="false"
    >
      <NSpace vertical :size="12">
        <div class="hint">
          目标内核：<strong class="mono">{{ installTarget }}</strong>
        </div>
        <div class="hint">版本号留空表示安装最新版本；安装进度通过 WebSocket 实时推送。</div>
        <NInput v-model:value="installVersion" placeholder="例如 v1.10.0（留空 = 最新）" />
      </NSpace>
      <template #footer>
        <NSpace justify="end">
          <NButton @click="showInstall = false">取消</NButton>
          <NButton type="primary" :loading="installBusy" @click="confirmInstall">
            开始安装
          </NButton>
        </NSpace>
      </template>
    </NModal>

    <NModal
      v-model:show="showVersionOp"
      preset="card"
      :title="versionOpMode === 'upgrade' ? '升级内核' : '回滚内核'"
      style="width: 480px"
      :bordered="false"
    >
      <NSpace vertical :size="12">
        <div class="hint">
          目标内核：<strong class="mono">{{ versionOpTarget }}</strong>
        </div>
        <NInput v-model:value="versionOpValue" placeholder="请输入目标版本号，例如 v1.10.0" />
      </NSpace>
      <template #footer>
        <NSpace justify="end">
          <NButton @click="showVersionOp = false">取消</NButton>
          <NButton type="primary" :loading="versionOpBusy" @click="confirmVersionOp">
            {{ versionOpMode === 'upgrade' ? '确认升级' : '确认回滚' }}
          </NButton>
        </NSpace>
      </template>
    </NModal>
  </div>
</template>
