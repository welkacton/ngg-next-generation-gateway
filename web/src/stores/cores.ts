import { defineStore } from 'pinia'
import * as api from '../api/ngg'
import type {
  AuditRecord,
  ConfigBackup,
  ConfigFile,
  CoreDescriptor,
  CoreStats,
  CoreStatus,
  GatewayEvent,
  HealthInfo,
  LogEntry,
  PortAllocation,
  SystemInfo,
  VersionInfo,
} from '../api/ngg'

interface CoresState {
  cores: CoreStatus[]
  registry: CoreDescriptor[]
  system: SystemInfo | null
  health: HealthInfo | null
  ports: PortAllocation[]
  audit: AuditRecord[]
  stats: Record<string, CoreStats>
  // 配置管理
  config: ConfigFile | null
  configCoreId: string
  configDraft: string
  backups: ConfigBackup[]
  validateOutput: string
  // 版本管理
  versions: VersionInfo[]
  currentVersion: VersionInfo | null
  // 实时日志
  logs: LogEntry[]
  activeLogCore: string
  // 安装进度 / WS / 通用状态
  installProgress: Record<string, string>
  wsConnected: boolean
  busy: Record<string, boolean>
  baseLoading: boolean
  loaded: boolean
  lastError: string
}

/** 统一吞掉已由 axios 拦截器提示过的错误，返回 null 表示失败。 */
async function safe<T>(promise: Promise<T>): Promise<T | null> {
  try {
    return await promise
  } catch {
    return null
  }
}

/** 统一按钮 loading + 成功提示。store 用 any 传入，避免泛型与 this 的循环推断。 */
async function busy<T>(
  store: any,
  key: string,
  fn: () => Promise<T>,
  okMsg?: string,
): Promise<T | null> {
  store.busy = { ...store.busy, [key]: true }
  try {
    const result = await fn()
    if (okMsg && window.$message) window.$message.success(okMsg)
    return result
  } catch (error) {
    store.lastError = error instanceof Error ? error.message : String(error)
    return null
  } finally {
    const next = { ...store.busy }
    delete next[key]
    store.busy = next
  }
}

function notify(kind: 'success' | 'warning' | 'error' | 'info', text: string): void {
  const m = window.$message
  if (!m) return
  m[kind](text)
}

let basePromise: Promise<void> | null = null

