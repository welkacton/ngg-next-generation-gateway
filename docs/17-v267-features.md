# v2.6.7 增量说明

## 1. 网络概览再次压缩

上一版仅下调了一档，实测仍偏大。本版按「企业路由器仪表盘」密度重做：

| 元素 | v2.6.6 | v2.6.7 |
| --- | --- | --- |
| 页面 padding / 圆角 | 16px / 18px | 12px / 12px |
| 页面最小高度 | `100vh-120px` | 移除（内容自适应） |
| 页面标题 | 22px | 18px |
| 总速率数字 | 30px | 22px |
| KPI 数字 | 20px | 16px |
| 卡片 padding / 圆角 | 14/16px / 14px | 10/12px / 10px |
| 卡片标题 | 14.5px | 13px |
| 行高（连接/引擎行） | padding 7px、12.5px | padding 5px、11.5px |
| 图表 viewBox | 800×260 | 1000×170 |
| 图表宽高比 | 800/260（宽屏下约 350px 高） | 1000/170（约 170px 高） |

图表改为更扁的 viewBox，并同步调整内边距与刻度字号（9.5px），宽屏下不再被拉成半屏高。

## 2. 网关中心 DNS（独立页面）

`/gateway/dns` 不再复用「配置引擎 → DNS 配置」，改为**企业网关/路由器自身的 DNS 服务**，绑定 `gateway.dns`：

| 分组 | 字段 |
| --- | --- |
| 解析模式 | 转发上游 / 本地递归；主上游 DNS、备用 DNS；DoH 端点（https://） |
| 缓存与本地 | 缓存开关、缓存条数、TTL 秒、本地域名后缀 |
| 静态解析与拦截 | hosts 键值对、拦截域名、DNSSEC、查询日志 |

与「配置引擎 → DNS 配置」的区别：后者是内核 DNS 语法与 NGG 统一 DNS 引擎；前者是网关设备对外提供解析服务时的转发/缓存/本地解析策略。

## 3. 网关中心页面密度重做

WAN / LAN / DNS / 防火墙统一为紧凑密度：页面 padding 12px、卡片 10–12px、圆角 10px、正文 11.5–12px、卡片标题 13px、表格 `size="small"` + 6px 单元格、操作按钮 `tiny`。移除了所有说明性文案，只保留 label、表头、按钮与数据值。

## 4. 多 WAN（6 口）

`GatewayConfig` 增加 `wans: GatewayWAN[]`（固定 6 口）与 `dns`，`wan` 保留为 `wans[0]` 的兼容字段。

每个 WAN 口：

| 字段 | 说明 |
| --- | --- |
| `id` / `name` | `wan1..wan6` / WAN 1..WAN 6 |
| `enabled` | 是否启用 |
| `mode` | `dhcp` / `static` / `pppoe` |
| `interface` / `address` / `gateway` / `mtu` | 接口与地址参数 |
| `dns` / `ipv6` / `username` / `password_set` | 该口独立 DNS、IPv6、PPPoE 账号 |
| `route` | `local` 本地直出；`proxy` 交由配置引擎走国际代理 |
| `weight` / `priority` | 负载权重 1–100、优先级 1–10（越小越优先） |

- 默认 `wan1` 启用（DHCP、本地直出），其余 5 口默认停用。
- 旧配置（只有单 `wan`、没有 `wans` / `dns`）在加载时自动迁移：`wan` 填入 `wans[0]`，其余取默认，DNS 取默认。
- `GET /gateway/status` 增加 `wan_links`：对每个启用的 WAN 输出接口、路由、地址与在线状态（接口存在且有地址才算在线）。

**边界（如实说明）**：本版是声明式配置与真实状态展示。`route=proxy` 记录了「该 WAN 应走配置引擎国际代理」的意图，但 NGG 尚未按 WAN 做真实的策略路由与多出口负载均衡；把它真正落到数据面需要系统路由表与内核出站绑定能力。

## 5. 验证

`go test ./...`、`go vet ./...`、`go build ./...`、`vue-tsc --noEmit && vite build`、`gofmt`、Markdown 相对链接检查；发布由 CI 构建多平台资产并用 `SHA256SUMS` 校验。
