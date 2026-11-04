package com.ngg.gateway

import mobile.Gateway
import mobile.Mobile

/**
 * 共享 Go 核心（仓库 `internal/mobile`，`package mobile`）的 Kotlin 薄封装。
 *
 * 能力边界：这是 **骨架 / scaffold**，不是成品 App。
 * - 本类只把 gomobile bind 生成的 Java 绑定包一层，方便 future 的原生 UI 调用；
 * - Android 的 `VpnService`、原生界面、权限申请、系统代理配置 **均未实现**；
 * - 共享核心是真实 REST 客户端：所有方法都会向
 *   `http://127.0.0.1:9090/api/v1` 发真实 HTTP 请求，不是本地模拟。
 *
 * Go 侧对应关系（internal/mobile/mobile.go）：
 *   mobile.New(baseURL, token) -> Gateway
 *   Gateway.Connectable()      -> connectable()
 *   Gateway.StartCore(id)      -> startCore(id)
 *   Gateway.NetworkStatus()    -> networkStatus()
 */
class GatewayBridge(private val gateway: Gateway) {

    companion object {
        /**
         * 创建网关实例。
         *
         * 注意：`gomobile bind` 会把 Go 包 `mobile` 生成为 Java 包 `mobile`，
         * 导出函数 `New` 的 Java 符号名随 gomobile 版本不同，可能是
         * `mobile.Mobile.new(...)`、`mobile.Mobile.new_(...)`，
         * 或被当作构造器生成为 `mobile.Gateway(baseUrl, token)`（因 `new` 是 Java 关键字）。
         * TODO: 生成 AAR 后用 `javap -classpath ngg.aar mobile.Mobile mobile.Gateway`
         *       核对实际符号并固定下面这一行；本文件为骨架，尚未接真实 AAR。
         */
        fun create(baseUrl: String, token: String): GatewayBridge {
            val gw: Gateway = Mobile.new(baseUrl, token)
            return GatewayBridge(gw)
        }
    }

    /** 真实探测控制面可达性（GET /api/v1/health）。 */
    fun connectable(): Boolean = gateway.connectable()

    /** 共享核心版本串。 */
    fun coreVersion(): String = gateway.coreVersion()

    /**
     * 启动指定内核。
     * Go 侧返回 (string, error)，gomobile 在 Java 侧映射为「返回 String、失败抛异常」。
     * 调用方需处理运行时异常。
     */
    fun startCore(id: String): String = gateway.startCore(id)

    /** 查询网络状态（含 ingress 地址），Go 侧返回 (string, error)。 */
    fun networkStatus(): String = gateway.networkStatus()

    /**
     * TODO: VpnService 尚未实现。
     * 真实接管流量需要：
     *  1. 继承 android.net.VpnService，申请 BIND_VPN_SERVICE 权限；
     *  2. 在 AndroidManifest.xml 声明 service 与 `android:permission`；
     *  3. 建立 TUN fd 后，把流量导向 NGG 的 ingress（默认 127.0.0.1:18080），
     *     或按平台约束使用 NGG 提供的 SOCKS5/HTTP CONNECT 入口；
     *  4. 处理 DNS（默认 127.0.0.1:18053）与 fail_closed 策略；
     *  5. 在原生 UI 中完成 Token 录入（不要用本骨架里的占位值）。
     * 上述内容均不在本骨架范围内。
     */
}
