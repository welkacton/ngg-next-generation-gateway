# NGG 跨平台客户端骨架（platforms/）

> **能力边界（务必先读）**：本目录是 **骨架 / scaffold**，不是已发布的客户端 App。
> 它只提供三样东西：**可编译的共享 Go 核心接入方式**、**各平台接入骨架代码/配置**、
> **构建脚本与接入文档**。iOS / iPadOS / Android / HarmonyOS / Android TV 的
> **原生 UI App 均未实现**，桌面托盘 App 也未实现。要得到成品 App，仍需在对应平台
> 安装官方 SDK（Android SDK/NDK、Xcode、DevEco Studio 等）并完成原生 UI 与系统
> VPN/代理能力的开发。请勿把本目录描述为「已发布客户端」或「开箱即用的 VPN App」。

## 1. 这里已经有什么（真实能力）

NGG 后端是 Go，已实现 Stage 1~5 的服务端能力。控制面为：

- REST：`http://127.0.0.1:9090/api/v1`
- WebSocket：`/api/v1/ws?token=<TOKEN>`
- 认证：`Authorization: Bearer <token>`（WS 可用查询参数传 token）
- 默认**仅监听本机回环地址**（`server.listen: 127.0.0.1`），并在中间件二次校验对端为回环地址

共享 Go 核心位于仓库 `internal/mobile/mobile.go`（`package mobile`），是给
**gomobile bind** 用的**真实 REST 客户端，不是模拟器**：它直接向上述控制面发 HTTP 请求。
导出类型与方法：

| 方法 | 说明 |
| --- | --- |
| `New(baseURL, token string) *Gateway` | 创建网关客户端 |
| `Connectable() bool` | 真实发起 `GET /api/v1/health` 判断可达性 |
| `CoreVersion() string` | 核心版本串 |
| `SystemInfo() / Cores() / NetworkStatus() / Stages() / SDWANStatus() / Protocols()` | 查询类，返回 `(string, error)` |
| `StartCore(id) / StopCore(id) / StartNetwork() / StopNetwork()` | 操作类，返回 `(string, error)` |
| `BatchStart(idsJSON string, exclusive bool)` | 批量启动内核 |
| `SelfTest(coreID string)` | 网络自检 |
| `SetRules(rulesJSON string)` | 下发分流规则 |
| `ProxyURL() (string, error)` | 从 `NetworkStatus()` 解析入口地址 |
| `SetToken(token string)` | 更新 Token |

数据面默认入口：

- 入口（SOCKS5 / HTTP CONNECT）：`127.0.0.1:18080`
- DNS 监听：`127.0.0.1:18053`
- Linux 统一 TUN 需要 `/dev/net/tun` 且具备 `CAP_NET_ADMIN`；Windows / macOS 走
  userspace SOCKS5/HTTP CONNECT 入口 + 系统代理设置。

## 2. 平台矩阵

状态图例：**可构建** = 纯 Go 可直接交叉编译出可运行守护进程；**需外部 SDK** =
必须借助平台官方 SDK（gomobile + NDK / Xcode）才能产出二进制库；**骨架** =
仅有接入示例代码/配置与文档，原生 UI 需自行开发；**未实现** = 该能力当前没有实现。

