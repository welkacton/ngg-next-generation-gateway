# HarmonyOS 接入骨架

> **骨架 / scaffold，非成品 App。** 本目录只提供 ArkTS `UIAbility` 生命周期骨架与
> 接入路径说明。HarmonyOS 原生 UI、VPN Kit 接入、Token 安全存储、签名上架
> **均未实现**。需要 DevEco Studio + HarmonyOS SDK 完成后续原生开发。

## 1. 能力边界（务必先读）

- **gomobile 不支持 HarmonyOS**：`build-mobile.sh` 只产出 Android AAR 与
  iOS XCFramework，二者都**不能**直接用于 HarmonyOS。
- 本目录**没有**可安装的 HAP 包，**没有** ArkUI 页面，**没有** NAPI 桥接代码，
  **没有** `VpnExtensionAbility`。
- 共享 Go 核心 `internal/mobile` 是控制面的真实 REST 客户端；在 HarmonyOS 上如何调用
  它，见第 3 节的两种候选路径，**两种路径目前都还没实现**。

## 2. 目录

```
harmony/
├── README.md
└── EntryAbility.ets     # ArkTS UIAbility 生命周期骨架（含大量 TODO）
```

`EntryAbility.ets` 中引用的 `pages/Index` 只是占位页面，未随本骨架提供。

## 3. 共享核心的两种候选接入路径（均未实现）

### 路径 A：NAPI 调用共享 Go 核心

1. 把 `internal/mobile`（或为其新写一层 C ABI 包装）用
   `go build -buildmode=c-shared` 编出 `.so`；需要为 `Gateway` 的
   `New / Connectable / StartCore / NetworkStatus` 等增加 `//export` 的 C 函数。
2. 用 HarmonyOS NAPI（C/C++）写桥接模块，把这些 C 函数暴露给 ArkTS。
3. ArkTS 侧调用 NAPI 模块拿到网关句柄，再走原生 UI。

注意：当前仓库**没有**这层 C 导出，gomobile 也不产出 HarmonyOS 可用的库，
因此路径 A 需要额外开发，不能假定已经可用。

### 路径 B：经本机 REST 控制面（较简单）

不经绑定库，直接由 ArkTS 用 `@ohos.net.http` 访问控制面：

- Base URL：`http://127.0.0.1:9090/api/v1`
- 认证头：`Authorization: Bearer <token>`
- WebSocket：`/api/v1/ws?token=<token>`

这与 `internal/mobile` 的真实 REST 语义完全一致，且无需编译绑定库。
但要注意控制面**默认仅监听回环地址**：HarmonyOS 设备本机通常不运行 `nggd`，
「如何获得本机可达的控制面」仍是待解决的接入前提，不得假定可把控制面暴露到局域网。

配置字段可参考 `platforms/configs/ngg-mobile.example.json`
（`base_url` / `token` 占位 / `ingress` / `dns` / `default_core` / `fail_closed`）。

## 4. 仍需完成（未实现清单）

- 选定并实现路径 A 或路径 B（当前都只是设计）
- ArkUI 页面（Token 录入、内核列表、开关、状态展示）
- VPN Kit：`VpnExtensionAbility` + `module.json5` 声明 + TUN 流量对接 NGG ingress
  （默认 `127.0.0.1:18080`）/ DNS（默认 `127.0.0.1:18053`）
- Token 安全存储、fail_closed、断线重连、自检等产品化逻辑
- DevEco Studio 工程文件（`build-profile.json5`、`oh-package.json5`、`module.json5` 等）

## 5. 安全说明

控制面 REST/WS 默认只监听 `127.0.0.1` 并要求 `Bearer <token>`；示例中的
`<token>` 一律为占位符，禁止硬编码进 ArkTS 源码或打包资源。不要把控制面改成监听
非回环地址（架构约束 ADR-005）；跨设备共用网关的做法见 `platforms/README.md` 第 6 节。
