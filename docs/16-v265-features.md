# v2.6.5 增量说明

## 1. 更新期间的错误提示

v2.6.4 及以前，更新过程会替换 `nggd.exe` 并重启服务，期间页面上的 `/network/status`、`/network/sessions`、`/network/endpoints` 等轮询请求全部返回 `Network Error`，被全局拦截器逐条弹成红色错误。

本版修复：

- `web/src/api/client.ts` 新增 `setMaintenance(active)`；更新流程在「应用更新」成功后进入维护模式，直到页面刷新或超时失败。
- 传输层错误（`!error.response`，即服务不可达）不再逐条弹错，改为 8 秒节流一次「后端服务暂时不可达，正在自动重连…」，并派发 `ngg-backend-unreachable` 事件供页面暂停轮询。
- 一键更新、手动应用、自动应用三条路径都接入维护模式；自动应用在 30 秒后自动刷新页面。
- HTTP 4xx/5xx 的真实业务错误仍按原有方式提示，未被静默。

## 2. 新增规则集自动定位

「规则集栏」标题旁的「新增规则集」按钮改为 `addNewRuleSet()`：先重置表单，再 `scrollIntoView` 到下方规则集表单卡片（新增 `id="ruleset-form-card"`）。取消编辑按钮行为不变。

## 3. 网络概览紧凑化

`NetworkOverview.vue` 的尺寸整体下调而不删功能：

| 元素 | 原值 | 现值 |
| --- | --- | --- |
| 页面 padding / 圆角 | 26px / 26px | 16px / 18px |
| 总速率数字 | 46px | 30px |
| KPI 数字 | 29px | 20px |
| 卡片 padding / 圆角 | 20/22px / 20px | 14/16px / 14px |
| 卡片间距 | 16px | 10–12px |
| 迷你曲线高度 | 40px | 26px |
| 趋势图最小高度 | 200px | 150px |

## 4. 网关中心（新增一级栏目）

仿 UniFi OS 的信息架构，一级「网关中心」下五个二级页面：

| 页面 | 路由 | 内容 |
| --- | --- | --- |
| WAN 设置 | `/gateway/wan` | 真实上行接口 / 地址 / 网关 / MTU；DHCP / 静态 IP / PPPoE；DNS、IPv6 |
| LAN 设置 | `/gateway/lan` | LAN 地址与掩码、DHCP 开关与地址池、租约、本地域名 |
| DNS 设置 | `/gateway/dns` | 复用现有 DNS 配置页（自有引擎 + 三内核官方 DNS 语法） |
| SD-WAN | `/gateway/sdwan` | 复用现有 SD-WAN 页，从「后端管理」迁入；旧 `/sdwan` 自动重定向 |
| 防火墙设置 | `/gateway/firewall` | 默认入站策略、规则表、端口转发、UPnP、连接跟踪 |

后端：

- `internal/gateway` 提供 `Config` / `Store` / `Status`，持久化 `<data>/gateway-config.json`（0600 原子写 + 失败回滚）。
- `GET /gateway/status` 用标准库真实读取网卡（`net.Interfaces`）、用 UDP 出站拨号确定默认上行接口与地址；Linux 下解析 `/proc/net/route` 得到默认网关，其它平台留空。
- PPPoE 密码只落盘，`GET /gateway/config` 仅返回 `password_set`，不回显明文；保存时密码留空则保留原值。
- 校验：模式/协议枚举、MTU 576–9000、IPv4 地址与掩码、DHCP 池同网段且 start < end、租约 1–10080、端口 1–65535、ID 唯一。

**边界（如实说明）**：网关中心是「声明式配置 + 真实状态展示」，不会直接改写系统网卡、路由或防火墙表。把配置真正下发到操作系统需要对应的系统权限与平台实现，当前版本不伪造「已生效」状态。

## 5. 验证

`go test ./...`、`go vet ./...`、`go build ./...`、`vue-tsc --noEmit && vite build`、`gofmt`、Markdown 相对链接检查；发布由 CI 构建多平台资产并用 `SHA256SUMS` 校验。
