import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'

const routes: RouteRecordRaw[] = [
  { path: '/', redirect: { name: 'dashboard' } },
  {
    path: '/dashboard',
    name: 'dashboard',
    component: () => import('../views/Dashboard.vue'),
    meta: { title: '概览' },
  },
  {
    path: '/cores',
    name: 'cores',
    component: () => import('../views/Cores.vue'),
    meta: { title: '内核管理' },
  },
  {
    path: '/config',
    name: 'config',
    component: () => import('../views/Config.vue'),
    meta: { title: '配置管理' },
  },
  {
    path: '/logs',
    name: 'logs',
    component: () => import('../views/Logs.vue'),
    meta: { title: '实时日志' },
  },
  {
    path: '/versions',
    name: 'versions',
    component: () => import('../views/Versions.vue'),
    meta: { title: '版本管理' },
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('../views/Settings.vue'),
    meta: { title: '系统设置' },
  },
  {
    path: '/network',
    name: 'network',
    component: () => import('../views/Network.vue'),
    meta: { title: 'Stage 2 网络引擎' },
  },
  {
    path: '/rules',
    name: 'rules',
    component: () => import('../views/Rules.vue'),
    meta: { title: 'Stage 3 规则与 DNS' },
  },
  {
    path: '/protocols',
    name: 'protocols',
    component: () => import('../views/Protocols.vue'),
    meta: { title: 'Stage 4 协议中心' },
  },
  {
    path: '/pq',
    name: 'pq',
    component: () => import('../views/PQ.vue'),
    meta: { title: '后量子密码' },
  },
  {
    path: '/plugins',
    name: 'plugins',
    component: () => import('../views/Plugins.vue'),
    meta: { title: '插件运行时' },
  },
  {
    path: '/sdwan',
    name: 'sdwan',
    component: () => import('../views/SDWAN.vue'),
    meta: { title: 'Stage 5 SD-WAN' },
  },
  {
    path: '/tenants',
    name: 'tenants',
    component: () => import('../views/Tenants.vue'),
    meta: { title: '多租户' },
  },
  {
    path: '/platforms',
    name: 'platforms',
    component: () => import('../views/Platforms.vue'),
    meta: { title: '跨平台构建' },
  },
  {
    path: '/stages',
    name: 'stages',
    component: () => import('../views/Stages.vue'),
    meta: { title: '阶段总览' },
  },
  { path: '/:pathMatch(.*)*', redirect: { name: 'dashboard' } },
]

const router = createRouter({
  // hash 模式便于后端直接托管静态文件，无需额外的 history fallback 配置
  history: createWebHashHistory(),
  routes,
})

export default router
