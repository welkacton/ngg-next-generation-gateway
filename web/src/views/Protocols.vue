<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NDrawer,
  NDrawerContent,
  NInput,
  NModal,
  NSelect,
  NSwitch,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type {
  CatalogKernel,
  Profile,
  ProfileKernelOption,
  ProfileOptions,
  ProtocolCatalog,
} from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { formatTime } from '../utils/format'

const { isBusy, run } = useApiAction()

/* ------------------------------------------------------------------ *
 * ① 内核支持协议：纯文字目录，官网定时同步，不可编辑
 * ------------------------------------------------------------------ */
const catalog = ref<ProtocolCatalog | null>(null)
const catalogError = ref('')
let pollTimer: number | undefined

async function loadCatalog(silent = false) {
  const data = silent
    ? await api.getProtocolCatalog().catch(() => null)
    : await run('catalog', () => api.getProtocolCatalog())
  if (data) {
    catalog.value = data
    catalogError.value = data.stale ? data.message : ''
  }
}

async function refreshCatalog() {
  const data = await run('catalog-refresh', () => api.refreshProtocolCatalog(), '已从内核官网同步协议目录')
  if (data) {
    catalog.value = data
    catalogError.value = data.stale ? data.message : ''
  }
}

const kernelLabel: Record<string, string> = {
  mihomo: 'Mihomo',
  'sing-box': 'sing-box',
  'xray-core': 'Xray-core',
}

function syncLabel(k: CatalogKernel) {
  if (k.error) return '同步失败 · 沿用上次结果'
  if (k.from_cache) return '本地缓存'
  return '官网已同步'
}

/* ------------------------------------------------------------------ *
 * ② 配置文件：卡片列表（参考截图），点开可编辑
 * ------------------------------------------------------------------ */
const profiles = ref<Profile[]>([])
const activeId = ref('')
const options = ref<ProfileOptions | null>(null)
const search = ref('')
const checkedIds = ref<string[]>([])

const visibleProfiles = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return profiles.value
  return profiles.value.filter(
    (p) =>
      p.name.toLowerCase().includes(q) ||
      p.kernel.toLowerCase().includes(q) ||
      (p.description ?? '').toLowerCase().includes(q),
  )
})

async function loadProfiles() {
  const data = await run('profiles', () => api.listProfiles())
  if (data) {
    profiles.value = data.profiles ?? []
    activeId.value = data.active ?? ''
    const valid = new Set(profiles.value.map((p) => p.id))
    checkedIds.value = checkedIds.value.filter((id) => valid.has(id))
  }
}

async function applyToggle(ids: string[], all: boolean, enabled: boolean) {
  if (!all && !ids.length) {
    window.$message?.warning('请先勾选要操作的配置文件')
    return
  }
  const res = await run(
    `toggle:${all ? 'all' : 'selected'}:${enabled}`,
    () => api.toggleProfiles({ ids, all, enabled }),
    `${enabled ? '启用' : '停用'}完成`,
  )
  if (!res) return
  profiles.value = res.profiles ?? profiles.value
  activeId.value = res.active ?? activeId.value
}

async function toggleOne(p: Profile, enabled: boolean) {
  await applyToggle([p.id], false, enabled)
}

function toggleChecked(id: string, on: boolean) {
  checkedIds.value = on
    ? [...checkedIds.value, id]
    : checkedIds.value.filter((x) => x !== id)
}

async function loadOptions() {
  const data = await run('options', () => api.getProfileOptions())
  if (data) options.value = data
}

function kernelOf(p: Profile): ProfileKernelOption | undefined {
  return options.value?.kernels.find((k) => k.kernel === p.kernel)
}

function sectionCount(p: Profile) {
  return (p.sections ?? []).filter((s) => String(s.content ?? '').trim() !== '').length
}

/* -------------------------------- 新建 -------------------------------- */
const showNew = ref(false)
const newName = ref('')
const newKernel = ref('mihomo')
const newDesc = ref('')

const kernelSelectOptions = computed(() =>
  (options.value?.kernels ?? []).map((k) => ({
    label: `${k.label} · ${k.format.toUpperCase()}`,
    value: k.kernel,
  })),
)

function openNew() {
  newName.value = ''
  newKernel.value = 'mihomo'
  newDesc.value = ''
  showNew.value = true
}

