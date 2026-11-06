<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCollapse,
  NCollapseItem,
  NInput,
  NInputNumber,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
} from 'naive-ui'
import * as api from '../../api/ngg'
import type {
  ControlCenterState,
  ControlStep,
  GatewayMaster,
  GlobalTakeoverResult,
} from '../../api/ngg'
import { useApiAction } from '../../composables/useApi'

const { isBusy, run } = useApiAction()

const state = ref<ControlCenterState | null>(null)
const loading = ref(false)
const globalStatus = ref<GlobalTakeoverResult | null>(null)
const commands = ref<string[]>([])
const driverMsg = ref('')
const lastSteps = ref<ControlStep[]>([])

const form = reactive({
  adapterName: 'NGG',
  adapterMetric: 5,
  captureAll: true,
  autoStart: false,
  bypassPrivate: true,
  bypassText: '',
  egressInterface: '',
  wanId: '',
  uplinkMode: 'gateway-wan' as 'gateway-wan' | 'pipeline',
})

const adapters = computed(() => state.value?.adapters ?? [])
const steps = computed(() => (lastSteps.value.length ? lastSteps.value : state.value?.steps ?? []))
const tunOpen = computed(() => !!state.value?.tun.adapter.open)
const captureActive = computed(() => !!state.value?.datapath.capture_active)
const driver = computed(() => state.value?.tun.capabilities.driver)

const adapterOptions = computed(() =>
  adapters.value
    .filter((a) => !a.loopback)
    .map((a) => ({
      label: `${a.name}${a.is_ngg ? ' · NGG' : ''}${a.virtual && !a.is_ngg ? ' · 虚拟' : ''}${
        a.ipv4.length ? ' · ' + a.ipv4[0] : ' · 无 IPv4'
      }`,
      value: a.name,
    })),
)

const egressOptions = computed(() =>
  adapters.value
    .filter((a) => !a.loopback && !a.is_ngg)
    .map((a) => ({
      label: `${a.name}${a.default ? ' · 当前默认出口' : ''}${a.virtual ? ' · 虚拟' : ''}${
        a.ipv4.length ? ' · ' + a.ipv4[0] : ''
      }`,
      value: a.name,
    })),
)

const wanOptions = computed(() =>
  (state.value?.wans ?? [])
    .filter((w) => w.enabled)
    .map((w) => ({
      label: `${w.name || w.id.toUpperCase()} · ${w.interface || '未绑定网卡'} · ${
        w.route === 'proxy' ? '国际代理' : '本地直出'
      }`,
      value: w.id,
    })),
)

const stats = computed(() => {
  const st = state.value
  const dp = st?.datapath
  return [
    {
      label: '虚拟网卡',
      value: st?.tun.adapter.adapter || st?.master.adapter_name || '—',
      note: st?.tun.adapter.open ? '已创建' : '未创建',
      tone: st?.tun.adapter.open ? 'ok' : 'warn',
    },
    {
      label: '流量接管',
      value: dp?.capture_active ? '已接管' : '未接管',
      note: dp ? (dp.elevated ? '已提权' : '未提权') : '',
      tone: dp?.capture_active ? 'ok' : 'warn',
    },
    {
      label: '默认路由',
      value: dp?.default_route_via_adapter ? '经 NGG' : '系统默认',
      note: dp && dp.adapter_metric >= 0 ? '接口优先级 ' + dp.adapter_metric : '',
      tone: dp?.default_route_via_adapter ? 'ok' : 'warn',
    },
    {
      label: 'WAN 出口',
      value: st?.egress.interface || '系统默认',
      note: st?.egress.interface ? '本地运营商' : '未指定',
      tone: st?.egress.interface ? 'ok' : 'warn',
    },
  ]
})

function stepClass(s: ControlStep): string {
  return `step step--${s.status}`
}

function stepLabel(s: ControlStep): string {
  return { ok: '就绪', warn: '待处理', fail: '失败', skip: '未开始' }[s.status] ?? s.status
}

