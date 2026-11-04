<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NGi,
  NGrid,
  NInput,
  NInputNumber,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type {
  IngressNodeConfig,
  NetworkConfigPatchResult,
  NetworkSession,
  NetworkStatus,
  TunNodeConfig,
} from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { makeColumns } from '../composables/tableColumns'
import { prettyJson, toArray } from '../utils/format'

const { isBusy, run } = useApiAction()

const status = ref<NetworkStatus | null>(null)
const sessions = ref<NetworkSession[]>([])
const result = ref<NetworkConfigPatchResult | null>(null)
const selftestResult = ref<unknown>(null)
const selftestCore = ref('')

const ingress = ref<IngressNodeConfig>({
  enabled: true, listen: '127.0.0.1', port: 18080, auth_user: '', auth_pass: '',
})
const tun = ref<TunNodeConfig>({
  name: 'ngg0', mtu: 1500, ipv4: '10.89.0.1/24', ipv6: 'fd89::1/64',
  auto_route: false, strict_route: true,
})

const coreOptions = [
  { label: 'DIRECT（不经内核）', value: '' },
  { label: 'Mihomo', value: 'mihomo' },
  { label: 'Xray-core', value: 'xray-core' },
  { label: 'sing-box', value: 'sing-box' },
]

function fillForms(cfg: unknown) {
  if (!cfg || typeof cfg !== 'object') return
  const c = cfg as Record<string, unknown>
  const i = c.ingress as Partial<IngressNodeConfig> | undefined
  if (i) {
    ingress.value = {
      enabled: i.enabled ?? true,
      listen: i.listen ?? '127.0.0.1',
      port: i.port ?? 18080,
      auth_user: i.auth_user ?? '',
      auth_pass: i.auth_pass ?? '',
    }
  }
  const t = c.tun as Partial<TunNodeConfig> | undefined
  if (t) {
    tun.value = {
      name: t.name ?? 'ngg0',
      mtu: t.mtu ?? 1500,
      ipv4: t.ipv4 ?? '10.89.0.1/24',
      ipv6: t.ipv6 ?? 'fd89::1/64',
      auto_route: t.auto_route ?? false,
      strict_route: t.strict_route ?? true,
    }
  }
}

async function refreshAll() {
  const s = await run('status', () => api.getNetworkStatus())
  if (s) status.value = s
  const c = await run('config', () => api.getNetworkConfig())
  if (c) fillForms(c)
  const sess = await run('sessions', () => api.getNetworkSessions())
  if (sess) sessions.value = toArray<NetworkSession>(sess)
}

async function saveIngress() {
  if (ingress.value.enabled && (ingress.value.port < 1 || ingress.value.port > 65535)) {
    window.$message?.error('监听端口必须在 1 ~ 65535 之间')
    return
  }
  if (ingress.value.enabled && ingress.value.listen !== '127.0.0.1' && ingress.value.listen !== 'localhost' &&
      (!ingress.value.auth_user || !ingress.value.auth_pass)) {
    window.$message?.error('非回环监听必须设置认证账号与密码')
    return
  }
  const r = await run('save-ingress', () =>
    api.patchNetworkConfig({ ingress: { ...ingress.value, listen: ingress.value.listen.trim() } }),
    '入口配置已保存')
  if (r) result.value = r
  await refreshAll()
}

async function saveTun() {
  const r = await run('save-tun', () => api.patchNetworkConfig({ tun: { ...tun.value } }), 'TUN 配置已保存')
  if (r) result.value = r
  await refreshAll()
}

async function engine(action: 'start' | 'stop') {
  const fn = action === 'start' ? api.startNetwork : api.stopNetwork
  const r = await run('engine-' + action, fn, action === 'start' ? '网络引擎已启动' : '网络引擎已停止')
  if (r) result.value = r as NetworkConfigPatchResult
  await refreshAll()
}

async function doSelftest() {
  const r = await run('selftest', () => api.networkSelftest(selftestCore.value))
  if (r) selftestResult.value = r
  await refreshAll()
}

