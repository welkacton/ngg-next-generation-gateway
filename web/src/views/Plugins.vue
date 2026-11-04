<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NGi,
  NGrid,
  NInput,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type { Plugin, PluginManifest, PluginReloadResult } from '../api/ngg'
import { useApiAction } from '../composables/useApi'
import { prettyJson, toArray } from '../utils/format'

const { isBusy, run } = useApiAction()

const kindOptions = [
  { label: 'javascript', value: 'javascript' },
  { label: 'wasm', value: 'wasm' },
]

const plugins = ref<Plugin[]>([])
const showWasmHint = ref(false)
const actionResult = ref<unknown>(null)
const reloadResult = ref<PluginReloadResult | null>(null)
const testId = ref('')
const testInput = ref('{\n  "domain": "example.com"\n}')
const testResult = ref<unknown>(null)

const form = ref({
  id: '',
  name: '',
  kind: 'javascript',
  source: '',
  hooks: '',
  permissions: '',
  enabled: true,
  description: '',
})

function splitList(text: string): string[] {
  return text
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

function buildManifest(): PluginManifest {
  return {
    id: form.value.id.trim(),
    name: form.value.name.trim(),
    kind: form.value.kind,
    source: form.value.source,
    hooks: splitList(form.value.hooks),
    permissions: splitList(form.value.permissions),
    enabled: form.value.enabled,
    description: form.value.description,
  }
}

async function refreshPlugins() {
  const d = await run('plugins', () => api.listPlugins())
  if (d) plugins.value = toArray<Plugin>(d)
}

function fillJsExample() {
  form.value = {
    id: 'demo-js-example',
    name: 'JS 示例插件',
    kind: 'javascript',
    source:
      'var result={}; if(ngg.input.domain.indexOf("example")>=0){result.core_id="sing-box";result.tags=["demo"];}',
    hooks: 'on_session',
    permissions: '',
    enabled: true,
    description: '示例：命中 example 域名时把会话路由到 sing-box',
  }
  window.$message?.success('已填入 JS 插件示例，确认后点击「安装插件」')
}

async function install() {
  if (!form.value.id.trim() || !form.value.source.trim()) {
    window.$message?.warning('请填写插件 ID 与 source')
    return
  }
  const r = await run('install', () => api.installPlugin(buildManifest()), '插件安装命令已下发')
  if (r) actionResult.value = r
  await refreshPlugins()
}

async function toggle(row: Plugin) {
  const next = !row.enabled
  await run(
    'enable:' + row.id,
    () => api.enablePlugin(row.id, next),
    `插件 ${row.id} 已${next ? '启用' : '停用'}`,
  )
  await refreshPlugins()
}

function confirmDelete(id: string) {
  window.$dialog?.warning({
    title: '删除插件',
    content: `确定删除插件 ${id} 吗？`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const r = await run('del:' + id, () => api.deletePlugin(id), '插件已删除')
      if (r) actionResult.value = r
      await refreshPlugins()
    },
  })
}

function selectTest(id: string) {
  testId.value = id
}

async function doTest() {
  if (!testId.value.trim()) {
    window.$message?.warning('请填写要测试的插件 ID')
    return
  }
  let input: Record<string, unknown>
  try {
    input = JSON.parse(testInput.value || '{}')
  } catch {
    window.$message?.error('input 必须是合法的 JSON 对象')
    return
  }
  const r = await run('test', () => api.testPlugin(testId.value.trim(), input))
  if (r) testResult.value = r
}

async function doReload() {
  const r = await run('reload', () => api.reloadPlugins(), '插件已重载')
  if (r) reloadResult.value = r
  await refreshPlugins()
}

const columns: any[] = [
  {
    title: 'ID',
    key: 'id',
    minWidth: 150,
    render: (r: Plugin) => h('span', { class: 'mono' }, r.id),
  },
  { title: '名称', key: 'name', minWidth: 140 },
  {
    title: '类型',
    key: 'kind',
    width: 120,
    render: (r: Plugin) =>
      h(NTag, { size: 'small', bordered: false, type: 'info' }, { default: () => r.kind }),
  },
  {
    title: '启用',
    key: 'enabled',
    width: 80,
    render: (r: Plugin) =>
      h(
        NTag,
        { size: 'small', bordered: false, type: r.enabled ? 'success' : 'default' },
        { default: () => (r.enabled ? '是' : '否') },
      ),
  },
  {
    title: 'Hooks',
    key: 'hooks',
    minWidth: 160,
    render: (r: Plugin) => (Array.isArray(r.hooks) ? r.hooks.join('、') : '—'),
  },
  {
    title: '权限',
    key: 'permissions',
    minWidth: 160,
    render: (r: Plugin) =>
      Array.isArray(r.permissions) ? r.permissions.join('、') : '—',
  },
  { title: '说明', key: 'description', minWidth: 200, render: (r: Plugin) => r.description ?? '—' },
  {
    title: '操作',
    key: 'actions',
    width: 220,
    fixed: 'right',
    render: (r: Plugin) =>
      h('div', { style: 'display: flex; gap: 4px; flex-wrap: wrap' }, [
        h(
          NButton,
          {
            size: 'tiny',
            type: r.enabled ? 'warning' : 'primary',
            loading: isBusy('enable:' + r.id),
            onClick: () => toggle(r),
          },
          { default: () => (r.enabled ? '停用' : '启用') },
        ),
        h(
          NButton,
          { size: 'tiny', onClick: () => selectTest(r.id) },
          { default: () => '测试' },
        ),
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
      ]),
  },
]

