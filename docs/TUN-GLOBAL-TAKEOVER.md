# 「TUN 按钮 → 自动装虚拟网卡 → 自动接管全部流量」可行性与实施路径

> 结论先说：**现在不能**。把 Web UI 里的 TUN 开关打开，不会自动安装虚拟网卡，
> 也不会接管全部流量。这不是配置问题，而是**数据面尚未实现**。
> 本文给出代码级现状、缺什么、分几步做，以及每一步的**真实验收标准**。
> 遵循仓库纪律：`06-roadmap.md` §1.2 —— 不接受 mock / 桩 / 假状态灯充当通过证据。

---

## 1. 当前实际状态（代码级，不是宣传口径）

| 位置 | 现状 |
| --- | --- |
| `internal/netengine/tun_linux.go` | **真** Linux TUN：`/dev/net/tun` + `TUNSETIFF` + `ip addr` / `ip link` / `ip route` / `fwmark`。需要 `CAP_NET_ADMIN` |
| `internal/netengine/tun_other.go` | Windows / macOS / 其它平台 `Open()` 直接返回 `ErrTUNUnsupported`，**没有任何驱动安装、没有虚拟网卡** |
| `internal/netengine/netengine.go` `tunLoop()` | 读真实 L3 包 → `packets.Parse()` → 仅对 **IPv4 UDP/53** 用 `dnsEngine` 构造真实 DNS 应答包写回；**其余包一律 `Dropped++` 并 Fail-Closed** |
| `internal/netengine/netengine.go` `HandleTCP()` | 真正的数据面，但它接收的是 `net.Conn`（来自 SOCKS5 / HTTP CONNECT 入口），**不是** TUN 里的裸 IP 包 |
| `internal/netengine/dispatcher.go` / `bridge.go` / `rules.go` | 逐连接决策、Failover、Fail-Closed、10 类规则 / 4 类动作，均已实现并被真实 socket 测试覆盖 |

所以今天的正确描述是：

- **Linux**：NGG 能创建 TUN、能改路由、能在 TUN 里回答 DNS；但 TCP 数据面**没接上**，开了 TUN 反而会把 TCP 包丢掉（Fail-Closed，不泄漏、也不伪装成功）。
- **Windows**：连 TUN 设备都没有，全部靠 `127.0.0.1:18080` 的 userspace SOCKS5 / HTTP CONNECT 入口 + 系统代理。
- Web UI 的「TUN」开关目前只能改 `network.tun.autoroute` 这类配置，**不等于**「一键全局接管」。

> 🛑 **不要**在生产环境把 `autoroute: true` 当成「已经全局代理」使用：当前 TCP 会被丢弃。
> 这条限制在 `README.md`、`06-roadmap.md` §3.6、`07-stage2-network-engine.md` 里都如实标注过。

---

## 2. 先纠正一个概念：不是「虚拟网卡决定去哪」

虚拟网卡（TUN/Wintun adapter）只负责一件事：**把本机发出的原始 IP 包交给 NGG，并把 NGG 构造的 IP 包送回内核**。
它不做路由决策。真正决定「这个连接走哪个内核 / DIRECT / REJECT / 哪个出口」的是：

```
TUN/Wintun → PacketProcessor → SessionManager → RuleEngine
          → ProtocolDispatcher → CoreAdapterBridge（选内核）
          → EgressManager（选出接口 / SO_MARK 防回环）→ 真实 socket
```

这套决策链 Stage 2/3 **已经存在**（`HandleTCP` 路径），缺的是把「裸 IP 包」变成「可交给这条链的 `net.Conn`」的那一层。

---

## 3. 要补齐的 6 个组件

| # | 组件 | 说明 | 现状 |
| --- | --- | --- | --- |
| 1 | **Windows 虚拟网卡驱动** | Wintun（或 TAP-Windows）驱动：签名驱动随安装包分发、管理员权限安装/卸载、创建 `NGG` adapter、设置 IPv4/IPv6/MTU | ❌ 未实现。Windows 上 `tun_other.go` 明确拒绝 |
| 2 | **跨平台路由 / DNS 接管与还原** | Windows：`route add` / `netsh` / NRPT 或设 DNS；Linux：`ip rule` / `ip route` / `fwmark`。必须**先备份原路由和 DNS，失败能完整还原**，否则会把用户断网 | ⚠️ Linux 有 `AutoRoute` 基础，Windows 无 |
| 3 | **Userspace TCP/IP 协议栈（最大缺口）** | `gVisor netstack` / `smoltcp` / `lwIP` 之一，把 TUN 里的裸 TCP SYN/数据流终止成 `net.Conn`，再喂给 `HandleTCP()`；同时处理 UDP、连接状态、重传、窗口、RST | ❌ 未实现（`tunLoop` 注释明确说明） |
| 4 | **与现有引擎接线** | netstack 的每条连接 → `sessions.Create` → `rules.Match` → `dispatcher.Decide` → `bridge` → `egress.Dialer`；Fail-Closed 必须沿用 | ❌ 未接线 |
| 5 | **Windows 防回环** | Linux 已有 `SO_MARK 0x4E47` 绕过 TUN；Windows 需要「绑定物理网卡出口 + 排除路由 / WFP 规则」，否则内核出站又回到 Wintun → 死循环 | ❌ 未实现 |
| 6 | **按钮 → 事务式生命周期** | 点「开启」= 装驱动 → 建网卡 → 备份并改路由/DNS → 起 netstack → 自检真实流量；任一步失败**自动回滚全部改动**并给出错误；点「关闭」反向还原 | ❌ 未实现 |

