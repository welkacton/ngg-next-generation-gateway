<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  darkTheme,
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
  NSpace,
  NTag,
  NAlert,
  type MenuOption,
} from 'naive-ui'
import { themeOverrides } from './styles/theme'
import { useCoresStore } from './stores/cores'
import { useWebSocket } from './composables/useWebSocket'
import { getToken } from './api/client'

const route = useRoute()
const router = useRouter()
const store = useCoresStore()

function menuIcon() {
  return h('span', { class: 'menu-dot' })
}

const menuOptions: MenuOption[] = [
  { label: '概览', key: 'dashboard', icon: menuIcon },
  { label: '内核管理', key: 'cores', icon: menuIcon },
  { label: '配置管理', key: 'config', icon: menuIcon },
  { label: '实时日志', key: 'logs', icon: menuIcon },
  { label: '版本管理', key: 'versions', icon: menuIcon },
  { label: '系统设置', key: 'settings', icon: menuIcon },
  {
    type: 'group',
    label: 'Stage 2 ~ 5 能力',
    key: 'stage-group',
    children: [
      { label: 'Stage 2 网络引擎', key: 'network', icon: menuIcon },
      { label: 'Stage 3 规则与 DNS', key: 'rules', icon: menuIcon },
      { label: 'Stage 4 协议中心', key: 'protocols', icon: menuIcon },
      { label: '后量子', key: 'pq', icon: menuIcon },
      { label: '插件', key: 'plugins', icon: menuIcon },
      { label: 'Stage 5 SD-WAN', key: 'sdwan', icon: menuIcon },
      { label: '多租户', key: 'tenants', icon: menuIcon },
      { label: '跨平台', key: 'platforms', icon: menuIcon },
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

onMounted(() => {
  store.ensureBase()
  window.addEventListener('ngg-token-change', syncToken)
})

onUnmounted(() => {
  window.removeEventListener('ngg-token-change', syncToken)
})
</script>

<template>
  <NConfigProvider
    :theme="darkTheme"
    :theme-overrides="themeOverrides"
    :locale="zhCN"
    :date-locale="dateZhCN"
  >
    <NGlobalStyle />
    <NLayout has-sider style="height: 100vh">
      <NLayoutSider
        bordered
        :width="216"
        :native-scrollbar="false"
        content-style="position: relative; min-height: 100%;"
      >
        <div class="brand">
          <div class="brand-logo">N</div>
          <div>
            <div class="brand-title">NGG 管理中心</div>
            <div class="brand-sub">Next-Generation Gateway</div>
          </div>
        </div>
        <NMenu
          :options="menuOptions"
          :value="activeKey"
          :root-indent="22"
          :indent="18"
          @update:value="handleMenu"
        />
        <div class="sider-footer">
          Phase 1 · 统一管理中心 + 内核引擎<br />
          Stage 2~5 · 网络 / 规则 / 协议 / PQ / 插件 / SD-WAN
        </div>
      </NLayoutSider>

      <NLayout>
        <NLayoutHeader class="app-header" bordered>
          <div class="app-header-title">{{ pageTitle }}</div>
          <NSpace align="center" :size="12">
            <NTag :bordered="false" size="small" :type="connected ? 'success' : 'error'">
              <template #icon>
                <span :class="['ws-dot', connected ? 'ws-on' : 'ws-off']" />
              </template>
              {{ connected ? 'WebSocket 已连接' : 'WebSocket 未连接' }}
            </NTag>
            <NTag :bordered="false" size="small" type="info">
              运行中 {{ store.runningCount }} / {{ store.cores.length }}
            </NTag>
            <NButton size="small" tertiary :loading="store.baseLoading" @click="refreshAll">
              刷新数据
            </NButton>
          </NSpace>
        </NLayoutHeader>

        <NLayoutContent :native-scrollbar="false" content-style="min-height: calc(100vh - 56px);">
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
  </NConfigProvider>
</template>

<style scoped>
.menu-dot :deep(),
:deep(.menu-dot) {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #36adcb;
  opacity: 0.7;
  margin-right: 4px;
}
</style>
