# v2.6.4 增量说明

> 基于 v2.6.3（登录门禁、链路编排、Windows 真 TUN、主机控制台、网络概览、用户管理）继续补齐用户提出的 7 项优化。
> 本版不改动 v2.6.3 已有的登录、TUN、hosts、pipeline 主体逻辑，只做增量。

## 1. 规则配置

| 能力 | 实现 |
| --- | --- |
| GitHub 来源自定义增删 | `POST /rulesets/github/presets` 新增、`DELETE /rulesets/github/presets/{repo}` 删除，内置预设不可删；`GET` 增加 `custom` 列表；持久化 `<data>/ruleset-github/presets.json`（0600 原子写） |
| 执行内核下拉 | 挂载执行内核、规则默认内核、规则集应用内核、单条规则内核 4 处由 `NInput` 改为 `NSelect`，选项来自 `GET /profiles/options` 的三内核 + 自动 |
| 规则批量删除 | `POST /network/rules/delete` 支持 `{ids}` / `{all:true}`；前端提供单条 / 删除选中 / 全部删除，均二次确认 |
| 官方语法可视化新增规则集 | `GET /rulesets/syntax` 返回 mihomo `rule-providers`、sing-box `route.rule_set`、Xray `routing.rules` 三套字段 schema 与官方示例；表单按字段动态渲染并生成官方语法文本，`POST /rulesets` 带 `kernel` 保存 |

## 2. 全站苹果极简

`web/src/styles/main.css` 一处统一关闭说明层（`.hint` / `.page-subtitle` / `.major-sub` / `.pc-help` / `.pc-flow` / `.pc-section-hint` 等），并用稳健的标签边界解析器移除 21 个说明型 `NAlert`（错误、告警与结果展示块保留）。新增 `.row-actions`（flex nowrap）统一操作列。表单标签、表头、按钮、数据值不受影响。

## 3. DNS 配置

`GET /profiles/dns-schema` 返回三内核官方 DNS schema；DNS 页新增三内核 Tab（字段表 / 官方文档链接 / 只读示例 / 可编辑 dns 段 + 校验），v2.6.3 已有的 NGG 自有 DNS 引擎配置、解析测试与 Hosts 策略全部保留。

## 4. 接管全局

- `GET /network/status` 增加 `default_core`、`direct_on_failure`、`bypass_tun` 与 8 项 `components`。
- `POST /network/components/toggle`：策略类 4 项即时更新运行时配置并持久化；组件类 4 项持久化后明确返回 `requires_restart:true`，不声称运行时已启停。
- `PUT /network/config` 校验 `default_core` 是否在注册表内。
- 状态卡片改用可读中文（「故障保护（断连不泄漏）」「内核故障自动切换」「能力集 Stage N」），每项带开关；默认内核改为三内核下拉。

## 5. 入口 / 出口节点

在 v2.6.3 的 `HostManager.vue`（入口/出口页实际渲染者）上新增四组能力：节点配置文件、采集数据、密钥生成与双方配对、节点级 TCP 连接测试。后端新增 `PUT /nodes/{id}/config-file`、`GET /nodes/{id}/metrics`、`POST /nodes/keys/generate`、`POST /nodes/keys/pair`、`POST /nodes/{id}/probe`，`Node` 增加 `Status` / `LastSeen` / `ConfigFile`。

采集口径如实：CPU / 内存只对绑定内核的**本机进程**真实采样，取不到时返回 `source:"unavailable"` 且数值为 0，不伪造远程主机指标；远程主机 CPU / 内存仍需该主机部署 Agent 上报。

## 6. 用户管理

v2.6.3 已提供 9 列用户表格、固定操作列与 `wrap:false`；本版沿用，并为操作列提供全局 `.row-actions` 规范。用户 CRUD 与用量重置端点沿用 v2.6.3，未重复实现。

## 7. 链路编排

`Pipeline.vue` 的「承载内核」下拉由单一 mihomo 改为 `GET /profiles/options` 的**全部三个内核**；「混合端口（SOCKS5 + HTTP）」仍只属于「客户端接入协议」下拉，不混入内核选项。非 mihomo 内核的预览/校验如实展示后端返回的 warn，不伪装成功。

## 8. 网络概览（追加）

在 v2.6.3 已有的 Hero 速率、流量趋势图、KPI、连接与策略卡片之上，新增「网络组件」卡（8 项状态 + 启动/停止引擎 + 跳转网络引擎页）与「配置概览」卡（节点 / 规则 / 规则集 / 用户计数 + 快捷跳转）。数据源用 `Promise.allSettled`，单接口失败不影响其它卡片。

## 9. 验证口径

`go test ./...`、`go build ./...`、`vue-tsc --noEmit && vite build`、`gofmt`、Markdown 相对链接检查；发布资产用 `SHA256SUMS` 校验。

## 10. 分支说明

本轮早期曾基于旧基线 v2.2.2 实现过同一批能力，保存在本地分支 `backup/v230-local`（未推送、未合入 main）。发现远端已推进到 v2.6.3 后，全部能力在最新基线上重新落地，避免回退他人工作。
