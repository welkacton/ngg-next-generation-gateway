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
  NModal,
  NInput,
  NAlert,
  type MenuOption,
} from 'naive-ui'
import { themeOverrides } from './styles/theme'
import { useCoresStore } from './stores/cores'
import { useWebSocket } from './composables/useWebSocket'
import * as api from './api/ngg'
import { setMaintenance } from './api/client'
import type {
  AuthState,
  UpdateCheckResult,
  UpdateStatus,
  UpdateApplyResult,
} from './api/ngg'
import UpdatePanel from './components/UpdatePanel.vue'
import Login from './views/Login.vue'

const route = useRoute()
const router = useRouter()
const store = useCoresStore()

/* ---------------------------------------------------------------- 登录门禁 --- */

const authReady = ref(false)
const authState = ref<AuthState | null>(null)
const authed = ref(false)

async function refreshAuthState() {
  try {
    authState.value = await api.getAuthState()
    authed.value = !!authState.value.authenticated
  } catch {
    authed.value = false
  } finally {
    authReady.value = true
  }
}

function onAuthenticated(username: string) {
  authed.value = true
  if (authState.value) {
    authState.value.authenticated = true
    authState.value.username = username
    authState.value.setup_required = false
  }
  boot()
}

async function doLogout() {
  try {
    await api.authLogout()
  } catch {
    // 会话可能已过期，仍然回到登录页
  }
  stopWs()
  authed.value = false
  showAccount.value = false
  if (authState.value) {
    authState.value.authenticated = false
    authState.value.username = ''
  }
}

// 会话过期：任何 401 都会把界面切回登录页，而不是停留在一屏红色报错上。
function onUnauthorized() {
  if (authed.value) {
    authed.value = false
    showAccount.value = false
    stopWs()
  }
}

/* ------------------------------------------------------------ 账号与改密 --- */

const showAccount = ref(false)
const oldPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const pwdBusy = ref(false)
const pwdError = ref('')

async function submitPassword() {
  pwdError.value = ''
  if (newPassword.value.length < 8) {
    pwdError.value = '新密码至少 8 位'
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    pwdError.value = '两次输入的新密码不一致'
    return
  }
  pwdBusy.value = true
  try {
    await api.changePassword(oldPassword.value, newPassword.value)
    oldPassword.value = ''
    newPassword.value = ''
    confirmPassword.value = ''
    showAccount.value = false
    window.$message?.success('密码已更新，请用新密码重新登录')
    await doLogout()
  } catch (e) {
    pwdError.value = e instanceof Error ? e.message : String(e)
  } finally {
    pwdBusy.value = false
  }
}

/* ---------------------------------------------------------------- 菜单 --- */

function menuIcon() {
  return h('span', { class: 'menu-dot' })
}

const MENU_EXPANDED_KEY = 'ngg_menu_expanded'
const AUTO_CHECK_KEY = 'ngg_auto_update_check'
const defaultExpanded = ['group-gateway', 'group-core-engine', 'group-config-engine']