function syncFrom(st: ControlCenterState): void {
  // 自动识别：网卡已经创建时以系统里的真实名字为准，未创建才回落到配置值。
  const live = st.tun.adapter.open && st.tun.adapter.adapter ? st.tun.adapter.adapter : ''
  form.adapterName = live || st.master.adapter_name || st.tun.config_name || 'NGG'
  form.adapterMetric = typeof st.master.adapter_metric === 'number' ? st.master.adapter_metric : 5
  form.captureAll = st.master.capture_all !== false
  form.autoStart = st.master.auto_start === true
  form.bypassPrivate = st.master.bypass_private !== false
  form.bypassText = Array.isArray(st.master.bypass_cidrs) ? st.master.bypass_cidrs.join(', ') : ''
  form.egressInterface = st.egress.interface || ''
  form.wanId = st.master.uplink?.wan_id || ''
  form.uplinkMode = st.master.uplink?.mode === 'pipeline' ? 'pipeline' : 'gateway-wan'
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const st = await api.getControlCenter()
    state.value = st
    syncFrom(st)
    lastSteps.value = []
    void api.getGlobalStatus().then((g) => { globalStatus.value = g }).catch(() => {})
  } finally {
    loading.value = false
  }
}

async function refreshOnly(): Promise<void> {
  const st = await api.getControlCenter().catch(() => null)
  if (st) {
    state.value = st
    syncFrom(st)
  }
}