export const useCoresStore = defineStore('cores', {
  state: (): CoresState => ({
    cores: [],
    registry: [],
    system: null,
    health: null,
    ports: [],
    audit: [],
    stats: {},
    config: null,
    configCoreId: '',
    configDraft: '',
    backups: [],
    validateOutput: '',
    versions: [],
    currentVersion: null,
    logs: [],
    activeLogCore: '',
    installProgress: {},
    wsConnected: false,
    busy: {},
    baseLoading: false,
    loaded: false,
    lastError: '',
  }),

  getters: {
    runningCount: (s): number => s.cores.filter((c) => c.state === 'running').length,
    installedCount: (s): number => s.cores.filter((c) => c.installed).length,
    coreOptions: (s) =>
      s.cores.map((c) => ({
        label: `${c.display_name}（${c.id}）`,
        value: c.id,
      })),
    isBusy: (s) => (key: string): boolean => !!s.busy[key],
  },

  actions: {
    setWsConnected(v: boolean): void {
      this.wsConnected = v
    },

    /* ---------------- 基础数据 ---------------- */

    async ensureBase(force = false): Promise<void> {
      if (this.loaded && !force) return
      if (basePromise && !force) return basePromise
      basePromise = this.loadBase().finally(() => {
        basePromise = null
      })
      return basePromise
    },

    async loadBase(): Promise<void> {
      this.baseLoading = true
      try {
        const results = await Promise.allSettled([
          api.getSystem().then((d) => (this.system = d)),
          api.getHealth().then((d) => (this.health = d)),
          api.listCores().then((d) => (this.cores = d)),
          api.getRegistry().then((d) => (this.registry = d)),
          api.getPorts().then((d) => (this.ports = d)),
          api.getAudit().then((d) => (this.audit = d)),
        ])
        const failed = results.filter((r) => r.status === 'rejected')
        this.lastError = failed.length
          ? `${failed.length} 项基础数据加载失败，请检查 Token 与后端服务`
          : ''
        this.loaded = failed.length === 0
      } finally {
        this.baseLoading = false
      }
    },

    async forceRefresh(): Promise<void> {
      this.loaded = false
      await this.ensureBase(true)
      await this.refreshCores()
      if (window.$message) window.$message.success('数据已刷新')
    },

    async refreshCores(): Promise<void> {
      const data = await safe(api.listCores())
      if (data) this.cores = data
    },

    async refreshSystemInfo(): Promise<void> {
      const [system, health, audit] = await Promise.all([
        safe(api.getSystem()),
        safe(api.getHealth()),
        safe(api.getAudit()),
      ])
      if (system) this.system = system
      if (health) this.health = health
      if (audit) this.audit = audit
    },

    async refreshAudit(): Promise<void> {
      const data = await safe(api.getAudit())
      if (data) this.audit = data
    },

    async refreshPorts(): Promise<void> {
      const data = await safe(api.getPorts())
      if (data) this.ports = data
    },

    async refreshRegistry(): Promise<void> {
      const data = await safe(api.getRegistry())
      if (data) this.registry = data
    },

    async refreshRegistryAndCores(): Promise<void> {
      await Promise.all([this.refreshRegistry(), this.refreshCores()])
    },

    async reloadRegistryAction(): Promise<void> {
      const result = await busy(this, 'reload-registry', () =>
        api.reloadRegistry(),
      )
      if (!result) return
      notify('success', `注册表已重载，共加载 ${result.loaded} 个内核描述`)
      if (result.problems && result.problems.length) {
        notify('warning', `存在 ${result.problems.length} 个问题：${result.problems.join('；')}`)
      }
      await this.refreshRegistryAndCores()
    },

    async pollStats(): Promise<void> {
      const running = this.cores.filter((c) => c.state === 'running')
      await Promise.all(
        running.map(async (c) => {
          const s = await safe(api.getStats(c.id))
          if (s) this.stats = { ...this.stats, [c.id]: s }
        }),
      )
    },

    /* ---------------- 内核操作 ---------------- */

    async startCoreAction(id: string): Promise<void> {
      await busy(this, `start:${id}`, () => api.startCore(id), `内核 ${id} 启动命令已下发`)
      await this.refreshCores()
    },

    async stopCoreAction(id: string): Promise<void> {
      await busy(this, `stop:${id}`, () => api.stopCore(id), `内核 ${id} 停止命令已下发`)
      await this.refreshCores()
    },

    async restartCoreAction(id: string): Promise<void> {
      await busy(this, `restart:${id}`, () => api.restartCore(id), `内核 ${id} 重启命令已下发`)
      await this.refreshCores()
    },

    async installCoreAction(id: string, version?: string): Promise<void> {
      const result = await busy(this, `install:${id}`, () =>
        api.installCore(id, version),
      )
      if (!result) return
      this.installProgress = {
        ...this.installProgress,
        [id]: '安装任务已提交，等待后端进度事件…',
      }
      notify('success', `内核 ${id} 安装已开始`)
      await this.refreshCores()
    },

    async uninstallCoreAction(id: string): Promise<void> {
      const result = await busy(this, `uninstall:${id}`, () =>
        api.uninstallCore(id),
      )
      if (!result) return
      notify('success', `内核 ${id} 已卸载`)
      const next = { ...this.installProgress }
      delete next[id]
      this.installProgress = next
      await this.refreshCores()
    },

    async upgradeCoreAction(id: string, version: string): Promise<void> {
      const result = await busy(this, `upgrade:${id}`, () =>
        api.upgradeCore(id, version),
      )
      if (!result) return
      notify('success', `内核 ${id} 已升级到 ${version}`)
      await Promise.all([this.refreshCores(), this.loadVersions(id)])
    },

    async rollbackVersionAction(id: string, version: string): Promise<void> {
      const result = await busy(this, `rollback-version:${id}`, () =>
        api.rollbackCore(id, version),
      )
      if (!result) return
      notify('success', `内核 ${id} 已回滚到 ${version}`)
      await Promise.all([this.refreshCores(), this.loadVersions(id)])
    },

    async batchStartAction(ids: string[], exclusive: boolean): Promise<void> {
      const result = await busy(this, 'batch-start', () =>
        api.batchStart(ids, exclusive),
      )
      if (!result) return
      const failed = result.results.filter((r) => !r.ok)
      if (failed.length) {
        notify(
          'warning',
          `${failed.length} 个内核启动失败：${failed
            .map((r) => `${r.id}（${r.message}）`)
            .join('；')}`,
        )
      } else {
        notify(
          'success',
          `${exclusive ? '单核独占' : '多核并行'}启动成功，共 ${result.results.length} 个内核`,
        )
      }
      await this.refreshCores()
    },

    async batchStopAction(ids: string[]): Promise<void> {
      const result = await busy(this, 'batch-stop', () => api.batchStop(ids))
      if (!result) return
      const failed = result.results.filter((r) => !r.ok)
      if (failed.length) {
        notify(
          'warning',
          `${failed.length} 个内核停止失败：${failed
            .map((r) => `${r.id}（${r.message}）`)
            .join('；')}`,
        )
      } else {
        notify('success', `已停止 ${result.results.length} 个内核`)
      }
      await this.refreshCores()
    },

    /* ---------------- 配置管理 ---------------- */

    async loadConfig(id: string): Promise<void> {
      this.configCoreId = id
      this.validateOutput = ''
      const data = await safe(api.getConfig(id))
      if (data) {
        this.config = data
        this.configDraft = data.content
      }
      await this.loadBackups(id)
    },

    async loadBackups(id: string): Promise<void> {
      const data = await safe(api.listBackups(id))
      if (data) this.backups = data
    },

    async validateConfigAction(id: string, content: string): Promise<void> {
      const result = await busy(this, 'validate', () =>
        api.validateConfig(id, content),
      )
      if (!result) return
      this.validateOutput = result.output || '（内核未返回任何输出）'
      if (result.ok) notify('success', '配置校验通过')
      else notify('error', '配置校验未通过，请查看校验输出')
    },

    async saveConfigAction(
      id: string,
      content: string,
      reason?: string,
    ): Promise<void> {
      const result = await busy(this, 'save-config', () =>
        api.saveConfig(id, content, reason),
      )
      if (!result) return
      notify('success', '配置已保存：已自动备份 → 原生校验 → 原子替换')
      await this.loadConfig(id)
    },

    async rollbackConfigAction(id: string, backupId: string): Promise<void> {
      const result = await busy(this, `rollback-config:${backupId}`, () =>
        api.rollbackConfig(id, backupId),
      )
      if (!result) return
      notify('success', '配置已回滚到所选备份')
      await this.loadConfig(id)
    },

    async exportConfigAction(id: string): Promise<void> {
      const result = await busy(this, 'export-config', () => api.exportConfig(id))
      if (!result) return
      const url = URL.createObjectURL(result.blob)
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = result.filename
      document.body.appendChild(anchor)
      anchor.click()
      document.body.removeChild(anchor)
      URL.revokeObjectURL(url)
      notify('success', `已导出配置文件 ${result.filename}`)
    },

    async importConfigAction(id: string, file: File): Promise<void> {
      const result = await busy(this, 'import-config', () =>
        api.importConfig(id, file),
      )
      if (!result) return
      notify('success', '配置文件已导入')
      await this.loadConfig(id)
    },

    /* ---------------- 版本管理 ---------------- */

    async loadVersions(id: string): Promise<void> {
      const [list, current] = await Promise.all([
        safe(api.listVersions(id)),
        safe(api.getCurrentVersion(id)),
      ])
      if (list) this.versions = list
      this.currentVersion = current
    },

    /* ---------------- 实时日志 ---------------- */

    setLogCore(id: string): void {
      this.activeLogCore = id
      this.logs = []
      if (id) this.loadHistory(id, 0, 300)
    },

    async loadHistory(id: string, after: number, limit: number): Promise<void> {
      const data = await safe(api.getLogs(id, after, limit))
      if (!data) return
      for (const entry of data) this.appendLog(entry)
    },

    appendLog(entry: unknown): void {
      if (!entry || typeof entry !== 'object') return
      const log = entry as LogEntry
      if (this.activeLogCore && log.core_id && log.core_id !== this.activeLogCore) {
        return
      }
      if (this.logs.some((x) => x.seq === log.seq && x.core_id === log.core_id)) {
        return
      }
      this.logs.push(log)
      if (this.logs.length > 2000) {
        this.logs.splice(0, this.logs.length - 2000)
      }
    },

    async clearLogsAction(id: string): Promise<void> {
      const result = await busy(this, 'clear-logs', () => api.clearLogs(id))
      if (!result) return
      this.logs = []
      notify('success', `内核 ${id} 的日志已清空`)
    },

    /* ---------------- WebSocket 事件 ---------------- */

    applyEvent(event: GatewayEvent): void {
      if (!event || !event.type) return
      switch (event.type) {
        case 'core.state':
          this.refreshCores()
          this.refreshSystemInfo()
          break
        case 'core.log':
          if (Array.isArray(event.payload)) {
            for (const item of event.payload) this.appendLog(item)
          } else {
            this.appendLog(event.payload)
          }
          break
        case 'core.stats':
          if (event.core_id && event.payload) {
            this.stats = {
              ...this.stats,
              [event.core_id]: event.payload as CoreStats,
            }
          }
          break
        case 'core.install': {
          const id = event.core_id || ''
          const payload = event.payload as any
          const text =
            typeof event.payload === 'string'
              ? event.payload
              : payload && typeof payload === 'object'
                ? String(payload.message ?? payload.status ?? payload.progress ?? JSON.stringify(payload))
                : String(event.payload ?? '')
          this.installProgress = { ...this.installProgress, [id]: text }
          const done =
            payload &&
            typeof payload === 'object' &&
            (payload.done === true ||
              payload.ok === true ||
              payload.status === 'done' ||
              payload.status === 'completed' ||
              payload.status === 'finished')
          if (done) {
            notify('success', `内核 ${id} 安装完成`)
            this.refreshCores()
          }
          break
        }
        case 'system.alert': {
          const text =
            typeof event.payload === 'string'
              ? event.payload
              : JSON.stringify(event.payload ?? {})
          if (window.$notification) {
            window.$notification.warning({
              title: '系统告警',
              content: text,
              duration: 6000,
            })
          } else {
            notify('warning', `系统告警：${text}`)
          }
          this.refreshAudit()
          break
        }
        case 'session.rejected': {
          const text =
            typeof event.payload === 'string'
              ? event.payload
              : JSON.stringify(event.payload ?? {})
          notify('warning', `会话被拒绝：${text}`)
          window.dispatchEvent(
            new CustomEvent('ngg-network-event', { detail: event }),
          )
          break
        }
        default:
          // network.* 事件通知 Network 页面刷新状态；其余未知类型静默忽略
          if (event.type.startsWith('network.')) {
            window.dispatchEvent(
              new CustomEvent('ngg-network-event', { detail: event }),
            )
          }
          break
      }
    },
  },
})
