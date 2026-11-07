import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'

const routes: RouteRecordRaw[] = [
  { path: '/', redirect: { name: 'dashboard' } },
  {
    path: '/dashboard',
    name: 'dashboard',
    component: () => import('../views/Dashboard.vue'),
    meta: { title: '系统概览' },
  },
  {
    path: '/network-overview',
    name: 'network-overview',
    component: () => import('../views/NetworkOverview.vue'),
    meta: { title: '网络概览' },
  },
  {
    path: '/gateway/wan',
    name: 'gateway-wan',
    component: () => import('../views/gateway/WanSettings.vue'),
    meta: { title: 'WAN 设置' },
  },
  {
    path: '/gateway/master',
    name: 'gateway-master',
    component: () => import('../views/gateway/ControlCenter.vue'),
    meta: { title: '控制中心' },
  },
  {
    path: '/clients',
    name: 'client-access',
    component: () => import('../views/ClientAccess.vue'),
    meta: { title: '客户端接入' },
  },
  {
    path: '/gateway/lan',
    name: 'gateway-lan',
    component: () => import('../views/gateway/LanSettings.vue'),
    meta: { title: 'LAN 设置' },
  },
  {
    path: '/gateway/dns',
    name: 'gateway-dns',
    component: () => import('../views/gateway/GatewayDNS.vue'),
    meta: { title: 'DNS 设置' },
  },
  {
    path: '/gateway/sdwan',
    name: 'gateway-sdwan',
    component: () => import('../views/SDWAN.vue'),
    meta: { title: 'SD-WAN' },
  },
  {
    path: '/gateway/firewall',
    name: 'gateway-firewall',
    component: () => import('../views/gateway/FirewallSettings.vue'),
    meta: { title: '防火墙设置' },
  },
  { path: '/sdwan', redirect: { name: 'gateway-sdwan' } },
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
    meta: { title: '高级诊断' },
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
  { path: '/inbound', redirect: { name: 'nodes' } },
  { path: '/outbound', redirect: { name: 'nodes' } },
  {
    path: '/users',
    name: 'users',
    component: () => import('../views/Users.vue'),
    meta: { title: '用户管理' },
  },
  {
    path: '/pipeline',
    name: 'pipeline',
    component: () => import('../views/Pipeline.vue'),
    meta: { title: '链路编排' },
  },
  {
    path: '/protocols',
    name: 'protocols',
    component: () => import('../views/Protocols.vue'),
    meta: { title: '协议配置' },
  },
  {
    path: '/nodes',
    name: 'nodes',
    component: () => import('../views/Nodes.vue'),
    meta: { title: '节点管理' },
  },
  {
    path: '/module-nodes',
    name: 'module-nodes',
    component: () => import('../views/ModuleNodes.vue'),
    meta: { title: '① 节点管理（模块架构）' },
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
