<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NModal,
  NSpace,
  NSwitch,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import { setMaintenance } from '../api/client'
import type {
  UpdateCheckResult,
  UpdateDownloadResult,
  UpdateApplyResult,
  UpdateStatus,
} from '../api/ngg'
import { useApiAction } from '../composables/useApi'

const props = defineProps<{
  show: boolean
  autoCheck: boolean
  autoApply: boolean
  autoApplyResult: UpdateApplyResult | null
  initialResult: UpdateCheckResult | null
}>()
const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  (e: 'auto-check-change', v: boolean): void
  (e: 'auto-apply-change', v: boolean): void
  (e: 'checked', v: UpdateCheckResult | null): void
}>()

const { isBusy, run } = useApiAction()
const status = ref<UpdateStatus | null>(null)
const result = ref<UpdateCheckResult | null>(props.initialResult)
const downloaded = ref<UpdateDownloadResult | null>(null)
const applied = ref<UpdateApplyResult | null>(null)
const errorText = ref('')

type StepState = 'pending' | 'running' | 'done' | 'fail'
interface Step { label: string; state: StepState; detail?: string }
const steps = ref<Step[]>([])
const quickRunning = ref(false)
const restartNote = ref('')

const visible = computed({
  get: () => props.show,
  set: (v: boolean) => emit('update:show', v),
})

watch(
  () => props.initialResult,
  (v) => { if (v) result.value = v },
)

function shortDate(v?: string) {
  return v ? String(v).replace('T', ' ').slice(0, 19) : '—'
}

function sleep(ms: number) {
  return new Promise<void>((resolve) => setTimeout(resolve, ms))
}

async function loadStatus() {
  const s = await run('status', () => api.getUpdateStatus())
  if (s) status.value = s
}

function friendlyError(prefix: string, e: unknown) {
  const raw = e instanceof Error ? e.message : String(e)
  if (/404|repository not found/i.test(raw)) {
    return `${prefix}：GitHub Releases 不可访问（仓库不存在或网络无法访问 api.github.com）。`
  }
  if (/403|rate limit/i.test(raw)) {
    return `${prefix}：GitHub API 触发匿名速率限制，请稍后重试；无头部署可选设置 NGG_GITHUB_TOKEN 提高配额。`
  }
  if (/SHA256SUMS|mismatch/i.test(raw)) {
    return `${prefix}：Release 缺少 SHA256SUMS 或摘要不匹配，出于安全已拒绝下载。`
  }
  return `${prefix}：${raw}`
}

async function doCheck() {
  errorText.value = ''
  const r = await run('check', () => api.checkUpdate())
  if (r) {
    result.value = r
    downloaded.value = null
    applied.value = null
    if (r.status) status.value = r.status
    emit('checked', r)
  } else {
    errorText.value = '检查失败：无法访问 GitHub Releases，请确认本机网络可访问 api.github.com。'
  }
  return r
}

async function doDownload(silent = false) {
  const r = result.value
  if (!r?.release || !r.asset) {
    if (!silent) window.$message?.warning('请先「检查更新」，并确认存在匹配当前平台的资产')
    return null
  }
  const d = await run('download', () =>
    api.downloadUpdate({ tag: r.release!.tag_name, asset: r.asset!.name }),
  )
  if (d) {
    downloaded.value = d
    if (!silent) window.$message?.success(`已下载并校验 ${d.asset}`)
  } else if (!silent) {
    errorText.value = '下载失败：Release 缺少 SHA256SUMS 或摘要不匹配时会拒绝下载。'
  }
  return d
}

async function doApply(silent = false) {
  const r = result.value
  if (!r?.release || !r.asset) return null
  const a = await run('apply', () =>
    api.applyUpdate({ tag: r.release!.tag_name, asset: r.asset!.name }),
  )
  if (a) {
    applied.value = a
    if (!silent) {
      if (a.started) window.$message?.success('更新已在后台启动，完成后页面会自动刷新')
      else window.$message?.warning('当前平台需在服务器终端执行返回的命令')
    }
  } else if (!silent) {
    errorText.value = '应用失败：请先完成下载与校验，并确认更新脚本随安装包一起部署。'
  }
  return a
}

