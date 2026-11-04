import { onMounted, onUnmounted, ref } from 'vue'
import { buildWsUrl } from '../api/client'
import type { GatewayEvent } from '../api/ngg'

/**
 * 订阅 /api/v1/ws 事件流。
 * - 断线后按指数退避（1s → 2s → 4s … 上限 30s）自动重连
 * - token 变更（ngg-token-change 事件）时立即用新 token 重连
 * - 组件卸载时关闭连接，避免重复连接
 */
export function useWebSocket(onEvent: (event: GatewayEvent) => void) {
  const connected = ref(false)
  const lastError = ref('')

  let socket: WebSocket | null = null
  let retryCount = 0
  let reconnectTimer: number | undefined
  let disposed = false

  function clearTimer(): void {
    if (reconnectTimer !== undefined) {
      window.clearTimeout(reconnectTimer)
      reconnectTimer = undefined
    }
  }

  function scheduleReconnect(): void {
    if (disposed) return
    retryCount = Math.min(retryCount + 1, 6)
    const delay = Math.min(1000 * 2 ** (retryCount - 1), 30000)
    lastError.value = `连接已断开，${delay / 1000}s 后重连（第 ${retryCount} 次）`
    clearTimer()
    reconnectTimer = window.setTimeout(connect, delay)
  }

  function connect(): void {
    if (disposed) return
    clearTimer()
    try {
      const ws = new WebSocket(buildWsUrl())
      socket = ws
      ws.onopen = () => {
        connected.value = true
        retryCount = 0
        lastError.value = ''
      }
      ws.onmessage = (message: MessageEvent) => {
        try {
          const event = JSON.parse(String(message.data)) as GatewayEvent
          onEvent(event)
        } catch {
          // 忽略无法解析的帧，不影响后续消息
        }
      }
      ws.onerror = () => {
        lastError.value = 'WebSocket 连接发生错误'
      }
      ws.onclose = () => {
        connected.value = false
        if (socket === ws) socket = null
        scheduleReconnect()
      }
    } catch (error) {
      lastError.value = `建立 WebSocket 失败：${String(error)}`
      scheduleReconnect()
    }
  }

  function closeCurrent(): void {
    if (socket) {
      const old = socket
      socket = null
      old.onclose = null
      old.onerror = null
      old.onmessage = null
      try {
        old.close()
      } catch {
        // 忽略关闭异常
      }
    }
  }

  function reconnect(): void {
    if (disposed) return
    clearTimer()
    closeCurrent()
    connected.value = false
    retryCount = 0
    connect()
  }

  function stop(): void {
    disposed = true
    clearTimer()
    closeCurrent()
    connected.value = false
  }

  onMounted(() => {
    disposed = false
    connect()
    window.addEventListener('ngg-token-change', reconnect)
  })

  onUnmounted(() => {
    stop()
    window.removeEventListener('ngg-token-change', reconnect)
  })

  return { connected, lastError, reconnect }
}
