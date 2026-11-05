import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createDiscreteApi, zhCN, dateZhCN } from 'naive-ui'
import App from './App.vue'
import router from './router'
import { themeOverrides } from './styles/theme'
import './styles/main.css'

// 认证完全交给后端会话 Cookie：首次打开是「创建管理员」，之后是登录页。
// 不再从 URL 读取 API Token，避免凭据出现在浏览器历史里。

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