/* One click: check -> download+SHA256 -> apply -> wait for the service to
   come back on the NEW version, then reload the Web console automatically. */
async function waitForRestart(targetTag: string): Promise<boolean> {
  const want = targetTag.replace(/^v/, '').split('-')[0]
  for (let i = 0; i < 45; i++) {
    await sleep(2000)
    try {
      const res = await fetch('/api/v1/system', {
        credentials: 'same-origin',
        cache: 'no-store',
      })
      if (!res.ok) continue
      const data = await res.json()
      if (data?.ngg_version === want) return true
    } catch {
      // service is restarting; keep polling
    }
  }
  return false
}

function resetSteps() {
  steps.value = [
    { label: '检查 Release 新版本', state: 'pending' },
    { label: '下载并强制 SHA256 校验', state: 'pending' },
    { label: '备份 / 替换 / 重启服务', state: 'pending' },
    { label: '等待服务恢复并自动刷新', state: 'pending' },
  ]
}

function setStep(i: number, state: StepState, detail?: string) {
  const next = steps.value.slice()
  if (next[i]) {
    next[i] = { ...next[i], state, detail }
    steps.value = next
  }
}

async function quickUpdate() {
  if (quickRunning.value) return
  errorText.value = ''
  restartNote.value = ''
  resetSteps()
  quickRunning.value = true
  // Hold the silent mode from "apply" until the page reloads, so the restart
  // window never spams the console with transport errors.
  let holdMaintenance = false
  try {
    // 1. check
    setStep(0, 'running')
    const r = await doCheck()
    if (!r) { setStep(0, 'fail'); return }
    setStep(0, 'done', r.latest)
    if (!r.update_available || !r.release || !r.asset) {
      setStep(1, 'done', '已是最新版本，无需下载')
      setStep(2, 'done', '跳过')
      setStep(3, 'done', '跳过')
      restartNote.value = '当前已是最新版本，无需更新。'
      return
    }

    // 2. download
    setStep(1, 'running')
    const d = await doDownload(true)
    if (!d) { setStep(1, 'fail'); return }
    setStep(1, 'done', `${d.asset} · ${Math.round(d.size / 1024 / 1024 * 10) / 10} MB`)

    // 3. apply
    setStep(2, 'running')
    const a = await doApply(true)
    if (!a) { setStep(2, 'fail'); return }
    if (!a.started) {
      setStep(2, 'done', '需在服务器终端执行')
      setStep(3, 'done', '跳过')
      restartNote.value = a.note || '请按下方返回的命令在服务器终端执行。'
      return
    }
    setStep(2, 'done', '后台脚本已启动')
    setMaintenance(true)
    holdMaintenance = true

    // 4. wait for the NEW version, then refresh the Web console
    setStep(3, 'running')
    const ok = await waitForRestart(r.release.tag_name)
    if (ok) {
      setStep(3, 'done', '服务已恢复，正在刷新')
      window.location.reload()
    } else {
      setStep(3, 'fail', '90 秒内未检测到新版本')
      restartNote.value = '服务未在 90 秒内以新版本恢复，请查看更新日志并确认服务状态。'
      setMaintenance(false)
      holdMaintenance = false
    }
  } catch (e) {
    errorText.value = friendlyError('一键更新中断', e)
  } finally {
    quickRunning.value = false
    if (!holdMaintenance) setMaintenance(false)
  }
}

function confirmQuickUpdate() {
  window.$dialog?.warning({
    title: '立即更新',
    content: '将自动完成：检查新版本 → 下载并 SHA256 校验 → 备份、替换程序文件并重启服务 → 界面自动刷新。期间管理界面会短暂中断（约 10~30 秒），失败会自动回滚。确定继续？',
    positiveText: '立即更新',
    negativeText: '取消',
    onPositiveClick: () => { void quickUpdate() },
  })
}

async function manualApply() {
  errorText.value = ''
  const r = result.value
  if (!r?.release || !r.asset) return
  window.$dialog?.warning({
    title: '应用更新',
    content: '会停止 NGG 服务、替换程序文件并重启（失败自动回滚）。过程中管理界面会短暂中断，确定继续？',
    positiveText: '应用更新',
    negativeText: '取消',
    onPositiveClick: async () => {
      const a = await doApply()
      if (a?.started) {
        setMaintenance(true)
        void waitForRestart(r.release!.tag_name).then((ok) => {
          if (ok) window.location.reload()
          else setMaintenance(false)
        })
      }
    },
  })
}

