# iOS / iPadOS 接入骨架

> **骨架 / scaffold，非成品 App。** 本目录只提供 Swift 薄封装。
> iOS / iPadOS 的原生 UI、`NEPacketTunnelProvider`（Network Extension）、
> App Group / Keychain / entitlements、上架签名 **均未实现**。
> 生成 XCFramework 需要 **macOS + Xcode**，属于需外部 SDK 的目标。

## 1. 能力边界（务必先读）

- 共享 Go 核心 `internal/mobile` 是**真实 REST 客户端**，通过 gomobile bind 打成
  `NGG.xcframework`，由它去访问控制面 `http://127.0.0.1:9090/api/v1`
  （Bearer Token，默认仅本机回环）。
- 本目录**没有**可安装的 .ipa，**没有** Storyboard / SwiftUI 界面，
  **没有** Network Extension target。
- `NEPacketTunnelProvider` 是流量真正走 NGG 的前提，当前为 TODO，见
  `NGGGateway.swift` 末尾注释。
- 重要接入前提：控制面 REST/WS **默认只监听 `127.0.0.1`**。iOS 设备本机通常不运行
  `nggd`，因此「移动端如何获得一个本机可达的控制面」是**尚未解决**的工程问题，
  本骨架不提供方案，也不假定可以把控制面直接暴露到局域网。

## 2. 目录

```
ios/
├── README.md
└── NGGGateway.swift     # Swift 薄封装：MobileNew / connectable / startCore / networkStatus
```

## 3. 生成并接入 XCFramework

```bash
# 在仓库根目录；必须在 macOS + 已安装 Xcode 的机器上
bash platforms/build-mobile.sh
# 成功后得到 bin/platforms/mobile/NGG.xcframework
```

`build-mobile.sh` 实际执行的是：

```bash
gomobile bind -target=ios -o NGG.xcframework ./internal/mobile
```

在非 macOS 系统、或缺 Xcode / gomobile 时，脚本会打印明确的跳过原因并退出 0，
**不会**生成占位文件、不会假装成功。

把生成的 `NGG.xcframework` 拖入 Xcode 工程的
「Frameworks, Libraries, and Embedded Content」并设为 Embed & Sign。
若 Swift 报 `No such module 'NGG'`，说明生成的 framework 模块名不同（部分 gomobile
版本为 `Mobile`），请把 `NGGGateway.swift` 顶部的 `import` 改成实际模块名。

## 4. gomobile 绑定符号

gomobile 把 Go 包 `mobile` 生成为带 `Mobile` 前缀的 Objective-C 类与函数：

| Go（internal/mobile） | ObjC / Swift（生成） |
| --- | --- |
| `New(baseURL, token) *Gateway` | `MobileNew(NSString*, NSString*) -> MobileGateway*` |
| `Gateway.Connectable() bool` | `-[MobileGateway connectable] -> BOOL` |
| `Gateway.StartCore(id) (string,error)` | `-[MobileGateway startCore:error:] -> NSString*` |
| `Gateway.NetworkStatus() (string,error)` | `-[MobileGateway networkStatus:error:] -> NSString*` |
| `Gateway.CoreVersion() string` | `-[MobileGateway coreVersion] -> NSString*` |

带 `error` 的方法在 Swift 中导入为 `throws`，所以 `startCore` / `networkStatus`
在 `NGGGateway.swift` 里用 `try` 调用。若实际头文件符号不同，请以生成的
`NGG.xcframework/Headers/*.h` 为准调整薄封装。

## 5. 调用示例（概念性）

```swift
// token 必须由用户在设备侧安全录入（Keychain），禁止硬编码
let gateway = NGGGateway(
    baseURL: "http://127.0.0.1:9090",
    token: "<LOCAL_API_TOKEN>"
)

if gateway.connectable() {
    let status = try? gateway.networkStatus()
    try? gateway.startCore("mihomo")   // id 需与 GET /api/v1/cores 中一致
}
```

## 6. 仍需完成（未实现清单）

- Network Extension target + `NEPacketTunnelProvider` 实现
- `NETunnelProviderManager` 隧道配置加载/保存、App Group、entitlements
- 把 TUN 流量对接 NGG ingress（默认 `127.0.0.1:18080`）/ DNS（默认 `127.0.0.1:18053`）
- 原生 UI（SwiftUI / UIKit）、Token 安全存储（Keychain）
- 解决「iOS 本机可达控制面」的接入前提（见第 1 节）
- iPadOS 多窗口 / 分屏适配、fail_closed、断线重连等产品化逻辑

## 7. 安全说明

控制面 REST/WS 默认只监听 `127.0.0.1` 并要求 `Bearer <token>`。所有示例中的
`<LOCAL_API_TOKEN>` 均为占位符。按架构约束（ADR-005）不要把控制面改成监听非回环
地址；跨设备共用网关的正确做法见 `platforms/README.md` 第 6 节。
