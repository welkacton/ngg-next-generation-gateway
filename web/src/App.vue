<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  zhCN,
  dateZhCN,
  NConfigProvider,
  NGlobalStyle,
  NLayout,
  NLayoutSider,
  NLayoutHeader,
  NLayoutContent,
  NMenu,
  NButton,
  NAlert,
  type MenuOption,
} from 'naive-ui'
import { themeOverrides } from './styles/theme'
import { useCoresStore } from './stores/cores'
import { useWebSocket } from './composables/useWebSocket'
import { getToken } from './api/client'
import * as api from './api/ngg'
import type {
  UpdateCheckResult,
  UpdateStatus,
  UpdateApplyResult,
} from './api/ngg'
import UpdatePanel from './components/UpdatePanel.vue'

const route = useRoute()
const router = useRouter()
const store = useCoresStore()

function menuIcon() {
  return h('span', { class: 'menu-dot' })
}

// 一级大分类是可折叠子菜单；「后端管理」默认收缩（不放入默认展开列表）。
const MENU_EXPANDED_KEY = 'ngg_menu_expanded'
const AUTO_CHECK_KEY = 'ngg_auto_update_check'
const defaultExpanded = ['group-core-engine', 'group-config-engine']

function loadExpandedKeys(): string[] {
  try {
    const raw = localStorage.getItem(MENU_EXPANDED_KEY)
    if (raw) {
      const parsed = JSON.parse(raw)
      if (Array.isArray(parsed)) return parsed.map(String)
    }
  } catch {
    // 损坏的本地偏好回退到默认展开，不写入任何模拟状态
  }
  return [...defaultExpanded]
}

const expandedKeys = ref<string[]>(loadExpandedKeys())

function handleExpandedKeys(keys: (string | number)[]) {
  expandedKeys.value = keys.map(String)
  try {
    localStorage.setItem(MENU_EXPANDED_KEY, JSON.stringify(expandedKeys.value))
  } catch {
    // 隐私模式下写不进去也不影响菜单功能
  }
}

const menuOptions: MenuOption[] = [
  { label: '系统概览', key: 'dashboard', icon: menuIcon },
  { label: '网络概览', key: 'network-overview', icon: menuIcon },
  { type: 'divider', key: 'divider-top' },
  {
    label: '内核引擎',
    key: 'group-core-engine',
    icon: menuIcon,
    children: [
      { label: '内核管理', key: 'cores', icon: menuIcon },
      { label: '配置管理', key: 'config', icon: menuIcon },
      { label: '实时日志', key: 'logs', icon: menuIcon },
      { label: '版本管理', key: 'versions', icon: menuIcon },
      { label: '系统设置', key: 'settings', icon: menuIcon },
    ],
  },
  {
    label: '配置引擎',
    key: 'group-config-engine',
    icon: menuIcon,
    children: [
      { label: '协议配置', key: 'protocols', icon: menuIcon },
      { label: '规则配置', key: 'rules', icon: menuIcon },
      { label: 'DNS配置', key: 'dns', icon: menuIcon },
      { label: '入口节点', key: 'inbound', icon: menuIcon },
      { label: '出口节点', key: 'outbound', icon: menuIcon },
    ],
  },
  { type: 'divider', key: 'divider-backend' },
  {
    label: '后端管理',
    key: 'group-backend-management',
    icon: menuIcon,
    children: [
      { label: '网络引擎', key: 'network', icon: menuIcon },
      { label: '后量子', key: 'pq', icon: menuIcon },
      { label: '插件运行时', key: 'plugins', icon: menuIcon },
      { label: 'SD-WAN', key: 'sdwan', icon: menuIcon },
      { label: '多租户', key: 'tenants', icon: menuIcon },
      { label: '跨平台构建', key: 'platforms', icon: menuIcon },
      { label: '阶段总览', key: 'stages', icon: menuIcon },
    ],
  },
]

const activeKey = computed(() => String(route.name || 'dashboard'))
const pageTitle = computed(() => String(route.meta.title || '统一管理中心'))

function handleMenu(key: string | number) {
  router.push({ name: String(key) })
}

function refreshAll() {
  store.forceRefresh()
}

