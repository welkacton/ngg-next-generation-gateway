# NGG — Next-Generation Gateway（次世代网关）

> **Single Unified TUN + Unified Network Engine + Multi-Core Protocol Runtime**

<div align="center">

### 🏷️ Generation 2 / 第二代 · Release v2.0.0

**第二代 · 本地部署与统一控制台** — VPS / Windows 安装包、极简控制台、规则与规则集批量启停、仓库内更新栏

</div>

NGG 是**唯一的系统网络入口、统一流量控制中心、统一出口调度中心**。
Mihomo、Xray-core、sing-box 以及未来新增的内核都是**可插拔的协议执行引擎**，不是流量接管者。

| 代际 | 版本 | 主题 | 标志性能力 |
| --- | --- | --- | --- |
| **Gen 1** | v1.0.0 及以前 | 服务端平台 | Stage 1~5：统一管理中心、网络引擎、规则/DNS、协议中心、后量子、插件、SD-WAN、多租户 |
| **Gen 2（当前）** | **v2.0.0** | 本地部署与统一控制台 | 多发行版 VPS 一键部署、公网/域名/SSL、Windows 原生服务安装包、极简 UI、规则与规则集批量启停、界面内仓库更新栏 |

代际沿革与判定标准见 [`docs/GENERATIONS.md`](docs/GENERATIONS.md)；
当前版本由 `internal/version` 单一来源维护，`GET /api/v1/update/status` 与界面顶栏显示同一个 `Gen 2 · v2.0.0`。

本仓库已交付 **Stage 1 → Stage 5** 的完整服务端平台：统一管理中心、统一网络引擎、
规则/DNS、VPN 协议中心、后量子控制信道、插件运行时、SD-WAN、多租户与跨平台骨架。

---

## 一键部署（VPS / 电脑 / 软路由）

私有仓库（**仅自己可见**）：**https://github.com/welkacton/ngg-next-generation-gateway** · MIT 许可（见 `LICENSE`）。
仓库当前为 `private`，只有仓库所有者授权账号可以 clone；部署到自己的服务器、电脑、NAS 或软路由时，
请先用有权限的账号完成克隆或下载 Release 产物。若日后要共享，由所有者手动改为 Public。

> **Windows 安装包直接下载**：<https://github.com/welkacton/ngg-next-generation-gateway/releases/tag/v2.0.0>
> → `NGG-Setup-v2.0.0.exe`（双击，UAC 确认即可，无需命令行）或 `NGG-v2.0.0-windows-amd64.zip`
> （解压后双击 `install.bat`）。附带 `SHA256SUMS`，运行前请校验。

| 场景 | 最短路径 | 完整步骤 |
| --- | --- | --- |
| VPS / 云服务器（Docker） | `cp .env.example .env && docker compose up -d --build` | [`docs/DEPLOY-VPS.md`](docs/DEPLOY-VPS.md) 第 3 节 |
| **VPS / 云服务器（systemd 原生，推荐）** | `sudo --preserve-env=GITHUB_TOKEN ./scripts/deploy-vps.sh install --version v2.0.0 --public` | **[`docs/DEPLOY-VPS.md`](docs/DEPLOY-VPS.md)** |
| VPS 域名 + SSL | 同上再加 `--domain ngg.example.com --with-tls --email you@example.com` | `docs/DEPLOY-VPS.md` §5.3 |
| VPS 用 Docker（含 Alpine） | `./scripts/deploy-vps.sh docker` | `docs/DEPLOY-VPS.md` §3 |
| **Windows 安装程序** | 双击 `NGG-Setup-v2.0.0.exe`，或 `powershell -File install.ps1 -Source .` | **[`docs/INSTALL-WINDOWS.md`](docs/INSTALL-WINDOWS.md)** |
| Linux VPS（从源码构建） | `sudo bash scripts/install.sh` → `systemctl enable --now ngg` | `docs/04-installation.md` |
| 本地电脑 / macOS / Linux | `bash scripts/quickstart.sh`（或 `--release v2.0.0` 下载预编译包） | `docs/DEPLOY.md` 第 4 节 |
| 软路由 / OpenWrt | `platforms/openwrt/` 的 procd init + UCI 配置 | `platforms/openwrt/README.md` |