| 平台 | 架构 | 共享核心 | 入口方式 | 状态 |
| --- | --- | --- | --- | --- |
| Linux（服务器 / 桌面 / 软路由） | amd64 / arm64 / 386 / arm / mipsle | 同源复用 `cmd/nggd` + `internal/mobile` | 统一 TUN（`/dev/net/tun` + `CAP_NET_ADMIN`），或 userspace SOCKS5 | **可构建**（`build.sh`） |
| Windows | amd64 / arm64 | 同源复用 | userspace SOCKS5/HTTP CONNECT `127.0.0.1:18080` + 系统代理 | **可构建**（`build.sh`） |
| macOS | amd64 / arm64 | 同源复用 | userspace SOCKS5/HTTP CONNECT + `networksetup` 系统代理 | **可构建**（`build.sh`） |
| FreeBSD | amd64 | 同源复用 | userspace 入口 | **可构建**（`build.sh`） |
| OpenWrt / 软路由 | arm64 / arm / mipsle 等 | 同源复用 | TUN + procd init | **可构建**（需自行交叉编译并打包 ipk，见 `openwrt/`） |
| Android 手机 / 平板 | arm64 | gomobile bind -> `ngg.aar` | `VpnService`（**未实现**） | **需外部 SDK**（Android SDK/NDK）+ **骨架** |
| iOS / iPadOS | arm64 | gomobile bind -> `NGG.xcframework` | `NEPacketTunnelProvider`（**未实现**） | **需外部 SDK**（macOS + Xcode）+ **骨架** |
| HarmonyOS | 视设备而定 | gomobile 不直接支持；可用 NAPI 调共享 Go 核心或经本机 REST 控制面 | VPN Kit（**未实现**） | **骨架** |
| Android TV / 电视 | arm64 等 | 可复用 Android AAR，或经局域网 NGG 网关代理 | 系统代理 / leanback 遥控器 UI（**未实现**） | **骨架 / 未实现** |
| 桌面托盘 App（Win/mac/Linux） | 视宿主 | 复用本机 `nggd` 服务 + Web UI | 服务模式 + 浏览器 Web UI；托盘 UI（**未实现**） | **骨架 / 未实现** |

补充说明：

- `android/arm64`、`ios/arm64` 属于**需外部 SDK 的目标**：`build.sh` 不构建它们，
  由 `build-mobile.sh` 通过 gomobile 生成 AAR / XCFramework，且缺工具链时会明确跳过。
- 所有「原生 UI App」条目（Android/iOS/HarmonyOS/TV/托盘）都是**骨架或尚未实现**，
  本目录不含任何可安装、可上架的成品应用。

## 3. 目录说明

```
platforms/
├── README.md                      本文件：平台矩阵、构建命令、能力边界、安全说明
├── build.sh                       纯 Go 目标交叉编译（默认 6 个目标，可传参覆盖）
├── build-mobile.sh                gomobile 生成 Android AAR / iOS XCFramework（缺 SDK 则跳过）
├── configs/
│   └── ngg-mobile.example.json    移动端/嵌入式接入配置示例（token、ingress、dns 等占位）
├── android/                       Android 接入骨架（Gradle + Kotlin 薄封装）
│   └── app/
│       ├── build.gradle.kts       声明 implementation(files("libs/ngg.aar"))
│       └── src/main/java/com/ngg/gateway/GatewayBridge.kt
├── ios/                           iOS 接入骨架（Swift 薄封装）
│   ├── README.md
│   └── NGGGateway.swift
├── harmony/                       HarmonyOS 接入骨架（ArkTS UIAbility）
│   ├── README.md
│   └── EntryAbility.ets
├── desktop/                       Windows/macOS/Linux 桌面接法（服务模式 + Web UI）
│   └── README.md
├── tv/                            Android TV / 电视端接法
│   └── README.md
└── openwrt/                       OpenWrt 软路由（procd init + UCI 配置）
    ├── README.md
    └── files/etc/
        ├── init.d/ngg             procd init 脚本
        └── config/ngg             UCI 配置示例
```

构建产物统一落在仓库根目录的 `bin/platforms/`（已被 `.gitignore` 忽略），
不入库、不提交任何二进制。

## 4. 构建命令

在仓库根目录执行：

```bash
# 1) 纯 Go 目标：默认 6 个目标
bash platforms/build.sh

# 2) 只构建指定目标（全部可构建目标见 build.sh 注释 / 下表）
bash platforms/build.sh linux/arm linux/mipsle windows/arm64 freebsd/amd64

# 3) 全部 10 个纯 Go 目标
bash platforms/build.sh \
  linux/amd64 linux/arm64 linux/386 linux/arm linux/mipsle \
  windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 freebsd/amd64
```

