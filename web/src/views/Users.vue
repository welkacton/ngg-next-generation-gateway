<script setup lang="ts">
/**
 * 配置引擎 · 用户管理
 *
 * 布局参考 Remnawave 的用户页（统计条 + 用户表 + 右侧详情抽屉），
 * 配色与圆角沿用本项目浅色控制台。状态与用量全部由后端真实字段推导：
 * enabled / expires_at / data_limit / used_bytes；前端不生成任何模拟数值。
 */
import { computed, h, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NDatePicker,
  NDrawer,
  NDrawerContent,
  NEmpty,
  NGi,
  NGrid,
  NInput,
  NInputNumber,
  NProgress,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type { PortalUser, UserResetStrategy, UserStats, UserStatus } from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { formatBytes, formatTime } from '../utils/format'

const { isBusy, run } = useApiAction()

const users = ref<PortalUser[]>([])
const stats = ref<UserStats | null>(null)
const keyword = ref('')
const statusFilter = ref<'all' | UserStatus>('all')
const loadError = ref('')

const showDrawer = ref(false)
const editingId = ref<string | null>(null)
const tagDraft = ref('')

interface UserForm {
  username: string
  enabled: boolean
  email: string
  telegram_id: string
  tag: string
  group: string
  note: string
  expiresMs: number | null
  limitGib: number | null
  reset_strategy: UserResetStrategy
  hwid_disabled: boolean
  hwid_limit: number | null
  subscription_url: string
}

function emptyForm(): UserForm {
  return {
    username: '',
    enabled: true,
    email: '',
    telegram_id: '',
    tag: '',
    group: 'Default-Squad',
    note: '',
    expiresMs: null,
    limitGib: null,
    reset_strategy: 'never',
    hwid_disabled: false,
    hwid_limit: 0,
    subscription_url: '',
  }
}
const form = ref<UserForm>(emptyForm())

const STATUS_META: Record<UserStatus, { label: string; type: 'success' | 'error' | 'warning' | 'default' }> = {
  active: { label: 'Active', type: 'success' },
  expired: { label: 'Expired', type: 'error' },
  limited: { label: 'Limited', type: 'warning' },
  disabled: { label: 'Disabled', type: 'default' },
}

/** Go's zero time serialises as 0001-01-01T00:00:00Z and means “never expires”. */
function isExpired(u: PortalUser): boolean {
  if (!u.expires_at || u.expires_at.startsWith('0001-')) return false
  const t = new Date(u.expires_at).getTime()
  return !Number.isNaN(t) && t <= Date.now()
}

function statusOf(u: PortalUser): UserStatus {
  if (!u.enabled) return 'disabled'
  if (isExpired(u)) return 'expired'
  if (u.data_limit > 0 && u.used_bytes >= u.data_limit) return 'limited'
  return 'active'
}

const filtered = computed(() => {
  const q = keyword.value.trim().toLowerCase()
  return users.value.filter((u) => {
    if (statusFilter.value !== 'all' && statusOf(u) !== statusFilter.value) return false
    if (!q) return true
    return [u.username, u.id, u.email, u.tag, u.group, u.last_node_name]
      .join(' ')
      .toLowerCase()
      .includes(q)
  })
})

const statCards = computed(() => [
  { key: 'total', label: '总计', value: stats.value?.total ?? 0, tone: 'blue' },
  { key: 'active', label: 'Active', value: stats.value?.active ?? 0, tone: 'green' },
  { key: 'expired', label: 'Expired', value: stats.value?.expired ?? 0, tone: 'red' },
  { key: 'limited', label: 'Limited', value: stats.value?.limited ?? 0, tone: 'orange' },
  { key: 'disabled', label: 'Disabled', value: stats.value?.disabled ?? 0, tone: 'gray' },
])

async function refresh() {
  try {
    const res = await api.listUsers()
    users.value = res.users ?? []
    stats.value = res.stats ?? null
    loadError.value = ''
  } catch {
    loadError.value = '无法读取用户列表，请检查 API Token 与后端服务'
  }
}

function openCreate() {
  editingId.value = null
  form.value = emptyForm()
  tagDraft.value = ''
  showDrawer.value = true
}

function openEdit(u: PortalUser) {
  editingId.value = u.id
  form.value = {
    username: u.username,
    enabled: u.enabled,
    email: u.email,
    telegram_id: u.telegram_id,
    tag: u.tag,
    group: u.group,
    note: u.note,
    expiresMs: u.expires_at ? new Date(u.expires_at).getTime() : null,
    limitGib: u.data_limit > 0 ? u.data_limit / 1024 ** 3 : null,
    reset_strategy: u.reset_strategy || 'never',
    hwid_disabled: u.hwid_disabled,
    hwid_limit: u.hwid_limit ?? 0,
    subscription_url: u.subscription_url,
  }
  tagDraft.value = ''
  showDrawer.value = true
}

function toPayload(): Partial<PortalUser> {
  const f = form.value
  return {
    username: f.username.trim(),
    enabled: f.enabled,
    email: f.email.trim(),
    telegram_id: f.telegram_id.trim(),
    tag: f.tag.trim(),
    group: f.group.trim(),
    note: f.note.trim(),
    expires_at: f.expiresMs ? new Date(f.expiresMs).toISOString() : '0001-01-01T00:00:00Z',
    data_limit: Math.max(0, Math.round((f.limitGib ?? 0) * 1024 ** 3)),
    reset_strategy: f.reset_strategy,
    hwid_disabled: f.hwid_disabled,
    hwid_limit: Math.max(0, Math.round(f.hwid_limit ?? 0)),
    subscription_url: f.subscription_url.trim(),
  }
}

async function save() {
  if (!form.value.username.trim()) {
    window.$message?.warning('用户名必填')
    return
  }
  const payload = toPayload() as PortalUser
  const res = editingId.value
    ? await run('save', () => api.updateUser(editingId.value!, payload), '用户已保存')
    : await run('create', () => api.addUser(payload), '用户已创建')
  if (res) {
    showDrawer.value = false
    await refresh()
  }
}

function confirmDelete(u: PortalUser) {
  window.$dialog?.warning({
    title: '删除用户',
    content: `确定删除用户 ${u.username}（${u.id}）吗？该操作只删除本地账号记录。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await run('del:' + u.id, () => api.deleteUser(u.id), '用户已删除')
      if (res) {
        if (editingId.value === u.id) showDrawer.value = false
        await refresh()
      }
    },
  })
}

function confirmDeleteCurrent() {
  const u = users.value.find((x) => x.id === editingId.value)
  if (u) confirmDelete(u)
}

async function resetUsage(u: PortalUser) {
  const res = await run('reset:' + u.id, () => api.resetUserUsage(u.id), '用量已重置')
  if (res) await refresh()
}

function addTag() {
  const v = tagDraft.value.trim()
  if (!v) return
  form.value.tag = v
  tagDraft.value = ''
}

function usagePercent(u?: PortalUser | null): number {
  if (!u || !u.data_limit) return 0
  return Math.min(100, Math.round((u.used_bytes / u.data_limit) * 100))
}

function usageText(u?: PortalUser | null): string {
  if (!u) return '—'
  if (!u.data_limit) return `${formatBytes(u.used_bytes)} / 不限`
  return `${formatBytes(u.used_bytes)} / ${formatBytes(u.data_limit)}`
}

function relativeSeen(u?: PortalUser | null): string {
  if (!u || !u.last_seen_at || u.last_seen_at.startsWith('0001-')) return '从未连接'
  const diff = Date.now() - new Date(u.last_seen_at).getTime()
  if (Number.isNaN(diff) || diff < 0) return formatTime(u.last_seen_at)
  const sec = Math.floor(diff / 1000)
  if (sec < 60) return `${sec} 秒前`
  if (sec < 3600) return `${Math.floor(sec / 60)} 分钟前`
  if (sec < 86400) return `${Math.floor(sec / 3600)} 小时前`
  return `${Math.floor(sec / 86400)} 天前`
}

function expiryText(u: PortalUser): string {
  if (!u.expires_at || u.expires_at.startsWith('0001-')) return '永不过期'
  return formatTime(u.expires_at)
}

const columns = computed<any[]>(() => [
  { type: 'selection', width: 44 },
  {
    title: '用户名',
    key: 'username',
    width: 190,
    render: (u: PortalUser) =>
      h('div', { class: 'user-cell' }, [
        h('span', { class: `user-dot user-dot--${statusOf(u)}` }),
        h('div', null, [
          h('div', { class: 'user-name' }, u.username),
          h('div', { class: 'hint' }, relativeSeen(u)),
        ]),
      ]),
  },
  { title: 'ID', key: 'id', width: 110, ellipsis: { tooltip: true }, render: (u: PortalUser) => h('span', { class: 'mono' }, u.id) },
  {
    title: '状态',
    key: 'status',
    width: 104,
    render: (u: PortalUser) => {
      const m = STATUS_META[statusOf(u)]
      return h(NTag, { size: 'small', type: m.type, bordered: false }, { default: () => m.label })
    },
  },
  {
    title: '最后连接节点',
    key: 'last_node',
    width: 150,
    ellipsis: { tooltip: true },
    render: (u: PortalUser) => u.last_node_name || u.last_node_id || '—',
  },
  { title: '过期时间', key: 'expires_at', width: 168, ellipsis: { tooltip: true }, render: expiryText },
  {
    title: '数据用量',
    key: 'usage',
    width: 210,
    render: (u: PortalUser) =>
      h('div', { class: 'user-usage' }, [
        h(NProgress, {
          type: 'line',
          percentage: usagePercent(u),
          height: 6,
          showIndicator: false,
          status: statusOf(u) === 'limited' ? 'error' : 'success',
        }),
        h('span', { class: 'hint' }, usageText(u)),
      ]),
  },
  { title: '标签', key: 'tag', width: 110, ellipsis: { tooltip: true }, render: (u: PortalUser) => u.tag || '—' },
  { title: '内部分组', key: 'group', width: 130, ellipsis: { tooltip: true }, render: (u: PortalUser) => u.group || '—' },
  {
    title: '操作',
    key: 'actions',
    width: 186,
    fixed: 'right',
    render: (u: PortalUser) =>
      h(NSpace, { size: 4, wrap: false }, {
        default: () => [
          h(NButton, { size: 'tiny', type: 'primary', onClick: () => openEdit(u) }, { default: () => '编辑' }),
          h(NButton, {
            size: 'tiny',
            loading: isBusy('reset:' + u.id),
            onClick: () => resetUsage(u),
          }, { default: () => '重置用量' }),
          h(NButton, {
            size: 'tiny',
            type: 'error',
            loading: isBusy('del:' + u.id),
            onClick: () => confirmDelete(u),
          }, { default: () => '删除' }),
        ],
      }),
  },
])

onMounted(refresh)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">用户管理</h2>
        <p class="page-subtitle">本地账号、配额、到期与分组 · 状态由真实字段推导</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="isBusy('list')" @click="refresh">刷新</NButton>
        <NButton size="small" type="primary" @click="openCreate">＋ 新增用户</NButton>
      </NSpace>
    </div>

    <NAlert v-if="loadError" type="error" :bordered="false" class="section-gap">{{ loadError }}</NAlert>

    <NGrid cols="2 m:5" responsive="screen" :x-gap="12" :y-gap="12" class="section-gap">
      <NGi v-for="c in statCards" :key="c.key">
        <NCard size="small" class="user-stat" :class="`user-stat--${c.tone}`">
          <div class="user-stat__label">{{ c.label }}</div>
          <div class="user-stat__value">{{ c.value }}</div>
        </NCard>
      </NGi>
    </NGrid>

    <NCard size="small" class="section-gap">
      <NSpace align="center" :wrap="true" :size="10">
        <NInput v-model:value="keyword" size="small" clearable placeholder="搜索用户名 / ID / 邮箱 / 标签" style="width: 260px" />
        <NSelect
          v-model:value="statusFilter"
          size="small"
          style="width: 150px"
          :options="[
            { label: '全部状态', value: 'all' },
            { label: 'Active', value: 'active' },
            { label: 'Expired', value: 'expired' },
            { label: 'Limited', value: 'limited' },
            { label: 'Disabled', value: 'disabled' },
          ]"
        />
        <NTag :bordered="false" size="small">显示 {{ filtered.length }} / {{ users.length }}</NTag>
        <span class="hint">「最后连接节点 / 数据用量」来自真实数据面；未接入计量的用户用量保持 0，不会伪造。</span>
      </NSpace>
    </NCard>

    <NCard size="small" class="section-gap">
      <NEmpty v-if="!users.length" description="还没有用户，点击右上角「＋ 新增用户」创建第一个账号。">
        <template #extra>
          <NButton size="small" type="primary" @click="openCreate">＋ 新增用户</NButton>
        </template>
      </NEmpty>
      <NDataTable
        v-else
        :columns="columns"
        :data="filtered"
        :row-key="(row: any) => String(row.id)"
        :bordered="false"
        :scroll-x="1440"
        size="small"
        :pagination="{ pageSize: 20 }"
      />
    </NCard>

    <NDrawer v-model:show="showDrawer" :width="820" placement="right">
      <NDrawerContent closable :native-scrollbar="false">
        <template #header>
          <NSpace :size="8" align="center">
            <span>{{ editingId ? form.username : '新增用户' }}</span>
            <NTag v-if="editingId" size="small" :bordered="false">{{ editingId }}</NTag>
          </NSpace>
        </template>

        <NGrid cols="1 m:2" responsive="screen" :x-gap="14" :y-gap="14">
          <NGi>
            <NCard size="small" title="用户信息" class="user-panel">
              <div class="kv-label">用户名</div>
              <NInput v-model:value="form.username" placeholder="US-01" />
              <div class="kv-label" style="margin-top: 10px">启用</div>
              <NSwitch v-model:value="form.enabled" />
              <div class="kv-label" style="margin-top: 10px">订阅 URL（可选，外部面板生成）</div>
              <NInput v-model:value="form.subscription_url" placeholder="https://…/api/sub/…" />
              <div class="hint" style="margin-top: 6px">NGG 不自建订阅服务，留空表示未设置。</div>
            </NCard>

            <NCard size="small" title="连接信息" class="user-panel">
              <div class="kv-label">Telegram ID</div>
              <NInput v-model:value="form.telegram_id" placeholder="可选" />
              <div class="kv-label" style="margin-top: 10px">Email</div>
              <NInput v-model:value="form.email" placeholder="可选" />
              <div class="user-kv"><span>最后连接节点</span><span>{{ users.find((x) => x.id === editingId)?.last_node_name || '—' }}</span></div>
              <div class="user-kv"><span>最近在线</span><span>{{ editingId ? relativeSeen(users.find((x) => x.id === editingId)) : '—' }}</span></div>
            </NCard>

            <NCard size="small" title="设备与标签" class="user-panel">
              <div class="kv-label">Tag</div>
              <NSpace :size="6">
                <NInput v-model:value="tagDraft" placeholder="例如 EXAMPLE_TAG_1" @keyup.enter="addTag" />
                <NButton size="small" @click="addTag">设置</NButton>
              </NSpace>
              <div class="hint" style="margin-top: 4px">当前：{{ form.tag || '未设置' }}</div>
              <div class="kv-label" style="margin-top: 10px">HWID 设备限制</div>
              <NSpace :size="8" align="center">
                <NSwitch v-model:value="form.hwid_disabled" />
                <span class="hint">禁用 HWID 限制</span>
                <NInputNumber v-model:value="form.hwid_limit" :min="0" :max="100" style="width: 110px" />
                <span class="hint">台</span>
              </NSpace>
              <div class="kv-label" style="margin-top: 10px">备注</div>
              <NInput v-model:value="form.note" type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" />
            </NCard>
          </NGi>

          <NGi>
            <NCard size="small" title="流量限制" class="user-panel">
              <div class="kv-label">总量上限（GiB，0 / 留空 = 不限）</div>
              <NInputNumber v-model:value="form.limitGib" :min="0" :max="1024" :step="1" style="width: 100%" />
              <div class="kv-label" style="margin-top: 10px">用量重置策略</div>
              <NSelect
                v-model:value="form.reset_strategy"
                :options="[
                  { label: '永不重置', value: 'never' },
                  { label: '每月重置', value: 'monthly' },
                  { label: '每周重置', value: 'weekly' },
                ]"
              />
              <div v-if="editingId" class="user-usage-block">
                <div class="hint">当前真实用量：{{ usageText(users.find((x) => x.id === editingId)) }}</div>
                <NProgress
                  type="line"
                  :percentage="usagePercent(users.find((x) => x.id === editingId))"
                  :height="8"
                  :show-indicator="false"
                />
              </div>
            </NCard>

            <NCard size="small" title="访问设置" class="user-panel">
              <div class="kv-label">到期时间（留空 = 永不过期）</div>
              <NDatePicker v-model:value="form.expiresMs" type="datetime" clearable style="width: 100%" />
              <div class="kv-label" style="margin-top: 10px">内部分组</div>
              <NInput v-model:value="form.group" placeholder="Default-Squad" />
              <div class="kv-label" style="margin-top: 10px">用户 ID</div>
              <div class="mono">{{ editingId || '保存后由后端生成' }}</div>
            </NCard>

            <NCard size="small" title="真实状态" class="user-panel">
              <NAlert type="default" :bordered="false" size="small">
                Active / Expired / Limited / Disabled 由 enabled、到期时间与配额实时计算，
                不存在手工设置的状态字段。数据用量只由数据面累加。
              </NAlert>
            </NCard>
          </NGi>
        </NGrid>

        <template #footer>
          <NSpace justify="end" :size="8">
            <NButton
              v-if="editingId"
              size="small"
              type="error"
              :disabled="!editingId"
              @click="confirmDeleteCurrent"
            >删除用户</NButton>
            <NButton size="small" @click="showDrawer = false">取消</NButton>
            <NButton size="small" type="primary" :loading="isBusy('save') || isBusy('create')" @click="save">
              保存
            </NButton>
          </NSpace>
        </template>
      </NDrawerContent>
    </NDrawer>
  </div>
</template>

<style scoped>
.user-stat {
  border-left: 3px solid #d2d2d7;
}
.user-stat__label {
  font-size: 12.5px;
  color: #6e6e73;
}
.user-stat__value {
  font-size: 26px;
  font-weight: 600;
  line-height: 1.35;
  font-variant-numeric: tabular-nums;
}
.user-stat--blue { border-left-color: #0071e3; }
.user-stat--green { border-left-color: #34c759; }
.user-stat--red { border-left-color: #ff3b30; }
.user-stat--orange { border-left-color: #ff9f0a; }
.user-stat--gray { border-left-color: #8e8e93; }

.user-cell {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  min-width: 0;
}
.user-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-top: 6px;
  flex: 0 0 auto;
  background: #c7c7cc;
}
.user-dot--active { background: #34c759; box-shadow: 0 0 0 3px rgba(52, 199, 89, 0.14); }
.user-dot--expired { background: #ff3b30; }
.user-dot--limited { background: #ff9f0a; }
.user-dot--disabled { background: #8e8e93; }
.user-name {
  font-weight: 600;
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.user-usage {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}
.user-usage .hint {
  font-size: 11px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.user-panel { margin-bottom: 14px; }
.user-kv {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  padding: 6px 0;
  border-bottom: 1px solid #f2f2f4;
  font-size: 12.5px;
  color: #6e6e73;
}
.user-kv span:last-child {
  color: #1d1d1f;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.user-usage-block {
  margin-top: 12px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
</style>
