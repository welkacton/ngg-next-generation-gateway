<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NGi,
  NGrid,
  NInput,
  NInputNumber,
  NSpace,
  NSwitch,
} from 'naive-ui'
import * as api from '../api/ngg'
import type { Tenant } from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { makeColumns } from '../composables/tableColumns'
import { prettyJson, toArray } from '../utils/format'

const { isBusy, run } = useApiAction()

const tenants = ref<Tenant[]>([])
const actionResult = ref<unknown>(null)

const form = ref({
  id: '',
  name: '',
  enabled: true,
  max_sessions: 0 as number | null,
  quota_bytes: 0 as number | null,
  used_bytes: 0 as number | null,
  allowed_cores: '',
  allowed_protocols: '',
  default_core: '',
  rule_set_tag: '',
})

function splitList(text: string): string[] {
  return text
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

async function refreshTenants() {
  const d = await run('tenants', () => api.listTenants())
  if (d) tenants.value = toArray<Tenant>(d)
}

function buildTenant(): Tenant {
  return {
    id: form.value.id.trim(),
    name: form.value.name.trim(),
    enabled: form.value.enabled,
    max_sessions: form.value.max_sessions ?? 0,
    quota_bytes: form.value.quota_bytes ?? 0,
    used_bytes: form.value.used_bytes ?? 0,
    allowed_cores: splitList(form.value.allowed_cores),
    allowed_protocols: splitList(form.value.allowed_protocols),
    default_core: form.value.default_core.trim(),
    rule_set_tag: form.value.rule_set_tag.trim(),
  }
}

async function createTenant() {
  if (!form.value.id.trim()) {
    window.$message?.warning('请填写租户 ID')
    return
  }
  const r = await run('create', () => api.createTenant(buildTenant()), '租户已创建')
  if (r) actionResult.value = r
  await refreshTenants()
}

function confirmDelete(id: string) {
  window.$dialog?.warning({
    title: '删除租户',
    content: `确定删除租户 ${id} 吗？`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const r = await run('del:' + id, () => api.deleteTenant(id), '租户已删除')
      if (r) actionResult.value = r
      await refreshTenants()
    },
  })
}

const columns = computed<any[]>(() => [
  ...makeColumns(tenants.value),
  {
    title: '操作',
    key: '__actions',
    width: 90,
    fixed: 'right',
    render: (r: Tenant) =>
      h(
        NButton,
        {
          size: 'tiny',
          type: 'error',
          loading: isBusy('del:' + r.id),
          onClick: () => confirmDelete(r.id),
        },
        { default: () => '删除' },
      ),
  },
])

onMounted(refreshTenants)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">多租户</h2>
        <p class="page-subtitle">租户配额、允许内核 / 协议与规则集标签</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="isBusy('tenants')" @click="refreshTenants">刷新列表</NButton>
      </NSpace>
    </div>

    <NAlert type="info" :bordered="false" class="section-gap">
      <template #header>租户字段</template>
      id / name / enabled / max_sessions / quota_bytes / used_bytes / allowed_cores /
      allowed_protocols / default_core / rule_set_tag。列表列按后端返回字段动态生成，
      不会遗漏任何字段。
    </NAlert>

    <NCard size="small" title="租户列表（/tenants）" class="section-gap">
      <NDataTable
        :columns="columns"
        :data="tenants"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="1500"
      />
    </NCard>

    <NCard size="small" title="新建租户（POST /tenants）" class="section-gap">
      <NSpace vertical :size="10" style="width: 100%">
        <NGrid cols="1 s:2 m:3" responsive="screen" :x-gap="12" :y-gap="10">
          <NGi>
            <div class="kv-label">ID（必填）</div>
            <NInput v-model:value="form.id" placeholder="tenant-a" />
          </NGi>
          <NGi>
            <div class="kv-label">名称</div>
            <NInput v-model:value="form.name" placeholder="租户 A" />
          </NGi>
          <NGi>
            <div class="kv-label">启用</div>
            <NSwitch v-model:value="form.enabled" />
          </NGi>
          <NGi>
            <div class="kv-label">最大会话 max_sessions</div>
            <NInputNumber v-model:value="form.max_sessions" style="width: 100%" :min="0" />
          </NGi>
          <NGi>
            <div class="kv-label">配额 quota_bytes</div>
            <NInputNumber v-model:value="form.quota_bytes" style="width: 100%" :min="0" />
          </NGi>
          <NGi>
            <div class="kv-label">已用 used_bytes</div>
            <NInputNumber v-model:value="form.used_bytes" style="width: 100%" :min="0" />
          </NGi>
          <NGi>
            <div class="kv-label">允许内核（逗号分隔）</div>
            <NInput v-model:value="form.allowed_cores" placeholder="mihomo,sing-box" />
          </NGi>
          <NGi>
            <div class="kv-label">允许协议（逗号分隔）</div>
            <NInput v-model:value="form.allowed_protocols" placeholder="vpn,encrypted-proxy" />
          </NGi>
          <NGi>
            <div class="kv-label">默认内核 default_core</div>
            <NInput v-model:value="form.default_core" placeholder="mihomo" />
          </NGi>
          <NGi :span="3">
            <div class="kv-label">规则集标签 rule_set_tag</div>
            <NInput v-model:value="form.rule_set_tag" placeholder="可选" />
          </NGi>
        </NGrid>
        <NButton size="small" type="primary" :loading="isBusy('create')" @click="createTenant">
          新建租户
        </NButton>
        <pre v-if="actionResult" class="mono output-block" style="max-height: 180px">{{ prettyJson(actionResult) }}</pre>
      </NSpace>
    </NCard>
  </div>
</template>