onMounted(() => {
  loadStatus()
  if (props.autoCheck && !result.value) void doCheck()
})
</script>

<template>
  <NModal
    v-model:show="visible"
    preset="card"
    title="系统更新"
    :style="{ width: '700px', maxWidth: '94vw' }"
    :bordered="false"
  >
    <div class="upd-section">
      <div class="upd-row">
        <div>
          <div class="upd-h">当前版本</div>
          <div class="upd-v">
            {{ status?.generation_label || 'Gen 2' }} ·
            v{{ status?.release || '2.2.1' }}
            <NTag size="small" :bordered="false">{{ status?.generation_name || '第二代' }}</NTag>
          </div>
        </div>
        <div class="upd-meta">
          仓库 <span class="mono">{{ status?.repo || 'welkacton/ngg-next-generation-gateway' }}</span><br />
          平台 <span class="mono">{{ status?.platform || '—' }}</span>
        </div>
      </div>
      <div class="hint" style="margin-top: 8px">
        这是浏览器访问本机服务的 Web 控制台。更新会替换服务端的
        <span class="mono">nggd.exe</span> 与 <span class="mono">web/dist</span>，完成后界面自动刷新。
      </div>
    </div>

    <!-- Private repository: provide a PAT right here instead of forcing a
         server-side environment variable the Web UI cannot set. -->
    <div class="upd-section upd-controls">
      <NSpace align="center" :wrap="true" :size="10">
        <NButton
          size="small"
          type="primary"
          :loading="quickRunning"
          :disabled="isBusy('check') || isBusy('download') || isBusy('apply')"
          @click="confirmQuickUpdate"
        >
          立即更新（一键完成）
        </NButton>
        <NButton size="small" :loading="isBusy('check')" :disabled="quickRunning" @click="doCheck">
          检查更新
        </NButton>
        <NButton size="small" :loading="isBusy('download')" :disabled="!result?.asset || quickRunning" @click="doDownload()">
          下载更新
        </NButton>
        <NButton size="small" :loading="isBusy('apply')" :disabled="!downloaded || quickRunning" @click="manualApply">
          应用更新
        </NButton>
      </NSpace>
      <NSpace align="center" :wrap="true" :size="16" style="margin-top: 10px">
        <span class="upd-switch">
          自动检查
          <NSwitch
            size="small"
            :value="autoCheck"
            @update:value="(v: boolean) => emit('auto-check-change', v)"
          />
        </span>
        <span class="upd-switch">
          自动应用更新
          <NSwitch
            size="small"
            :value="autoApply"
            @update:value="(v: boolean) => emit('auto-apply-change', v)"
          />
        </span>
      </NSpace>
      <div class="hint" style="margin-top: 8px">
        「立即更新」会按 检查 → 下载校验 → 备份替换重启 → 自动刷新 一次完成；下面的单步按钮用于排查问题。
        自动检查在打开界面时与每 6 小时各执行一次；开启「自动应用更新」后同样走完整链路，开启时需二次确认。
      </div>
    </div>

    <div v-if="steps.length" class="upd-section upd-steps">
      <div v-for="(s, i) in steps" :key="i" class="upd-step" :class="'is-' + s.state">
        <span class="upd-step-dot">{{ s.state === 'done' ? '✓' : s.state === 'fail' ? '!' : i + 1 }}</span>
        <span class="upd-step-label">{{ s.label }}</span>
        <span v-if="s.detail" class="upd-step-detail">{{ s.detail }}</span>
      </div>
      <div v-if="restartNote" class="hint" style="margin-top: 8px">{{ restartNote }}</div>
    </div>

    <NAlert v-if="errorText" type="warning" :bordered="false" class="upd-section">
      {{ errorText }}
    </NAlert>

    <div v-if="result" class="upd-section">
      <div class="upd-row">
        <div>
          <div class="upd-h">仓库最新版本</div>
          <div class="upd-v">
            {{ result.latest || '—' }}
            <NTag v-if="result.update_available" size="small" type="info" :bordered="false">有新版本</NTag>
            <NTag v-else size="small" :bordered="false">已是最新</NTag>
          </div>
        </div>
        <div class="upd-meta">
          检查时间 <span class="mono">{{ shortDate(result.checked_at) }}</span><br />
          来源 <span class="mono">{{ result.source }}</span>
        </div>
      </div>

      <div v-if="result.release" class="upd-release">
        <div class="hint">
          {{ shortDate(result.release.published_at) }} ·
          {{ result.release.prerelease ? '预发布' : '正式版' }} ·
          {{ result.asset ? result.asset.name : '无匹配当前平台的资产' }}
        </div>
        <pre v-if="result.release.body" class="output-block upd-notes">{{ result.release.body }}</pre>
        <div v-if="result.note" class="hint" style="margin-top: 8px">{{ result.note }}</div>
        <div class="hint" style="margin-top: 8px">
          <a :href="result.release.html_url" target="_blank" rel="noopener">在 GitHub 查看该 Release →</a>
        </div>
      </div>
    </div>

    <div v-if="downloaded" class="upd-section">
      <NAlert type="success" :bordered="false">
        <template #header>已下载并通过 SHA256 校验</template>
        <div class="mono">{{ downloaded.path }}</div>
        <div class="hint">sha256: {{ downloaded.sha256 }}</div>
      </NAlert>
    </div>

    <div v-if="applied || autoApplyResult" class="upd-section">
      <NAlert :type="(applied || autoApplyResult)?.started ? 'info' : 'default'" :bordered="false">
        <template #header>
          {{ (applied || autoApplyResult)?.started ? '更新已在后台启动' : '需要在服务器终端执行' }}
          <span v-if="!applied && autoApplyResult">（自动应用）</span>
        </template>
        <div>{{ (applied || autoApplyResult)?.note || '请查看更新日志了解结果。' }}</div>
        <pre v-if="(applied || autoApplyResult)?.manual_command" class="output-block upd-notes">{{ (applied || autoApplyResult)?.manual_command }}</pre>
        <div class="hint" style="margin-top: 8px">
          日志：<span class="mono">{{ (applied || autoApplyResult)?.log_path }}</span>
        </div>
      </NAlert>
    </div>

    <template #footer>
      <div class="hint">
        更新脚本：{{ status?.script_path || '—' }} ·
        {{ status?.script_ready ? '已就绪' : '未随安装包部署（只能检查与下载）' }}
      </div>
    </template>
  </NModal>