`build.sh` 使用 `GOOS=.. GOARCH=.. CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`，
逐目标打印成功/失败，单目标失败不中止其它目标，最后给汇总。

```bash
# 4) 移动端绑定（需要 gomobile + Android NDK / Xcode）
bash platforms/build-mobile.sh
# 工具链缺失时会打印明确的跳过原因并退出 0，不生成占位文件、不假装成功。
# 成功后得到：
#   bin/platforms/mobile/ngg.aar
#   bin/platforms/mobile/NGG.xcframework
```

各平台的原生工程（Android Studio / Xcode / DevEco Studio）与系统 VPN 接入，
请分别阅读 `android/`、`ios/`、`harmony/`、`desktop/`、`tv/`、`openwrt/` 下的 README。

## 5. 能力边界声明（禁止夸大）

- **不是成品 App**：本目录没有任何已实现、已签名、可安装的原生客户端；
  iOS / iPadOS / Android / HarmonyOS / Android TV 的原生 UI App 与桌面托盘 App
  **均未实现**。
- **只是骨架**：提供的是可编译的共享 Go 核心接入方式、平台接入骨架、构建脚本与文档。
- **共享核心是真实 REST 客户端**：`internal/mobile` 走真实 HTTP 请求到控制面，
  不是本地模拟；连通性由 `Connectable()` 真实探测。
- **移动端仍需外部 SDK**：`android/arm64`、`ios/arm64` 需 gomobile + Android NDK /
  Xcode；缺工具链时 `build-mobile.sh` 明确跳过并返回 0。
- **原生 VPN 能力未接入**：`VpnService`（Android）、`NEPacketTunnelProvider`（iOS）、
  VPN Kit（HarmonyOS）、leanback 遥控器友好 UI（TV）、系统托盘 UI（桌面）
  均为待开发项，骨架代码里以 TODO 标注。
- **数据面入口形态按平台区分**：Linux 可用统一 TUN（需 `/dev/net/tun` +
  `CAP_NET_ADMIN`）；Windows/macOS 用 userspace SOCKS5/HTTP CONNECT 入口
  （默认 `127.0.0.1:18080`）+ 系统代理；DNS 默认 `127.0.0.1:18053`。

## 6. Token 与「仅本机」安全说明

- 控制面 REST / WS **默认只监听 `127.0.0.1`**，并在中间件二次校验对端为回环地址，
  不对局域网/公网开放。所有 `/api/v1/**` 请求需要 `Authorization: Bearer <token>`，
  WS 可用 `?token=<token>`（仅 `/api/v1/ws` 生效）。
- Token 由 `nggd` 首次启动时自动生成并打印到控制台、写回 `configs/ngg.yaml`；
  `configs/ngg.yaml` 含 Token，已在 `.gitignore` 中排除，**切勿提交或硬编码进客户端**。
- 移动端/嵌入式配置模板 `configs/ngg-mobile.example.json` 中 `token` 为占位符
  `<REPLACE_WITH_LOCAL_API_TOKEN>`，实际应由用户在设备侧安全录入（例如首次绑定时
  扫码/手动输入），不要写入源码或打包资源。
- **不要把控制面改成监听非回环地址**：这是架构约束（ADR-005）。若电视等其它设备
  需要共用网关，正确做法是在局域网另一台主机运行 NGG，并把**数据面 ingress**
  调整到该主机可达地址；按 `internal/netengine` 的约束，**非回环 ingress 必须配置
  `auth_user` / `auth_pass` 认证**（否则会被 `ErrIngressInsecure` 拒绝）。控制面本身
  仍应保持仅本机，由该主机本地管理。
- 任何示例命令中的 Token 都用 `<TOKEN>` 占位，不代表真实凭据。
