<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { NButton, NInput, NInputNumber, NSwitch } from 'naive-ui'
import * as api from '../../api/ngg'
import type { GatewayConfig, GatewayDNS } from '../../api/ngg'
import { useApiAction } from '../../composables/useApi'

const { isBusy, run } = useApiAction()

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

function isIP(value: string): boolean {
  if (IPV4.test(value)) return true
  return value.includes(':') && /^[0-9a-fA-F:.]+$/.test(value)
}

interface HostRow {
  key: string
  value: string
}

const MODES: Array<{ id: GatewayDNS['mode']; label: string }> = [
  { id: 'forward', label: '转发上游' },
  { id: 'recursive', label: '本地递归' },
]

const config = ref<GatewayConfig | null>(null)
const loading = ref(false)

const form = reactive({
  mode: 'forward' as GatewayDNS['mode'],
  serversText: '',
  fallbackText: '',
  doh: '',
  cacheEnabled: true,
  cacheSize: 10000 as number | null,
  cacheTtl: 300 as number | null,
  localDomain: '',
  blocklistText: '',
  dnssec: false,
  logQueries: false,
})
const hosts = ref<HostRow[]>([])

const servers = computed(() => splitList(form.serversText))
const stats = computed(() => [
  { label: '解析模式', value: form.mode === 'recursive' ? '本地递归' : '转发上游' },
  { label: '上游 DNS', value: String(servers.value.length) },
  { label: '缓存', value: form.cacheEnabled ? '已开启' : '已关闭' },
  { label: 'DNSSEC', value: form.dnssec ? '已开启' : '已关闭' },
])

