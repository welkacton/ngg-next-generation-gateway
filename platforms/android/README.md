# Android 接入骨架

> **骨架 / scaffold，非成品 App。** 本目录只提供 Kotlin 薄封装与最小 Gradle 声明。
> Android 原生 UI、`VpnService`、权限声明、上架签名 **均未实现**。
> 需要 Android Studio + Android SDK/NDK 完成后续原生开发。

## 1. 能力边界（务必先读）

- 共享 Go 核心 `internal/mobile` 是**真实 REST 客户端**，通过 gomobile bind 打成 AAR，
  由它去访问控制面 `http://127.0.0.1:9090/api/v1`（Bearer Token，默认仅本机）。
- 本目录**没有**可安装的 APK，**没有** Activity / 布局 / 资源，**没有** `VpnService`。
- `VpnService`（系统 VPN 接管）是流量能真正走 NGG 的前提，当前为 TODO，见
  `GatewayBridge.kt` 末尾注释。
- 在 `VpnService` 实现之前，AAR 只能用于「查询状态 / 启停内核」等控制面操作，
  不能接管设备流量。

## 2. 目录

```
android/
├── README.md
└── app/
    ├── build.gradle.kts                               # 声明 implementation(files("libs/ngg.aar"))
    └── src/main/java/com/ngg/gateway/GatewayBridge.kt # Kotlin 薄封装
```

`app/libs/ngg.aar` 不在仓库中，需由 `platforms/build-mobile.sh` 生成后拷贝进来。

## 3. 生成并接入 AAR

```bash
# 在仓库根目录；需要 gomobile + Android NDK，缺失时脚本会明确跳过并退出 0
bash platforms/build-mobile.sh

# 成功后把 AAR 拷到 app 模块
mkdir -p platforms/android/app/libs
cp bin/platforms/mobile/ngg.aar platforms/android/app/libs/ngg.aar
```

`build-mobile.sh` 实际执行的是：

```bash
gomobile bind -target=android -o ngg.aar ./internal/mobile
```

gomobile 会把 Go 包 `mobile` 生成为 Java 包 `mobile`：`Gateway` 类对应
`mobile.Gateway`，`New` 等导出函数对应 `mobile.Mobile` 上的静态方法。
由于 `new` 是 Java 关键字，实际生成的符号（可能是 `Mobile.new` / `Mobile.new_`
或 `Gateway` 构造器）请在 AAR 生成后用下面命令核对：

```bash
javap -classpath app/libs/ngg.aar mobile.Mobile mobile.Gateway
```

核对结果用于固定 `GatewayBridge.kt` 中 `create()` 的那一行工厂调用。

## 4. Gradle 工程骨架

本目录只给了 app 模块文件。要组成标准 Android Studio 工程，还需在根目录补：

`settings.gradle.kts`（示例）：

```kotlin
pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}
rootProject.name = "NGG"
include(":app")
```

根 `build.gradle.kts`（示例，插件版本按需调整）：

```kotlin
plugins {
    id("com.android.application") version "8.7.3" apply false
    id("org.jetbrains.kotlin.android") version "2.0.21" apply false
}
```

以及 `gradle/wrapper/` 与 `gradlew`。`app/build.gradle.kts` 中最关键的一行是：

```kotlin
dependencies {
    implementation(files("libs/ngg.aar"))
}
```

## 5. 调用示例（概念性）

```kotlin
// token 必须由用户在设备侧安全录入，禁止硬编码或写进打包资源
val bridge = GatewayBridge.create(
    baseUrl = "http://127.0.0.1:9090",
    token = "<LOCAL_API_TOKEN>"
)

if (bridge.connectable()) {
    val status = bridge.networkStatus()   // 可能抛异常，需 try/catch
    bridge.startCore("mihomo")            // id 需与 GET /api/v1/cores 中一致
}
```

`GatewayBridge.kt` 覆盖的共享核心方法：`Connectable()`、`StartCore()`、
`NetworkStatus()`、`CoreVersion()`，对应 Go 侧 `internal/mobile/mobile.go`。

## 6. 仍需完成（未实现清单）

- `VpnService` 子类 + `BIND_VPN_SERVICE` 权限 + `AndroidManifest.xml` 声明
- 把 TUN 流量对接 NGG ingress（默认 `127.0.0.1:18080`）/ DNS（默认 `127.0.0.1:18053`）
- 原生 UI（Token 录入、内核列表、开关、状态展示）
- 控制面 Token 的安全存储（Android Keystore / 加密偏好）
- fail_closed、断线重连、自检（`SelfTest` / `ProxyURL`）等产品化逻辑

## 7. 安全说明

控制面 REST/WS 默认只监听 `127.0.0.1` 并要求 `Bearer <token>`。移动端若与 `nggd`
同机（如本机调试、或同一设备上的守护进程）可直连回环地址；若跨设备，请按
`platforms/README.md` 第 6 节的约束处理，**不要**把控制面暴露到局域网。
示例中的 `<LOCAL_API_TOKEN>` 一律为占位符。
