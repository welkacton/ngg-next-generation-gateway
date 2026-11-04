<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
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
  EgressNodeConfig,
  IngressNodeConfig,
  NetworkConfigPatchResult,
  NodeConfigResult,
  NodeKind,
  NodeProtocolInfo,
  ProtocolNode,
  TunNodeConfig,
} from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { prettyJson, toArray } from '../utils/format'

const props = defineProps<{
  kind: NodeKind
  title: string
  subtitle: string
}>()

const { isBusy, run } = useApiAction()

const nodes = ref<ProtocolNode[]>([])
const protocols = ref<NodeProtocolInfo[]>([])
const storePath = ref('')
const actionResult = ref<unknown>(null)
const configResult = ref<NodeConfigResult | null>(null)
const globalResult = ref<NetworkConfigPatchResult | null>(null)

const jsonMode = ref(false)
const jsonText = ref('{\n  "nodes": []\n}')
const showGlobal = ref(false)

const editingId = ref<string | null>(null)
const paramsText = ref('{}')
const tagsText = ref('')
const configKernel = ref('sing-box')
const kernelOptions = [
  { label: 'Mihomo', value: 'mihomo' },
  { label: 'Xray-core', value: 'xray-core' },
  { label: 'sing-box', value: 'sing-box' },
]

function emptyNode(): ProtocolNode {
  return {
    id: '',
    name: '',
    kind: props.kind,
    protocol: props.kind === 'inbound' ? 'shadowsocks' : 'vless-reality',
    address: props.kind === 'inbound' ? '0.0.0.0' : '',
    port: 0,
    enabled: true,
    core_id: '',
    params: {},
    tags: [],
    description: '',
  }
}
const form = ref<ProtocolNode>(emptyNode())

// Global (non-node) parameters that still belong to this side of the engine.
const ingress = ref<IngressNodeConfig>({
  enabled: true, listen: '127.0.0.1', port: 18080, auth_user: '', auth_pass: '',
})
const tun = ref<TunNodeConfig>({
  name: 'ngg0', mtu: 1500, ipv4: '10.89.0.1/24', ipv6: 'fd89::1/64',
  auto_route: false, strict_route: true,
})
const egress = ref<EgressNodeConfig>({
  interface: '', local_addr: '', mark: 20039, bypass_tun: true,
})
const policy = ref({
  fail_closed: true, failover: true, default_core: '', direct_on_failure: false,
})

const protocolOptions = computed(() =>
  protocols.value.map((p) => ({
    label: `${p.name}${p.encrypted ? ' · 加密' : ''}${p.pq_capable ? ' · 后量子' : ''}`,
    value: p.id,
  })),
)
const selectedProtocol = computed(() =>
  protocols.value.find((p) => p.id === form.value.protocol),
)

const columns: any[] = [
  { title: 'ID', key: 'id', minWidth: 150, render: (r: ProtocolNode) => String(r.id) },
  { title: '名称', key: 'name', minWidth: 150 },
  {
    title: '加密协议', key: 'protocol', minWidth: 180,
    render: (r: ProtocolNode) => {
      const info = protocols.value.find((p) => p.id === r.protocol)
      return String(info?.name ?? r.protocol)
    },
  },
  {
    title: '类型', key: 'encrypted', minWidth: 110,
    render: (r: ProtocolNode) => {
      const info = protocols.value.find((p) => p.id === r.protocol)
      return info?.pq_capable ? '后量子' : (info?.encrypted ? '加密' : '明文')
    },
  },
  {
    title: '地址', key: 'address', minWidth: 180,
    render: (r: ProtocolNode) => `${r.address || '—'}:${r.port || '—'}`,
  },
  { title: '内核', key: 'core_id', minWidth: 120, render: (r: ProtocolNode) => r.core_id || '自动' },
  {
    title: '启用', key: 'enabled', minWidth: 80,
    render: (r: ProtocolNode) => (r.enabled ? '是' : '否'),
  },
]

