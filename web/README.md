# NGG Web 管理界面（Phase 1）

Vue 3 + TypeScript + Vite + Naive UI 实现的 NGG 统一管理中心前端，暗色主题、简体中文。
所有数据均来自真实后端 REST / WebSocket 接口，不含任何模拟数据。

## 启动

需要 Node.js 18+（已在 Node 22 验证）。

```bash
cd web
npm install
npm run dev
```

开发服务器默认监听 `http://127.0.0.1:5173`，Vite 会把 `/api` 代理到
`http://127.0.0.1:9090`（含 WebSocket 升级转发），因此需先启动 NGG 后端。

首次打开请在「系统设置」页填入后端配置的默认 API Token，前端会将其保存到
`localStorage.ngg_token`，并自动为所有请求加上 `Authorization: Bearer <token>`、
为 WebSocket 连接加上 `?token=<token>`。Token 变更后 WebSocket 会立即重连。

## 构建与预览

```bash
npm run build      # vue-tsc --noEmit && vite build，输出到 dist/
npm run preview    # 预览构建产物，同样代理 /api 到后端
npm run typecheck  # 仅做 TypeScript / 模板类型检查
```

生产部署时把 `dist/` 交给后端静态文件服务托管，并保证 `/api/v1` 与
`/api/v1/ws` 指向 NGG 后端（路由使用 hash 模式，无需 history fallback）。

## 目录结构

```
src/
├── api/
│   ├── client.ts            # axios 实例：Token 注入、统一错误提示、WS URL 构造
│   └── ngg.ts               # 全部 REST 端点封装 + TS interface
├── composables/useWebSocket.ts   # WS 自动重连（指数退避）、事件分发
├── stores/cores.ts          # pinia：共享状态、按钮 loading、WS 事件处理
├── router/index.ts          # 六个页面路由
├── styles/                  # 暗色主题覆盖（主色 #36adcb）与全局样式
├── utils/format.ts          # 状态配色、时间/字节格式化
├── views/                   # Dashboard / Cores / Config / Logs / Versions / Settings
├── App.vue                  # 侧边导航、顶栏、WS 生命周期
└── main.ts                  # createDiscreteApi：全局 $message / $dialog / $notification
```

## 页面与接口对应

| 页面 | 主要接口 |
| --- | --- |
| 概览 | `/system`、`/health`、`/cores`、`/cores/{id}/stats`、`/audit` |
| 内核管理 | `/cores`、`/cores/{id}/{start,stop,restart,install,upgrade,rollback}`、`/cores/batch/*`、`/registry` |
| 配置管理 | `/cores/{id}/config*`（校验 / 保存 / 备份 / 回滚 / 导入 / 导出） |
| 实时日志 | `/cores/{id}/logs`、`/api/v1/ws`（`core.log`） |
| 版本管理 | `/cores/{id}/versions`、`/cores/{id}/version`、升级 / 回滚 |
| 系统设置 | `/ports`、`/registry/reload`、Token 配置 |

WebSocket 事件：`core.state` 刷新内核列表、`core.log` 追加日志、
`core.stats` 更新资源指标、`core.install` 显示安装进度、`system.alert` 弹出告警。

## 说明

- 配置保存遵循后端事务顺序：**备份 → 内核原生校验 → 原子替换**，失败保留原配置。
- 「单核独占启动」对应 `exclusive: true`（先停其他内核），「多核并行启动」为普通批量启动。
- Stage 2/3 的统一 TUN / 规则引擎 / DNS 引擎为预留能力，本阶段界面不展示。