`scripts/deploy-vps.sh` 直接安装 Release 预编译包（VPS 上**不需要 Go / Node**），自动完成：SHA256 校验 →
发行版探测（apt/dnf/yum/zypper/pacman）与依赖安装 → 系统用户与目录 → 绝对路径配置 + 随机 Token →
systemd unit → 可选防火墙 / 公网入口（`http://公网IP:端口/16位随机后缀`） / 域名 + Let's Encrypt SSL →
健康检查 / 备份 / 升级 / 卸载。`docker` 子命令覆盖无 systemd 的环境。
Windows 端提供 Inno Setup GUI 安装包、便携 ZIP、PowerShell 安装/卸载脚本，以及 `nggd.exe` 内置的
**Windows 服务**（开机自启、失败重启、优雅停止、`logs/ngg-service.log`）。

全场景部署、端口、备份、安全清单见 **[`docs/DEPLOY.md`](docs/DEPLOY.md)**；
VPS 专属手册见 **[`docs/DEPLOY-VPS.md`](docs/DEPLOY-VPS.md)**，Windows 专属手册见 **[`docs/INSTALL-WINDOWS.md`](docs/INSTALL-WINDOWS.md)**。

> ⚠️ 仓库请勿提交真实凭据（即使当前为私有仓库，也应始终保持脱敏）：`configs/ngg.yaml`（含 API Token）与 `data/`（含节点密码、私钥、
> 后量子密钥）均已 gitignore。分享原生配置前请先脱敏；`configs/examples/native-xray.sample.json` 全是占位符。
## 架构要点

- **统一 TUN 由 NGG 创建**（Linux `/dev/net/tun` + `CAP_NET_ADMIN`）；三个内核一律 Worker Mode，
  禁止自建系统 TUN / DNS 劫持 / 全局路由。
- **NGG 独立拥有** TUN Manager、Packet Processor、Session Manager、Rule Engine、DNS Engine、
  Routing Engine、Protocol Dispatcher、Core Adapter Bridge、Egress Manager、Network Resource Manager。
- **多核并行**：任意多个已注册内核可同时启动/运行；每条连接由 NGG 独立决定执行路径；
  一个内核故障不影响其它内核。Core Plugin Registry 支持描述文件新增第 N 个内核。
- **Fail-Closed**：内核不可用时默认丢弃连接，绝不意外 DIRECT 泄漏。
- 架构修订已明确**覆盖**初始化指令里的「默认单主内核模式」；「单核模式」只是用户主动的批量操作。

---

## 阶段交付

| 阶段 | 内容 | 状态 |
| --- | --- | --- |
| Stage 1 | 统一管理中心 + 内核引擎（Mihomo / Xray-core / sing-box）、安装/升级/回滚、配置事务、日志监控、SQLite 状态恢复、REST+WS、Web UI | ✅ 完成 |
| Stage 2 | 统一网络引擎：Linux 真 TUN、全平台 SOCKS5/SOCKS4a/HTTP CONNECT 入口、IPv4/IPv6 Packet Processor、Session Manager、Protocol Dispatcher、Core Adapter Bridge、多核代理链、Egress Manager、资源隔离与防回环 | ✅ 实现（TUN 有 Linux 能力边界） |
| Stage 3 | 10 类规则 + 4 类动作、规则表新增/修改/删除、自定义规则集（内联 / URL 订阅 / 上传文档自动识别并附加到规则集栏、一键合并进实时规则表）、本地 DNS 引擎（缓存/hosts/block/DoH）、TUN 内 DNS 应答包、入口与出口策略 | ✅ 实现 |
| Stage 4 | 16 项 VPN/加密代理协议能力中心 + 真实内核原生配置生成、**ML-KEM-768 + X25519 混合后量子握手**（FIPS 203）与 AES-256-GCM 控制信道、JS(goja) + WASM(wazero) 插件运行时 | ✅ 实现 |
| Stage 5 | SD-WAN 真实链路探测/选路/故障切换、企业多租户隔离与配额、跨平台构建矩阵 + gomobile 共享 Go 核心 + 各平台接入骨架 | ✅ 服务端实现 / 客户端为骨架 |

### 如实标注的能力边界

- **Linux TUN**：真机 `/dev/net/tun` + `TUNSETIFF` + iproute2 + fwmark 绕过已实现；Windows/macOS
  明确返回 `ErrTUNUnsupported`，改用全平台可用的 userspace 入口（已完整验证）。
- **TUN 上的 TCP 流终止**需要 userspace TCP 协议栈，属于**尚未实现的已知缺口**；当前 TUN 读环回
  完成真实 L3 解析、DNS 应答包构造与 Fail-Closed 记录，不伪装成功。TCP 数据面由 SOCKS5/HTTP CONNECT
  入口承载并通过真实 socket 测试。