另外还有非功能性要求：驱动需代码签名、安装包需管理员、卸载需清理网卡与路由、崩溃/进程退出必须自动还原路由（否则用户断网），以及真实网络环境的验收（不是编译通过就算完成）。

---

## 4. 分阶段实施计划（建议顺序）

| 阶段 | 交付 | 真实验收标准（缺一不可） |
| --- | --- | --- |
| **P0 底座** | 引入 `gVisor netstack`（或等价栈），把 `tunLoop()` 里的裸 TCP 终止成 `net.Conn` 并接到 `HandleTCP()`；保留 UDP/DNS 分支 | Linux 上真实 `curl` 经 TUN → NGG → 内核 → 目标，抓包确认无直连泄漏；`TestEndToEndThroughRealSockets` 扩展到 TUN 路径并全绿 |
| **P1 Linux 全局接管** | `autoroute` 真正可用：默认路由 / `ip rule` / `fwmark` / DNS 接管 + 事务式还原；进程退出自动还原 | 真实机器开启后 `curl ifconfig.me` 走 NGG；`kill -9 nggd` 后网络自动恢复；断网/端口占用故障注入能 Fail-Closed 且回滚 |
| **P2 Windows Wintun** | `tun_windows.go`：随安装包分发 Wintun 驱动（签名）、创建/销毁 adapter、读写包；`tun_other.go` 收窄到 macOS 等 | 真实 Windows 10/11 x64 上 `Get-NetAdapter` 可见 NGG 网卡；能收发真实 IP 包；卸载后网卡消失 |
| **P3 Windows 接管 + 防回环** | 路由/DNS 配置、绑定物理出口、排除路由，复用 P0 的 netstack 与决策链 | 真机打开按钮后浏览器真实访问外网且经 NGG；无回环、无 DNS 泄漏；关闭按钮后路由/DNS 完整还原 |
| **P4 产品化** | Web UI 按钮状态机、失败原因展示、开机自启、崩溃还原守护、多平台一致的能力边界文案、全平台真实设备验收 | 每台目标设备按 `06-roadmap.md` §6.3 完成真实安装/真实联网转发/真实故障切换，禁止用 mock 通过 |

> 工作量估计：P0 是核心（最大），P2/P3 需要真实 Windows 驱动签名与多版本真机测试。
> 这不是「改个按钮」的量级，也不应该在没有真实验收前把状态写成「已实现」。

---

## 5. 现在就能用的过渡方案（不假装是 TUN）

在 P0~P3 完成之前，Windows 想让「大部分应用的流量」走 NGG，可用**系统代理**方式，
它不需要驱动、不需要管理员装网卡，但**不是全局接管**（不遵守系统代理的程序、UDP、部分游戏/下载工具不会走）：

- `installer/windows/set-system-proxy.ps1 -Enable`：设置 WinINET + WinHTTP 代理到 `127.0.0.1:18080`；
- `-Disable`：完整还原原设置（脚本会先备份到 `%LOCALAPPDATA%\NGG\proxy-backup.json`）；
- 配合 NGG 的 `network.ingress`（SOCKS5 / HTTP CONNECT）与 `network.dns` 使用。

Linux 用户可继续用 `HTTP_PROXY` / `HTTPS_PROXY` / 应用级代理，或等 P1。
配置示例见 [`platforms/desktop/README.md`](../platforms/desktop/README.md)。

---

## 6. 相关文档

| 文档 | 内容 |
| --- | --- |
| [`06-roadmap.md`](06-roadmap.md) | 五阶段路线图、§3.6 能力边界、§1.2 真实环境验收纪律 |
| [`07-stage2-network-engine.md`](07-stage2-network-engine.md) | 统一网络引擎设计、TUN 能力边界、四种入口、Fail-Closed 决策表 |
| [`INSTALL-WINDOWS.md`](INSTALL-WINDOWS.md) | Windows 安装与服务，含「为什么 TUN 按钮还不能一键接管」 |
| [`DEPLOY-VPS.md`](DEPLOY-VPS.md) | VPS 部署与 `--enable-tun` 的真实边界 |
