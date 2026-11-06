<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
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
import type { CoreDiagnostics, CoreStatus } from '../api/ngg'
import { getCoreDiagnostics } from '../api/ngg'

const store = useCoresStore()
const selectedKeys = ref<Array<string | number>>([])

const showDiag = ref(false)
const diagLoading = ref(false)
const diagTarget = ref('')
const diag = ref<CoreDiagnostics | null>(null)

async function openDiagnostics(id: string): Promise<void> {
  diagTarget.value = id
  diag.value = null
  showDiag.value = true
  await loadDiagnostics(id)
}

async function loadDiagnostics(id = diagTarget.value): Promise<void> {
  if (!id) return
  diagLoading.value = true
  try {
    diag.value = await getCoreDiagnostics(id)
  } catch {
    // axios 拦截器已统一提示错误
  } finally {
    diagLoading.value = false
  }
}

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

/* ------------------------------------------------------------------ *
 * 内核状态：所有按钮可用性 / 配色唯一来源，禁止再写死颜色
 * ------------------------------------------------------------------ */

type CorePhase =
  | 'missing'
  | 'stopped'
  | 'running'
  | 'starting'
  | 'stopping'
  | 'failed'
  | 'unknown'

/** 以真实后端 CoreStatus.state 为准，不以按钮点击记录推断。 */
function phaseOf(row: CoreStatus): CorePhase {
  if (!row.installed || row.state === 'not_installed') return 'missing'
  switch (row.state) {
    case 'running':
      return 'running'
    case 'starting':
      return 'starting'
    case 'stopping':
      return 'stopping'
    case 'failed':
      return 'failed'
    case 'installed':
    case 'stopped':
      return 'stopped'
    default:
      return 'unknown'
  }
}

const runningCores = computed(() => store.cores.filter((c) => phaseOf(c) === 'running'))
const startableCores = computed(() =>
  store.cores.filter((c) => ['stopped', 'failed'].includes(phaseOf(c))),
)
const stoppedCores = computed(() => store.cores.filter((c) => phaseOf(c) === 'stopped'))
const missingCores = computed(() => store.cores.filter((c) => phaseOf(c) === 'missing'))
const runningCount = computed(() => runningCores.value.length)
const startableCount = computed(() => startableCores.value.length)

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
  return selectedKeys.value.length ? selectedKeys.value.map(String) : fallback
}

/** 批量启动模式：两个按钮只高亮当前选中的模式，避免同时发蓝无法分辨。 */
const batchMode = ref<'parallel' | 'exclusive'>('parallel')

function batchStart(exclusive: boolean): void {
  batchMode.value = exclusive ? 'exclusive' : 'parallel'
  const ids = selectedIds(startableCores.value.map((c) => c.id))
  if (!ids.length) {
    window.$message?.warning('当前没有可启动的内核（需已安装且未运行）')
    return
  }
  store.batchStartAction(ids, exclusive)
}

function batchStop(): void {
  const ids = selectedIds(runningCores.value.map((c) => c.id))
  if (!ids.length) {
    window.$message?.warning('当前没有运行中的内核')
    return
  }
  store.batchStopAction(ids)
}

/* ------------------------------------------------------------------ *
 * 操作按钮：彩色 = 当前唯一可执行的生命周期动作
 *  未安装 → 只有「安装」蓝
 *  已停止 → 只有「启动」蓝
 *  运行中 → 只有「停止」红，启动变灰
 * ------------------------------------------------------------------ */

interface OpButtonOptions {
  busyKey?: string
  onClick: () => void
  disabled?: boolean
  loading?: boolean
  type?: 'default' | 'primary' | 'info' | 'success' | 'warning' | 'error'
  ghost?: boolean
  title?: string
}

function opButton(text: string, o: OpButtonOptions) {
  return h(
    NButton,
    {
      size: 'tiny',
      type: o.type ?? 'default',
      ghost: o.ghost ?? false,
      disabled: o.disabled ?? false,
      loading: o.loading ?? (o.busyKey ? store.isBusy(o.busyKey) : false),
      title: o.title,
      onClick: o.onClick,
      class: 'core-op-btn',
    },
    { default: () => text },
  )
}

function stateHint(phase: CorePhase): string {
  switch (phase) {
    case 'running':
      return '运行中 · 下一步：停止'
    case 'starting':
      return '启动中 · 请稍候'
    case 'stopping':
      return '停止中 · 请稍候'
    case 'stopped':
      return '未运行 · 下一步：启动'
    case 'failed':
      return '上次失败 · 可重新启动'
    case 'missing':
      return '未安装 · 下一步：安装'
    default:
      return '状态未知 · 请刷新'
  }
}