async function createNew() {
  const name = newName.value.trim()
  if (!name) {
    window.$message?.warning('请填写配置文件名称')
    return
  }
  const tpl = await run('new-tpl', () => api.getProfileTemplate(newKernel.value, name))
  if (!tpl) return
  const payload: Partial<Profile> = {
    name: tpl.name,
    kernel: tpl.kernel,
    format: tpl.format,
    description: newDesc.value.trim() || tpl.description,
    sections: tpl.sections,
    enabled: true,
    default: false,
  }
  const res = await run('new', () => api.createProfile(payload), '配置文件已创建')
  if (!res) return
  showNew.value = false
  await loadProfiles()
  openEditor(res.profile.id)
}

/* -------------------------------- 编辑 -------------------------------- */
const showEditor = ref(false)
const editing = ref<Profile | null>(null)
const activeSection = ref('protocols')
const editorNote = ref('')

async function openEditor(id: string) {
  const data = await run('open:' + id, () => api.getProfile(id))
  if (!data) return
  editing.value = JSON.parse(JSON.stringify(data)) as Profile
  activeSection.value = 'protocols'
  editorNote.value = ''
  showEditor.value = true
}

function currentSection(): string {
  return editing.value?.sections.find((x) => x.key === activeSection.value)?.content ?? ''
}

function setCurrentSection(v: string) {
  const s = editing.value?.sections.find((x) => x.key === activeSection.value)
  if (s) s.content = v
}

const currentMeta = computed(() =>
  editing.value?.sections.find((x) => x.key === activeSection.value),
)

async function changeKernel(v: string) {
  if (!editing.value) return
  const fmt = options.value?.kernels.find((k) => k.kernel === v)?.format ?? 'json'
  editing.value.kernel = v
  editing.value.format = fmt
  editorNote.value = '内核已切换。若当前分区仍是旧内核语法，请点「重建模板」生成对应语法的带注释模板。'
}

async function rebuildTemplate() {
  if (!editing.value) return
  const p = editing.value
  window.$dialog?.warning({
    title: '重建模板',
    content: `将用 ${p.kernel} 的官方语法覆盖当前 6 个分区的示例内容（手改内容会丢失）。继续吗？`,
    positiveText: '重建',
    negativeText: '取消',
    onPositiveClick: async () => {
      const tpl = await run('rebuild', () => api.getProfileTemplate(p.kernel, p.name))
      if (!tpl || !editing.value) return
      editing.value.sections = tpl.sections
      editing.value.format = tpl.format
      editorNote.value = `已重建 ${p.kernel} 官方语法模板，请重新校验后保存。`
    },
  })
}

async function validateEditor() {
  if (!editing.value) return
  const res = await run('validate', () => api.validateProfile(editing.value!), '语法校验通过')
  if (res) editorNote.value = res.message
}

async function saveEditor(): Promise<boolean> {
  if (!editing.value) return false
  const p = editing.value
  if (!p.name.trim()) {
    window.$message?.warning('请填写配置文件名称')
    return false
  }
  const res = p.id
    ? await run('save', () => api.updateProfile(p.id, p), '配置文件已保存')
    : await run('save', () => api.createProfile(p), '配置文件已创建')
  if (!res) return false
  editing.value = JSON.parse(JSON.stringify(res.profile)) as Profile
  await loadProfiles()
  editorNote.value = `已保存 · ${formatTime(editing.value.updated_at)}`
  return true
}

/* ------------------------------ 完整配置 ------------------------------ *
 * 双向编辑：分段 -> Render -> 完整配置 -> Split -> 分段。
 * 完整配置不再是只读 <pre>：用户编辑后（停止输入 650ms）自动调用
 * /profiles/split，把改动写回上方 6 个分区；语法错误时不覆盖分区，
 * 只在状态栏提示，关窗时再让用户决定是修正还是放弃。
 * -------------------------------------------------------------------- */
const showFull = ref(false)
const fullContent = ref('')
const fullFormat = ref('')
const fullBase = ref('') // 最近一次与分区一致的文本
const fullAutoSync = ref(true)
const fullDirty = ref(false)
const fullWarnings = ref<string[]>([])
const fullChanged = ref<string[]>([])
type SyncState = 'idle' | 'dirty' | 'syncing' | 'synced' | 'error'
const fullSyncState = ref<SyncState>('idle')
const fullSyncMsg = ref('')
let fullDebounce: number | undefined
let fullSeq = 0 // 丢弃过期的异步响应，避免快速输入时旧结果覆盖新结果

const statusTitle = computed(
  () =>
    ({ idle: '未修改', dirty: '待同步', syncing: '同步中', synced: '已同步', error: '未同步' })[
      fullSyncState.value
    ],
)

