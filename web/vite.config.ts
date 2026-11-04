import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 开发环境（以及 preview）把所有 /api 请求代理到 NGG 后端。
// ws: true 保证 /api/v1/ws 的 WebSocket 升级也能被转发。
const proxy = {
  '/api': {
    target: 'http://127.0.0.1:9090',
    changeOrigin: true,
    ws: true,
  },
}

export default defineConfig({
  plugins: [vue()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    proxy,
  },
  preview: {
    host: '127.0.0.1',
    port: 4173,
    proxy,
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
    chunkSizeWarningLimit: 1500,
  },
})