const ingressNodes = computed(() => {
  const st = status.value
  if (!st) return []
  const ing = st.ingress || ({} as NetworkStatus['ingress'])
  const protos = Array.isArray(ing.protocols) ? ing.protocols : []
  const rows = protos.map((p) => ({
    name: p,
    type: p,
    address: ing.address || '—',
    state: st.running && ing.enabled ? '运行中' : '未运行',
  }))
  rows.push({
    name: '统一 TUN',
    type: `tun/${st.tun?.name || 'ngg0'}`,
    address: `mtu ${st.tun?.mtu ?? '—'}`,
    state: st.tun?.active ? '运行中' : (st.tun?.configured ? '已配置未激活' : '未启用'),
  })
  return rows
})
const nodeColumns: any[] = [
  { title: '入口节点', key: 'name', minWidth: 180 },
  { title: '协议 / 接口', key: 'type', minWidth: 180 },
  { title: '监听地址', key: 'address', minWidth: 200 },
  {
    title: '状态', key: 'state', minWidth: 130,
    render: (r: { state: string }) => String(r.state),
  },
]
const sessionColumns = computed(() => makeColumns(sessions.value))

onMounted(refreshAll)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">入口节点</h2>
        <p class="page-subtitle">配置引擎 · 系统流量进入 NGG 的唯一入口与 TUN 生命周期</p>
      </div>
      <NSpace :size="8" :wrap="true">
        <NButton size="small" :loading="isBusy('status')" @click="refreshAll">刷新</NButton>
        <NButton size="small" type="primary" :loading="isBusy('engine-start')" @click="engine('start')">
          启动引擎
        </NButton>
        <NButton size="small" :loading="isBusy('engine-stop')" @click="engine('stop')">停止引擎</NButton>
      </NSpace>
    </div>

    <NAlert type="info" :bordered="false" class="section-gap">
      <template #header>NGG 是唯一系统网络入口</template>
      SOCKS5 / SOCKS4a / HTTP CONNECT / HTTP 绝对形式共用一个监听端口；统一 TUN 由 NGG 创建，
      内核一律 Worker Mode，禁止自建 TUN、DNS 劫持与全局路由。非回环监听（局域网代理）强制要求认证。
    </NAlert>

    <NCard size="small" title="入口节点（数据来自 /network/status）" class="section-gap">
      <template #header-extra>
        <NTag size="small" :bordered="false" :type="status?.running ? 'success' : 'default'">
          {{ status?.running ? '引擎运行中' : '引擎未运行' }}
        </NTag>
      </template>
      <NDataTable
        :columns="nodeColumns"
        :data="ingressNodes"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="760"
      />
    </NCard>

    <NGrid cols="1 m:2" responsive="screen" :x-gap="14" :y-gap="14" class="section-gap">
      <NGi>
        <NCard size="small" title="入口配置（PUT /network/config · ingress）">
          <template #header-extra>
            <NButton size="small" type="primary" :loading="isBusy('save-ingress')" @click="saveIngress">
              保存
            </NButton>
          </template>
          <NSpace vertical :size="10" style="width: 100%">
            <div class="kv-label">启用 userspace 入口</div>
            <NSwitch v-model:value="ingress.enabled" />
            <NGrid cols="2" :x-gap="10" :y-gap="8">
              <NGi>
                <div class="kv-label">监听地址 listen</div>
                <NInput v-model:value="ingress.listen" placeholder="127.0.0.1" />
              </NGi>
              <NGi>
                <div class="kv-label">端口 port</div>
                <NInputNumber v-model:value="ingress.port" style="width: 100%" :min="1" :max="65535" />
              </NGi>
              <NGi>
                <div class="kv-label">认证账号 auth_user（非回环必填）</div>
                <NInput v-model:value="ingress.auth_user" placeholder="留空 = 不认证" />
              </NGi>
              <NGi>
                <div class="kv-label">认证密码 auth_pass</div>
                <NInput v-model:value="ingress.auth_pass" type="password" show-password-on="click" placeholder="留空 = 不认证" />
              </NGi>
            </NGrid>
            <div class="hint">修改监听地址或端口后需重启网络引擎才能生效。</div>
          </NSpace>
        </NCard>
      </NGi>

      <NGi>
        <NCard size="small" title="统一 TUN（PUT /network/config · tun）">
          <template #header-extra>
            <NButton size="small" type="primary" :loading="isBusy('save-tun')" @click="saveTun">
              保存
            </NButton>
          </template>
          <NSpace vertical :size="10" style="width: 100%">
            <NGrid cols="2" :x-gap="10" :y-gap="8">
              <NGi>
                <div class="kv-label">接口名 name</div>
                <NInput v-model:value="tun.name" />
              </NGi>
              <NGi>
                <div class="kv-label">MTU</div>
                <NInputNumber v-model:value="tun.mtu" style="width: 100%" :min="576" :max="65535" />
              </NGi>
              <NGi>
                <div class="kv-label">IPv4</div>
                <NInput v-model:value="tun.ipv4" />
              </NGi>
              <NGi>
                <div class="kv-label">IPv6</div>
                <NInput v-model:value="tun.ipv6" />
              </NGi>
              <NGi>
                <div class="kv-label">自动接管路由 auto_route</div>
                <NSwitch v-model:value="tun.auto_route" />
              </NGi>
              <NGi>
                <div class="kv-label">严格路由 strict_route</div>
                <NSwitch v-model:value="tun.strict_route" />
              </NGi>
            </NGrid>
            <NAlert type="warning" :bordered="false" :show-icon="false">
              auto_route 仅在 Linux 且具备 /dev/net/tun + CAP_NET_ADMIN 时可用；其它平台返回
              ErrTUNUnsupported，入口自动走 userspace 代理。
            </NAlert>
          </NSpace>
        </NCard>
      </NGi>
    </NGrid>

    <NCard size="small" title="配置结果与自检" class="section-gap">
      <NSpace vertical :size="10" style="width: 100%">
        <NSpace :size="8" :wrap="true" align="center">
          <NSelect v-model:value="selftestCore" :options="coreOptions" style="width: 220px" />
          <NButton size="small" type="primary" :loading="isBusy('selftest')" @click="doSelftest">
            端到端自检
          </NButton>
          <span class="hint">真实建立连接：NGG 入口 → 会话 → 规则 → 调度 → 内核/直连 → 回显服务</span>
        </NSpace>
        <pre class="mono output-block" style="max-height: 200px">{{ prettyJson(selftestResult) }}</pre>
        <template v-if="result">
          <NSpace :size="6" :wrap="true">
            <NTag size="small" :bordered="false" type="success">
              已变更 {{ (result.changed || []).join(', ') || '无' }}
            </NTag>
            <NTag v-if="(result.requires_restart || []).length" size="small" :bordered="false" type="warning">
              需重启：{{ (result.requires_restart || []).join(', ') }}
            </NTag>
            <NTag size="small" :bordered="false" :type="result.persisted ? 'info' : 'default'">
              {{ result.persisted ? '已写入配置文件' : '仅内存生效' }}
            </NTag>
          </NSpace>
          <NAlert v-for="(w, i) in result.warnings || []" :key="i" type="warning" :bordered="false">
            {{ String(w) }}
          </NAlert>
        </template>
      </NSpace>
    </NCard>

    <NCard size="small" title="活动会话（GET /network/sessions）" class="section-gap">
      <template #header-extra>
        <NTag size="small" :bordered="false">{{ sessions.length }} 条</NTag>
      </template>
      <NDataTable
        v-if="sessions.length"
        :columns="sessionColumns"
        :data="sessions"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="1200"
      />
      <div v-else class="hint">当前无活动会话记录。</div>
    </NCard>
  </div>
</template>
