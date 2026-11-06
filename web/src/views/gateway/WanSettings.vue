<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { NButton, NInput, NInputNumber, NSelect, NSwitch } from 'naive-ui'
import * as api from '../../api/ngg'
import type { GatewayConfig, GatewayStatus, GatewayWAN } from '../../api/ngg'
import { useApiAction } from '../../composables/useApi'

const { isBusy, run } = useApiAction()

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

const status = ref<GatewayStatus | null>(null)
const config = ref<GatewayConfig | null>(null)
const loading = ref(false)
const dnsText = ref('')

const form = reactive({
  mode: 'dhcp' as GatewayWAN['mode'],
  interface: '',
  address: '',
  gateway: '',
  mtu: 1500 as number | null,
  ipv6: false,
  username: '',
  password: '',
  passwordSet: false,
})

const MODES: Array<{ id: GatewayWAN['mode']; label: string }> = [
  { id: 'dhcp', label: 'DHCP 自动' },
  { id: 'static', label: '静态 IP' },
  { id: 'pppoe', label: 'PPPoE 拨号' },
]

const interfaces = computed(() => status.value?.interfaces ?? [])
const interfaceOptions = computed(() => {
  const options = interfaces.value.map((i) => ({
    label: i.addresses.length ? `${i.name} · ${i.addresses[0]}` : i.name,
    value: i.name,
  }))
  if (form.interface && !options.some((o) => o.value === form.interface)) {
    options.unshift({ label: form.interface, value: form.interface })
  }
  return options
})

const linkUp = computed(() => Boolean(status.value?.default_interface))

const defaultMtu = computed(() => {
  const name = status.value?.default_interface
  const iface = interfaces.value.find((i) => i.name === name)
  return iface && iface.mtu ? String(iface.mtu) : '—'
})

const statRows = computed(() => [
  { label: '上行接口', value: status.value?.default_interface || '—' },
  { label: '上行地址', value: status.value?.default_address || '—' },
  { label: '默认网关', value: status.value?.default_gateway || '—' },
  { label: 'MTU', value: defaultMtu.value },
])

const passwordPlaceholder = computed(() =>
  form.passwordSet ? '已保存，留空则不修改' : 'PPPoE 密码',
)

function syncFrom(cfg: GatewayConfig): void {
  const wan = cfg.wan
  form.mode = wan.mode ?? 'dhcp'
  form.interface = wan.interface ?? ''
  form.address = wan.address ?? ''
  form.gateway = wan.gateway ?? ''
  form.mtu = typeof wan.mtu === 'number' && wan.mtu > 0 ? wan.mtu : 1500
  form.ipv6 = wan.ipv6 === true
  form.username = wan.username ?? ''
  form.password = ''
  form.passwordSet = wan.password_set === true
  dnsText.value = Array.isArray(wan.dns) ? wan.dns.join(', ') : ''
}