// composable 在挂载时自动连接，并监听 ngg-token-change 事件重连
const { connected } = useWebSocket((event) => {
  store.applyEvent(event)
})

watch(connected, (v) => store.setWsConnected(v))

const hasToken = ref(!!getToken())

function syncToken(): void {
  hasToken.value = !!getToken()
}

/* ---- 版本代际与仓库更新栏 ---- */

const AUTO_APPLY_KEY = 'ngg_auto_update_apply'
const updateStatus = ref<UpdateStatus | null>(null)
const checkResult = ref<UpdateCheckResult | null>(null)
const showUpdatePanel = ref(false)
const autoCheck = ref(true)
const autoApply = ref(false)
const autoApplyResult = ref<UpdateApplyResult | null>(null)
let autoCheckTimer: number | undefined
let autoApplyInFlight = false

function loadAutoCheckPref(): boolean {
  try {
    return localStorage.getItem(AUTO_CHECK_KEY) !== '0'
  } catch {
    return true
  }
}

function loadAutoApplyPref(): boolean {
  try {
    return localStorage.getItem(AUTO_APPLY_KEY) === '1'
  } catch {
    return false
  }
}

function persist(key: string, v: boolean) {
  try {
    localStorage.setItem(key, v ? '1' : '0')
  } catch {
    // ignore
  }
}

function setAutoCheck(v: boolean) {
  autoCheck.value = v
  persist(AUTO_CHECK_KEY, v)
}

function setAutoApply(v: boolean) {
  if (v) {
    window.$dialog?.warning({
      title: '开启自动应用更新',
      content: '开启后，界面检查到新版本会自动下载、校验并替换程序文件、重启服务（失败自动回滚）。期间管理界面会短暂中断。建议仅在测试环境开启。',
      positiveText: '我已了解，开启',
      negativeText: '取消',
      onPositiveClick: () => {
        autoApply.value = true
        persist(AUTO_APPLY_KEY, true)
      },
    })
    return
  }
  autoApply.value = false
  persist(AUTO_APPLY_KEY, false)
}

// 自动应用：真实走 download（含 SHA256 校验）→ apply，失败不伪装成功。
async function maybeAutoApply() {
  const r = checkResult.value
  if (!autoApply.value || autoApplyInFlight || !r?.update_available || !r.release || !r.asset) return
  autoApplyInFlight = true
  const target = { tag: r.release.tag_name, asset: r.asset.name }
  try {
    await api.downloadUpdate(target)
    const applied = await api.applyUpdate(target)
    autoApplyResult.value = applied
    if (applied.started) window.$message?.info('已自动开始应用更新，约 30 秒后刷新页面')
    else window.$message?.warning('自动应用在当前平台需执行终端命令，详见更新面板')
  } catch {
    autoApplyResult.value = null
    window.$message?.warning('自动更新失败（下载校验或应用脚本未就绪），可在更新面板手动重试')
  } finally {
    autoApplyInFlight = false
  }
}

const needsGithubToken = ref(false)

async function silentCheck() {
  try {
    checkResult.value = await api.checkUpdate()
    if (checkResult.value?.status) updateStatus.value = checkResult.value.status
    needsGithubToken.value = false
    await maybeAutoApply()
  } catch (e) {
    // 检查失败不弹提示，也不伪造「已是最新」状态；私有仓库缺 Token 时
    // 只在顶栏给出一个温和的可操作提示，点开面板即可填写。
    const msg = e instanceof Error ? e.message : String(e)
    needsGithubToken.value = /404|401|403|GitHub Token|private repo/i.test(msg)
  }
}

const pillLabel = computed(() => {
  const s = updateStatus.value
  if (!s) return '版本'
  return `${s.generation_label} · v${s.release}`
})
const hasUpdate = computed(() => !!checkResult.value?.update_available)

function onChecked(v: UpdateCheckResult | null) {
  checkResult.value = v
  if (v?.status) updateStatus.value = v.status
  void maybeAutoApply()
}