</template>

<style scoped>
.upd-section {
  margin-bottom: 16px;
}
.upd-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}
.upd-h {
  font-size: 12px;
  color: #86868b;
  margin-bottom: 4px;
}
.upd-v {
  font-size: 18px;
  font-weight: 600;
  letter-spacing: -0.4px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.upd-meta {
  font-size: 12px;
  color: #8e8e93;
  line-height: 1.8;
  text-align: right;
}
.upd-controls {
  padding: 12px 14px;
  background: #fafafb;
  border: 1px solid #e6e6eb;
  border-radius: 10px;
}
.upd-token {
  padding: 12px 14px;
  background: #ffffff;
  border: 1px solid #e6e6eb;
  border-radius: 10px;
}
.upd-switch {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-size: 12.5px;
  color: #6e6e73;
}
.upd-release {
  margin-top: 12px;
}
.upd-notes {
  max-height: 180px;
  margin-top: 8px;
  white-space: pre-wrap;
}
.upd-steps {
  padding: 10px 14px;
  background: #fafafb;
  border: 1px solid #e6e6eb;
  border-radius: 10px;
}
.upd-step {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 0;
  font-size: 12.5px;
  color: #8e8e93;
}
.upd-step.is-running { color: #0066cc; }
.upd-step.is-done { color: #1d9a68; }
.upd-step.is-fail { color: #d64545; }
.upd-step-dot {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  border: 1px solid currentColor;
  font-size: 10px;
  flex-shrink: 0;
}
.upd-step-label { flex: 1; }
.upd-step-detail {
  font-size: 11.5px;
  color: #8e8e93;
}
a {
  color: #0066cc;
  text-decoration: none;
}
a:hover {
  text-decoration: underline;
}
</style>
