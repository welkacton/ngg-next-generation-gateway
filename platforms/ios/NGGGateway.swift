import Foundation
// gomobile bind -target=ios -o NGG.xcframework ./internal/mobile 生成的模块。
// 模块名以实际生成的 XCFramework 为准（常见为 NGG，部分版本为 Mobile）；
// 若编译报「no such module」，请按 Xcode 中 Framework 的实际 module 名调整。
import NGG

/// 共享 Go 核心（仓库 `internal/mobile`，`package mobile`）的 Swift 薄封装。
///
/// 能力边界：这是 **骨架 / scaffold**，不是成品 App。
/// - 只把 gomobile 生成的 Objective-C 绑定包一层，方便后续原生 UI 调用；
/// - iOS 的 `NEPacketTunnelProvider`（Network Extension）、原生界面、
///   App Group / Keychain 配置、上架签名 **均未实现**；
/// - 共享核心是真实 REST 客户端：所有方法都会向控制面
///   `http://127.0.0.1:9090/api/v1` 发真实 HTTP 请求，不是本地模拟。
///
/// Go 侧 -> gomobile iOS 绑定符号对应：
///   mobile.New(baseURL, token) -> MobileNew(...) -> MobileGateway
///   Gateway.Connectable()      -> connectable()
///   Gateway.StartCore(id)      -> startCore(id)
///   Gateway.NetworkStatus()    -> networkStatus()
final class NGGGateway {

    private let gateway: MobileGateway

    /// 用共享核心的 `MobileNew`（对应 Go 的 `mobile.New`）创建网关。
    /// token 必须由用户在设备侧安全录入（Keychain），禁止硬编码。
    init(baseURL: String, token: String) {
        guard let gw = MobileNew(baseURL, token) else {
            // 骨架阶段直接暴露问题；产品化时应改为可抛出/可恢复的错误。
            fatalError("MobileNew 返回空，检查 gomobile 绑定符号与参数")
        }
        self.gateway = gw
    }

    /// 真实探测控制面可达性（GET /api/v1/health）。
    func connectable() -> Bool {
        return gateway.connectable()
    }

    /// 共享核心版本串。
    func coreVersion() -> String {
        return gateway.coreVersion()
    }

    /// 启动指定内核。Go 侧返回 (string, error)，gomobile 在 ObjC/Swift 侧
    /// 映射为带 NSError** 的方法，Swift 中需 `try`。
    func startCore(_ id: String) throws -> String {
        return try gateway.startCore(id)
    }

    /// 查询网络状态（含 ingress 地址）。
    func networkStatus() throws -> String {
        return try gateway.networkStatus()
    }

    // TODO: NEPacketTunnelProvider 尚未实现。
    // 真实接管流量需要：
    //  1. 新建 Network Extension target，实现 NEPacketTunnelProvider；
    //  2. 在宿主 App 中通过 NETunnelProviderManager 加载/保存隧道配置；
    //  3. 建立 TUN 后把流量导向 NGG ingress（默认 127.0.0.1:18080，
    //     userspace SOCKS5/HTTP CONNECT），并处理 DNS（默认 127.0.0.1:18053）；
    //  4. 配置 App Group 共享配置、Keychain 保存 Token、entitlements；
    //  5. 完成原生 UI（Token 录入、内核开关、状态展示）。
    // 上述内容均不在本骨架范围内。
    //
    // 另需特别注意：iOS 设备本机通常不运行 nggd，而控制面默认仅监听
    // 127.0.0.1。如何在设备上获得「本机可达的控制面」是未解决的接入前提，
    // 不得假定把控制面直接暴露到局域网；详见 ios/README.md。
}