function dnsList(): string[] {
  return dnsText.value
    .split(/[,，\s]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

function validate(): string | null {
  if (!form.interface.trim()) return '请选择接口'
  const mtu = form.mtu
  if (mtu === null || Number.isNaN(mtu)) return 'MTU 不能为空'
  if (mtu < 576 || mtu > 9000) return 'MTU 需在 576–9000 之间'
  if (form.mode === 'static') {
    if (!IPV4.test(form.address.trim())) return 'IP 地址格式无效'
    if (!IPV4.test(form.gateway.trim())) return '默认网关格式无效'
  }
  if (form.mode === 'pppoe') {
    if (!form.username.trim()) return 'PPPoE 账号不能为空'
    if (!form.password && !form.passwordSet) return 'PPPoE 密码不能为空'
  }
  for (const dns of dnsList()) {
    if (!IPV4.test(dns)) return `DNS 地址格式无效：${dns}`
  }
  return null
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const [cfg, st] = await Promise.allSettled([api.getGatewayConfig(), api.getGatewayStatus()])
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

  const wan: GatewayWAN = {
    ...base.wan,
    mode: form.mode,
    interface: form.interface.trim(),
    mtu: Number(form.mtu),
    dns: dnsList(),
    ipv6: form.ipv6,
    address: form.mode === 'static' ? form.address.trim() : base.wan.address,
    gateway: form.mode === 'static' ? form.gateway.trim() : base.wan.gateway,
  }
  if (form.mode === 'pppoe') {
    wan.username = form.username.trim()
    if (form.password) wan.password = form.password
    wan.password_set = form.passwordSet
  } else {
    delete wan.password
  }

  const saved = await run(
    'save',
    () => api.saveGatewayConfig({ ...base, wan }),
    '已保存',
  )
  if (saved) {
    config.value = saved.config
    syncFrom(saved.config)
  }
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="gw">
    <div class="gw-head">
      <h2 class="page-title">WAN 设置</h2>
      <div class="gw-head__right">
        <span class="gw-pill">
          <i class="gw-dot" :class="linkUp ? 'is-on' : 'is-off'" />
          网卡 {{ interfaces.length }}
        </span>
        <button class="gw-refresh" type="button" :disabled="loading" @click="load">
          {{ loading ? '刷新中' : '刷新' }}
        </button>
      </div>
    </div>

    <section class="gw-stats">
      <div v-for="row in statRows" :key="row.label" class="gw-stat">
        <div class="gw-stat__label">{{ row.label }}</div>
        <div class="gw-stat__value" :title="row.value">{{ row.value }}</div>
      </div>
    </section>

    <section class="gw-card">
      <div class="gw-card__head">
        <h3 class="gw-card__title">连接方式</h3>
        <div class="gw-seg" role="group" aria-label="连接方式">
          <button
            v-for="m in MODES"
            :key="m.id"
            type="button"
            :class="{ 'is-active': form.mode === m.id }"
            @click="form.mode = m.id"
          >{{ m.label }}</button>
        </div>
      </div>

      <div class="gw-form">
        <div class="gw-field">
          <div class="gw-label">接口</div>
          <NSelect
            v-model:value="form.interface"
            size="small"
            :options="interfaceOptions"
            placeholder="选择接口"
          />
        </div>
        <div class="gw-field">
          <div class="gw-label">MTU</div>
          <NInputNumber v-model:value="form.mtu" size="small" :min="576" :max="9000" />
        </div>

        <template v-if="form.mode === 'static'">
          <div class="gw-field">
            <div class="gw-label">IP 地址</div>
            <NInput v-model:value="form.address" size="small" placeholder="192.168.1.10" />
          </div>
          <div class="gw-field">
            <div class="gw-label">默认网关</div>
            <NInput v-model:value="form.gateway" size="small" placeholder="192.168.1.1" />
          </div>
        </template>

        <template v-else-if="form.mode === 'pppoe'">
          <div class="gw-field">
            <div class="gw-label">账号</div>
            <NInput v-model:value="form.username" size="small" />
          </div>
          <div class="gw-field">
            <div class="gw-label">密码</div>
            <NInput
              v-model:value="form.password"
              size="small"
              type="password"
              show-password-on="click"
              :placeholder="passwordPlaceholder"
            />
          </div>
        </template>

        <div class="gw-field span-2">
          <div class="gw-label">DNS</div>
          <NInput v-model:value="dnsText" size="small" placeholder="1.1.1.1, 8.8.8.8" />
        </div>
        <div class="gw-field">
          <div class="gw-label">IPv6</div>
          <NSwitch v-model:value="form.ipv6" size="small" />
        </div>
      </div>

      <div class="gw-foot">
        <NButton type="primary" size="small" :loading="isBusy('save')" @click="save">保存</NButton>
      </div>
    </section>
  </div>
</template>

<style scoped>
.gw {
  font-family: -apple-system, BlinkMacSystemFont, 'SF Pro Text', 'PingFang SC',
    'Microsoft YaHei', sans-serif;
  background: #f5f5f7;
  border-radius: 18px;
  padding: 16px;
  color: #1d1d1f;
  min-height: calc(100vh - 120px);
}

.gw-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.gw-head__right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.gw-pill {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-size: 12.5px;
  font-weight: 600;
  padding: 6px 12px;
  border-radius: 999px;
  background: #fff;
  border: 1px solid #e8e8ed;
  font-variant-numeric: tabular-nums;
}
.gw-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #8e8e93;
  flex: 0 0 auto;
}
.gw-dot.is-on {
  background: #34c759;
  box-shadow: 0 0 0 3px rgba(52, 199, 89, 0.15);
}
.gw-dot.is-off {
  background: #ff9f0a;
  box-shadow: 0 0 0 3px rgba(255, 159, 10, 0.14);
}
.gw-refresh {
  appearance: none;
  border: 1px solid #e8e8ed;
  background: #fff;
  color: #0071e3;
  font: inherit;
  font-size: 12.5px;
  font-weight: 600;
  padding: 7px 14px;
  border-radius: 10px;
  cursor: pointer;
}
.gw-refresh:disabled {
  opacity: 0.55;
  cursor: default;
}

.gw-stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
  margin-bottom: 12px;
}
.gw-stat,
.gw-card {
  background: #fff;
  border: 1px solid #e8e8ed;
  border-radius: 13px;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.03);
}
.gw-stat {
  padding: 11px 13px 12px;
  min-width: 0;
}
.gw-stat__label {
  font-size: 12px;
  color: #6e6e73;
  font-weight: 500;
}
.gw-stat__value {
  font-size: 20px;
  font-weight: 650;
  letter-spacing: -0.02em;
  margin-top: 5px;
  line-height: 1.25;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.gw-card {
  padding: 14px 16px 16px;
}
.gw-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 14px;
}
.gw-card__title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  letter-spacing: -0.01em;
}

.gw-seg {
  display: inline-flex;
  gap: 2px;
  padding: 3px;
  background: #e9e9ed;
  border-radius: 10px;
}
.gw-seg button {
  appearance: none;
  border: 0;
  background: transparent;
  color: #6e6e73;
  font: inherit;
  font-size: 12.5px;
  padding: 5px 12px;
  border-radius: 7px;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.18s ease, color 0.18s ease, box-shadow 0.18s ease;
}
.gw-seg button:hover {
  color: #1d1d1f;
}
.gw-seg button.is-active {
  background: #fff;
  color: #1d1d1f;
  font-weight: 600;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

.gw-form {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 16px;
}
.gw-field {
  min-width: 0;
}
.gw-field.span-2 {
  grid-column: 1 / -1;
}
.gw-label {
  font-size: 12.5px;
  color: #6e6e73;
  margin-bottom: 5px;
  font-weight: 500;
}

.gw-foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
  padding-top: 13px;
  border-top: 1px solid #f0f0f2;
}

@media (max-width: 1080px) {
  .gw-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 720px) {
  .gw-form,
  .gw-stats {
    grid-template-columns: 1fr;
  }
}
</style>
