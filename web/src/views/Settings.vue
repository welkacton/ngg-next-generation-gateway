<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NDescriptions,
  NDescriptionsItem,
  NSpace,
  NTag,
} from 'naive-ui'
import { useCoresStore } from '../stores/cores'
import { API_BASE } from '../api/client'
import type { AuthState, CoreDescriptor, PortAllocation } from '../api/ngg'
import * as api from '../api/ngg'

const store = useCoresStore()
const backendTarget = 'http://127.0.0.1:9090'
const origin = window.location.origin
const authState = ref<AuthState | null>(null)

onMounted(() => {
  api.getAuthState().then((s) => { authState.value = s }).catch(() => {})
})

function portRowKey(row: any): string {
  return String(row.core_id)
}

function registryRowKey(row: any): string {
  return String(row.id)
}

const portColumns: any[] = [
  {
    title: '内核',
    key: 'core_id',
    minWidth: 160,
    render: (row: PortAllocation) => h('span', { class: 'mono' }, row.core_id),
  },
  {
    title: '端口',
    key: 'ports',
    minWidth: 200,
    render: (row: PortAllocation) =>
      row.ports && row.ports.length ? row.ports.join(', ') : '—',
  },
  {
    title: '端口用途（names）',
    key: 'names',
    minWidth: 280,
    render: (row: PortAllocation) => {
      const entries = Object.entries(row.names || {})
      if (!entries.length) return h('span', { class: 'hint' }, '—')
      return h(
        'div',
        { class: 'cap-tags' },
        entries.map(([port, name]) =>
          h(NTag, { size: 'small', bordered: false }, {
            default: () => `${port} → ${name}`,
          }),
        ),
      )
    },
  },
]

const registryColumns: any[] = [
  {
    title: 'ID',
    key: 'id',
    minWidth: 130,
    render: (row: CoreDescriptor) => h('span', { class: 'mono' }, row.id),
  },
  { title: '名称', key: 'display_name', minWidth: 140 },
  { title: '说明', key: 'description', minWidth: 220 },
  {
    title: '来源',
    key: 'builtin',
    width: 120,
    render: (row: CoreDescriptor) =>
      h(
              { size: 'small', bordered: false, type: row.builtin ? 'info' : 'default' },
        { default: () => (row.builtin ? '内置' : '描述文件') },
      ),
  },
  {
    title: '描述来源',
    key: 'source',
    minWidth: 160,
    render: (row: CoreDescriptor) => h('span', { class: 'mono' }, row.source || '—'),
  },
  {
    title: '仓库',
    key: 'repository',
    minWidth: 220,
    render: (row: CoreDescriptor) => h('span', { class: 'mono' }, row.repository || '—'),
  },
]

onMounted(() => {
  store.ensureBase()
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">系统设置</h2>
        <p class="page-subtitle">认证、连接信息、端口分配与架构说明</p>
      </div>
    </div>

    <NCard size="small" title="账号与认证" class="section-gap">
      <NSpace vertical :size="10" style="width: 100%">
        <div class="hint">
          管理台使用 <strong>HttpOnly 会话 Cookie</strong> 认证：首次打开需要手动创建管理员账号，
          之后每次打开都是登录窗口。浏览器不再保存任何 API Token。
        </div>
        <NDescriptions bordered :column="2" size="small" label-placement="left">
          <NDescriptionsItem label="登录账号">
            <span class="mono">{{ authState?.username || '—' }}</span>
          </NDescriptionsItem>
          <NDescriptionsItem label="首个管理员">
            {{ authState?.setup_required ? '尚未创建' : '已创建' }}
          </NDescriptionsItem>
          <NDescriptionsItem label="会话有效期">
            {{ authState ? `${Math.round(authState.policy.session_hours / 24)} 天 / 记住我 ${Math.round(authState.policy.remember_hours / 24)} 天` : '—' }}
          </NDescriptionsItem>
          <NDescriptionsItem label="口令存储">
            Argon2id（64 MiB / 3 轮 / 2 线程，随机盐）
          </NDescriptionsItem>
        </NDescriptions>
        <div class="hint">
          修改密码与退出登录在右上角<strong>账号菜单</strong>中操作；改密会让所有已登录会话立即失效。
          自动化脚本仍可使用 <span class="mono">ngg.yaml</span> 中的静态 Token 调用 REST API。
        </div>
      </NSpace>
    </NCard>

    <NCard size="small" title="后端连接信息" class="section-gap">
      <NDescriptions bordered :column="2" size="small" label-placement="left">
        <NDescriptionsItem label="API Base URL">
          <span class="mono">{{ API_BASE }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="开发 / 预览代理目标">
          <span class="mono">{{ backendTarget }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="WebSocket 路径">
          <span class="mono">{{ API_BASE }}/ws</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="当前页面地址">
          <span class="mono">{{ origin }}</span>
        </NDescriptionsItem>
        <NDescriptionsItem label="后端服务" :span="2">
          <span class="mono">{{ store.system?.listen_addr || '—' }}</span>
          · 阶段 {{ store.system?.stage || '—' }} · 模式 {{ store.system?.mode || '—' }}
        </NDescriptionsItem>
      </NDescriptions>
    </NCard>

    <NCard size="small" title="端口分配（/ports）" class="section-gap">
      <template #header-extra>
        <NButton size="small" @click="store.refreshPorts()">刷新</NButton>
      </template>
      <NDataTable
        :columns="portColumns"
        :data="store.ports"
        :row-key="portRowKey"
        :bordered="false"
        :scroll-x="760"
        size="small"
        :pagination="false"
      />
    </NCard>

    <NCard size="small" title="内核注册表" class="section-gap">
      <template #header-extra>
        <NButton
          size="small"
          type="primary"
          :loading="store.isBusy('reload-registry')"
          @click="store.reloadRegistryAction()"
        >
          重载注册表
        </NButton>
      </template>
      <NSpace vertical :size="10" style="width: 100%">
        <div class="hint">
          支持通过描述文件新增内核；新增或修改描述文件后点击「重载注册表」生效。
          重载结果会返回加载数量与问题描述（problems）。
        </div>
        <NDataTable
          :columns="registryColumns"
          :data="store.registry"
          :row-key="registryRowKey"
          :bordered="false"
          :scroll-x="1100"
          size="small"
          :pagination="false"
        />
      </NSpace>
    </NCard>

    <NCard size="small" title="架构与安全说明">
          </NCard>
  </div>
</template>