// 直接读取 editing.sections：同步成功后这里的行数会立刻变化，用户能看到
// 「上面的分段配置」确实被更新了。
const fullSectionStats = computed(() =>
  (editing.value?.sections ?? []).map((s) => {
    const text = String(s.content ?? '').trim()
    return {
      key: s.key,
      label: s.label,
      lines: text ? text.split('\n').length : 0,
      changed: fullChanged.value.includes(s.key),
    }
  }),
)

async function openFull() {
  if (!editing.value) return
  // 用本地（可能未保存）的分段渲染，而不是上一次保存的版本。
  const res = await run('render', () => api.validateProfile(editing.value!))
  if (!res) return
  fullContent.value = res.content
  fullBase.value = res.content
  fullFormat.value = res.format
  fullDirty.value = false
  fullWarnings.value = []
  fullChanged.value = []
  fullSyncState.value = 'synced'
  fullSyncMsg.value = '与上方 6 个分区一致，可直接编辑整份配置'
  showFull.value = true
}

function cleanErr(e: unknown): string {
  let msg = e instanceof Error ? e.message : String(e)
  msg = msg.replace(/^HTTP \d+ · [^·]*·\s*/, '')
  msg = msg.replace(/^[A-Z_]+:\s*/, '')
  return msg || '同步失败'
}

async function syncFull(showToast = false): Promise<boolean> {
  const target = editing.value
  if (!target) return false
  if (fullDebounce) {
    window.clearTimeout(fullDebounce)
    fullDebounce = undefined
  }
  const seq = ++fullSeq
  const content = fullContent.value
  if (!content.trim()) {
    fullSyncState.value = 'error'
    fullSyncMsg.value = '完整配置不能为空'
    return false
  }
  fullSyncState.value = 'syncing'
  fullSyncMsg.value = '正在按内核语法拆分到上方分区…'
  try {
    const res = await api.splitProfile({ profile: target, content }, { silentError: true })
    if (seq !== fullSeq) return false
    target.sections = res.sections
    fullFormat.value = res.format
    fullBase.value = content
    fullWarnings.value = res.warnings ?? []
    fullChanged.value = res.changed ?? []
    fullDirty.value = false
    fullSyncState.value = 'synced'
    fullSyncMsg.value =
      res.message + (res.changed?.length ? ` · 更新 ${res.changed.length} 个分区` : ' · 分区无变化')
    editorNote.value = '完整配置已自动同步到上方分区，请点「保存」写入磁盘。'
    if (showToast) window.$message?.success('已同步到上方分区')
    return true
  } catch (e) {
    if (seq !== fullSeq) return false
    fullSyncState.value = 'error'
    fullSyncMsg.value = cleanErr(e)
    return false
  }
}

watch(fullContent, (v) => {
  if (!showFull.value) return
  if (v === fullBase.value) {
    fullDirty.value = false
    fullSyncState.value = 'synced'
    fullSyncMsg.value = '与上方 6 个分区一致'
    return
  }
  fullDirty.value = true
  if (!fullAutoSync.value) {
    fullSeq++ // 让在途请求失效
    fullSyncState.value = 'dirty'
    fullSyncMsg.value = '已修改，点「立即同步到分区」写回上方分区'
    return
  }
  if (fullDebounce) window.clearTimeout(fullDebounce)
  fullSyncState.value = 'syncing'
  fullSyncMsg.value = '输入中，停止输入后自动同步到上方分区…'
  fullDebounce = window.setTimeout(() => void syncFull(), 650)
})

async function closeFull() {
  if (fullDebounce) {
    window.clearTimeout(fullDebounce)
    fullDebounce = undefined
  }
  if (!fullDirty.value) {
    showFull.value = false
    return
  }
  if (await syncFull()) {
    showFull.value = false
    return
  }
  window.$dialog?.warning({
    title: '完整配置尚未写回分区',
    content: `${fullSyncMsg.value}\n\n关闭会放弃这次在完整配置里的修改（上方分区保持不变）；继续编辑可修正语法后重试。`,
    positiveText: '放弃修改并关闭',
    negativeText: '继续编辑',
    onPositiveClick: () => {
      fullDirty.value = false
      showFull.value = false
    },
  })
}

function onFullShow(v: boolean) {
  if (v) showFull.value = true
  else void closeFull()
}

function onAutoSyncChange(v: boolean) {
  if (fullDebounce) {
    window.clearTimeout(fullDebounce)
    fullDebounce = undefined
  }
  if (!v) {
    fullSeq++
    if (fullDirty.value) {
      fullSyncState.value = 'dirty'
      fullSyncMsg.value = '自动同步已关闭，点「立即同步到分区」写回上方分区'
    }
    return
  }
  if (fullDirty.value) void syncFull()
}

