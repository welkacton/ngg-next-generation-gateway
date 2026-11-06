<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NButton, NInput, NInputNumber, NSelect, NSwitch } from 'naive-ui'
import * as api from '../../api/ngg'
import type { GatewayConfig, GatewayStatus, GatewayWAN } from '../../api/ngg'
import { useApiAction } from '../../composables/useApi'

const { isBusy, run } = useApiAction()

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

interface WanDraft {
  id: string
  name: string
  enabled: boolean
  mode: GatewayWAN['mode']
  interface: string
  address: string
  gateway: string
  mtu: number | null
  dnsText: string
  ipv6: boolean
  username: string
  password: string
  password_set: boolean
  route: GatewayWAN['route']
  weight: number | null
  priority: number | null
  /** route=proxy 时绑定的链路编排；空 = 默认链路 */
  pipelineId: string
}

const MODES: Array<{ id: GatewayWAN['mode']; label: string }> = [
  { id: 'dhcp', label: 'DHCP' },
  { id: 'static', label: '静态' },
  { id: 'pppoe', label: 'PPPoE' },
]
const MODE_LABEL: Record<GatewayWAN['mode'], string> = {
  dhcp: 'DHCP',
  static: '静态',
  pppoe: 'PPPoE',
}
const ROUTES: Array<{ id: GatewayWAN['route']; label: string }> = [
  { id: 'local', label: '本地直出' },
  { id: 'proxy', label: '国际代理' },
]

const config = ref<GatewayConfig | null>(null)
const status = ref<GatewayStatus | null>(null)
const list = ref<WanDraft[]>([])
const loading = ref(false)
const editingId = ref<string | null>(null)
/** 链路编排下拉：当前仓库只有一条可执行链路，但接口允许按 ID 绑定。 */
const pipelineOptions = ref<Array<{ label: string; value: string }>>([
  { label: '默认链路', value: '' },
])

const interfaces = computed(() => status.value?.interfaces ?? [])
const interfaceOptions = computed(() =>
  interfaces.value.map((i) => ({
    label: i.addresses.length ? `${i.name} · ${i.addresses[0]}` : i.name,
    value: i.name,
  })),
)

const stats = computed(() => {
  const enabled = list.value.filter((w) => w.enabled)
  return [
    { label: '已启用 WAN', value: String(enabled.length) },
    { label: '本地出口', value: String(enabled.filter((w) => w.route === 'local').length) },
    { label: '国际代理出口', value: String(enabled.filter((w) => w.route === 'proxy').length) },
    { label: '上行接口', value: status.value?.default_interface || '—' },
  ]
})

