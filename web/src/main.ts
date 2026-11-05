import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createDiscreteApi, zhCN, dateZhCN } from 'naive-ui'
import { setToken, TOKEN_KEY } from './api/client'
import App from './App.vue'
import router from './router'
import { themeOverrides } from './styles/theme'
import './styles/main.css'

// 本机首次启动引导：nggd 会在控制台打印自动生成的 API Token，用户可用
// http://127.0.0.1:9090/?token=<TOKEN> 打开管理界面完成一次性绑定。
// Token 仅经本机回环传递，写入 localStorage 后立即从地址栏移除。
const params = new URLSearchParams(window.location.search)
const bootstrapToken = params.get(TOKEN_KEY) || params.get('token')
if (bootstrapToken) {
  setToken(bootstrapToken)
  params.delete(TOKEN_KEY)
  params.delete('token')
  const rest = params.toString()
  window.history.replaceState(
    {},
    '',
    window.location.pathname + (rest ? `?${rest}` : '') + window.location.hash,
  )
}

// Naive UI 的离散 API：让 axios 拦截器、非组件上下文也能弹出全局提示。
// 离散组件挂载在 body 上，不在 n-config-provider 内，因此需显式复用主题覆盖。
const { message, dialog, notification } = createDiscreteApi(
  ['message', 'dialog', 'notification'],
  {
    configProviderProps: {
      themeOverrides,
      locale: zhCN,
      dateLocale: dateZhCN,
    },
  },
)

window.$message = message
window.$dialog = dialog
window.$notification = notification

createApp(App).use(createPinia()).use(router).mount('#app')