onMounted(() => {
  store.ensureBase()
  window.addEventListener('ngg-token-change', syncToken)
  autoCheck.value = loadAutoCheckPref()
  autoApply.value = loadAutoApplyPref()
  api.getUpdateStatus().then((s) => { updateStatus.value = s }).catch(() => {})
  if (autoCheck.value && hasToken.value) silentCheck()
  autoCheckTimer = window.setInterval(() => {
    if (autoCheck.value && hasToken.value) silentCheck()
  }, 6 * 60 * 60 * 1000)
})

onUnmounted(() => {
  window.removeEventListener('ngg-token-change', syncToken)
  if (autoCheckTimer) window.clearInterval(autoCheckTimer)
})
</script>

<template>
  <NConfigProvider
    :theme-overrides="themeOverrides"
    :locale="zhCN"
    :date-locale="dateZhCN"
  >
    <NGlobalStyle />
    <NLayout has-sider style="height: 100vh">
      <NLayoutSider
        class="app-sider"
        bordered
        :width="236"
        :native-scrollbar="false"
        content-style="height: 100%;"
      >
        <div class="sider-inner">
          <div class="brand">
            <div class="brand-logo">N</div>
            <div>
              <div class="brand-title">NGG 管理中心</div>
              <div class="brand-sub">Next-Generation Gateway</div>
            </div>
          </div>
          <div class="sider-menu-scroll">
            <NMenu
              :options="menuOptions"
              :value="activeKey"
              :expanded-keys="expandedKeys"
              :root-indent="22"
              :indent="18"
              @update:value="handleMenu"
              @update:expanded-keys="handleExpandedKeys"
            />
          </div>
          <div class="sider-footer">
            NGG · 统一网络引擎<br />
            多内核协议运行时 / 统一出入口调度
          </div>
        </div>
      </NLayoutSider>

      <NLayout>
        <NLayoutHeader class="app-header" bordered>
          <div class="app-header-title">{{ pageTitle }}</div>
          <div class="header-actions">
            <span class="gen-badge">
              <strong>{{ pillLabel }}</strong>
              <span>{{ updateStatus?.generation_name || '第二代' }}</span>
            </span>
            <button
              class="update-pill"
              :class="{ 'has-update': hasUpdate, 'needs-token': needsGithubToken && !hasUpdate }"
              type="button"
              @click="showUpdatePanel = true"
            >
              <span v-if="hasUpdate || needsGithubToken" class="update-dot" />
              {{ hasUpdate ? '有新版本' : needsGithubToken ? '需要 GitHub Token' : '系统更新' }}
            </button>
            <span class="header-status" :title="connected ? 'WebSocket 已连接' : 'WebSocket 未连接'">
              <span :class="['ws-dot', connected ? 'ws-on' : 'ws-off']" />
              {{ connected ? '已连接' : '未连接' }}
            </span>
            <span class="header-status">
              运行中 {{ store.runningCount }} / {{ store.cores.length }}
            </span>
            <NButton size="small" tertiary :loading="store.baseLoading" @click="refreshAll">
              刷新
            </NButton>
          </div>
        </NLayoutHeader>

        <NLayoutContent :native-scrollbar="false" content-style="min-height: calc(100vh - 60px);">
          <NAlert
            v-if="!hasToken"
            type="warning"
            :bordered="false"
            style="margin: 16px 24px 0"
          >
            <template #header>尚未配置 API Token</template>
            请前往「系统设置」填写后端默认 Token 后刷新数据。未配置时所有接口请求都会返回认证错误，
            页面不会展示任何模拟数据。
          </NAlert>
          <router-view />
        </NLayoutContent>
      </NLayout>
    </NLayout>

    <UpdatePanel
      v-model:show="showUpdatePanel"
      :auto-check="autoCheck"
      :auto-apply="autoApply"
      :auto-apply-result="autoApplyResult"
      :initial-result="checkResult"
      @auto-check-change="setAutoCheck"
      @auto-apply-change="setAutoApply"
      @checked="onChecked"
    />
  </NConfigProvider>
</template>

<style scoped>
.menu-dot :deep(),
:deep(.menu-dot) {
  display: inline-block;
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: #c7c7cc;
  margin-right: 4px;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.header-status {
  display: inline-flex;
  align-items: center;
  font-size: 12px;
  color: #8e8e93;
  white-space: nowrap;
}
</style>
