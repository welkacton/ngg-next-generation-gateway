<script setup lang="ts">
/**
 * 规划 1 · 节点管理
 *
 * 新建的是「空白节点」：表单里只有服务器 IP + 端口。
 * API 密码 / 后量子密钥 / mTLS 证书全部由后端自动生成，创建后一次性展示。
 * 之后在服务器上执行安装脚本，再点「连接探测」读取真实主机信息。
 */
import { computed, onMounted, ref } from 'vue'
import {
  NAlert, NButton, NCard, NGrid, NGi, NInput, NInputNumber, NModal, NSpin, NTag,
} from 'naive-ui'
import * as api from '../api/ngg'
import type {
  CreateModuleNodeResult, ModuleNodeProbe, ModuleNodeSecrets, ModuleNodeView,
} from '../api/ngg'

const nodes = ref<ModuleNodeView[]>([])
const loading = ref(false)
const creating = ref(false)
const probing = ref<string | null>(null)

const showCreate = ref(false)
const newAddress = ref('')
const newPort = ref<number | null>(6691)

const created = ref<CreateModuleNodeResult | null>(null)
const showSecrets = ref(false)

const showScript = ref(false)
const scriptText = ref('')
const scriptNodeId = ref('')
const scriptLoading = ref(false)

async function load() {
  loading.value = true
  try {
    const res = await api.listModuleNodes()
    nodes.value = res.nodes ?? []
  } catch {
    window.$message?.warning('读取节点列表失败')
  } finally {
    loading.value = false
  }
}

onMounted(load)

async function createNode() {
  const address = newAddress.value.trim()
  if (!address) {
    window.$message?.warning('填写服务器 IP 或域名')
    return
  }
  creating.value = true
  try {
    const res = await api.createModuleNode(address, Number(newPort.value ?? 0))
    created.value = res
    showCreate.value = false
    showSecrets.value = true
    newAddress.value = ''
    newPort.value = 6691
    await load()
    window.$message?.success('空白节点已创建，三类凭据已自动生成')
  } catch (e) {
    window.$message?.error('创建失败：' + errText(e))
  } finally {
    creating.value = false
  }
}

async function openScript(id: string) {
  scriptNodeId.value = id
  scriptLoading.value = true
  showScript.value = true
  try {
    scriptText.value = await api.getModuleNodeScript(id)
  } catch (e) {
    scriptText.value = ''
    window.$message?.error('读取安装脚本失败：' + errText(e))
  } finally {
    scriptLoading.value = false
  }
}

async function probe(id: string) {
  probing.value = id
  try {
    const res = await api.probeModuleNode(id)
    const item = nodes.value.find((x) => x.node.id === id)
    if (item) item.node.probe = res.probe
    window.$message?.success('连接探测成功，已读取真实主机信息')
  } catch (e) {
    window.$message?.error('连接探测失败：' + errText(e))
    await load()
  } finally {
    probing.value = null
  }
}

async function remove(id: string) {
  try {
    await api.deleteModuleNode(id)
    window.$message?.success('节点已删除')
    await load()
  } catch (e) {
    window.$message?.error('删除失败：' + errText(e))
  }
}

async function copy(text: string, label: string) {
  try {
    await navigator.clipboard.writeText(text)
    window.$message?.success(label + '已复制')
  } catch {
    window.$message?.warning('浏览器未授予剪贴板权限')
  }
}

