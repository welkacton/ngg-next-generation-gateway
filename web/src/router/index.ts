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
    meta: { title: '网络引擎' },
  },
  {
    path: '/rules',
    name: 'rules',
    component: () => import('../views/Rules.vue'),
    meta: { title: '规则配置' },
  },
  {
    path: '/dns',
    name: 'dns',
    component: () => import('../views/DNS.vue'),
    meta: { title: 'DNS配置' },
  },
  {
    path: '/inbound',
    name: 'inbound',
    component: () => import('../views/Inbound.vue'),
    meta: { title: '入口节点' },
  },
  {
    path: '/outbound',
    name: 'outbound',
    component: () => import('../views/Outbound.vue'),
    meta: { title: '出口节点' },
  },
  {
    path: '/protocols',
    name: 'protocols',
    component: () => import('../views/Protocols.vue'),
    meta: { title: '协议配置' },
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
    meta: { title: 'SD-WAN' },
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