function fillGlobal(cfg: unknown) {
  if (!cfg || typeof cfg !== 'object') return
  const c = cfg as Record<string, unknown>
  const i = c.ingress as Partial<IngressNodeConfig> | undefined
  if (i) ingress.value = {
    enabled: i.enabled ?? true, listen: i.listen ?? '127.0.0.1', port: i.port ?? 18080,
    auth_user: i.auth_user ?? '', auth_pass: i.auth_pass ?? '',
  }
  const t = c.tun as Partial<TunNodeConfig> | undefined
  if (t) tun.value = {
    name: t.name ?? 'ngg0', mtu: t.mtu ?? 1500,
    ipv4: t.ipv4 ?? '10.89.0.1/24', ipv6: t.ipv6 ?? 'fd89::1/64',
    auto_route: t.auto_route ?? false, strict_route: t.strict_route ?? true,
  }
  const e = c.egress as Partial<EgressNodeConfig> | undefined
  if (e) egress.value = {
    interface: e.interface ?? '', local_addr: e.local_addr ?? '',
    mark: e.mark ?? 20039, bypass_tun: e.bypass_tun ?? true,
  }
  policy.value = {
    fail_closed: c.fail_closed === undefined ? true : Boolean(c.fail_closed),
    failover: c.failover === undefined ? true : Boolean(c.failover),
    default_core: String(c.default_core ?? ''),
    direct_on_failure: Boolean(c.direct_on_failure ?? false),
  }
}

async function refresh() {
  const list = await run('list', () => api.listNodes(props.kind))
  if (list) {
    nodes.value = toArray<ProtocolNode>(list.nodes)
    storePath.value = String(list.path ?? '')
  }
  const cfg = await run('cfg', () => api.getNetworkConfig())
  if (cfg) fillGlobal(cfg)
}

async function refreshCatalog() {
  const p = await run('protocols', () => api.listNodeProtocols(props.kind))
  if (p) protocols.value = toArray<NodeProtocolInfo>(p)
}

async function loadJson() {
  const d = await run('export', () => api.exportNodes())
  if (d) {
    const all = toArray<ProtocolNode>(d.nodes).filter((n) => n.kind === props.kind)
    jsonText.value = JSON.stringify({ nodes: all }, null, 2)
  }
}

watch(jsonMode, (on) => {
  if (on) loadJson()
})

function startEdit(n: ProtocolNode) {
  editingId.value = n.id
  form.value = {
    id: n.id, name: n.name, kind: props.kind, protocol: n.protocol,
    address: n.address ?? '', port: n.port ?? 0, enabled: n.enabled,
    core_id: n.core_id ?? '', params: { ...(n.params ?? {}) },
    tags: [...(n.tags ?? [])], description: n.description ?? '',
  }
  paramsText.value = JSON.stringify(form.value.params, null, 2)
  tagsText.value = (n.tags ?? []).join(', ')
}

function cancelEdit() {
  editingId.value = null
  form.value = emptyNode()
  paramsText.value = '{}'
  tagsText.value = ''
}

function onProtocolChange(id: string) {
  form.value.protocol = id
  const info = protocols.value.find((p) => p.id === id)
  if (info && !form.value.port && info.default_port > 0) {
    form.value.port = info.default_port
  }
}

async function saveNode() {
  let params: Record<string, unknown>
  try {
    params = JSON.parse(paramsText.value || '{}')
  } catch {
    window.$message?.error('协议参数必须是合法 JSON 对象')
    return
  }
  if (params === null || typeof params !== 'object' || Array.isArray(params)) {
    window.$message?.error('协议参数必须是 JSON 对象')
    return
  }
  const n: ProtocolNode = {
    id: form.value.id.trim(),
    name: form.value.name.trim(),
    kind: props.kind,
    protocol: form.value.protocol,
    address: form.value.address.trim(),
    port: form.value.port,
    enabled: form.value.enabled,
    core_id: form.value.core_id.trim(),
    params,
    tags: tagsText.value.split(/[,，\n]+/).map((s) => s.trim()).filter(Boolean),
    description: form.value.description.trim(),
  }
  if (!n.id || !n.name) {
    window.$message?.warning('请填写节点 ID 与名称')
    return
  }
  if (n.port < 0 || n.port > 65535) {
    window.$message?.error('端口必须在 0 ~ 65535 之间')
    return
  }
  const editing = editingId.value
  const r = editing
    ? await run('save', () => api.updateNode(editing, n), '节点已修改')
    : await run('save', () => api.addNode(n), '节点已新增')
  if (r) {
    actionResult.value = r
    cancelEdit()
    await refresh()
    if (jsonMode.value) await loadJson()
  }
}