function downloadScript() {
  const blob = new Blob([scriptText.value], { type: 'text/x-shellscript;charset=utf-8' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = `ngg-mnode-${scriptNodeId.value}.sh`
  a.click()
  URL.revokeObjectURL(a.href)
}

function errText(e: unknown): string {
  const any = e as { response?: { data?: { error?: string } }; message?: string }
  return any?.response?.data?.error || any?.message || '未知错误'
}

function bytes(v?: number): string {
  if (!v) return '未采集'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let n = v
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++ }
  return `${n.toFixed(i ? 1 : 0)} ${units[i]}`
}

function uptime(v?: number): string {
  if (!v) return '未采集'
  const d = Math.floor(v / 86400)
  const h = Math.floor((v % 86400) / 3600)
  return `${d} 天 ${h} 小时`
}

const probeRows = (p?: ModuleNodeProbe) => {
  if (!p) return []
  return [
    { label: '连接状态', value: p.online ? '在线' : '离线' },
    { label: '往返延迟', value: p.latency_ms ? `${p.latency_ms} ms` : '—' },
    { label: '主机名', value: p.hostname || '未采集' },
    { label: '系统 / 架构', value: `${p.os || '未采集'} / ${p.arch || '未采集'}` },
    { label: 'CPU', value: p.cpu_percent != null ? `${p.cpu_percent}%` : '未采集' },
    { label: '内存', value: `${bytes(p.memory_used)} / ${bytes(p.memory_total)}` },
    { label: '磁盘', value: `${bytes(p.disk_used)} / ${bytes(p.disk_total)}` },
    { label: '运行时长', value: uptime(p.uptime_sec) },
    { label: '累计流量', value: `↓ ${bytes(p.net_rx_bytes)} · ↑ ${bytes(p.net_tx_bytes)}` },
  ]
}

const secretRows = computed(() => {
  const s: ModuleNodeSecrets | undefined = created.value?.secrets
  if (!s) return []
  return [
    { label: 'API 密码', value: s.api_password },
    { label: '后量子套件', value: created.value?.node.node.pq_suite || '—' },
    { label: '后量子公钥', value: s.pq_public },
    { label: '后量子指纹', value: s.pq_fingerprint },
    { label: 'mTLS 证书 CN', value: created.value?.node.node.mtls_common_name || '—' },
    { label: '节点私钥', value: s.node_key_pem },
  ]
})
</script>

<template>
  <div class="mn">
    <header class="mn__head">
      <div>
        <h1>节点管理 <span class="mn__badge">规划 1</span></h1>
        <p class="mn__sub">
          新建空白节点只填服务器 IP + 端口；API 密码、后量子密钥、mTLS 证书自动生成。
          在服务器执行安装脚本后，面板用双向 TLS 连入读取真实主机信息。
        </p>
      </div>
      <NButton type="primary" @click="showCreate = true">＋ 新建空白节点</NButton>
    </header>

    <NAlert type="info" :bordered="false" style="margin-bottom: 16px">
      这是按新架构搭建的第 1 期：独立的模块节点域，不改动原有「节点管理」页面与数据。
      后续依次开发规划 2 ~ 10（内核 / 协议组 / 规则 / DNS / 策略组 / 模块 / 用户 / 链接 / 总控）。
    </NAlert>

    <NSpin :show="loading">
      <div v-if="!nodes.length" class="mn__empty">
        还没有节点。点「新建空白节点」，只填 IP 和端口即可。
      </div>
      <NGrid v-else cols="1 m:2 l:3" responsive="screen" :x-gap="14" :y-gap="14">
        <NGi v-for="item in nodes" :key="item.node.id">
          <NCard class="mn__card" :bordered="true">
            <div class="mn__card-head">
              <div>
                <div class="mn__addr">{{ item.node.address }}:{{ item.node.port }}</div>
                <div class="mn__id mono">{{ item.node.id }}</div>
              </div>
              <NTag :type="item.node.probe?.online ? 'success' : 'default'" size="small">
                {{ item.node.probe?.online ? '在线' : '未连接' }}
              </NTag>
            </div>

            <div class="mn__creds">
              <div><span>API 密码</span><b>已生成 · 仅存哈希</b></div>
              <div><span>后量子</span><b class="mono">{{ item.node.pq_fingerprint.slice(0, 16) }}…</b></div>
              <div><span>mTLS CN</span><b class="mono">{{ item.node.mtls_common_name }}</b></div>
            </div>

            <div v-if="item.node.probe?.error" class="mn__err">
              {{ item.node.probe.error }}
            </div>

            <div class="mn__actions">
              <NButton size="small" @click="openScript(item.node.id)">安装脚本</NButton>
              <NButton size="small" type="primary" :loading="probing === item.node.id" @click="probe(item.node.id)">
                连接探测
              </NButton>
              <NButton size="small" quaternary type="error" @click="remove(item.node.id)">删除</NButton>
            </div>

            <div v-if="item.node.probe && !item.node.probe.error" class="mn__probe">
              <div v-for="row in probeRows(item.node.probe)" :key="row.label" class="mn__probe-row">
                <span>{{ row.label }}</span><b>{{ row.value }}</b>
              </div>
            </div>
          </NCard>
        </NGi>
      </NGrid>
    </NSpin>

    <!-- 新建：只有两个输入项 -->
    <NModal v-model:show="showCreate" preset="card" title="新建空白节点" style="width: 460px">
      <div class="mn__form">
        <label>服务器 IP / 域名 <em>必填</em></label>
        <NInput v-model:value="newAddress" placeholder="例如 50.118.185.194" />
        <label>节点端口</label>
        <NInputNumber v-model:value="newPort" :min="1" :max="65535" style="width: 100%" />
        <NAlert type="default" :bordered="false">
          创建时会自动生成：API 密码（32 字节随机）、后量子密钥对（ML-KEM-768 + X25519）、
          mTLS 证书（面板 CA 签发，CN = 节点 ID）。这些都不需要手填。
        </NAlert>
      </div>
      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 8px">
          <NButton @click="showCreate = false">取消</NButton>
          <NButton type="primary" :loading="creating" @click="createNode">创建</NButton>
        </div>
      </template>
    </NModal>

    <!-- 一次性凭据 -->
    <NModal v-model:show="showSecrets" preset="card" title="一次性凭据（请立即保存）" style="width: 720px">
      <NAlert type="warning" :bordered="false">
        {{ created?.secrets.notice }} 关闭本窗口后无法再次查看；安装脚本里已包含所需材料。
      </NAlert>
      <div v-for="row in secretRows" :key="row.label" class="mn__secret">
        <span>{{ row.label }}</span>
        <code class="mono">{{ row.value }}</code>
        <NButton size="tiny" @click="copy(row.value, row.label)">复制</NButton>
      </div>
      <template #footer>
        <NButton type="primary" @click="showSecrets = false">我已保存</NButton>
      </template>
    </NModal>

    <!-- 安装脚本 -->
    <NModal v-model:show="showScript" preset="card" title="节点安装脚本" style="width: 820px">
      <NAlert type="info" :bordered="false">
        在服务器上以 root 执行：<code>bash ngg-mnode-{{ scriptNodeId }}.sh</code>。
        脚本会写入三种自动生成的凭据、注册 ngg-mnode.service 并做双向 TLS 自检。
      </NAlert>
      <NSpin :show="scriptLoading">
        <textarea class="mn__script mono" readonly :value="scriptText" />
      </NSpin>
      <template #footer>
        <div style="display: flex; gap: 8px; justify-content: flex-end">
          <NButton @click="copy(scriptText, '脚本')">复制</NButton>
          <NButton type="primary" :disabled="!scriptText" @click="downloadScript">下载 .sh</NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.mn { padding: 20px 24px 40px; max-width: 1400px; }
.mn__head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 12px; }
.mn h1 { font-size: 20px; margin: 0 0 6px; font-weight: 650; }
.mn__badge { font-size: 11px; color: #2080f0; border: 1px solid #2080f0; border-radius: 4px; padding: 1px 6px; margin-left: 8px; vertical-align: middle; }
.mn__sub { margin: 0; color: #6b7280; font-size: 13px; line-height: 1.7; max-width: 860px; }
.mn__empty { padding: 48px; text-align: center; color: #9ca3af; border: 1px dashed #d1d5db; border-radius: 10px; }
.mn__card { border-radius: 10px; }
.mn__card-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 8px; }
.mn__addr { font-size: 15px; font-weight: 650; word-break: break-all; }
.mn__id { font-size: 11px; color: #9ca3af; margin-top: 2px; }
.mono { font-family: 'SFMono-Regular', Consolas, monospace; }
.mn__creds { margin: 14px 0; border-top: 1px solid #f0f1f3; border-bottom: 1px solid #f0f1f3; padding: 10px 0; }
.mn__creds > div { display: flex; justify-content: space-between; gap: 10px; font-size: 12px; padding: 3px 0; }
.mn__creds span { color: #6b7280; }
.mn__creds b { font-weight: 550; text-align: right; word-break: break-all; }
.mn__err { font-size: 12px; color: #c0392b; background: #fff5f5; border-radius: 6px; padding: 8px; margin-bottom: 10px; word-break: break-all; }
.mn__actions { display: flex; gap: 6px; flex-wrap: wrap; }
.mn__probe { margin-top: 12px; border-top: 1px solid #f0f1f3; padding-top: 8px; }
.mn__probe-row { display: flex; justify-content: space-between; gap: 8px; font-size: 12px; padding: 3px 0; }
.mn__probe-row span { color: #6b7280; }
.mn__form label { display: block; font-size: 13px; font-weight: 550; margin: 12px 0 6px; }
.mn__form label em { font-style: normal; color: #c0392b; font-size: 11px; margin-left: 4px; }
.mn__form .n-alert { margin-top: 14px; }
.mn__secret { display: grid; grid-template-columns: 120px 1fr auto; align-items: center; gap: 8px; margin-top: 10px; }
.mn__secret span { font-size: 12px; color: #6b7280; }
.mn__secret code { font-size: 11px; background: #f6f7f9; padding: 6px 8px; border-radius: 5px; overflow: auto; max-height: 62px; white-space: pre-wrap; word-break: break-all; }
.mn__script { width: 100%; height: 340px; font-size: 11px; line-height: 1.55; border: 1px solid #e5e7eb; border-radius: 6px; padding: 10px; resize: vertical; background: #fafbfc; }
</style>