function splitList(text: string): string[] {
  return text
    .split(/[,，\s]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

function toDraft(w: GatewayWAN): WanDraft {
  return {
    id: w.id,
    name: w.name ?? '',
    enabled: w.enabled === true,
    mode: w.mode ?? 'dhcp',
    interface: w.interface ?? '',
    address: w.address ?? '',
    gateway: w.gateway ?? '',
    mtu: typeof w.mtu === 'number' && w.mtu > 0 ? w.mtu : 1500,
    dnsText: Array.isArray(w.dns) ? w.dns.join(', ') : '',
    ipv6: w.ipv6 === true,
    username: w.username ?? '',
    password: '',
    password_set: w.password_set === true,
    route: w.route === 'proxy' ? 'proxy' : 'local',
    weight: typeof w.weight === 'number' && w.weight > 0 ? w.weight : 1,
    priority: typeof w.priority === 'number' && w.priority > 0 ? w.priority : 1,
    pipelineId: w.pipeline_id ?? '',
  }
}

function syncFrom(cfg: GatewayConfig): void {
  const wans = Array.isArray(cfg.wans) && cfg.wans.length ? cfg.wans : [cfg.wan]
  list.value = wans.map(toDraft)
  editingId.value = null
}

function wanLabel(w: WanDraft): string {
  return w.name.trim() || w.id.toUpperCase()
}

function toggle(w: WanDraft): void {
  editingId.value = editingId.value === w.id ? null : w.id
}

function validate(): string | null {
  if (!list.value.length) return '未获取到 WAN 配置'
  if (!list.value.some((w) => w.enabled)) return '至少启用一个 WAN'

  for (const w of list.value) {
    const tag = wanLabel(w)
    const mtu = w.mtu
    if (mtu === null || Number.isNaN(mtu)) return `${tag} MTU 不能为空`
    if (mtu < 576 || mtu > 9000) return `${tag} MTU 需在 576–9000`
    const weight = w.weight
    if (weight === null || Number.isNaN(weight) || weight < 1 || weight > 100)
      return `${tag} 权重需在 1–100`
    const priority = w.priority
    if (priority === null || Number.isNaN(priority) || priority < 1 || priority > 10)
      return `${tag} 优先级需在 1–10`
    for (const dns of splitList(w.dnsText)) {
      if (!IPV4.test(dns)) return `${tag} DNS 地址无效：${dns}`
    }
    const address = w.address.trim()
    const gateway = w.gateway.trim()
    if (address && !IPV4.test(address)) return `${tag} IP 地址格式无效`
    if (gateway && !IPV4.test(gateway)) return `${tag} 默认网关格式无效`
    if (!w.enabled) continue
    if (!w.interface.trim()) return `${tag} 接口不能为空`
    if (w.mode === 'static') {
      if (!address) return `${tag} 静态模式需填写 IP 地址`
      if (!gateway) return `${tag} 静态模式需填写默认网关`
    }
    if (w.mode === 'pppoe') {
      if (!w.username.trim()) return `${tag} PPPoE 账号不能为空`
      if (!w.password && !w.password_set) return `${tag} PPPoE 密码不能为空`
    }
  }
  return null
}

function toPayload(w: WanDraft): GatewayWAN {
  const item: GatewayWAN = {
    id: w.id,
    name: wanLabel(w),
    enabled: w.enabled,
    mode: w.mode,
    interface: w.interface.trim(),
    address: w.address.trim(),
    gateway: w.gateway.trim(),
    mtu: Number(w.mtu),
    dns: splitList(w.dnsText),
    ipv6: w.ipv6,
    route: w.route,
    weight: Number(w.weight),
    priority: Number(w.priority),
    pipeline_id: w.pipelineId.trim(),
  }
  if (w.mode === 'pppoe') {
    item.username = w.username.trim()
    item.password_set = w.password_set
    if (w.password) item.password = w.password
  }
  return item
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const [cfg, st, pipe] = await Promise.allSettled([
      api.getGatewayConfig(),
      api.getGatewayStatus(),
      api.getPipeline(),
    ])
    if (pipe.status === 'fulfilled' && pipe.value?.config?.id) {
      pipelineOptions.value = [
        { label: '默认链路', value: '' },
        { label: pipe.value.config.name || pipe.value.config.id, value: pipe.value.config.id },
      ]
    }
    if (cfg.status === 'fulfilled') {
      config.value = cfg.value.config
      syncFrom(cfg.value.config)
    }
    if (st.status === 'fulfilled') status.value = st.value
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  const err = validate()
  if (err) {
    window.$message?.warning(err)
    return
  }
  const latest = await api.getGatewayConfig().catch(() => null)
  const base = latest?.config ?? config.value
  if (!base) return

  const wans = list.value.map(toPayload)
  const saved = await run(
    'save',
    () => api.saveGatewayConfig({ ...base, wan: wans[0], wans }),
    '已保存',
  )
  if (saved) {
    config.value = saved.config
    syncFrom(saved.config)
    // Declarative save: surface the honest limits instead of implying they took effect.
    if (saved.warnings?.length) {
      window.$message?.info(saved.warnings.slice(0, 2).join("；"), { duration: 6000 })
    }
  }
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="gw">
    <header class="gw-head">
      <h2 class="gw-title">WAN 设置</h2>
      <div class="gw-head__ops">
        <span class="gw-meta mono">WAN {{ list.length }}</span>
        <NButton size="tiny" quaternary type="primary" :loading="loading" @click="load">刷新</NButton>
        <NButton size="tiny" type="primary" :loading="isBusy('save')" @click="save">保存</NButton>
      </div>
    </header>

    <section class="gw-stats">
      <div v-for="item in stats" :key="item.label" class="gw-stat">
        <div class="gw-stat__label">{{ item.label }}</div>
        <div class="gw-stat__value" :title="item.value">{{ item.value }}</div>
      </div>
    </section>

    <section class="gw-card">
      <div class="gw-card__head">
        <h3 class="gw-card__title">WAN 接口</h3>
        <span class="gw-meta">{{ interfaces.length }} 个网卡</span>
      </div>

      <div v-for="w in list" :key="w.id" class="wan" :class="{ 'is-open': editingId === w.id }">
        <div class="wan-row">
          <div class="wan-id">
            <span class="wan-no">{{ w.id.toUpperCase() }}</span>
            <span class="wan-name" :title="wanLabel(w)">{{ wanLabel(w) }}</span>
          </div>
          <div class="wan-meta">
            <span class="chip">{{ MODE_LABEL[w.mode] }}</span>
            <span class="chip mono">{{ w.interface || '—' }}</span>
            <span class="chip mono">
              {{ w.mode === 'static' ? w.address || '—' : w.mode === 'pppoe' ? w.username || '—' : '自动获取' }}
            </span>
            <span class="route" :class="w.route === 'proxy' ? 'is-proxy' : 'is-local'">
              {{ w.route === 'proxy' ? '国际代理' : '本地直出' }}
            </span>
          </div>
          <div class="wan-ops">
            <NSwitch v-model:value="w.enabled" size="small" />
            <NButton size="tiny" quaternary type="primary" @click="toggle(w)">
              {{ editingId === w.id ? '收起' : '编辑' }}
            </NButton>
          </div>
        </div>

        <div v-if="editingId === w.id" class="wan-panel">
          <div class="grid">
            <div class="field span-4">
              <div class="label">连接方式</div>
              <div class="seg">
                <button
                  v-for="m in MODES"
                  :key="m.id"
                  type="button"
                  :class="{ 'is-active': w.mode === m.id }"
                  @click="w.mode = m.id"
                >{{ m.label }}</button>
              </div>
            </div>

            <div class="field">
              <div class="label">名称</div>
              <NInput v-model:value="w.name" size="small" :maxlength="64" :placeholder="w.id.toUpperCase()" />
            </div>
            <div class="field">
              <div class="label">接口</div>
              <NSelect
                v-model:value="w.interface"
                size="small"
                filterable
                tag
                :options="interfaceOptions"
                placeholder="eth0"
              />
            </div>
            <template v-if="w.mode === 'static'">
              <div class="field">
                <div class="label">IP 地址</div>
                <NInput v-model:value="w.address" size="small" placeholder="192.168.1.10" />
              </div>
              <div class="field">
                <div class="label">默认网关</div>
                <NInput v-model:value="w.gateway" size="small" placeholder="192.168.1.1" />
              </div>
            </template>
            <template v-else-if="w.mode === 'pppoe'">
              <div class="field">
                <div class="label">账号</div>
                <NInput v-model:value="w.username" size="small" />
              </div>
              <div class="field">
                <div class="label">密码</div>
                <NInput
                  v-model:value="w.password"
                  size="small"
                  type="password"
                  show-password-on="click"
                  :placeholder="w.password_set ? '已保存，留空不修改' : '密码'"
                />
              </div>
            </template>
            <div class="field">
              <div class="label">MTU</div>
              <NInputNumber v-model:value="w.mtu" size="small" :min="576" :max="9000" />
            </div>
            <div class="field">
              <div class="label">IPv6</div>
              <div class="switch-row"><NSwitch v-model:value="w.ipv6" size="small" /></div>
            </div>

            <div class="field span-2">
              <div class="label">DNS</div>
              <NInput v-model:value="w.dnsText" size="small" placeholder="1.1.1.1, 8.8.8.8" />
            </div>
            <div class="field span-2">
              <div class="label">出口路由</div>
              <div class="seg">
                <button
                  v-for="r in ROUTES"
                  :key="r.id"
                  type="button"
                  :class="{ 'is-active': w.route === r.id }"
                  @click="w.route = r.id"
                >{{ r.label }}</button>
              </div>
            </div>
            <div v-if="w.route === 'proxy'" class="field span-2">
              <div class="label">绑定链路编排</div>
              <NSelect
                v-model:value="w.pipelineId"
                size="small"
                :options="pipelineOptions"
                placeholder="默认链路"
              />
            </div>
            <div class="field">
              <div class="label">权重</div>
              <NInputNumber v-model:value="w.weight" size="small" :min="1" :max="100" />
            </div>
            <div class="field">
              <div class="label">优先级</div>
              <NInputNumber v-model:value="w.priority" size="small" :min="1" :max="10" />
            </div>
          </div>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.gw {
  font-family: -apple-system, BlinkMacSystemFont, 'SF Pro Text', 'PingFang SC',
    'Microsoft YaHei', sans-serif;
  background: #f5f5f7;
  border-radius: 12px;
  padding: 12px;
  color: #1d1d1f;
  font-size: 12px;
  min-height: calc(100vh - 120px);
}

.gw-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.gw-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  letter-spacing: -0.3px;
  line-height: 1.3;
}
.gw-head__ops {
  display: flex;
  align-items: center;
  gap: 6px;
}
.gw-meta {
  font-size: 10.5px;
  color: #6e6e73;
  font-variant-numeric: tabular-nums;
}

.gw-stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
  margin-bottom: 8px;
}
.gw-stat {
  background: #fff;
  border: 1px solid #e6e6eb;
  border-radius: 10px;
  padding: 8px 10px;
  min-width: 0;
}
.gw-stat__label {
  font-size: 10.5px;
  color: #6e6e73;
  font-weight: 500;
}
.gw-stat__value {
  font-size: 18px;
  font-weight: 600;
  letter-spacing: -0.02em;
  margin-top: 2px;
  line-height: 1.25;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.gw-card {
  background: #fff;
  border: 1px solid #e6e6eb;
  border-radius: 10px;
  padding: 10px 12px;
}
.gw-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 4px;
}
.gw-card__title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  letter-spacing: -0.01em;
}

