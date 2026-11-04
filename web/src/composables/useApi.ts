import { ref } from 'vue'

/**
 * 页面级异步动作包装：统一管理按钮 loading 与成功提示。
 * - 接口错误已由 axios 响应拦截器用 window.$message 统一提示，这里静默返回 null；
 * - 页面据此只在拿到真实结果时更新视图，不写入任何模拟/兜底数据。
 */
export function useApiAction() {
  const busy = ref<Record<string, boolean>>({})

  function isBusy(key: string): boolean {
    return !!busy.value[key]
  }

  async function run<T>(
    key: string,
    fn: () => Promise<T>,
    okMsg?: string,
  ): Promise<T | null> {
    busy.value = { ...busy.value, [key]: true }
    try {
      const result = await fn()
      if (okMsg) window.$message?.success(okMsg)
      return result
    } catch {
      // 错误提示已由拦截器处理
      return null
    } finally {
      const next = { ...busy.value }
      delete next[key]
      busy.value = next
    }
  }

  return { busy, isBusy, run }
}