onMounted(refreshPlugins)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2 class="page-title">插件运行时</h2>
        <p class="page-subtitle">JavaScript / WASM 插件安装、启用、测试与重载</p>
      </div>
      <NSpace>
        <NButton size="small" :loading="isBusy('plugins')" @click="refreshPlugins">刷新列表</NButton>
      </NSpace>
    </div>

    <NAlert type="info" :bordered="false" class="section-gap">
      <template #header>插件说明</template>
      <span class="mono">kind</span> 为 <span class="mono">javascript</span> 或
      <span class="mono">wasm</span>；安装时的 body 即插件 manifest。
      <strong>WASM 插件需导出 <span class="mono">ngg_hook() -&gt; i32</span></strong> 供网关在 hook 时调用。
    </NAlert>

    <NCard size="small" title="插件列表（/plugins）" class="section-gap">
      <template #header-extra>
        <NButton size="small" type="primary" :loading="isBusy('reload')" @click="doReload">
          重载全部
        </NButton>
      </template>
      <NDataTable
        :columns="columns"
        :data="plugins"
        :bordered="false"
        size="small"
        :pagination="false"
        :scroll-x="1400"
      />
      <div v-if="reloadResult" style="margin-top: 12px">
        <div class="hint">重载结果（loaded / problems）：</div>
        <pre class="mono output-block" style="max-height: 180px">{{ prettyJson(reloadResult) }}</pre>
      </div>
    </NCard>

    <NCard size="small" title="安装插件（POST /plugins）" class="section-gap">
      <template #header-extra>
        <NSpace :size="8" :wrap="true">
          <NButton size="small" @click="fillJsExample">填入 JS 示例</NButton>
          <NButton size="small" @click="showWasmHint = !showWasmHint">WASM 说明</NButton>
        </NSpace>
      </template>

      <NAlert v-if="showWasmHint" type="warning" :bordered="false" style="margin-bottom: 14px">
        <template #header>WASM 插件约定</template>
        WASM 模块的 <span class="mono">source</span> 需要指向可加载的 .wasm 模块，且
        <strong>必须导出函数 <span class="mono">ngg_hook() -&gt; i32</span></strong>；
        网关在对应 hook 被调用时执行该导出函数，并通过返回的 i32 结果判定处理动作。
        请选择 <span class="mono">kind = wasm</span> 后安装。
      </NAlert>

      <NSpace vertical :size="10" style="width: 100%">
        <NGrid cols="1 s:2 m:3" responsive="screen" :x-gap="12" :y-gap="10">
          <NGi>
            <div class="kv-label">ID（必填）</div>
            <NInput v-model:value="form.id" placeholder="demo-js-example" />
          </NGi>
          <NGi>
            <div class="kv-label">名称</div>
            <NInput v-model:value="form.name" placeholder="示例插件" />
          </NGi>
          <NGi>
            <div class="kv-label">kind</div>
            <NSelect v-model:value="form.kind" :options="kindOptions" />
          </NGi>
          <NGi>
            <div class="kv-label">hooks（逗号/换行分隔）</div>
            <NInput v-model:value="form.hooks" placeholder="on_session" />
          </NGi>
          <NGi>
            <div class="kv-label">permissions（逗号/换行分隔）</div>
            <NInput v-model:value="form.permissions" placeholder="可选" />
          </NGi>
          <NGi>
            <div class="kv-label">启用</div>
            <NSwitch v-model:value="form.enabled" />
          </NGi>
          <NGi :span="3">
            <div class="kv-label">source（必填）</div>
            <NInput v-model:value="form.source" type="textarea" class="code-editor" :rows="4" />
          </NGi>
          <NGi :span="3">
            <div class="kv-label">description</div>
            <NInput v-model:value="form.description" placeholder="可选" />
          </NGi>
        </NGrid>
        <NButton size="small" type="primary" :loading="isBusy('install')" @click="install">
          安装插件
        </NButton>
        <pre v-if="actionResult" class="mono output-block" style="max-height: 180px">{{ prettyJson(actionResult) }}</pre>
      </NSpace>
    </NCard>

    <NCard size="small" title="插件测试（POST /plugins/{id}/test）" class="section-gap">
      <NSpace vertical :size="10" style="width: 100%">
        <NSpace :size="8" :wrap="true" align="center">
          <span class="hint">插件 ID</span>
          <NInput v-model:value="testId" style="width: 240px" placeholder="点击列表「测试」自动填入" />
          <NButton size="small" type="primary" :loading="isBusy('test')" @click="doTest">
            执行测试
          </NButton>
        </NSpace>
        <NInput v-model:value="testInput" type="textarea" class="code-editor" :rows="4" />
        <div v-if="testResult">
          <div class="hint">result / 原始返回：</div>
          <pre class="mono output-block" style="max-height: 260px">{{ prettyJson(testResult) }}</pre>
        </div>
      </NSpace>
    </NCard>
  </div>
</template>