function bypassCIDRs(): string[] {
  return form.bypassText
    .split(/[,，\s]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

async function saveSwitches(): Promise<void> {
  const base = state.value?.master
  if (!base) return
  const payload: GatewayMaster = {
    ...base,
    enabled: true,
    adapter_name: form.adapterName.trim() || 'NGG',
    adapter_metric: Number(form.adapterMetric ?? 5),
    capture_all: form.captureAll,
    auto_start: form.autoStart,
    bypass_private: form.bypassPrivate,
    bypass_cidrs: bypassCIDRs(),
    uplink: { ...base.uplink, mode: form.uplinkMode, wan_id: form.wanId },
  }
  const saved = await run('save', () => api.saveGatewayMaster(payload), '开关已保存')
  if (saved) await refreshOnly()
}

async function startAll(): Promise<void> {
  commands.value = []
  const res = await run('start', () =>
    api.startControl({
      adapter_name: form.adapterName.trim() || 'NGG',
      adapter_metric: Number(form.adapterMetric ?? 5),
      capture_all: true,
      bypass_private: form.bypassPrivate,
      auto_start: form.autoStart,
      egress_interface: form.egressInterface || undefined,
      wan_id: form.wanId || undefined,
    }),
  )
  if (!res) return
  lastSteps.value = res.steps ?? []
  commands.value = res.datapath?.commands ?? []
  if (res.control) {
    state.value = res.control
    syncFrom(res.control)
  }
  if (res.ok) window.$message?.success('已按「网卡 → 全量接管 → WAN 出口」顺序启动')
  else window.$message?.warning('有步骤未完成，请查看下方步骤说明', { duration: 8000 })
}

async function stopAll(): Promise<void> {
  const ok = await new Promise<boolean>((resolve) => {
    let settled = false
    const finish = (v: boolean): void => {
      if (settled) return
      settled = true
      resolve(v)
    }
    window.$dialog?.warning({
      title: '停止接管',
      content: '会先移除默认路由接管，再销毁 NGG 虚拟网卡。期间网络会短暂中断。',
      positiveText: '停止接管',
      negativeText: '取消',
      onPositiveClick: () => finish(true),
      onNegativeClick: () => finish(false),
      onClose: () => finish(false),
    })
  })
  if (!ok) return
  const res = await run('stop', () => api.stopControl(), '已恢复系统默认路由')
  if (!res) return
  lastSteps.value = res.steps ?? []
  if (res.control) {
    state.value = res.control
    syncFrom(res.control)
  }
}

async function installDriver(): Promise<void> {
  driverMsg.value = ''
  const res = await run('driver', () => api.installTunDriver())
  if (res) {
    driverMsg.value = res.driver?.message || '驱动已就绪'
    await refreshOnly()
  }
}

async function detachAdapter(): Promise<void> {
  const res = await run('detach', () => api.detachTun(), '虚拟网卡已销毁')
  if (res) await refreshOnly()
}

async function applyEgress(): Promise<void> {
  if (!form.egressInterface) {
    window.$message?.warning('请先选择承载出口的物理网卡')
    return
  }
  const res = await run('egress', () => api.setEgressInterface(form.egressInterface), '出口已切换')
  if (res) {
    await refreshOnly()
    window.$message?.info('出口绑定即时生效，无需重启网络引擎', { duration: 5000 })
  }
}

async function attachProxy(): Promise<void> {
  const res = await run('proxy', () => api.attachGlobalProxy(), '系统代理已接管')
  if (res) await refreshOnly()
}

async function detachProxy(): Promise<void> {
  const res = await run('proxy-off', () => api.detachGlobalProxy(), '系统代理已解除')
  if (res) await refreshOnly()
}

async function copyCommands(): Promise<void> {
  if (!commands.value.length) return
  try {
    await navigator.clipboard.writeText(commands.value.join('\n'))
    window.$message?.success('已复制')
  } catch {
    window.$message?.warning('浏览器拒绝了剪贴板访问，请手动复制')
  }
}

function fmtBytes(v?: number): string {
  if (!v) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let n = Number(v)
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i += 1
  }
  return `${i === 0 ? Math.round(n) : n.toFixed(2)} ${units[i]}`
}

onMounted(load)
</script>

<template>
  <div class="cc">
    <header class="cc-head">
      <div>
        <h2 class="cc-title">控制中心</h2>
        <p class="cc-sub">
          启动 NGG 虚拟网卡 → 强制接管全部网卡流量 → 在 WAN 出口选择物理网卡走本地运营商
        </p>
      </div>
      <NSpace :size="8">
        <NButton size="small" quaternary :loading="loading" @click="load">刷新</NButton>
        <NButton size="small" type="primary" :loading="isBusy('start')" @click="startAll">
          一键启动
        </NButton>
        <NButton size="small" :loading="isBusy('stop')" @click="stopAll">停止接管</NButton>
      </NSpace>
    </header>

    <section class="cc-stats">
      <div v-for="s in stats" :key="s.label" class="stat" :class="`stat--${s.tone}`">
        <div class="stat__label">{{ s.label }}</div>
        <div class="stat__value" :title="s.value">{{ s.value }}</div>
        <div class="stat__note">{{ s.note }}</div>
      </div>
    </section>

    <section class="cc-steps">
      <div v-for="s in steps" :key="s.key" :class="stepClass(s)">
        <div class="step__idx">{{ s.order }}</div>
        <div class="step__body">
          <div class="step__title">
            {{ s.title }}
            <span class="step__tag">{{ stepLabel(s) }}</span>
          </div>
          <div class="step__detail" :title="s.detail">{{ s.detail }}</div>
        </div>
      </div>
    </section>

    <!-- ① 虚拟网卡 -->
    <section class="cc-card">
      <div class="cc-card__head">
        <h3 class="cc-card__title">① NGG 虚拟网卡</h3>
        <NSpace :size="6" align="center">
          <NTag
            size="small"
            :bordered="false"
            :type="driver?.present && driver?.signed ? 'success' : 'warning'"
          >
            驱动 {{ driver?.present ? 'v' + (driver?.version || '?') : '未安装' }}
            {{ driver?.signed ? '· 签名有效' : '' }}
          </NTag>
          <NButton size="tiny" :loading="isBusy('driver')" @click="installDriver">
            {{ driver?.present ? '重新校验驱动' : '下载并安装驱动' }}
          </NButton>
        </NSpace>
      </div>

      <div class="grid">
        <div class="field">
          <div class="label">网卡名称（自动识别已创建网卡）</div>
          <NSelect
            v-model:value="form.adapterName"
            size="small"
            filterable
            tag
            :options="adapterOptions"
            placeholder="NGG"
          />
        </div>
        <div class="field">
          <div class="label">接口优先级（越小越优先）</div>
          <NInputNumber v-model:value="form.adapterMetric" size="small" :min="0" :max="9999" />
        </div>
        <div class="field field--wide">
          <div class="label">本机识别到的网卡</div>
          <div class="adapter-list">
            <span
              v-for="a in adapters.filter((x) => !x.loopback)"
              :key="a.name"
              class="adapter-chip"
              :class="{ 'is-ngg': a.is_ngg, 'is-default': a.default, 'is-down': !a.up }"
              :title="`${a.name} · ${a.detail} · MTU ${a.mtu}`"
            >
              {{ a.name }}
              <em v-if="a.default">默认</em>
              <em v-if="a.is_ngg">NGG</em>
              <em v-else-if="a.virtual">虚拟</em>
            </span>
            <span v-if="!adapters.length" class="hint">未枚举到网卡</span>
          </div>
        </div>
      </div>

      <div v-if="state?.tun.adapter.open" class="tun-facts">
        <div><span>地址</span><strong>{{ state.tun.adapter.ipv4 || '—' }}</strong></div>
        <div><span>MTU</span><strong>{{ state.tun.adapter.mtu }}</strong></div>
        <div><span>协议栈</span><strong>{{ state.tun.adapter.stack }}</strong></div>
        <div><span>活动会话</span><strong>{{ state.tun.adapter.stats?.sessions ?? 0 }}</strong></div>
        <div>
          <span>收 / 发</span>
          <strong>
            {{ fmtBytes(state.tun.adapter.stats?.bytes_in) }} /
            {{ fmtBytes(state.tun.adapter.stats?.bytes_out) }}
          </strong>
        </div>
        <div><span>默认路由</span><strong>{{ state.tun.adapter.route_all ? '已接管' : '未接管' }}</strong></div>
      </div>

      <div class="cc-card__foot">
        <NButton size="tiny" type="primary" :loading="isBusy('start')" @click="startAll">
          {{ tunOpen ? '重建并接管' : '创建网卡并接管' }}
        </NButton>
        <NButton size="tiny" :disabled="!tunOpen" :loading="isBusy('detach')" @click="detachAdapter">
          销毁网卡
        </NButton>
        <span class="hint">
          名称默认 NGG；创建需要已签名 wintun.dll 与管理员权限，服务以 LocalSystem 运行即满足。
        </span>
      </div>
      <NAlert
        v-if="state?.stale_adapters?.length"
        type="warning"
        :bordered="false"
        style="margin-top: 8px"
      >
        检测到上次接管留下的虚拟网卡：{{ state.stale_adapters.join('、') }}；
        启动时会自动删除它的默认路由并禁用，不会与新网卡并存。
      </NAlert>
      <div v-if="driverMsg" class="hint ok-text">{{ driverMsg }}</div>
      <div v-if="state?.tun.capabilities.reason" class="hint warn-text">
        {{ state.tun.capabilities.reason }}
      </div>
    </section>

    <!-- ② 全量接管 -->
    <section class="cc-card">
      <div class="cc-card__head">
        <h3 class="cc-card__title">② 强制接管全部流量</h3>
        <NTag size="small" :bordered="false" :type="captureActive ? 'success' : 'default'">
          {{ captureActive ? '接管生效中' : '未接管' }}
        </NTag>
      </div>

      <div class="grid">
        <div class="field">
          <div class="label">接管全部流量</div>
          <div class="switch-row"><NSwitch v-model:value="form.captureAll" size="small" /></div>
        </div>
        <div class="field">
          <div class="label">开机自动接管</div>
          <div class="switch-row"><NSwitch v-model:value="form.autoStart" size="small" /></div>
        </div>
        <div class="field">
          <div class="label">局域网与保留地址直连</div>
          <div class="switch-row"><NSwitch v-model:value="form.bypassPrivate" size="small" /></div>
        </div>
        <div class="field">
          <div class="label">例外网段（逗号分隔）</div>
          <NInput v-model:value="form.bypassText" size="small" placeholder="10.0.0.0/8, 192.168.0.0/16" />
        </div>
      </div>

      <NAlert v-if="state && !state.datapath.elevated" type="warning" :bordered="false" style="margin-top: 8px">
        {{ state.datapath.reason || '当前进程未提权：接管命令只会展示，不会执行。NGG 服务以 LocalSystem 运行时才具备权限。' }}
      </NAlert>

      <NCollapse v-if="commands.length" style="margin-top: 8px">
        <NCollapseItem title="未执行的接管命令" name="cmd">
          <template #header-extra>
            <NButton size="tiny" quaternary type="primary" @click.stop="copyCommands">复制</NButton>
          </template>
          <pre class="cmd">{{ commands.join('\n') }}</pre>
        </NCollapseItem>
      </NCollapse>

      <div class="cc-card__foot">
        <NButton size="tiny" type="primary" :loading="isBusy('start')" @click="startAll">
          接管全部流量
        </NButton>
        <NButton size="tiny" :disabled="!captureActive" :loading="isBusy('stop')" @click="stopAll">
          恢复原状
        </NButton>
        <NButton size="tiny" quaternary :loading="isBusy('save')" @click="saveSwitches">只保存开关</NButton>
      </div>
    </section>

    <!-- ③ WAN 出口 -->
    <section class="cc-card">
      <div class="cc-card__head">
        <h3 class="cc-card__title">③ WAN 出口（本地运营商）</h3>
        <NTag size="small" :bordered="false" :type="state?.egress.interface ? 'success' : 'warning'">
          {{ state?.egress.interface || '系统默认路由' }}
        </NTag>
      </div>

      <div class="grid">
        <div class="field">
          <div class="label">承载出口的物理网卡</div>
          <NSelect
            v-model:value="form.egressInterface"
            size="small"
            filterable
            clearable
            :options="egressOptions"
            placeholder="选择物理网卡（如 WLAN / 以太网）"
          />
        </div>
        <div class="field">
          <div class="label">WAN 口（网关中心归档）</div>
          <NSelect
            v-model:value="form.wanId"
            size="small"
            clearable
            :options="wanOptions"
            placeholder="可不选"
          />
        </div>
      </div>

      <div class="cc-card__foot">
        <NButton size="tiny" type="primary" :loading="isBusy('egress')" @click="applyEgress">
          保存并立即生效
        </NButton>
        <span class="hint">
          接管后所有流量都进 NGG 虚拟网络，NGG 自身的出站从这里选的物理网卡走本地运营商；切换即时生效，无需重启。
        </span>
      </div>
    </section>

    <!-- 兼容：系统代理 -->
    <section class="cc-card">
      <NCollapse>
        <NCollapseItem name="proxy">
          <template #header>
            <span class="cc-card__title">兼容模式 · 系统代理</span>
            <NTag
              size="tiny"
              :bordered="false"
              :type="globalStatus?.proxy?.attached ? 'success' : 'default'"
              style="margin-left: 8px"
            >
              {{ globalStatus?.proxy?.attached ? '已接管' : '未接管' }}
            </NTag>
          </template>
          <div class="hint" style="margin-bottom: 8px">
            只影响读取 WinINET 代理设置的应用（浏览器等），与上面的 TUN 全量接管互相独立；
            全量接管生效时无需再开这个开关。解除时恢复原有代理设置。
          </div>
          <NSpace :size="8" align="center">
            <NButton
              size="tiny"
              :loading="isBusy('proxy')"
              :disabled="!globalStatus?.proxy?.supported || globalStatus?.proxy?.attached"
              @click="attachProxy"
            >接管系统代理</NButton>
            <NButton
              size="tiny"
              :loading="isBusy('proxy-off')"
              :disabled="!globalStatus?.proxy?.attached"
              @click="detachProxy"
            >解除</NButton>
            <span class="mono hint">{{ globalStatus?.proxy?.server || '—' }}</span>
          </NSpace>
        </NCollapseItem>
      </NCollapse>
    </section>
  </div>
</template>

<style scoped>
.cc {
  font-family: -apple-system, BlinkMacSystemFont, 'SF Pro Text', 'PingFang SC', 'Microsoft YaHei', sans-serif;
  background: #f5f5f7;
  border-radius: 12px;
  padding: 12px;
  color: #1d1d1f;
  font-size: 12px;
  min-height: calc(100vh - 120px);
}
.cc-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}
.cc-title {
  margin: 0 0 2px;
  font-size: 18px;
  font-weight: 600;
  letter-spacing: -0.3px;
}
.cc-sub {
  margin: 0;
  font-size: 11.5px;
  color: #6e6e73;
}
.cc-stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
  margin-bottom: 8px;
}
.stat {
  background: #fff;
  border: 1px solid #e6e6eb;
  border-radius: 10px;
  padding: 8px 10px;
  min-width: 0;
}
.stat--ok {
  border-color: #cfe9d6;
}
.stat--warn {
  border-color: #f0dcb8;
}
.stat__label {
  font-size: 10.5px;
  color: #6e6e73;
}
.stat__value {
  font-size: 15px;
  font-weight: 600;
  letter-spacing: -0.02em;
  margin-top: 2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.stat__note {
  font-size: 10px;
  color: #8e8e93;
}
.cc-steps {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  margin-bottom: 8px;
}
.step {
  display: flex;
  gap: 8px;
  background: #fff;
  border: 1px solid #e6e6eb;
  border-radius: 10px;
  padding: 9px 10px;
  min-width: 0;
}
.step--ok {
  border-color: #cfe9d6;
}
.step--warn {
  border-color: #f0dcb8;
  background: #fffdf7;
}
.step--fail {
  border-color: #f0c2c6;
  background: #fff8f8;
}
.step__idx {
  width: 20px;
  height: 20px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  font-size: 11px;
  font-weight: 600;
  color: #fff;
  background: #0071e3;
  flex: 0 0 auto;
}
.step--ok .step__idx {
  background: #30a14e;
}
.step--warn .step__idx {
  background: #ff9f0a;
}
.step--fail .step__idx {
  background: #ff453a;
}
.step--skip .step__idx {
  background: #c7c7cc;
}
.step__body {
  min-width: 0;
}
.step__title {
  font-size: 12px;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 6px;
}
.step__tag {
  font-size: 10px;
  font-weight: 400;
  color: #8e8e93;
}
.step__detail {
  font-size: 11px;
  color: #6e6e73;
  line-height: 1.5;
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}
.cc-card {
  background: #fff;
  border: 1px solid #e6e6eb;
  border-radius: 10px;
  padding: 10px 12px;
  margin-bottom: 8px;
}
.cc-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.cc-card__title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
}
.cc-card__foot {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 10px;
}
.grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px 14px;
}
.field {
  display: grid;
  gap: 4px;
  min-width: 0;
}
.field--wide {
  grid-column: 1 / -1;
}
.label {
  font-size: 10.5px;
  color: #6e6e73;
}
.switch-row {
  height: 28px;
  display: flex;
  align-items: center;
}
.adapter-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.adapter-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: 10px;
  background: #f5f5f7;
  border: 1px solid #e6e6eb;
  font-size: 11px;
  color: #3a3a3c;
}
.adapter-chip em {
  font-style: normal;
  font-size: 9.5px;
  color: #0071e3;
}
.adapter-chip.is-ngg {
  border-color: #b9d8ff;
  background: #eef6ff;
}
.adapter-chip.is-default {
  border-color: #cfe9d6;
  background: #f2fbf5;
}
.adapter-chip.is-down {
  opacity: 0.55;
}
.tun-facts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 6px 14px;
  margin-top: 10px;
  padding-top: 9px;
  border-top: 1px solid #f0f0f2;
  font-size: 11.5px;
}
.tun-facts div {
  display: flex;
  justify-content: space-between;
  gap: 8px;
}
.tun-facts span {
  color: #8e8e93;
}
.tun-facts strong {
  font-weight: 500;
}
.cmd {
  margin: 0;
  padding: 8px;
  background: #1d1d1f;
  color: #f5f5f7;
  border-radius: 8px;
  font-size: 11px;
  line-height: 1.6;
  overflow: auto;
  max-height: 220px;
}
.hint {
  font-size: 11px;
  color: #8e8e93;
  line-height: 1.6;
}
.ok-text {
  color: #1f7a3a;
}
.warn-text {
  color: #b26a00;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
@media (max-width: 1180px) {
  .cc-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .cc-steps {
    grid-template-columns: 1fr;
  }
  .grid {
    grid-template-columns: 1fr;
  }
}
</style>
