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

async function loadStatus() {
  const s = await run('status', () => api.getUpdateStatus())
  if (s) status.value = s
}

async function doCheck() {
  errorText.value = ''
  const r = await run('check', () => api.checkUpdate())
  if (r) {
    result.value = r
    downloaded.value = null
    applied.value = null
    emit('checked', r)
  } else {
    errorText.value = '检查失败：请确认本机可访问 GitHub API；私有仓库需在服务端设置 NGG_GITHUB_TOKEN 或 GITHUB_TOKEN。'
  }
}

async function doDownload() {
  errorText.value = ''
  const r = result.value
  if (!r?.release || !r.asset) {
    window.$message?.warning('请先「检查更新」，并确认存在匹配当前平台的资产')
    return
  }
  const d = await run('download', () =>
    api.downloadUpdate({ tag: r.release!.tag_name, asset: r.asset!.name }),
  )
  if (d) {
    downloaded.value = d
    window.$message?.success(`已下载并校验 ${d.asset}`)
  } else {
    errorText.value = '下载失败：Release 缺少 SHA256SUMS 或摘要不匹配时会拒绝下载。'
  }
}

async function doApply() {
  errorText.value = ''
  const r = result.value
  if (!r?.release || !r.asset) return
  window.$dialog?.warning({
    title: '应用更新',
    content: '会停止 NGG 服务、替换程序文件并重启（失败自动回滚）。过程中管理界面会短暂中断，确定继续？',
    positiveText: '应用更新',
    negativeText: '取消',
    onPositiveClick: async () => {
      const a = await run('apply', () =>
        api.applyUpdate({ tag: r.release!.tag_name, asset: r.asset!.name }),
      )
      if (a) {
        applied.value = a
        if (a.started) window.$message?.success('更新已在后台启动，约 30 秒后刷新页面')
        else window.$message?.warning('当前平台需在服务器终端执行返回的命令')
      } else {
        errorText.value = '应用失败：请先完成下载与校验，并确认更新脚本随安装包一起部署。'
      }
    },
  })
}

onMounted(() => {
  loadStatus()
  if (props.autoCheck && !result.value) doCheck()
})
</script>

<template>
  <NModal
    v-model:show="visible"
    preset="card"
    title="系统更新"
    :style="{ width: '660px', maxWidth: '92vw' }"
    :bordered="false"
  >
    <div class="upd-section">
      <div class="upd-row">
        <div>
          <div class="upd-h">当前版本</div>
          <div class="upd-v">
            {{ status?.generation_label || 'Gen 2' }} ·
            v{{ status?.release || '2.0.0' }}
            <NTag size="small" :bordered="false">{{ status?.generation_name || '第二代' }}</NTag>
          </div>
        </div>
        <div class="upd-meta">
          仓库 <span class="mono">{{ status?.repo || 'welkacton/ngg-next-generation-gateway' }}</span><br />
          平台 <span class="mono">{{ status?.platform || '—' }}</span>
        </div>
      </div>
    </div>

    <div class="upd-section upd-controls">
      <NSpace align="center" :wrap="true" :size="10">
        <NButton size="small" type="primary" :loading="isBusy('check')" @click="doCheck">
          检查更新
        </NButton>
        <NButton size="small" :loading="isBusy('download')" :disabled="!result?.asset" @click="doDownload">
          下载更新
        </NButton>
        <NButton size="small" :loading="isBusy('apply')" :disabled="!downloaded" @click="doApply">
          应用更新
        </NButton>
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
        自动检查在打开界面时与每 6 小时各执行一次；默认只检查不安装。开启「自动应用更新」后，
        发现新版本会自动下载、SHA256 校验并替换程序文件、重启服务（失败自动回滚），开启时需二次确认。
        私有仓库请在服务端设置 <span class="mono">NGG_GITHUB_TOKEN</span> / <span class="mono">GITHUB_TOKEN</span>。
      </div>
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
.upd-switch {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-size: 12.5px;
  color: #6e6e73;
  margin-left: 6px;
}
.upd-release {
  margin-top: 12px;
}
.upd-notes {
  max-height: 180px;
  margin-top: 8px;
  white-space: pre-wrap;
}
a {
  color: #0066cc;
  text-decoration: none;
}
a:hover {
  text-decoration: underline;
}
</style>