.wan + .wan {
  border-top: 1px solid #f0f0f2;
}
.wan-row {
  display: grid;
  grid-template-columns: 140px minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  padding: 6px 0;
}
.wan-id {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.wan-no {
  flex: 0 0 auto;
  font-size: 10px;
  font-weight: 600;
  color: #6e6e73;
  background: #f2f2f5;
  border-radius: 5px;
  padding: 0 5px;
  line-height: 16px;
  letter-spacing: 0.02em;
}
.wan-name {
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.wan-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  min-width: 0;
}
.chip {
  font-size: 10.5px;
  color: #3a3a3c;
  background: #f5f5f7;
  border: 1px solid #ececf0;
  border-radius: 5px;
  padding: 0 5px;
  line-height: 16px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 180px;
}
.mono {
  font-family: 'SF Mono Nerd Font', 'PingFang SC', 'Microsoft YaHei', monospace;
}
.route {
  font-size: 10.5px;
  border-radius: 5px;
  padding: 0 6px;
  line-height: 16px;
  white-space: nowrap;
}
.route.is-local {
  color: #6e6e73;
  background: #f2f2f5;
}
.route.is-proxy {
  color: #0071e3;
  background: rgba(0, 113, 227, 0.08);
}
.wan-ops {
  display: flex;
  align-items: center;
  gap: 8px;
}

.wan-panel {
  padding: 0 0 10px;
}
.grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px 10px;
  background: #fafafb;
  border: 1px solid #f0f0f2;
  border-radius: 8px;
  padding: 10px;
}
.field {
  min-width: 0;
}
.label {
  font-size: 10.5px;
  color: #6e6e73;
  font-weight: 500;
  margin-bottom: 4px;
}
.span-2 {
  grid-column: span 2;
}
.span-4 {
  grid-column: 1 / -1;
}
.switch-row {
  display: flex;
  align-items: center;
  height: 28px;
}

.seg {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  background: #ececef;
  border-radius: 7px;
}
.seg button {
  appearance: none;
  border: 0;
  background: transparent;
  color: #6e6e73;
  font: inherit;
  font-size: 10.5px;
  padding: 3px 10px;
  border-radius: 5px;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.18s ease, color 0.18s ease, box-shadow 0.18s ease;
}
.seg button:hover {
  color: #1d1d1f;
}
.seg button.is-active {
  background: #fff;
  color: #1d1d1f;
  font-weight: 600;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.1);
}

@media (max-width: 1180px) {
  .grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .span-4 {
    grid-column: 1 / -1;
  }
}
@media (max-width: 900px) {
  .gw-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .wan-row {
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .wan-meta {
    grid-column: 1 / -1;
    order: 3;
  }
}
@media (max-width: 620px) {
  .grid {
    grid-template-columns: 1fr;
  }
  .span-2 {
    grid-column: 1 / -1;
  }
}
</style>