function confirmDelete(id: string) {
  window.$dialog?.warning({
    title: '删除节点',
    content: `确定删除节点 ${id} 吗？此操作会写入持久化文件。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const r = await run('del:' + id, () => api.deleteNode(id), '节点已删除')
      if (r) {
        actionResult.value = r
        if (editingId.value === id) cancelEdit()
        await refresh()
        if (jsonMode.value) await loadJson()
      }
    },
  })
}

async function generateConfig(id: string) {
  const r = await run('gen:' + id, () => api.generateNodeConfig(id, configKernel.value))
  if (r) configResult.value = r
}

async function saveJson() {
  let parsed: unknown
  try {
    parsed = JSON.parse(jsonText.value)
  } catch {
    window.$message?.error('JSON 解析失败，未提交任何修改')
    return
  }
  const list = Array.isArray(parsed)
    ? parsed
    : (parsed as Record<string, unknown>)?.nodes
  if (!Array.isArray(list)) {
    window.$message?.error('JSON 必须是数组或包含 nodes 数组的对象')
    return
  }
  const r = await run('import', () => api.importNodes(list as ProtocolNode[]), 'JSON 已全量校验并保存')
  if (r) {
    actionResult.value = r
    await refresh()
    await loadJson()
  }
}

async function saveGlobal() {
  const patch = props.kind === 'inbound'
    ? { ingress: ingress.value, tun: tun.value }
    : { egress: egress.value, ...policy.value }
  const r = await run('global', () => api.patchNetworkConfig(patch), '全局参数已保存')
  if (r) globalResult.value = r
  await refresh()
}

onMounted(async () => {
  await refreshCatalog()
  await refresh()
  if (jsonMode.value) await loadJson()
})
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">{{ title }}</h2>
        <p class="page-subtitle">{{ subtitle }}</p>
      </div>
      <NSpace :size="10" align="center" :wrap="true">
        <span class="hint">JSON 编辑</span>
        <NSwitch v-model:value="jsonMode" />
        <NButton size="small" :loading="isBusy('list')" @click="refresh">刷新</NButton>
      </NSpace>
    </div>

    <NAlert type="info" :bordered="false" class="section-gap">
      <template #header>{{ props.kind === 'inbound' ? '入口协议节点' : '出口协议节点' }}</template>
      可配置 Shadowsocks / VMess / VLESS+REALITY / Trojan / Hysteria2 / TUIC / WireGuard / OpenVPN / IPsec
      等加密代理与 VPN 协议，以及 ML-KEM-768 混合后量子控制信道。节点定义保存在
      <span class="mono">{{ storePath || 'data/nodes-config.json' }}</span>（0600）。
      NGG 负责节点定义与内核原生配置片段生成，实际握手与隧道由内核执行，NGG 不重写协议栈。
    </NAlert>

    <!-- ============================ 表单模式 ============================ -->
    <template v-if="!jsonMode">
      <NCard size="small" :title="`${title}列表（GET /nodes）`" class="section-gap">
        <template #header-extra>
          <NTag size="small" :bordered="false" type="info">{{ nodes.length }} 个节点</NTag>
        </template>
        <NDataTable
          :columns="columns"
          :data="nodes"
          :bordered="false"
          size="small"
          :pagination="false"
          :scroll-x="1250"
        />
        <div v-if="!nodes.length" class="hint" style="margin-top: 8px">
          暂无节点。可在下方表单新增，或切到 JSON 模式批量导入。
        </div>
        <div v-else style="margin-top: 12px">
          <div class="kv-label">节点操作</div>
          <NSpace :size="8" :wrap="true" style="margin-top: 6px">
            <NSpace v-for="n in nodes" :key="n.id" :size="4" align="center">
              <NButton size="tiny" @click="startEdit(n)">编辑 {{ n.id }}</NButton>
              <NButton size="tiny" type="error" :loading="isBusy('del:' + n.id)" @click="confirmDelete(n.id)">
                删除
              </NButton>
              <NButton size="tiny" :loading="isBusy('gen:' + n.id)" @click="generateConfig(n.id)">
                生成配置
              </NButton>
            </NSpace>
          </NSpace>
        </div>
      </NCard>

      <NCard
        size="small"
        :title="editingId ? `编辑节点 ${editingId}` : '新增节点'"
        class="section-gap"
      >
        <template #header-extra>
          <NSpace :size="8">
            <NButton size="small" type="primary" :loading="isBusy('save')" @click="saveNode">
              {{ editingId ? '保存修改' : '新增节点' }}
            </NButton>
            <NButton v-if="editingId" size="small" @click="cancelEdit">取消编辑</NButton>
          </NSpace>
        </template>
        <NGrid cols="1 m:2" responsive="screen" :x-gap="12" :y-gap="10">
          <NGi>
            <div class="kv-label">节点 ID（编辑时不可改）</div>
            <NInput v-model:value="form.id" :disabled="!!editingId" placeholder="in-ss-01" />
          </NGi>
          <NGi>
            <div class="kv-label">名称</div>
            <NInput v-model:value="form.name" placeholder="主入口 / 备用出口" />
          </NGi>
          <NGi>
            <div class="kv-label">加密协议</div>
            <NSelect
              :value="form.protocol"
              :options="protocolOptions"
              @update:value="onProtocolChange"
            />
          </NGi>
          <NGi>
            <div class="kv-label">地址（入口监听 / 出口服务器）</div>
            <NInput v-model:value="form.address" placeholder="0.0.0.0 或 example.com" />
          </NGi>
          <NGi>
            <div class="kv-label">端口（0 = 使用协议默认端口）</div>
            <NInputNumber v-model:value="form.port" style="width: 100%" :min="0" :max="65535" />
          </NGi>
          <NGi>
            <div class="kv-label">执行内核（留空 = 按规则调度）</div>
            <NInput v-model:value="form.core_id" placeholder="mihomo / xray-core / sing-box" />
          </NGi>
          <NGi>
            <div class="kv-label">启用</div>
            <NSwitch v-model:value="form.enabled" />
          </NGi>
          <NGi>
            <div class="kv-label">标签（逗号分隔）</div>
            <NInput v-model:value="tagsText" placeholder="production, encrypted" />
          </NGi>
          <NGi :span="2">
            <div class="kv-label">说明</div>
            <NInput v-model:value="form.description" placeholder="可选" />
          </NGi>
          <NGi :span="2">
            <div class="kv-label">
              协议参数 params（JSON 对象：密码 / UUID / cipher / private_key / TLS / obfs 等）
            </div>
            <NInput v-model:value="paramsText" type="textarea" class="code-editor" :rows="7" />
          </NGi>
        </NGrid>
        <NAlert v-if="selectedProtocol" type="default" :bordered="false" style="margin-top: 10px">
          {{ selectedProtocol.name }} · {{ selectedProtocol.description }}
          <NTag size="small" :bordered="false" :type="selectedProtocol.encrypted ? 'success' : 'default'">
            {{ selectedProtocol.encrypted ? '加密协议' : '明文/本地协议' }}
          </NTag>
          <NTag v-if="selectedProtocol.pq_capable" size="small" :bordered="false" type="warning">
            后量子
          </NTag>
          · 默认端口 {{ selectedProtocol.default_port || '无' }}
        </NAlert>
      </NCard>

      <NCard size="small" title="生成内核原生配置片段" class="section-gap">
        <NSpace :size="8" :wrap="true" align="center">
          <NSelect v-model:value="configKernel" :options="kernelOptions" style="width: 180px" />
          <span class="hint">选择节点后生成：</span>
        </NSpace>
        <NSpace :size="6" :wrap="true" style="margin-top: 10px">
          <NButton
            v-for="n in nodes"
            :key="n.id"
            size="tiny"
            :loading="isBusy('gen:' + n.id)"
            @click="generateConfig(n.id)"
          >
            生成 {{ n.id }}
          </NButton>
        </NSpace>
        <pre v-if="configResult" class="mono output-block" style="max-height: 260px; margin-top: 10px">{{ configResult.content ?? prettyJson(configResult) }}</pre>
        <div v-if="configResult?.note" class="hint" style="margin-top: 6px">{{ configResult.note }}</div>
      </NCard>
    </template>

    <!-- ============================ JSON 模式 ============================ -->
    <NCard v-else size="small" title="JSON 编辑（全量替换 · Fail-Closed）" class="section-gap">
      <template #header-extra>
        <NSpace :size="8">
          <NButton size="small" :loading="isBusy('export')" @click="loadJson">从服务端重新载入</NButton>
          <NButton size="small" type="primary" :loading="isBusy('import')" @click="saveJson">
            校验并保存
          </NButton>
        </NSpace>
      </template>
      <NAlert type="warning" :bordered="false" style="margin-bottom: 10px">
        保存时会逐条校验节点：协议是否在目录中、是否支持该 kind、端口是否合法。
        任意一条不合法则整体拒绝，原有节点保持不变。JSON 里 kind 字段应为
        <span class="mono">{{ props.kind }}</span>；此处只展示/保存当前类型的节点。
      </NAlert>
      <NInput v-model:value="jsonText" type="textarea" class="code-editor" :rows="20" />
      <pre v-if="actionResult" class="mono output-block" style="max-height: 160px; margin-top: 10px">{{ prettyJson(actionResult) }}</pre>
    </NCard>

    <!-- ========================= 全局参数（保留） ========================= -->
    <NCard size="small" class="section-gap">
      <template #header>
        <NSpace align="center" :size="10">
          <span>{{ props.kind === 'inbound' ? '统一入口 / TUN 全局参数' : '出口绑定 / 调度策略' }}</span>
          <NButton size="tiny" @click="showGlobal = !showGlobal">
            {{ showGlobal ? '收起' : '展开' }}
          </NButton>
        </NSpace>
      </template>
      <template v-if="showGlobal">
        <NGrid cols="1 m:2" responsive="screen" :x-gap="12" :y-gap="10">
          <template v-if="props.kind === 'inbound'">
            <NGi>
              <div class="kv-label">入口监听地址</div>
              <NInput v-model:value="ingress.listen" />
            </NGi>
            <NGi>
              <div class="kv-label">入口端口</div>
              <NInputNumber v-model:value="ingress.port" style="width: 100%" :min="1" :max="65535" />
            </NGi>
            <NGi>
              <div class="kv-label">认证账号（非回环必填）</div>
              <NInput v-model:value="ingress.auth_user" />
            </NGi>
            <NGi>
              <div class="kv-label">认证密码</div>
              <NInput v-model:value="ingress.auth_pass" />
            </NGi>
            <NGi>
              <div class="kv-label">TUN 接口 / MTU</div>
              <NSpace :size="8">
                <NInput v-model:value="tun.name" style="width: 120px" />
                <NInputNumber v-model:value="tun.mtu" style="width: 130px" />
              </NSpace>
            </NGi>
            <NGi>
              <div class="kv-label">TUN 自动接管路由（仅 Linux）</div>
              <NSwitch v-model:value="tun.auto_route" />
            </NGi>
          </template>
          <template v-else>
            <NGi>
              <div class="kv-label">物理出口接口</div>
              <NInput v-model:value="egress.interface" />
            </NGi>
            <NGi>
              <div class="kv-label">绑定源地址</div>
              <NInput v-model:value="egress.local_addr" />
            </NGi>
            <NGi>
              <div class="kv-label">SO_MARK</div>
              <NInputNumber v-model:value="egress.mark" style="width: 100%" />
            </NGi>
            <NGi>
              <div class="kv-label">绕过 NGG TUN</div>
              <NSwitch v-model:value="egress.bypass_tun" />
            </NGi>
            <NGi>
              <div class="kv-label">Fail-Closed</div>
              <NSwitch v-model:value="policy.fail_closed" />
            </NGi>
            <NGi>
              <div class="kv-label">内核故障自动切换</div>
              <NSwitch v-model:value="policy.failover" />
            </NGi>
          </template>
        </NGrid>
        <NSpace style="margin-top: 12px">
          <NButton size="small" type="primary" :loading="isBusy('global')" @click="saveGlobal">
            保存全局参数
          </NButton>
        </NSpace>
        <div v-if="globalResult" style="margin-top: 10px">
          <NSpace :size="6" :wrap="true">
            <NTag size="small" :bordered="false" type="success">
              已变更 {{ (globalResult.changed || []).join(', ') || '无' }}
            </NTag>
            <NTag v-if="(globalResult.requires_restart || []).length" size="small" :bordered="false" type="warning">
              需重启：{{ (globalResult.requires_restart || []).join(', ') }}
            </NTag>
          </NSpace>
        </div>
      </template>
      <div v-else class="hint">点击「展开」编辑引擎级参数（监听端口、TUN、出口绑定、Fail-Closed 策略）。</div>
    </NCard>
  </div>
</template>