async function saveFullAndClose() {
  if (fullDirty.value && !(await syncFull())) {
    window.$message?.error(`完整配置有语法错误，未保存：${fullSyncMsg.value}`)
    return
  }
  if (await saveEditor()) showFull.value = false
}

async function copyText(text: string, okMsg = '已复制到剪贴板') {
  try {
    await navigator.clipboard.writeText(text)
    window.$message?.success(okMsg)
  } catch {
    window.$message?.warning('浏览器拒绝了剪贴板访问，请手动全选复制')
  }
}

function downloadFull() {
  if (!editing.value) return
  const ext = fullFormat.value === 'yaml' ? 'yaml' : 'json'
  const blob = new Blob([fullContent.value], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${editing.value.name}.${ext}`
  a.click()
  URL.revokeObjectURL(url)
}

/* ---------------------------- 卡片行操作 ---------------------------- */
function duplicate(p: Profile) {
  window.$dialog?.warning({
    title: '复制配置文件',
    content: `将 ${p.name} 复制为「${p.name}-copy」？`,
    positiveText: '复制',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await run('dup:' + p.id, () => api.duplicateProfile(p.id, `${p.name}-copy`), '已创建副本')
      if (res) await loadProfiles()
    },
  })
}

async function activate(p: Profile) {
  const res = await run('act:' + p.id, () => api.activateProfile(p.id), `已将 ${p.name} 设为默认`)
  if (res) await loadProfiles()
}

function remove(p: Profile) {
  window.$dialog?.warning({
    title: '删除配置文件',
    content: `确定删除 ${p.name} 吗？此操作不可撤销。默认配置文件需先切换默认才能删除。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await run('del:' + p.id, () => api.deleteProfile(p.id), '配置文件已删除')
      if (!res) return
      if (editing.value?.id === p.id) {
        showEditor.value = false
        editing.value = null
      }
      await loadProfiles()
    },
  })
}

onMounted(async () => {
  await Promise.all([loadCatalog(true), loadProfiles(), loadOptions()])
  // 前端每 60 秒只读一次目录状态；服务端每 6 小时真实访问官网。
  pollTimer = window.setInterval(() => loadCatalog(true), 60_000)
})
onBeforeUnmount(() => {
  if (pollTimer) window.clearInterval(pollTimer)
  if (fullDebounce) window.clearTimeout(fullDebounce)
})
</script>

