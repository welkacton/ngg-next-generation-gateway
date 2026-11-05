/** 状态枚举的中文显示名。 */
export const STATE_LABELS: Record<string, string> = {
  not_installed: '未安装',
  installed: '已安装',
  starting: '启动中',
  running: '运行中',
  stopping: '停止中',
  stopped: '已停止',
  failed: '失败',
  unknown: '未知',
}

/** 状态 → NTag 配色：running 绿 / failed 红 / stopped 灰 / not_installed 橙。 */
export function stateTagType(
  state?: string,
): 'success' | 'error' | 'warning' | 'info' | 'default' | 'primary' {
  switch (state) {
    case 'running':
      return 'success'
    case 'failed':
      return 'error'
    case 'not_installed':
      return 'warning'
    case 'starting':
    case 'stopping':
      return 'info'
    case 'stopped':
    case 'installed':
    default:
      return 'default'
  }
}

export function formatUptime(sec?: number): string {
  if (sec === undefined || sec === null || Number.isNaN(sec)) return '—'
  const total = Math.max(0, Math.floor(sec))
  const days = Math.floor(total / 86400)
  const hours = Math.floor((total % 86400) / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const seconds = total % 60
  if (days) return `${days} 天 ${hours} 小时 ${minutes} 分`
  if (hours) return `${hours} 小时 ${minutes} 分 ${seconds} 秒`
  if (minutes) return `${minutes} 分 ${seconds} 秒`
  return `${seconds} 秒`
}

export function formatTime(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

export function formatBytes(value?: number): string {
  if (value === undefined || value === null || Number.isNaN(value)) return '—'
  // Byte counters are integral; rates derived from real deltas can be floats.
  // Never leak a long fractional tail into the UI.
  if (value < 1024) return `${Math.round(value)} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let size = value / 1024
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index += 1
  }
  return `${size.toFixed(1)} ${units[index]}`
}

export function shortSha(sha?: string): string {
  if (!sha) return '—'
  return sha.length > 18 ? `${sha.slice(0, 18)}…` : sha
}

/** 日志流 → CSS 颜色类。 */
export function logClass(stream?: string): string {
  switch (stream) {
    case 'stderr':
      return 'log-stderr'
    case 'ngg':
      return 'log-ngg'
    case 'stdout':
      return 'log-stdout'
    default:
      return 'log-other'
  }
}

/** 任意后端返回 → 便于阅读的 JSON 文本，用于 <pre class="mono"> 如实展示。 */
export function prettyJson(value: unknown): string {
  if (value === null || value === undefined) return '（无数据）'
  if (typeof value === 'string') return value
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

/**
 * 把后端返回规范化成数组：既兼容直接数组，也兼容
 * { sessions: [...] } / { results: [...] } 等包裹形态。
 */
export function toArray<T = Record<string, unknown>>(value: unknown): T[] {
  if (Array.isArray(value)) return value as T[]
  if (value && typeof value === 'object') {
    const record = value as Record<string, unknown>
    const keys = [
      'items', 'sessions', 'endpoints', 'rules', 'links', 'users',
      'plugins', 'protocols', 'stages', 'results', 'data', 'list',
      'clients', 'targets', 'answers',
    ]
    for (const key of keys) {
      if (Array.isArray(record[key])) return record[key] as T[]
    }
  }
  return []
}