- **iOS / iPadOS / Android / HarmonyOS / Android TV 原生 UI App 未实现**：`platforms/` 提供可编译的
  共享 Go 核心、gomobile 绑定脚本与平台接入骨架，需各平台 SDK 与原生 UI 开发。
- **后量子**目前用于 NGG 节点间控制信道，内核数据面协议栈的 PQ 集成属于后续工作。
- 传统 VPN（OpenVPN / IPsec / L2TP / PPTP）由外部 helper 执行，协议中心负责能力注册与配置管理，
  NGG 不重写内核协议栈。

---

## 快速开始

```bash
# 后端（Go 1.26+）
[ -f configs/ngg.yaml ] || cp configs/ngg.example.yaml configs/ngg.yaml   # 首次（含 token 的配置不入库）
make backend            # 生成 bin/nggd
./bin/nggd -config configs/ngg.yaml

# 首次启动会打印自动生成的 API Token 并写回 configs/ngg.yaml
# 用 http://127.0.0.1:9090/?token=<TOKEN> 打开管理界面完成一次性绑定

# 前端
make frontend           # npm install && npm run build → web/dist

# 开发模式
make dev                # Vite :5173（后端另开终端 make run）
```

服务启动会：恢复上次运行的内核 → 自动拉起统一网络引擎 → 监听 `127.0.0.1:9090`。
管理界面一级分类「内核引擎」「配置引擎」可点击折叠/展开；规则表支持增删改，入口/出口节点可配置加密协议并支持表单与 JSON 双模式编辑。
从界面修改的网络配置持久化到 `data/network-config.json`，不会改写含 Token 与注释的主配置文件。
以 daemon 运行且 PATH 不含 `go` 时，请在 `configs/ngg.yaml` 设置 `build.go_binary`（或 `NGG_GO_BINARY`），
否则 `POST /api/v1/platforms/build` 会如实报「找不到工具链」而不是伪装成功。
在 Web UI「Cores」页安装三个内核后，「Stage 2 网络引擎」页的端到端自检即可验证
`NGG 入口 → Session → Rule → Dispatcher → Core Bridge → 真实内核 → 目标` 全链路。

---

## 真实验收结果（Windows amd64 本机，非模拟）

| 项目 | 结果 |
| --- | --- |
| 三内核安装 | mihomo **1.19.33** / xray-core **26.10.31** / sing-box **1.14.4**，SHA256 与 GitHub `digest` 全部匹配 |
| 多核并行 | 三内核同时运行，真实 PID + gopsutil CPU/内存/线程；多核 ↔ 单核独占自由切换 |
| Stage 2 数据面 | 四条路径端到端自检全绿：`mihomo` / `xray-core` / `sing-box` / `DIRECT`，78 字节精确回显 |
| 规则 / DNS | 规则集加载 6 条、增删、host 解析 `ngg.local → 10.89.0.1`、IPv4/UDP/53 报文分类 |
| 后量子 | ML-KEM-768 + X25519 真实握手自测通过，封装密钥 1184B / 密文 1088B / AES-256-GCM 往返 |
| 插件 | goja JS 插件改写分流决策成功；wazero 加载真实 55 字节 WASM 模块并调用 `ngg_hook` |
| SD-WAN | 真实 TCP 探测 + 故障切换测试通过 |
| 质量门禁 | `gofmt -l` 空 / `go build ./...` / `go vet ./...` / `go test ./...` / `npm run build` 全绿 |

---

## 目录