function loadExpandedKeys(): string[] {
  try {
    const raw = localStorage.getItem(MENU_EXPANDED_KEY)
    if (raw) {
      const parsed = JSON.parse(raw)
      if (Array.isArray(parsed)) {
        const keys = parsed.map(String)
        // v2.6.5 added a first-level group; expand it once for existing users.
        if (!keys.includes('group-gateway')) keys.push('group-gateway')
        return keys
      }
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
  {
    label: '网关中心',
    key: 'group-gateway',
    icon: menuIcon,
    children: [
      { label: 'WAN 设置', key: 'gateway-wan', icon: menuIcon },
      { label: 'LAN 设置', key: 'gateway-lan', icon: menuIcon },
      { label: 'DNS 设置', key: 'gateway-dns', icon: menuIcon },
      { label: 'SD-WAN', key: 'gateway-sdwan', icon: menuIcon },
      { label: '防火墙设置', key: 'gateway-firewall', icon: menuIcon },
    ],
  },
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
      { label: '链路编排', key: 'pipeline', icon: menuIcon },
      { label: '协议配置', key: 'protocols', icon: menuIcon },
      { label: '规则配置', key: 'rules', icon: menuIcon },
      { label: 'DNS配置', key: 'dns', icon: menuIcon },
      { label: '接管全局', key: 'network', icon: menuIcon },
      { label: '入口节点', key: 'inbound', icon: menuIcon },
      { label: '出口节点', key: 'outbound', icon: menuIcon },
      { label: '用户管理', key: 'users', icon: menuIcon },
    ],
  },
  { type: 'divider', key: 'divider-backend' },
  {
    label: '后端管理',
    key: 'group-backend-management',
    icon: menuIcon,
    children: [
      { label: '后量子', key: 'pq', icon: menuIcon },
      { label: '插件运行时', key: 'plugins', icon: menuIcon },
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

const { connected, start: startWs, stop: stopWs } = useWebSocket((event) => {
  store.applyEvent(event)
})

watch(connected, (v) => store.setWsConnected(v))

/* ------------------------------------------------------ 版本代际与更新栏 --- */

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

async function maybeAutoApply() {
  const r = checkResult.value
  if (!autoApply.value || autoApplyInFlight || !r?.update_available || !r.release || !r.asset) return
  autoApplyInFlight = true
  const target = { tag: r.release.tag_name, asset: r.asset.name }
  try {
    await api.downloadUpdate(target)
    const applied = await api.applyUpdate(target)
    autoApplyResult.value = applied
    if (applied.started) {
      // The service is about to restart: silence transport errors until reload.
      setMaintenance(true)
      window.$message?.info('已自动开始应用更新，约 30 秒后刷新页面')
      window.setTimeout(() => window.location.reload(), 30000)
    } else {
      window.$message?.warning('自动应用在当前平台需执行终端命令，详见更新面板')
    }
  } catch {
    autoApplyResult.value = null
    window.$message?.warning('自动更新失败（下载校验或应用脚本未就绪），可在更新面板手动重试')
  } finally {
    autoApplyInFlight = false
  }
}

const updateNeedsNetwork = ref(false)

async function silentCheck() {
  try {
    checkResult.value = await api.checkUpdate()
    if (checkResult.value?.status) updateStatus.value = checkResult.value.status
    updateNeedsNetwork.value = false
    await maybeAutoApply()
  } catch (e) {
    // 检查失败不弹提示，也不伪造「已是最新」状态；只在顶栏给出温和提示。
    const msg = e instanceof Error ? e.message : String(e)
    updateNeedsNetwork.value = /404|401|403|network|dial|timeout/i.test(msg)
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

/* ---------------------------------------------------------------- 生命周期 --- */

function boot() {
  store.ensureBase()
  startWs()
  autoCheck.value = loadAutoCheckPref()
  autoApply.value = loadAutoApplyPref()
  api.getUpdateStatus().then((s) => { updateStatus.value = s }).catch(() => {})
  if (autoCheck.value) silentCheck()
  if (autoCheckTimer) window.clearInterval(autoCheckTimer)
  autoCheckTimer = window.setInterval(() => {
    if (autoCheck.value) silentCheck()
  }, 6 * 60 * 60 * 1000)
}

onMounted(async () => {
  window.addEventListener('ngg-unauthorized', onUnauthorized)
  await refreshAuthState()
  if (authed.value) boot()
})

onUnmounted(() => {
  window.removeEventListener('ngg-unauthorized', onUnauthorized)
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

    <!-- 未登录：整屏只渲染登录 / 首次创建管理员，管理台不挂载 -->
    <Login v-if="authReady && !authed" @authenticated="onAuthenticated" />

    <NLayout v-else-if="authed" has-sider style="height: 100vh">
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
              :class="{ 'has-update': hasUpdate, 'needs-token': updateNeedsNetwork && !hasUpdate }"
              type="button"
              @click="showUpdatePanel = true"
            >
              <span v-if="hasUpdate || updateNeedsNetwork" class="update-dot" />
              {{ hasUpdate ? '有新版本' : updateNeedsNetwork ? '更新检查失败' : '系统更新' }}
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
            <button class="account-pill" type="button" @click="showAccount = true">
              <span class="account-avatar">{{ (authState?.username || 'A').slice(0, 1).toUpperCase() }}</span>
              {{ authState?.username || '管理员' }}
            </button>
          </div>
        </NLayoutHeader>

        <NLayoutContent :native-scrollbar="false" content-style="min-height: calc(100vh - 60px);">
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

    <NModal
      v-model:show="showAccount"
      preset="card"
      title="账号与安全"
      style="max-width: 460px"
      :bordered="false"
    >
      <div class="account-body">
        <div class="account-row">
          <span class="account-label">当前账号</span>
          <span class="account-value mono">{{ authState?.username || '—' }}</span>
        </div>
        <div class="account-row">
          <span class="account-label">会话时长</span>
          <span class="account-value">
            {{ authState?.policy ? `${Math.round(authState.policy.session_hours / 24)} 天（记住我 ${Math.round(authState.policy.remember_hours / 24)} 天）` : '—' }}
          </span>
        </div>

        <div class="account-divider">修改密码</div>
        <NInput v-model:value="oldPassword" type="password" show-password-on="click" placeholder="当前密码" />
        <NInput v-model:value="newPassword" type="password" show-password-on="click" placeholder="新密码（至少 8 位）" />
        <NInput v-model:value="confirmPassword" type="password" show-password-on="click" placeholder="确认新密码" />
        <NAlert v-if="pwdError" type="error" :bordered="false">{{ pwdError }}</NAlert>
        <NButton type="primary" block :loading="pwdBusy" @click="submitPassword">
          更新密码并重新登录
        </NButton>
        <div class="account-hint">修改密码会让所有已登录的浏览器会话立即失效。</div>

        <div class="account-divider">会话</div>
        <NButton block tertiary @click="doLogout">退出登录</NButton>
      </div>
    </NModal>
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

.account-pill {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 30px;
  padding: 0 12px 0 5px;
  border-radius: 16px;
  border: 1px solid #e5e5ea;
  background: #fff;
  color: #1d1d1f;
  font-size: 12px;
  cursor: pointer;
  transition: background 0.18s ease, border-color 0.18s ease;
}
.account-pill:hover {
  background: #f5f5f7;
  border-color: #d2d2d7;
}
.account-avatar {
  width: 22px;
  height: 22px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: linear-gradient(140deg, #0071e3, #4aa3ff);
  color: #fff;
  font-size: 11px;
  font-weight: 600;
}

.account-body {
  display: grid;
  gap: 10px;
}
.account-row {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
}
.account-label {
  color: #8e8e93;
}
.account-value {
  color: #1d1d1f;
}
.account-divider {
  margin-top: 6px;
  font-size: 12px;
  font-weight: 600;
  color: #6e6e73;
  letter-spacing: 0.04em;
}
.account-hint {
  font-size: 11.5px;
  color: #8e8e93;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
</style>