/** 能力标签必须裁切在自己的列内，否则会画到右侧固定操作列上。 */
function renderCapabilities(row: CoreStatus) {
  const cap: any = row.capabilities || {}
  const protocols: string[] = Array.isArray(cap.protocols) ? cap.protocols : []
  const features: string[] = Array.isArray(cap.features) ? cap.features : []
  const tags = [
    ...protocols.slice(0, 3),
    ...features.slice(0, 2),
    ...(cap.worker_mode ? ['Worker Mode'] : []),
  ]
  const hidden = Math.max(0, protocols.length - 3) + Math.max(0, features.length - 2)
  const shown = tags.slice(0, 5)
  if (hidden > 0) shown.push(`+${hidden}`)
  if (!shown.length) return h('span', { class: 'hint' }, '—')
  const full = [...protocols, ...features, ...(cap.worker_mode ? ['Worker Mode'] : [])].join('、')
  return h(
    'div',
    { class: 'cap-tags cap-tags--clip', title: full },
    shown.map((t) => h(NTag, { size: 'small', bordered: false }, { default: () => t })),
  )
}

const columns = computed<any[]>(() => [
  { type: 'selection', width: 46 },
  {
    title: '内核',
    key: 'id',
    width: 190,
    render: (row: CoreStatus) => {
      const from = sourceOf(row.id)
      return h('div', { class: 'core-name' }, [
        h('div', { class: 'core-name__title' }, row.display_name || row.id),
        h('div', { class: 'hint mono core-name__id' }, row.id),
        h('div', { class: 'hint core-name__id', title: from }, from),
      ])
    },
  },
  {
    title: '状态',
    key: 'state',
    width: 150,
    render: (row: CoreStatus) => {
      const phase = phaseOf(row)
      const message = (row.message || '').trim()
      const short = message.length > 60 ? `${message.slice(0, 60)}…` : message
      const body: any[] = [
        h('div', { style: 'display: flex; align-items: center; gap: 7px; min-width: 0' }, [
          h('span', { class: `state-dot state-dot--${phase}` }),
          h(
            NTag,
            { size: 'small', type: stateTagType(row.state), bordered: false },
            { default: () => STATE_LABELS[row.state] || row.state },
          ),
        ]),
      ]
      // 失败原因不点开诊断也要看得见：单行小字 + 完整 tooltip。
      if (row.state === 'failed' && short) {
        body.push(
          h(
            'div',
            {
              title: message,
              style:
                'max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;' +
                'font-size: 11px; line-height: 1.5; color: #d03050; opacity: .9',
            },
            short,
          ),
        )
      }
      return h(
        'div',
        { class: 'state-cell', style: 'flex-direction: column; align-items: flex-start; gap: 2px' },
        body,
      )
    },
  },
  {
    title: '版本',
    key: 'version',
    width: 100,
    render: (row: CoreStatus) =>
      h('div', { class: 'cell-clip', title: row.version || '' }, row.version || '—'),
  },
  {
    title: 'PID',
    key: 'pid',
    width: 68,
    render: (row: CoreStatus) => row.pid || '—',
  },
  {
    title: '端口',
    key: 'ports',
    width: 145,
    render: (row: CoreStatus) => {
      const text = row.ports && row.ports.length ? row.ports.join(', ') : '未分配'
      return h('div', { class: 'cell-clip', title: text }, text)
    },
  },
  {
    title: '能力',
    key: 'capabilities',
    width: 200,
    render: renderCapabilities,
  },
  {
    title: '操作',
    key: 'actions',
    width: 250,
    fixed: 'right',
    render: (row: CoreStatus) => {
      const phase = phaseOf(row)
      const running = phase === 'running'
      const stopped = phase === 'stopped' || phase === 'failed'
      const missing = phase === 'missing'
      const installed = !missing
      const starting = phase === 'starting'
      const stopping = phase === 'stopping'

      const bStart = store.isBusy(`start:${row.id}`)
      const bStop = store.isBusy(`stop:${row.id}`)
      const bRestart = store.isBusy(`restart:${row.id}`)
      const bInstall = store.isBusy(`install:${row.id}`)
      const bUninstall = store.isBusy(`uninstall:${row.id}`)
      const anyTransition = starting || stopping || bStart || bStop || bRestart || bInstall || bUninstall

      return h('div', { class: 'core-ops' }, [
        h('div', { class: 'core-ops-row' }, [
          opButton('启动', {
            busyKey: `start:${row.id}`,
            onClick: () => store.startCoreAction(row.id),
            disabled: !stopped || stopping || bStop || bRestart || bUninstall,
            loading: bStart,
            type: stopped ? 'primary' : 'default',
            ghost: !stopped,
            title: stopped ? '启动该内核' : '当前状态不可启动',
          }),
          opButton('停止', {
            busyKey: `stop:${row.id}`,
            onClick: () => store.stopCoreAction(row.id),
            disabled: !running || starting || bStart || bRestart || bInstall,
            loading: bStop,
            type: running ? 'error' : 'default',
            ghost: !running,
            title: running ? '停止该内核' : '当前状态不可停止',
          }),
          opButton('重启', {
            busyKey: `restart:${row.id}`,
            onClick: () => store.restartCoreAction(row.id),
            disabled: !running || anyTransition,
            ghost: true,
            title: '仅运行中的内核可重启',
          }),
          opButton('诊断', {
            onClick: () => openDiagnostics(row.id),
            type: 'info',
            ghost: true,
            title: '查看真实运行环境与崩溃原因',
          }),
        ]),
        h('div', { class: 'core-ops-row' }, [
          opButton('安装', {
            busyKey: `install:${row.id}`,
            onClick: () => openInstall(row.id),
            disabled: running || starting || stopping || bStart || bStop || bUninstall,
            loading: bInstall,
            type: missing ? 'primary' : 'default',
            ghost: !missing,
            title: missing ? '安装该内核' : '重新安装（覆盖当前安装）',
          }),
          opButton('卸载', {
            busyKey: `uninstall:${row.id}`,
            onClick: () => confirmUninstall(row.id),
            disabled: !installed || running || anyTransition,
            ghost: true,
            title: installed ? '需先停止再卸载' : '尚未安装',
          }),
          opButton('升级', {
            onClick: () => openVersionOp('upgrade', row.id, row.version),
            disabled: !installed || running || anyTransition,
            ghost: true,
            title: '选择版本升级',
          }),
          opButton('回滚', {
            onClick: () => openVersionOp('rollback', row.id),
            disabled: !installed || running || anyTransition,
            ghost: true,
            title: '选择备份版本回滚',
          }),
        ]),
        h('div', { class: 'core-ops-state' }, stateHint(phase)),
      ])
    },
  },
])

