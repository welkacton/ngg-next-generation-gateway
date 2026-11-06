import axios, { AxiosError, type AxiosResponse } from 'axios'

// High-frequency editor requests (auto-sync while typing) must not spam the
// global error toast: the editor shows the parse error inline instead.
declare module 'axios' {
  export interface AxiosRequestConfig {
    silentError?: boolean
  }
}

export const API_BASE = '/api/v1'

// 浏览器端不再保存任何 API Token：控制台改由 HttpOnly 会话 Cookie 认证，
// 首次打开是「创建管理员」，之后是「登录」。启动时清理历史遗留凭据。
try {
  localStorage.removeItem('ngg_github_token')
  localStorage.removeItem('ngg_token')
} catch {
  // 隐私模式 / 禁用 localStorage 时忽略
}

export function buildWsUrl(): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}${API_BASE}/ws`
}

// 不设置全局 Content-Type：axios 会为普通对象自动加 application/json，
// 为 FormData 让浏览器自动带 multipart/form-data + boundary。
const client = axios.create({
  baseURL: API_BASE,
  timeout: 30000,
  withCredentials: true,
})

/**
 * While the updater replaces the binary and restarts the service, every
 * in-flight request fails with a transport error. Suppress those instead of
 * showing one red toast per request.
 */
let maintenance = false
let lastTransportToastAt = 0

export function setMaintenance(active: boolean): void {
  maintenance = active
}

function extractError(error: AxiosError): string {
  const data = error.response?.data as
    | { error?: string; message?: string }
    | string
    | undefined
  let detail = ''
  if (typeof data === 'string') {
    detail = data
  } else if (data && typeof data === 'object') {
    detail = data.error || data.message || ''
  }
  if (!detail) detail = error.message || '请求失败'

  const status = error.response?.status
  const method = (error.config?.method || '').toUpperCase()
  const url = error.config?.url || ''
  const prefix = status ? `HTTP ${status} · ` : ''
  return `${prefix}${method} ${url} · ${detail}`
}

client.interceptors.response.use(
  (response: AxiosResponse) => response,
  (error: AxiosError) => {
    const msg = extractError(error)
    const status = error.response?.status
    if (status === 401) {
      // 会话过期：交给 App 切回登录页，不弹通用错误提示。
      window.dispatchEvent(new Event('ngg-unauthorized'))
    } else if (!error.config?.silentError) {
      if (!error.response) {
        // Service restarting / unreachable. One throttled notice for the whole
        // burst, and a signal pages can use to pause polling.
        window.dispatchEvent(new Event('ngg-backend-unreachable'))
        const now = Date.now()
        if (!maintenance && now - lastTransportToastAt > 8000) {
          lastTransportToastAt = now
          window.$message?.warning('后端服务暂时不可达，正在自动重连…', { duration: 4000 })
        }
      } else if (window.$message) {
        window.$message.error(msg, { duration: 6000, keepAliveOnHover: true })
      } else {
        console.error('[NGG API]', msg)
      }
    }
    return Promise.reject(new Error(msg))
  },
)

export default client