```
cmd/nggd/                  服务入口
Dockerfile / docker-compose.yml / .env.example   容器化部署
.github/workflows/         CI + 多平台 Release 构建
pkg/contracts/             共享 DTO 与能力描述契约
internal/core/             Core Adapter 接口、Core Registry、模块端口
internal/adapters/         mihomo / xray / singbox / generic / builtin
internal/engine/           Core Manager + 10 个模块 + Stage 4/5 编排
internal/netengine/        Stage 2/3 统一网络引擎（TUN/入口/会话/规则/DNS/调度/桥接/出口）
internal/protocols/        Stage 4 VPN / 加密代理协议中心
internal/pq/               Stage 4 ML-KEM-768 混合后量子控制信道
internal/plugins/          Stage 4 JS(goja) + WASM(wazero) 运行时
internal/sdwan/            Stage 5 链路探测与选路
internal/tenants/          Stage 5 多租户
internal/mobile/           gomobile 共享 Go 核心
internal/store/            SQLite 状态持久化（纯 Go 免 CGO）
internal/api/              REST + WebSocket（本机限制 + Bearer 认证）
configs/                   主配置、能力描述目录、规则/DNS/SD-WAN/插件示例数据
platforms/                 跨平台构建脚本与 Android/iOS/HarmonyOS/TV/OpenWrt 骨架
web/                       Vue 3 + TS + Naive UI（极简控制台；一级分类「内核引擎 / 配置引擎 / 后端管理」）
docs/                      架构 / API / 开发 / 安装 / 排障 / 路线图 / Stage 2~5 / 部署 / 代际 / 更新
scripts/                   构建、开发、VPS 一键部署、端到端验收、平台内更新脚本
internal/version/          代际与 Release 的单一事实来源
internal/update/           GitHub Release 检查、SHA256 校验下载、更新交接
deploy/                    systemd unit 与 Nginx 反向代理模板（手工部署参考）
installer/windows/          Inno Setup 安装包、PowerShell 安装/卸载、构建脚本
```

## 文档

| 文档 | 内容 |
| --- | --- |
| `docs/01-architecture.md` | 最高级架构约束、分层图、ADR、状态机、Phase 1 边界 |
| `docs/02-api.md` | Phase 1 的 32 个端点 + Stage 2~5 端点总表 |
| `docs/03-development.md` | 开发流程、接口先行、如何新增内核 |
| `docs/04-installation.md` | Linux/systemd 部署与离线安装 |
| `docs/05-troubleshooting.md` | 故障排查 |
| `docs/06-roadmap.md` | 五阶段路线图与验收标准 |
| `docs/07-stage2-network-engine.md` | Stage 2 数据平面设计与能力边界 |
| `docs/08-stage3-5-features.md` | Stage 3~5 功能与实现状态 |
| `docs/09-stage2-5-api.md` | Stage 2~5 全部 API 细节 + 节点/原生配置编辑附录 |
| `docs/DEPLOY.md` | 私有仓库部署、Docker/systemd/软路由、安全清单、已知边界 |
| `docs/DEPLOY-VPS.md` | **VPS 完整手册**：系统准备、Release 一键安装、systemd、SSH 隧道 / Nginx HTTPS、防火墙、备份回滚、排障清单 |
| `docs/INSTALL-WINDOWS.md` | **Windows 完整手册**：GUI 安装包、便携 ZIP、Windows 服务、Token/日志、系统代理、升级卸载、排障清单 |
| `docs/TUN-GLOBAL-TAKEOVER.md` | **TUN 一键全局接管**：代码级现状、为何现在做不到、P0~P4 实施计划与真实验收标准 |
| `docs/GENERATIONS.md` | **版本代际**：Gen 1 / Gen 2 沿革、判定标准、代际在 GitHub 与界面上的显示位置 |
| `docs/10-updates.md` | **系统更新栏**：`/update/*` 端点、自动/手动更新、SHA256 校验、平台更新脚本 |
| `deploy/README.md` | systemd / Nginx 手工部署模板说明 |
| `installer/windows/` | 安装包源码（`.iss` / `.ps1`） |

## 验证命令

```bash
make vet
make test
NGG_TEST_MIHOMO_BINARY=/abs/path/to/mihomo \
NGG_TEST_ARCHIVE=/abs/path/to/mihomo.zip \
  go test ./... -count=1       # 额外启用真实二进制集成测试

node scripts/e2e-verify.js    # Phase 1 端到端
node scripts/e2e-stage5.js    # Stage 3~5 现场 API 验收（需服务运行）
node scripts/e2e-native.js    # 整份原生配置解析 / 校验 / 应用（需服务运行）
```

## 开源与许可

MIT License（见 `LICENSE`）。贡献见 `CONTRIBUTING.md`，安全披露见 `SECURITY.md`。

## 安全边界

- 控制 API 默认只监听 `127.0.0.1` 并二次校验对端；所有 `/api/` 需 Bearer Token。
- UI 不接触 Shell；所有内核操作经 REST → Core Manager → Core Adapter → 原生进程。
- 内核独立目录、独立 5 端口互斥块、Worker Mode。
- 下载仅接受 GitHub 官方域名 HTTPS + SHA256 digest + 真实 `-v` 版本比对；缺失即拒绝安装。
- 配置修改强制「先备份 → 原生校验 → 通过才替换」；校验失败保留原配置。
- 非回环入口强制要求认证；Fail-Closed 默认开启；NGG 保留端口与 TUN 网段防回环。