function splitList(text: string): string[] {
  return text
    .split(/[,，\s]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

function syncFrom(cfg: GatewayConfig): void {
  const dns = cfg.dns
  form.mode = dns.mode === 'recursive' ? 'recursive' : 'forward'
  form.serversText = Array.isArray(dns.servers) ? dns.servers.join(', ') : ''
  form.fallbackText = Array.isArray(dns.fallback) ? dns.fallback.join(', ') : ''
  form.doh = dns.doh ?? ''
  form.cacheEnabled = dns.cache_enabled !== false
  form.cacheSize =
    typeof dns.cache_size === 'number' && dns.cache_size > 0 ? dns.cache_size : 10000
  form.cacheTtl = typeof dns.cache_ttl === 'number' && dns.cache_ttl > 0 ? dns.cache_ttl : 300
  form.localDomain = dns.local_domain ?? ''
  form.blocklistText = Array.isArray(dns.blocklist) ? dns.blocklist.join(', ') : ''
  form.dnssec = dns.dnssec === true
  form.logQueries = dns.log_queries === true
  const entries = dns.hosts && typeof dns.hosts === 'object' ? dns.hosts : {}
  hosts.value = Object.keys(entries).map((key) => ({ key, value: String(entries[key]) }))
}

function addHost(): void {
  hosts.value.push({ key: '', value: '' })
}

function removeHost(index: number): void {
  hosts.value.splice(index, 1)
}

function isDomain(value: string): boolean {
  const text = value.trim().replace(/\.$/, '')
  if (!text || text.length > 253) return false
  return text.split('.').every((label) => {
    if (!label || label.length > 63) return false
    if (label.startsWith('-') || label.endsWith('-')) return false
    return /^[a-z0-9_-]+$/.test(label)
  })
}

function buildHosts(): Record<string, string> | null {
  const result: Record<string, string> = {}
  for (const row of hosts.value) {
    const key = row.key.trim().toLowerCase()
    const value = row.value.trim()
    if (!key && !value) continue
    if (!key || !value) return null
    if (!isDomain(key)) return null
    if (!IPV4.test(value)) return null
    if (result[key]) return null
    result[key] = value
  }
  return result
}

function validate(): string | null {
  if (!servers.value.length) return '至少填写一条上游 DNS'
  for (const server of servers.value) {
    if (!isIP(server)) return `DNS 地址格式无效：${server}`
  }
  for (const server of splitList(form.fallbackText)) {
    if (!isIP(server)) return `备用 DNS 格式无效：${server}`
  }
  const doh = form.doh.trim()
  if (doh && !/^https:\/\/\S+$/i.test(doh)) return 'DoH 端点需以 https:// 开头'
  const size = form.cacheSize
  if (size === null || Number.isNaN(size) || size < 100 || size > 1000000)
    return '缓存条数需在 100–1000000'
  const ttl = form.cacheTtl
  if (ttl === null || Number.isNaN(ttl) || ttl < 10 || ttl > 86400)
    return 'TTL 需在 10–86400 秒'
  if (!isDomain(form.localDomain)) return '本地域名后缀格式无效'
  if (buildHosts() === null) return 'hosts 记录需为合法域名与 IPv4'
  for (const entry of splitList(form.blocklistText)) {
    if (!isDomain(entry.replace(/^\*\./, ''))) return `拦截域名格式无效：${entry}`
  }
  return null
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await api.getGatewayConfig()
    config.value = res.config
    syncFrom(res.config)
  } catch {
    /* 错误提示已由拦截器处理 */
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

  const dns: GatewayDNS = {
    ...base.dns,
    mode: form.mode,
    servers: servers.value,
    fallback: splitList(form.fallbackText),
    doh: form.doh.trim(),
    cache_enabled: form.cacheEnabled,
    cache_size: Number(form.cacheSize),
    cache_ttl: Number(form.cacheTtl),
    local_domain: form.localDomain.trim().toLowerCase(),
    hosts: buildHosts() ?? {},
    blocklist: splitList(form.blocklistText).map((entry) => entry.toLowerCase()),
    dnssec: form.dnssec,
    log_queries: form.logQueries,
  }

  const saved = await run('save', () => api.saveGatewayConfig({ ...base, dns }), '已保存')
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
    <header class="gw-head">
      <h2 class="gw-title">网关 DNS</h2>
      <div class="gw-head__ops">
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
        <h3 class="gw-card__title">解析模式</h3>
      </div>
      <div class="grid">
        <div class="field span-2">
          <div class="label">模式</div>
          <div class="seg">
            <button
              v-for="m in MODES"
              :key="m.id"
              type="button"
              :class="{ 'is-active': form.mode === m.id }"
              @click="form.mode = m.id"
            >{{ m.label }}</button>
          </div>
        </div>
        <div class="field">
          <div class="label">主上游 DNS</div>
          <NInput v-model:value="form.serversText" size="small" placeholder="1.1.1.1, 8.8.8.8" />
        </div>
        <div class="field">
          <div class="label">备用 DNS</div>
          <NInput v-model:value="form.fallbackText" size="small" placeholder="9.9.9.9" />
        </div>
        <div class="field span-2">
          <div class="label">DoH 端点</div>
          <NInput v-model:value="form.doh" size="small" placeholder="https://dns.example/dns-query" />
        </div>
      </div>
    </section>

    <section class="gw-card">
      <div class="gw-card__head">
        <h3 class="gw-card__title">缓存与本地</h3>
        <div class="inline">
          <span class="label">缓存</span>
          <NSwitch v-model:value="form.cacheEnabled" size="small" />
        </div>
      </div>
      <div class="grid">
        <div class="field">
          <div class="label">缓存条数</div>
          <NInputNumber v-model:value="form.cacheSize" size="small" :min="100" :max="1000000" />
        </div>
        <div class="field">
          <div class="label">TTL 秒</div>
          <NInputNumber v-model:value="form.cacheTtl" size="small" :min="10" :max="86400" />
        </div>
        <div class="field span-2">
          <div class="label">本地域名后缀</div>
          <NInput v-model:value="form.localDomain" size="small" :maxlength="253" placeholder="lan" />
        </div>
      </div>
    </section>

    <section class="gw-card">
      <div class="gw-card__head">
        <h3 class="gw-card__title">静态解析</h3>
        <NButton size="tiny" type="primary" @click="addHost">新增 hosts</NButton>
      </div>

      <div v-if="hosts.length" class="hosts">
        <div v-for="(row, index) in hosts" :key="index" class="host-row">
          <NInput v-model:value="row.key" size="small" :maxlength="253" placeholder="nas.lan" />
          <NInput v-model:value="row.value" size="small" placeholder="192.168.10.20" />
          <NButton size="tiny" quaternary type="error" @click="removeHost(index)">删除</NButton>
        </div>
      </div>
      <div v-else class="empty">—</div>

      <div class="grid hosts-grid">
        <div class="field span-2">
          <div class="label">拦截域名</div>
          <NInput v-model:value="form.blocklistText" size="small" placeholder="ads.example.com" />
        </div>
      </div>

      <div class="switches">
        <div class="inline">
          <span class="label">DNSSEC</span>
          <NSwitch v-model:value="form.dnssec" size="small" />
        </div>
        <div class="inline">
          <span class="label">查询日志</span>
          <NSwitch v-model:value="form.logQueries" size="small" />
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
  margin-bottom: 8px;
}
.gw-card:last-child {
  margin-bottom: 0;
}
.gw-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;
}
.gw-card__title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  letter-spacing: -0.01em;
}
.inline {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px 12px;
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
  padding: 3px 12px;
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

.hosts {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 8px;
}
.host-row {
  display: grid;
  grid-template-columns: minmax(0, 1.2fr) minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
}
.empty {
  font-size: 11.5px;
  color: #a1a1a6;
  padding: 2px 0 8px;
}
.hosts-grid {
  margin-bottom: 8px;
}

.switches {
  display: flex;
  align-items: center;
  gap: 24px;
  flex-wrap: wrap;
  border-top: 1px solid #f0f0f2;
  padding-top: 8px;
}
.switches .label {
  margin-bottom: 0;
}

@media (max-width: 900px) {
  .gw-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 620px) {
  .grid {
    grid-template-columns: 1fr;
  }
  .span-2 {
    grid-column: 1 / -1;
  }
  .host-row {
    grid-template-columns: minmax(0, 1fr) auto;
  }
}
</style>
