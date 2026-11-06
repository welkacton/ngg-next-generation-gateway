<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { NButton, NInput, NInputNumber, NSelect, NSwitch } from 'naive-ui'
import * as api from '../../api/ngg'
import type { GatewayConfig, GatewayLAN, GatewayStatus } from '../../api/ngg'
import { useApiAction } from '../../composables/useApi'

const { isBusy, run } = useApiAction()

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

const NETMASKS = [
  { label: '/24 · 255.255.255.0', value: '255.255.255.0' },
  { label: '/16 · 255.255.0.0', value: '255.255.0.0' },
  { label: '/8 · 255.0.0.0', value: '255.0.0.0' },
]

const status = ref<GatewayStatus | null>(null)
const config = ref<GatewayConfig | null>(null)
const loading = ref(false)

const form = reactive({
  interface: '',
  address: '',
  netmask: '255.255.255.0',
  dhcpEnabled: true,
  dhcpStart: '',
  dhcpEnd: '',
  leaseMinutes: 1440 as number | null,
  domain: '',
})

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
const maskOptions = computed(() => {
  const options = [...NETMASKS]
  if (form.netmask && !options.some((o) => o.value === form.netmask)) {
    options.unshift({ label: form.netmask, value: form.netmask })
  }
  return options
})

const statRows = computed(() => [
  { label: 'LAN 接口', value: config.value?.lan.interface || '—' },
  { label: 'LAN 地址', value: config.value?.lan.address || '—' },
  {
    label: 'DHCP',
    value: config.value ? (config.value.lan.dhcp_enabled ? '已开启' : '已关闭') : '—',
  },
  { label: '租约数', value: '—' },
])

function ipToInt(ip: string): number | null {
  if (!IPV4.test(ip)) return null
  const parts = ip.split('.').map((p) => Number(p))
  return ((parts[0] << 24) | (parts[1] << 16) | (parts[2] << 8) | parts[3]) >>> 0
}

function isContiguousMask(mask: string): boolean {
  const value = ipToInt(mask)
  if (value === null || value === 0) return false
  const inverted = (~value) >>> 0
  return ((inverted + 1) & inverted) === 0
}

function syncFrom(cfg: GatewayConfig): void {
  const lan = cfg.lan
  form.interface = lan.interface ?? ''
  form.address = lan.address ?? ''
  form.netmask = lan.netmask || '255.255.255.0'
  form.dhcpEnabled = lan.dhcp_enabled === true
  form.dhcpStart = lan.dhcp_start ?? ''
  form.dhcpEnd = lan.dhcp_end ?? ''
  form.leaseMinutes = typeof lan.lease_minutes === 'number' && lan.lease_minutes > 0
    ? lan.lease_minutes
    : 1440
  form.domain = lan.domain ?? ''
}

function validate(): string | null {
  if (!form.interface.trim()) return '请选择接口'
  const address = form.address.trim()
  if (!IPV4.test(address)) return 'LAN 地址格式无效'
  const netmask = form.netmask.trim()
  if (!IPV4.test(netmask)) return '子网掩码格式无效'
  if (!isContiguousMask(netmask)) return '子网掩码不合法'

  const lease = form.leaseMinutes
  if (lease === null || Number.isNaN(lease)) return '租约分钟数不能为空'
  if (lease < 1 || lease > 525600) return '租约需在 1–525600 分钟之间'

  if (form.dhcpEnabled) {
    const start = form.dhcpStart.trim()
    const end = form.dhcpEnd.trim()
    if (!IPV4.test(start)) return '地址池起始地址格式无效'
    if (!IPV4.test(end)) return '地址池结束地址格式无效'
    const maskInt = ipToInt(netmask) ?? 0
    const netInt = (ipToInt(address) ?? 0) & maskInt
    const startInt = ipToInt(start) ?? 0
    const endInt = ipToInt(end) ?? 0
    if ((startInt & maskInt) !== netInt) return '起始地址不在 LAN 网段内'
    if ((endInt & maskInt) !== netInt) return '结束地址不在 LAN 网段内'
    if (startInt >= endInt) return '起始地址需小于结束地址'
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

  const lan: GatewayLAN = {
    ...base.lan,
    interface: form.interface.trim(),
    address: form.address.trim(),
    netmask: form.netmask.trim(),
    dhcp_enabled: form.dhcpEnabled,
    dhcp_start: form.dhcpStart.trim(),
    dhcp_end: form.dhcpEnd.trim(),
    lease_minutes: Number(form.leaseMinutes),
    domain: form.domain.trim(),
  }

  const saved = await run(
    'save',
    () => api.saveGatewayConfig({ ...base, lan }),
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
      <h2 class="page-title">LAN 设置</h2>
      <div class="gw-head__right">
        <span class="gw-pill">
          <i class="gw-dot" :class="form.interface ? 'is-on' : 'is-off'" />
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
        <h3 class="gw-card__title">接口参数</h3>
        <div class="gw-inline">
          <span class="gw-label">DHCP</span>
          <NSwitch v-model:value="form.dhcpEnabled" size="small" />
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
          <div class="gw-label">LAN 地址</div>
          <NInput v-model:value="form.address" size="small" placeholder="192.168.10.1" />
        </div>
        <div class="gw-field">
          <div class="gw-label">子网掩码</div>
          <NSelect
            v-model:value="form.netmask"
            size="small"
            filterable
            tag
            :options="maskOptions"
            placeholder="255.255.255.0"
          />
        </div>
        <div class="gw-field">
          <div class="gw-label">租约分钟数</div>
          <NInputNumber v-model:value="form.leaseMinutes" size="small" :min="1" :max="525600" />
        </div>

        <div class="gw-field span-2">
          <div class="gw-label">地址池</div>
          <div class="gw-pair">
            <NInput v-model:value="form.dhcpStart" size="small" placeholder="192.168.10.100" />
            <NInput v-model:value="form.dhcpEnd" size="small" placeholder="192.168.10.200" />
          </div>
        </div>

        <div class="gw-field span-2">
          <div class="gw-label">本地域名</div>
          <NInput v-model:value="form.domain" size="small" placeholder="lan" />
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
.gw-inline {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.gw-inline .gw-label {
  margin-bottom: 0;
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
.gw-pair {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
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
  .gw-stats,
  .gw-pair {
    grid-template-columns: 1fr;
  }
}
</style>
