import axios, { AxiosError, type AxiosResponse } from 'axios'

// High-frequency editor requests (auto-sync while typing) must not spam the
// global error toast: the editor shows the parse error inline instead.
declare module 'axios' {
  export interface AxiosRequestConfig {
    silentError?: boolean
  }
}

export const TOKEN_KEY = 'ngg_token'
export const API_BASE = '/api/v1'

// 旧版本曾把 GitHub PAT 保存在这里（私有仓库时期）。仓库已公开，更新面板
// 不再需要任何 GitHub Token；启动时主动清掉历史遗留凭据，避免长期留在浏览器。
try {
  localStorage.removeItem('ngg_github_token')
} catch {
  // 隐私模式 / 禁用 localStorage 时忽略
}

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) || ''
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token)
  // 通知 WebSocket 用新 token 重连
  window.dispatchEvent(new Event('ngg-token-change'))
}

export function buildWsUrl(): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}${API_BASE}/ws?token=${encodeURIComponent(getToken())}`
}

// 不设置全局 Content-Type：axios 会为普通对象自动加 application/json，
// 为 FormData 让浏览器自动带 multipart/form-data + boundary。
const client = axios.create({
  baseURL: API_BASE,
  timeout: 30000,
})

client.interceptors.request.use((config) => {
  const token = getToken()
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  // Update checks are anonymous: the repository is public and no GitHub token
  // is stored or attached anymore.
  return config
})

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
    if (!error.config?.silentError) {
      if (window.$message) {
        window.$message.error(msg, { duration: 6000, keepAliveOnHover: true })
      } else {
        console.error('[NGG API]', msg)
      }
    }
    return Promise.reject(new Error(msg))
  },
)

export default client