<template>
  <div class="pc-page">
    <!-- ① 内核支持协议：只读纯文字 -->
    <section class="pc-section">
      <div class="major-head">
        <span class="major-title">内核支持协议</span>
        <span class="major-sub">只读 · 定时从各内核官方文档同步</span>
      </div>

      <div class="pc-sync-bar">
        <div class="pc-sync-info">
          <span class="pc-sync-line">
            <strong>{{ catalog?.source === 'official-docs' ? '官网已同步' : catalog?.source === 'cache' ? '本地缓存' : '内置基线' }}</strong>
            <span class="pc-dot">·</span>
            上次同步 {{ formatTime(catalog?.synced_at) }}
            <span class="pc-dot">·</span>
            下次同步 {{ formatTime(catalog?.next_sync_at) }}
            <span class="pc-dot">·</span>
            每 {{ catalog?.interval_minutes ?? 360 }} 分钟
          </span>
          <span class="pc-sync-msg">{{ catalog?.message || '正在加载…' }}</span>
        </div>
        <NButton size="small" :loading="isBusy('catalog-refresh')" @click="refreshCatalog">
          刷新目录
        </NButton>
      </div>

      <NAlert v-if="catalogError" type="warning" :bordered="false" style="margin-bottom: 12px">
        {{ catalogError }}
      </NAlert>

      <div class="pc-catalog">
        <div v-for="k in catalog?.kernels ?? []" :key="k.kernel" class="pc-catalog-row">
          <div class="pc-catalog-head">
            <a :href="k.site_url" target="_blank" rel="noopener">{{ kernelLabel[k.kernel] ?? k.label }}</a>
            <span class="pc-catalog-count">{{ k.count }} 项</span>
            <span class="pc-catalog-state" :class="{ warn: !!k.error || k.from_cache }">
              {{ syncLabel(k) }}
            </span>
          </div>
          <div class="pc-catalog-names">
            <span v-for="p in k.protocols" :key="p.slug" class="pc-pname">{{ p.name }}</span>
          </div>
          <div v-if="k.error" class="pc-catalog-err">{{ k.error }}</div>
        </div>
        <div v-if="!catalog" class="hint">正在从内核官方文档加载协议目录…</div>
      </div>
    </section>

    <!-- ② 配置文件：卡片列表 -->
    <section class="pc-section">
      <div class="major-head">
        <span class="major-title">配置文件</span>
        <span class="major-sub">点击卡片编辑 · 内含协议内容 / 入站 / 出站 / 路由规则 / DNS</span>
      </div>

      <div class="pc-toolbar">
        <NInput
          v-model:value="search"
          size="small"
          placeholder="搜索配置文件"
          clearable
          style="width: 220px"
        />
        <NButton size="small" quaternary @click="checkedIds = visibleProfiles.map((p) => p.id)">
          全选
        </NButton>
        <NButton size="small" quaternary @click="checkedIds = []">清空选择</NButton>
        <span class="pc-selected">已选 {{ checkedIds.length }}</span>
        <div class="pc-toolbar-spacer" />
        <NButton size="small" @click="loadProfiles">刷新</NButton>
        <NButton size="small" type="primary" @click="openNew">+ 新建</NButton>
      </div>

      <div class="pc-toolbar pc-bulk">
        <span class="hint">批量启停（不同内核的配置文件可同时启用，各自对应自己的内核）：</span>
        <NButton
          size="small"
          :disabled="!checkedIds.length"
          :loading="isBusy('toggle:selected:true')"
          @click="applyToggle(checkedIds, false, true)"
        >启用选中</NButton>
        <NButton
          size="small"
          :disabled="!checkedIds.length"
          :loading="isBusy('toggle:selected:false')"
          @click="applyToggle(checkedIds, false, false)"
        >停用选中</NButton>
        <NButton
          size="small"
          :disabled="!profiles.length"
          :loading="isBusy('toggle:all:true')"
          @click="applyToggle([], true, true)"
        >全部启用</NButton>
        <NButton
          size="small"
          :disabled="!profiles.length"
          :loading="isBusy('toggle:all:false')"
          @click="applyToggle([], true, false)"
        >全部停用</NButton>
      </div>

      <div class="pc-cards">
        <div
          v-for="p in visibleProfiles"
          :key="p.id"
          class="pc-card"
          :class="{ active: p.id === activeId }"
          @click="openEditor(p.id)"
        >
          <div class="pc-card-check" @click.stop>
            <input
              type="checkbox"
              :checked="checkedIds.includes(p.id)"
              @change="toggleChecked(p.id, ($event.target as HTMLInputElement).checked)"
            />
          </div>
          <div class="pc-card-main">
            <div class="pc-card-title">
              <span class="pc-card-name">{{ p.name }}</span>
              <NTag v-if="p.default" size="small" :bordered="false" type="primary">默认</NTag>
              <NTag size="small" :bordered="false">{{ kernelOf(p)?.label ?? p.kernel }}</NTag>
              <NTag size="small" :bordered="false" :type="p.enabled ? 'success' : 'default'">
                {{ p.enabled ? '已启用' : '已停用' }}
              </NTag>
            </div>
            <div class="pc-card-desc">{{ p.description || '—' }}</div>
            <div class="pc-card-meta">
              {{ p.format.toUpperCase() }}
              <span class="pc-dot">·</span>
              {{ sectionCount(p) }} / {{ p.sections?.length ?? 0 }} 个分区
              <span class="pc-dot">·</span>
              更新 {{ formatTime(p.updated_at) }}
            </div>
          </div>
          <div class="pc-card-actions" @click.stop>
            <NButton size="tiny" :type="p.enabled ? 'default' : 'success'" quaternary
              @click="toggleOne(p, !p.enabled)">
              {{ p.enabled ? '停用' : '启用' }}
            </NButton>
            <NButton size="tiny" quaternary @click="openEditor(p.id)">编辑</NButton>
            <NButton size="tiny" quaternary :disabled="p.default" @click="activate(p)">设为默认</NButton>
            <NButton size="tiny" quaternary @click="duplicate(p)">复制</NButton>
            <NButton size="tiny" quaternary type="error" @click="remove(p)">删除</NButton>
          </div>
        </div>
        <div v-if="!visibleProfiles.length" class="pc-empty">
          {{ search ? '没有匹配的配置文件' : '还没有配置文件，点右上角「+ 新建」开始' }}
        </div>
      </div>

      <div class="pc-help">
        每个配置文件按目标内核的官方语法分成 6 个可编辑分区，顺序为：协议 → 入站 → 出站 → 路由规则 → DNS → 其他。
        分区内已写好带中文注释的模板，修改后先「校验语法」再「保存」；「编辑完整配置」可直接修改合并后的 YAML / JSON，
        编辑内容会自动同步回上方 6 个分区，两边始终保持一致。
      </div>
    </section>

    <!-- 新建弹窗 -->
    <NModal
      v-model:show="showNew"
      preset="card"
      title="新建配置文件"
      style="width: 460px"
      :bordered="false"
    >
      <div class="pc-form">
        <label>名称</label>
        <NInput v-model:value="newName" placeholder="例如 Home / Office / VPS" />
        <label>目标内核</label>
        <NSelect v-model:value="newKernel" :options="kernelSelectOptions" />
        <div class="hint">
          不同内核语法不同：Mihomo 用 YAML <span class="mono">proxies:</span>，
          sing-box / Xray-core 用 JSON <span class="mono">outbounds</span>。
          创建后会自动生成对应内核的带注释模板。
        </div>
        <label>备注（可选）</label>
        <NInput v-model:value="newDesc" placeholder="用途说明" />
      </div>
      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 8px">
          <NButton size="small" @click="showNew = false">取消</NButton>
          <NButton size="small" type="primary" :loading="isBusy('new') || isBusy('new-tpl')" @click="createNew">
            创建并编辑
          </NButton>
        </div>
      </template>
    </NModal>

    <!-- 编辑抽屉 -->
    <NDrawer v-model:show="showEditor" :width="880" placement="right">
      <NDrawerContent closable>
        <template #header>
          <div class="pc-editor-head">
            <span>编辑配置文件</span>
            <span class="major-sub">{{ editing?.name }}</span>
          </div>
        </template>

        <template v-if="editing">
          <div class="pc-editor-fields">
            <div>
              <label>名称</label>
              <NInput v-model:value="editing.name" size="small" />
            </div>
            <div>
              <label>目标内核</label>
              <NSelect v-model:value="editing.kernel" size="small" :options="kernelSelectOptions" @update:value="changeKernel" />
            </div>
            <div>
              <label>备注</label>
              <NInput v-model:value="editing.description" size="small" />
            </div>
            <div class="pc-editor-switch">
              <label>设为默认</label>
              <NSwitch v-model:value="editing.default" size="small" />
            </div>
          </div>

          <div class="pc-flow">
            <span>1 · 选内核</span><span>2 · 编辑下方分区</span><span>3 · 校验语法</span><span>4 · 保存</span>
          </div>

          <div class="proto-tabs pc-tabs">
            <button
              v-for="s in editing.sections"
              :key="s.key"
              class="proto-tab"
              :class="{ active: activeSection === s.key }"
              type="button"
              @click="activeSection = s.key"
            >
              {{ s.label }}
              <span v-if="String(s.content ?? '').trim()" class="tab-count">●</span>
            </button>
            <button class="proto-tab" type="button" @click="rebuildTemplate">重建模板</button>
          </div>

          <div class="pc-section-hint">
            <strong>{{ currentMeta?.label }}</strong>
            <span>{{ currentMeta?.hint }}</span>
            <a v-if="currentMeta?.doc_url" :href="currentMeta.doc_url" target="_blank" rel="noopener">官方文档 ↗</a>
          </div>

          <NInput
            :value="currentSection()"
            type="textarea"
            class="code-editor pc-code"
            :autosize="{ minRows: 16, maxRows: 28 }"
            spellcheck="false"
            @update:value="setCurrentSection"
          />

          <div class="pc-code-note">
            注释行（<span class="mono">#</span> / <span class="mono">//</span>）只用于说明，校验与保存时会自动剥离；
            落盘的是纯净配置。切换内核后请点「重建模板」，避免把 A 内核语法写进 B 内核。
          </div>

          <div v-if="editorNote" class="pc-editor-note">{{ editorNote }}</div>
        </template>

        <template #footer>
          <div class="pc-editor-footer">
            <NButton size="small" :loading="isBusy('validate')" @click="validateEditor">校验语法</NButton>
            <NButton size="small" :loading="isBusy('render')" @click="openFull">编辑完整配置</NButton>
            <div class="pc-toolbar-spacer" />
            <NButton size="small" type="primary" :loading="isBusy('save')" @click="saveEditor">保存</NButton>
          </div>
        </template>
      </NDrawerContent>
    </NDrawer>

    <!-- 完整配置：可编辑 + 自动同步回上方分区 -->
    <NModal
      :show="showFull"
      @update:show="onFullShow"
      preset="card"
      :title="`完整配置 · ${editing?.name ?? ''}`"
      style="width: 920px; max-width: calc(100vw - 48px)"
      :bordered="false"
    >
      <div class="pc-full-bar">
        <NTag size="small" :bordered="false">{{ fullFormat.toUpperCase() }}</NTag>
        <span class="pc-full-hint">由 6 个分区合并而成 · 可直接编辑整份配置</span>
        <div class="pc-toolbar-spacer" />
        <label class="pc-full-auto">
          <NSwitch v-model:value="fullAutoSync" size="small" @update:value="onAutoSyncChange" />
          <span>自动同步到分区</span>
        </label>
      </div>

      <NInput
        v-model:value="fullContent"
        type="textarea"
        class="code-editor code-editor-large pc-full-editor"
        :autosize="{ minRows: 18, maxRows: 26 }"
        spellcheck="false"
        placeholder="粘贴或编辑完整的内核配置"
      />

      <div class="pc-full-status" :class="fullSyncState">
        <span class="pc-full-dot">●</span>
        <strong>{{ statusTitle }}</strong>
        <span>{{ fullSyncMsg }}</span>
      </div>

      <div class="pc-full-sections">
        <span
          v-for="s in fullSectionStats"
          :key="s.key"
          class="pc-full-chip"
          :class="{ changed: s.changed }"
        >
          {{ s.label }} · {{ s.lines }} 行
        </span>
      </div>

      <NAlert v-if="fullWarnings.length" type="warning" :bordered="false" style="margin-top: 10px">
        <div v-for="w in fullWarnings" :key="w">{{ w }}</div>
      </NAlert>

      <div class="hint" style="margin-top: 10px">
        修改后停止输入约 0.6 秒即自动拆分回上方分区；语法错误时保留原分区并在上方红字提示。
        JSON 内核（sing-box / Xray-core）的 <span class="mono">outbounds</span> 会按「协议内容 / 出站」重新分组，
        分段里的说明注释不会保留；未识别的顶层键统一放入「其他」，不会丢失。
      </div>

      <template #footer>
        <div class="pc-full-footer">
          <NButton size="small" @click="copyText(fullContent)">复制</NButton>
          <NButton size="small" @click="downloadFull">下载</NButton>
          <NButton size="small" :loading="fullSyncState === 'syncing'" @click="syncFull(true)">
            立即同步到分区
          </NButton>
          <div class="pc-toolbar-spacer" />
          <NButton size="small" @click="closeFull">关闭</NButton>
          <NButton size="small" type="primary" :loading="isBusy('save')" @click="saveFullAndClose">
            保存并关闭
          </NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.pc-page {
  display: flex;
  flex-direction: column;
  gap: 28px;
  /* 内容不要顶到最左侧：与页面其它视图保持一致的水平留白 */
  padding: 4px 26px 28px 26px;
}
.pc-section { min-width: 0; }
.pc-sync-bar {
  display: flex; align-items: center; justify-content: space-between;
  gap: 16px; flex-wrap: wrap; padding: 11px 14px; margin: 12px 0 14px;
  border: 1px solid #e6e6eb; border-radius: 10px; background: #fafafb;
}
.pc-sync-info { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.pc-sync-line { font-size: 12.5px; color: #3a3a3c; }
.pc-sync-line strong { color: #1d1d1f; font-weight: 600; }
.pc-sync-msg { font-size: 11.5px; color: #8e8e93; }
.pc-dot { margin: 0 6px; color: #c7c7cc; }

.pc-catalog { display: flex; flex-direction: column; gap: 10px; }
.pc-catalog-row { padding: 12px 14px; border: 1px solid #e6e6eb; border-radius: 10px; background: #fff; }
.pc-catalog-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; margin-bottom: 8px; }
.pc-catalog-head a { font-size: 13px; font-weight: 600; color: #1d1d1f; text-decoration: none; }
.pc-catalog-head a:hover { color: #0066cc; }
.pc-catalog-count { font-size: 11px; color: #8e8e93; }
.pc-catalog-state { font-size: 11px; color: #147a51; }
.pc-catalog-state.warn { color: #a06a10; }
.pc-catalog-names { display: flex; flex-wrap: wrap; gap: 4px 14px; line-height: 1.85; }
.pc-pname {
  font-size: 12.5px; color: #3a3a3c;
  font-family: 'SF Mono Nerd Font', 'PingFang SC', 'Microsoft YaHei', monospace;
  font-feature-settings: 'calt' 0, 'liga' 0;
}
.pc-catalog-err { margin-top: 6px; font-size: 11px; color: #a06a10; }

.pc-toolbar { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin: 12px 0; }
.pc-toolbar-spacer { flex: 1; }
.pc-selected { font-size: 12px; color: #6e6e73; white-space: nowrap; }
.pc-bulk {
  padding: 10px 12px;
  border: 1px solid #e6e6eb;
  border-radius: 10px;
  background: #fafafb;
}
.pc-card-check { flex: 0 0 auto; display: flex; align-items: center; }
.pc-card-check input {
  width: 15px; height: 15px; cursor: pointer; accent-color: #0066cc;
}
.pc-cards { display: flex; flex-direction: column; gap: 10px; }
.pc-card {
  display: flex; align-items: center; justify-content: space-between; gap: 16px; flex-wrap: wrap;
  padding: 14px 16px; border: 1px solid #e6e6eb; border-radius: 12px; background: #fff;
  cursor: pointer; transition: border-color 0.15s ease, box-shadow 0.15s ease;
}
.pc-card:hover { border-color: #c7c7cc; box-shadow: 0 2px 10px rgba(0, 0, 0, 0.04); }
.pc-card.active { border-color: rgba(0, 102, 204, 0.45); }
.pc-card-main { min-width: 0; flex: 1; }
.pc-card-title { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.pc-card-name { font-size: 14px; font-weight: 600; color: #1d1d1f; }
.pc-card-desc { font-size: 12px; color: #6e6e73; margin-top: 5px; }
.pc-card-meta { font-size: 11px; color: #8e8e93; margin-top: 6px; }
.pc-card-actions { display: flex; gap: 2px; flex-wrap: wrap; }
.pc-empty { padding: 32px; text-align: center; color: #8e8e93; font-size: 12.5px; border: 1px dashed #dcdce1; border-radius: 12px; }
.pc-help { margin-top: 12px; font-size: 11.5px; color: #8e8e93; line-height: 1.8; }

.pc-form { display: flex; flex-direction: column; gap: 8px; }
.pc-form label, .pc-editor-fields label { display: block; font-size: 11.5px; font-weight: 600; color: #6e6e73; margin-top: 4px; }
.pc-editor-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; }
.pc-editor-fields { display: grid; grid-template-columns: 1fr 1fr; gap: 8px 14px; margin-bottom: 12px; }
.pc-editor-switch { display: flex; align-items: center; gap: 10px; }
.pc-editor-switch label { margin: 0; }
.pc-flow { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 12px; }
.pc-flow span { font-size: 11px; color: #6e6e73; padding: 3px 9px; border: 1px solid #e6e6eb; border-radius: 20px; background: #fafafb; }
.pc-tabs { margin-bottom: 12px; }
.pc-section-hint { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; font-size: 12px; color: #6e6e73; line-height: 1.7; margin-bottom: 8px; }
.pc-section-hint strong { color: #1d1d1f; }
.pc-section-hint a { color: #0066cc; text-decoration: none; }
.pc-code { width: 100%; }
.pc-code-note, .pc-editor-note { margin-top: 10px; font-size: 11.5px; color: #8e8e93; line-height: 1.8; }
.pc-editor-note { color: #147a51; }
.pc-editor-footer { display: flex; align-items: center; gap: 8px; }
.pc-full-bar { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-bottom: 10px; }
.pc-full-hint { font-size: 11.5px; color: #6e6e73; }
.pc-full-auto { display: flex; align-items: center; gap: 8px; font-size: 12px; color: #3a3a3c; cursor: pointer; }
.pc-full-editor { width: 100%; }
.pc-full-editor :deep(.n-input__textarea-el) { min-height: 360px; }
.pc-full-status {
  display: flex; align-items: baseline; gap: 8px; flex-wrap: wrap;
  margin-top: 10px; font-size: 12px; color: #6e6e73; line-height: 1.7;
}
.pc-full-status strong { color: #1d1d1f; font-weight: 600; }
.pc-full-dot { font-size: 10px; color: #8e8e93; }
.pc-full-status.synced .pc-full-dot, .pc-full-status.synced strong { color: #147a51; }
.pc-full-status.error .pc-full-dot, .pc-full-status.error strong { color: #c0392b; }
.pc-full-status.syncing .pc-full-dot, .pc-full-status.syncing strong { color: #0066cc; }
.pc-full-status.dirty .pc-full-dot, .pc-full-status.dirty strong { color: #a06a10; }
.pc-full-sections { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 10px; }
.pc-full-chip {
  font-size: 11px; color: #6e6e73; padding: 3px 10px;
  border: 1px solid #e6e6eb; border-radius: 20px; background: #fafafb;
}
.pc-full-chip.changed {
  color: #0066cc; border-color: rgba(0, 102, 204, 0.35); background: rgba(0, 102, 204, 0.06);
}
.pc-full-footer { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
</style>
