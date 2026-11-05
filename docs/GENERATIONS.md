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
| Release | **v2.0.0** |
| 仓库 | `welkacton/ngg-next-generation-gateway` |

---

## 2. 代际沿革

| 代际 | Release | 主题 | 标志性交付 |
| --- | --- | --- | --- |
| **Gen 1** | v1.0.0 及以前 | 服务端平台 | Stage 1：统一管理中心 / 多核并行 / Core Adapter / Registry；Stage 2~3：统一网络引擎、userspace 入口、真实 Linux TUN 读环回、10 类规则 + 4 类动作、DNS 引擎；Stage 4：协议中心、ML-KEM-768 混合后量子控制信道、JS/WASM 插件运行时；Stage 5：SD-WAN 探测选路、多租户、跨平台构建矩阵 |
| **Gen 2（当前）** | **v2.0.0** | 本地部署与统一控制台 | `scripts/deploy-vps.sh`（多发行版 apt/dnf/yum/zypper/pacman、Docker、公网 IP+端口+16 位随机后缀、域名 + Let's Encrypt SSL、备份/回滚/体检）；`installer/windows/`（Inno Setup GUI 安装包、便携 ZIP、一键 bat、真实 Windows 服务）；苹果/UniFi 风极简控制台；规则表与规则集栏的单条/多选/全部启停；界面内仓库更新栏（检查 / 下载并校验 / 应用） |

### 代际判定标准

进入下一代需要同时满足：

1. 有**用户可感知的形态变化**（例如从「只能在服务器跑源码」变为「双击安装包即可用」），而不是只加内部模块；
2. 前端、后端、安装包、文档、Release 版本号**同时对齐**；
3. 新增能力有真实运行证据（`go test ./...`、`npm run build`、ISCC 编译、真实 HTTP 端点测试），不把骨架写成已实现。

---

## 3. 代际在哪些地方可见

| 位置 | 显示内容 |
| --- | --- |
| GitHub 仓库首页 | README 顶部 `Generation 2 / 第二代 · Release v2.0.0` 徽章 + 代际表 |
| GitHub Releases | tag `v2.0.0`，Release 名称含代际 |
| Web UI 顶栏 | `Gen 2 · v2.0.0` 徽章 + 代际名称 |
| `GET /api/v1/update/status` | `generation` / `generation_label` / `generation_name` / `release` |
| `nggd -version` | `NGG 2.0.0`（来自 `internal/engine.NGGVersion = version.Release`） |
| `GET /api/v1/system` | `ngg_version: "2.0.0"` |

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
