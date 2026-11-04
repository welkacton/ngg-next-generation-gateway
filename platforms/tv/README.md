# Android TV / 电视端接法

> **骨架 / 未实现，非成品 App。** 本目录只有接入说明，不包含任何 TV 应用代码。
> Android TV 的 **leanback launcher 适配**、**遥控器友好 UI**、`VpnService` 接入、
> 设置页与上架签名 **均未实现**。TV 端目前最现实的用法是「复用 Android AAR 做伴生
> App」或「通过局域网内的 NGG 网关代理」。

## 1. 能力边界（务必先读）

- 没有 TV 端可安装 APK，没有 `leanback` 依赖、没有遥控器焦点/DPAD 适配、没有
  `AndroidManifest.xml` 的 `LEANBACK_LAUNCHER` 声明。
- 电视设备通常**不适合/不方便**在本机运行 `nggd` 守护进程，因此 TV 端一般不采用
  「本机控制面 + 本机 TUN」形态，而是把流量交给**局域网内另一台 NGG 网关**。
- 共享 Go 核心 `internal/mobile` 仍是真实 REST 客户端；若要在 TV 上编译伴生 App，
  只能复用 Android 的 `ngg.aar`（见 `platforms/android/`），**没有** TV 专用绑定库。
- `VpnService` 在 Android TV 上的支持因设备/系统版本而异，本骨架不提供实现。

## 2. 两种接法（均为设计，未实现成品）

### 接法 A：局域网 NGG 网关 + TV 系统代理（推荐）

在局域网另一台主机 / 软路由（见 `platforms/openwrt/`）上运行 NGG，把 TV 的系统代理
指向该网关的入口。

1. 网关侧：把 `network.ingress.listen` 从 `127.0.0.1` 改为该主机在局域网可达的地址
   （例如 `192.168.1.10`），端口仍可用 `18080`。
2. **必须**同时配置 `network.ingress.auth_user` / `network.ingress.auth_pass`：
   `internal/netengine` 明确要求「非回环 ingress 必须带认证」，否则会返回
   `ErrIngressInsecure`（见 `internal/netengine/ingress.go`）。
3. 控制面 REST（`:9090`）**保持仅本机**，不要暴露到局域网（架构约束 ADR-005）。
   TV 只连数据面 ingress，不连控制面。
4. TV 侧设置系统代理（Android TV 图形界面通常不提供代理项，可用 ADB 临时设置）：

```bash
# 仅用于调试/验证，重启或网络变化后可能失效；不是产品化方案
adb connect <TV_IP>
adb shell settings put global http_proxy 192.168.1.10:18080
# 清除代理
adb shell settings delete global http_proxy
```

> 注意：`http_proxy` 为 HTTP CONNECT 场景；若需 SOCKS5 或全局接管，仍需
> 设备支持 `VpnService` 或一个leanback 伴生 App（**未实现**）。
> 示例中的 IP / 认证信息均为占位，请替换为实际部署值。

### 接法 B：复用 Android AAR 做伴生 App

Android TV 也是 Android，理论上可复用：

- `bin/platforms/mobile/ngg.aar`（由 `platforms/build-mobile.sh` 生成）
- `platforms/android/app/src/main/java/com/ngg/gateway/GatewayBridge.kt`

用于「查询状态 / 启停内核」等控制面操作。但要成为 TV 端产品，还需补齐：

- `LEANBACK_LAUNCHER` 声明、`androidx.leanback` 依赖、遥控器焦点与 DPAD 导航
- 大屏布局、10-foot UI、无触屏交互
- `VpnService`（若设备支持）或系统代理配置
- Token 安全录入与存储

以上**全部未实现**。

## 3. 安全说明

- 控制面 REST/WS 默认只监听 `127.0.0.1` 并要求 `Bearer <token>`，
  **不要**为 TV 场景把控制面暴露到局域网。
- 把数据面 ingress 迁到局域网地址时，必须配置 `auth_user` / `auth_pass`；
  非回环且无认证的入口会被 NGG 拒绝启动。
- 示例中的 `<TOKEN>`、`<TV_IP>`、`192.168.1.10` 均为占位符，不代表真实凭据或拓扑。
- 跨设备共用网关的整体约束见 `platforms/README.md` 第 6 节。

## 4. 仍需完成（未实现清单）

- leanback launcher / 遥控器友好 UI、大屏布局
- `VpnService` 或可靠的全设备代理配置
- 网关侧 ingress 认证凭据的分发与管理
- 伴生 App 的构建、签名、上架
