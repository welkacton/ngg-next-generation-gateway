# NGG 版本代际（Generation）

> 「代际」是产品谱系标记，与语义化版本号（Release）相互独立。
> 单一事实来源：`internal/version/version.go`。
> GitHub 首页 README、界面顶栏徽章与 `GET /api/v1/update/status` 都读同一份常量，避免各处写死不一致。

---

## 1. 当前代际

| 字段 | 值 |
| --- | --- |
| Generation | **2** |
| Label | **Gen 2** |
| 名称 | **第二代 · 本地部署与统一控制台** |
| Release | **v2.6.6** |
| 仓库 | `welkacton/ngg-next-generation-gateway` |

---

## 2. 代际沿革

| 代际 | Release | 主题 | 标志性交付 |
| --- | --- | --- | --- |
| **Gen 1** | v1.0.0 及以前 | 服务端平台 | Stage 1：统一管理中心 / 多核并行 / Core Adapter / Registry；Stage 2~3：统一网络引擎、userspace 入口、真实 Linux TUN 读环回、10 类规则 + 4 类动作、DNS 引擎；Stage 4：协议中心、ML-KEM-768 混合后量子控制信道、JS/WASM 插件运行时；Stage 5：SD-WAN 探测选路、多租户、跨平台构建矩阵 |
| Gen 2 首发 | v2.0.0 | 本地部署与统一控制台 | `scripts/deploy-vps.sh`（多发行版 apt/dnf/yum/zypper/pacman、Docker、公网 IP+端口+16 位随机后缀、域名 + Let's Encrypt SSL、备份/回滚/体检）；`installer/windows/`（Inno Setup GUI 安装包、便携 ZIP、一键 bat、真实 Windows 服务）；苹果/UniFi 风极简控制台；规则表与规则集栏的单条/多选/全部启停；界面内仓库更新栏（检查 / 下载并校验 / 应用） |
| Gen 2 增补 | v2.1.0 | 协议中心重构与全站字体 | 三大类全内核协议目录（加密 / 未加密 / 普通代理，30+ 条且逐条生成可解析配置）；生成结果可自定义编辑、校验并保存为覆盖模板；自定义协议原始 JSON 编辑与增删改；协议单条 / 多选 / 全部启停（停用后真实拒绝生成）；全站西文字体统一为 SF Mono Nerd Font Ligaturized |
| Gen 2 增补 | v2.1.1 | Web 端一键更新与交互优化 | 私有仓库 PAT 可在更新面板直接填写（请求级 context，不落盘不写日志）+「立即更新」一键全链路 + 新版本自动刷新；协议目录五类视图；精简表格列修复文字叠加；点「配置」自动滚动定位；移除冗余 params 输入框与单条详情卡片；大栏目标题加粗放大 |
| Gen 2 增补 | v2.1.2 | 按内核官方语法配置 | 三内核协议目录；`internal/protocols/schema.go` 三套字段定义与官方文档链接；两卡片合并为「可视化表单 + 带注释代码编辑」 |
| Gen 2 增补 | v2.2.0 | 协议配置与 GitHub 规则集 | 「协议中心」改名**协议配置**：上半只读纯文字协议目录（`internal/protocols/catalog.go` 每 6 小时从三内核官方文档仓库同步）；下半 Profile 卡片列表（`internal/profiles` 6 分区、按内核官方语法、带中文注释与语法校验）；规则页 `internal/rulesets/github.go` 一次 git-trees 发现 668+ 分类，批量 mount/import |
| Gen 2 增补 | v2.2.1 | 内核安装修复与交互整理 | 内核安装改为**异步后台任务**（`internal/api/installs.go`：POST 立即 202、`/cores/install/status` 轮询、进度走 `core.install` WebSocket），修掉前端 30 秒超时导致三个内核装不上；`GET /protocols/catalog` 不再触发 GitHub 请求（避免页面轮询耗尽匿名 60/h 配额），安装器支持 `NGG_GITHUB_TOKEN`/`GITHUB_TOKEN` 与限流提示；配置引擎二级重排为 协议配置/规则配置/DNS配置/入口节点/出口节点；配置文件分区改为 协议→入站→出站→路由规则→DNS→其他→重建模板、页面留白、卡片支持单条与批量启停（`POST /profiles/toggle`）；GitHub 规则集可「全选全部」并自动按 50 一批加载；侧栏改 flex 三段式，修复展开后底部文字覆盖 |
| Gen 2 增补 | v2.2.2 | 完整配置双向编辑 | `internal/profiles/split.go` 实现 `Render` 的逆运算：YAML 按顶层键保留原文与注释，JSON 用 `json.Decoder.Token` 保序拆分并把 `outbounds` 按节点/策略重新分组；`POST /profiles/split` 纯计算不落盘，语法错误返回 422；前端 Protocols.vue 把只读 `<pre>` 换成可编辑 textarea + 650ms 防抖自动同步、`silentError` 静默请求、分区行数高亮与保存并关闭 |
| Gen 2 增补 | v2.6.4 | 规则集 / 接管全局 / DNS / 节点 / 链路编排 / 极简风 | v2.3.0~v2.6.3 并行演进已交付登录门禁、链路编排、Windows 真 TUN、主机控制台、网络概览与用户管理；本版补齐 `POST/DELETE /rulesets/github/presets`、`POST /network/rules/delete`、`GET /rulesets/syntax`、`GET /profiles/dns-schema`、`POST /network/components/toggle`、节点密钥/探测/采集端点，并把全站说明层收敛为 `main.css` 一处开关 |
| **Gen 2（当前）** | **v2.6.6** | 网关中心 + 更新体验 + 概览紧凑化 | `internal/gateway` 提供 WAN / LAN / Firewall 声明式配置（0600 原子写、PPPoE 密码只存不回显）与 `GET /gateway/status` 真实网卡状态（net.Interfaces + UDP 出站探测默认上行，Linux 解析 /proc/net/route）；`web/src/api/client.ts` 增加 `setMaintenance`，更新期间 `!response` 的传输错误只提示一次并派发 `ngg-backend-unreachable`；路由新增 /gateway/*，SD-WAN 从后端管理迁入网关中心；v2.6.6 修复网卡枚举为空时 WAN / LAN 接口无法选择（下拉支持搜索与手动输入） |

### 代际判定标准

进入下一代需要同时满足：

1. 有**用户可感知的形态变化**（例如从「只能在服务器跑源码」变为「双击安装包即可用」），而不是只加内部模块；
2. 前端、后端、安装包、文档、Release 版本号**同时对齐**；
3. 新增能力有真实运行证据（`go test ./...`、`npm run build`、ISCC 编译、真实 HTTP 端点测试），不把骨架写成已实现。

---

## 3. 代际在哪些地方可见

| 位置 | 显示内容 |
| --- | --- |
| GitHub 仓库首页 | README 顶部 `Generation 2 / 第二代 · Release v2.6.6` 徽章 + 代际表 |
| GitHub Releases | tag `v2.6.6`，Release 名称含代际 |
| Web UI 顶栏 | `Gen 2 · v2.6.6` 徽章 + 代际名称 |
| `GET /api/v1/update/status` | `generation` / `generation_label` / `generation_name` / `release` |
| `nggd -version` | `NGG 2.6.6`（来自 `internal/engine.NGGVersion = version.Release`） |
| `GET /api/v1/system` | `ngg_version: "2.6.6"` |

---

## 4. 如何升级代际

1. 修改 `internal/version/version.go` 的 `Generation` / `GenerationLabel` / `GenerationName` / `Release`；
2. 同步本文件、README 代际表与 Release 名称；
3. 打 `vX.Y.Z` tag，`release.yml` 会构建全平台产物 + Windows 安装包；
4. 客户端「系统更新」栏通过 `GET /api/v1/update/status` 与 GitHub Releases 自动识别新版本。

---

## 5. 相关文档

| 文档 | 内容 |
| --- | --- |
| [`10-updates.md`](10-updates.md) | 界面内仓库更新栏、更新 API 与自动更新脚本 |
| [`DEPLOY-VPS.md`](DEPLOY-VPS.md) | VPS 部署 |
| [`INSTALL-WINDOWS.md`](INSTALL-WINDOWS.md) | Windows 安装包 |
| [`TUN-GLOBAL-TAKEOVER.md`](TUN-GLOBAL-TAKEOVER.md) | 尚未完成的全局 TUN 接管计划（不属于 Gen 2 交付） |