/* 启动中 / 停止中 是后端过渡态：轮询直到稳定，保证颜色及时切换 */
let pollTimer: number | undefined
let pollTicks = 0
function stopPolling(): void {
  if (pollTimer !== undefined) {
    window.clearInterval(pollTimer)
    pollTimer = undefined
  }
  pollTicks = 0
}
watch(
  () => store.cores.map((c) => `${c.id}:${c.state}`).join('|'),
  () => {
    const transitional = store.cores.some((c) => ['starting', 'stopping'].includes(phaseOf(c)))
    if (transitional && pollTimer === undefined) {
      pollTimer = window.setInterval(() => {
        pollTicks += 1
        void store.refreshCores()
        if (pollTicks >= 25) stopPolling()
      }, 1200)
    } else if (!transitional) {
      stopPolling()
    }
  },
)

onMounted(() => {
  store.ensureBase()
})
onUnmounted(stopPolling)
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

    
    <NCard size="small" class="section-gap">
      <NSpace align="center" :wrap="true" :size="10">
        <NTag :bordered="false" size="small">已选 {{ selectedKeys.length }} 个</NTag>
        <NTag :bordered="false" size="small" type="success">运行中 {{ runningCount }}</NTag>
        <NTag :bordered="false" size="small">已停止 {{ stoppedCores.length }}</NTag>
        <NTag :bordered="false" size="small" type="warning">未安装 {{ missingCores.length }}</NTag>
        <NTag :bordered="false" size="small">
          启动模式：{{ batchMode === 'parallel' ? '多核并行' : '单核独占' }}
        </NTag>
        <NButton
          size="small"
          :type="startableCount && batchMode === 'parallel' ? 'primary' : 'default'"
          :disabled="!startableCount"
          :loading="store.isBusy('batch-start') && batchMode === 'parallel'"
          @click="batchStart(false)"
        >
          多核并行启动
        </NButton>
        <NButton
          size="small"
          :type="startableCount && batchMode === 'exclusive' ? 'primary' : 'default'"
          :disabled="!startableCount"
          :loading="store.isBusy('batch-start') && batchMode === 'exclusive'"
          @click="batchStart(true)"
        >
          单核独占启动
        </NButton>
        <NButton
          size="small"
          :type="runningCount ? 'error' : 'default'"
          :disabled="!runningCount"
          :loading="store.isBusy('batch-stop')"
          @click="batchStop"
        >
          全部停止
        </NButton>
        <NButton size="small" quaternary @click="selectedKeys = []">清空选择</NButton>
      </NSpace>
      <div class="hint" style="margin-top: 8px">
        未选择时，启动作用于全部「已安装且未运行」的内核（{{ startableCount }} 个），
        停止作用于全部「运行中」的内核（{{ runningCount }} 个）；也可用左侧复选框精确选择。
        「单核独占启动」会先停止其他内核再启动所选内核。
      </div>
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
      :scroll-x="1150"
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

    <NModal
      v-model:show="showDiag"
      preset="card"
      :title="`内核诊断 · ${diagTarget}`"
      style="width: 720px"
      :bordered="false"
    >
      <div v-if="diagLoading" class="hint">正在采集诊断信息…</div>
      <NSpace v-else-if="diag" vertical :size="12">
        <NAlert type="warning" :bordered="false">{{ diag.hint }}</NAlert>

        <div class="diag-row">
          <NTag size="small" :bordered="false">{{ STATE_LABELS[diag.state] || diag.state }}</NTag>
          <span class="hint mono">PID {{ diag.pid || '—' }}</span>
          <span class="hint">{{ diag.running ? '进程在运行' : '进程未运行' }}</span>
          <span class="hint mono">检测时间 {{ diag.checked_at }}</span>
        </div>

        <div class="diag-checks">
          <div v-for="c in diag.checks" :key="c.name" class="diag-check">
            <NTag size="small" :bordered="false" :type="c.ok ? 'success' : 'error'">
              {{ c.ok ? '通过' : '失败' }}
            </NTag>
            <span class="diag-check__name">{{ c.name }}</span>
            <span class="diag-check__detail mono">{{ c.detail }}</span>
          </div>
        </div>

        <div>
          <div class="hint">二进制</div>
          <div class="mono diag-path">{{ diag.binary.path || '—' }}</div>
          <div class="hint">
            {{
              diag.binary.exists
                ? `${diag.binary.size} 字节 · ${diag.binary.modified}`
                : '文件不存在'
            }}
          </div>
        </div>

        <div>
          <div class="hint">版本输出</div>
          <pre v-if="diag.version_output" class="diag-pre">{{ diag.version_output }}</pre>
          <div v-else class="hint">无输出</div>
          <div v-if="diag.version_error" class="diag-error">{{ diag.version_error }}</div>
        </div>

        <div>
          <div class="hint">配置文件</div>
          <div class="mono diag-path">{{ diag.config.path || '—' }}</div>
          <div class="hint">
            {{ diag.config.exists ? `${diag.config.size} 字节` : '文件不存在' }}
          </div>
          <pre v-if="diag.config.validation_error" class="diag-pre diag-pre--error">{{
            diag.config.validation_error
          }}</pre>
        </div>

        <div v-if="diag.ports.length">
          <div class="hint">端口</div>
          <div class="diag-ports">
            <div v-for="p in diag.ports" :key="p.port" class="diag-port">
              <span class="mono diag-port__num">{{ p.port }}</span>
              <NTag size="small" :bordered="false" :type="p.in_use ? 'error' : 'success'">
                {{ p.in_use ? '占用' : '空闲' }}
              </NTag>
              <span class="mono diag-port__pid">{{ p.owner_pid ? `PID ${p.owner_pid}` : '—' }}</span>
              <span class="hint diag-port__note">{{ p.note || '' }}</span>
            </div>
          </div>
        </div>

        <div>
          <div class="hint">日志尾部（最近 {{ diag.log_tail.length }} 行）</div>
          <pre v-if="diag.log_tail.length" class="diag-pre diag-pre--log">{{
            diag.log_tail.join('\n')
          }}</pre>
          <div v-else class="hint">无日志</div>
        </div>
      </NSpace>
      <template #footer>
        <NSpace justify="end">
          <NButton :loading="diagLoading" @click="loadDiagnostics()">重新检测</NButton>
          <NButton type="primary" @click="showDiag = false">关闭</NButton>
        </NSpace>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.diag-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.diag-checks {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.diag-check {
  display: flex;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
}
.diag-check__name {
  flex: 0 0 78px;
  font-size: 12px;
}
.diag-check__detail {
  flex: 1 1 auto;
  min-width: 0;
  font-size: 12px;
  opacity: 0.8;
  word-break: break-all;
}
.diag-path {
  font-size: 12px;
  word-break: break-all;
}
.diag-error {
  font-size: 12px;
  color: #d03050;
  word-break: break-all;
}
.diag-pre {
  margin: 4px 0 0;
  padding: 8px 10px;
  max-height: 180px;
  overflow: auto;
  background: rgba(125, 125, 125, 0.08);
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
}
.diag-pre--error {
  color: #d03050;
}
.diag-pre--log {
  max-height: 220px;
  white-space: pre;
}
.diag-ports {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-top: 4px;
}
.diag-port {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}
.diag-port__num {
  flex: 0 0 56px;
}
.diag-port__pid {
  flex: 0 0 76px;
  opacity: 0.8;
}
.diag-port__note {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
