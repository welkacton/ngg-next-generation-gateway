# NGG 五阶段路线图与验收标准

> **适用版本**：Phase 1 + Stage 2~5 当前实现
> **对应设计**：`01-architecture.md` 第 2 节（最高级架构约束）、第 11 节（路线图概要）、§12A（Stage 2~5 实现状态）
> **核心纪律**：接口提前预留，禁止提前堆积未经验证的实现；**所有模块必须真实网络环境测试，禁止模拟成功状态**（约束 10）
> **版本更新**：Stage 2/3/4/5 已由「待开发」更新为「**已实现（含能力边界）**」，逐阶段交付物与真实验收证据见 §3.5 / §4.4 / §5.4 / §6.4。**「真实网络环境验证、禁止模拟成功」纪律全文保留、继续有效。**

---

## 0. 阅读须知

- **Phase 1 = Stage 1**：第 2 节为 Phase 1 基线的交付与验收记录。
- **Stage 2~5 已实现（含能力边界）**：`internal/netengine/` 已由「接口占位」变为真实数据平面；规则/DNS、协议中心/后量子/插件、SD-WAN/多租户/跨平台共享核心均已落地，并新增 **40 个 REST 端点**。逐阶段交付物、真实测试证据与**如实标注的能力边界**见 §3.5 / §4.4 / §5.4 / §6.4 与 `07/08/09` 号文档。
- 本文所有「验收标准」都以**真实运行结果**为准，**不接受** mock / 桩 / 假数据充当通过证据。
- 阶段顺序**不可跳跃**：Stage 2 必须在 Stage 1 验收通过后启动，依此类推。
- **纪律不变**：即使阶段已实现，「真实网络环境验证、禁止模拟成功状态」（§1.2 / §1.3）仍是所有验收的前提；【骨架】与【能力边界】项**不计入已完成**，不得写成夸大表述。

### 0.1 阶段总览

| 阶段 | 主题 | 状态 |
| --- | --- | --- |
| **Stage 1** | 统一管理中心 + 多核并行 + Core Adapter + Core Registry | **Phase 1 已实现** |
| **Stage 2** | NGG TUN / 会话 / Packet Processor / Protocol Dispatcher / Core Bridge | **已实现（含能力边界）**：userspace 统一入口全平台可用；统一 TUN 仅 Linux 且需 `CAP_NET_ADMIN`；TUN 内 TCP 流终止未实现 |
| **Stage 3** | 统一规则引擎 / DNS / 出入口策略 | **已实现**：10 种规则 / 4 种动作、miekg/dns 引擎、Fail-Closed 全量替换、Egress SO_MARK |
| **Stage 4** | 传统 VPN / 加密代理 / 新内核 / 后量子 | **已实现（含能力边界 / 骨架）**：协议目录 + 原生配置生成、ML-KEM-768 混合控制信道、JS/WASM 运行时；不重写内核协议栈、插件市场为骨架 |
| **Stage 5** | SD-WAN / 企业网关 / 全平台 | **已实现（含能力边界 / 骨架）**：真实探测 + 4 策略、多租户、12 目标交叉编译 + gomobile 共享核心；原生 UI 客户端为骨架 |

---

## 1. 贯穿全阶段的工程纪律

### 1.1 开发顺序原则（约束 10）

> **接口提前预留，禁止提前堆积未经验证的实现。**

- 每个后续阶段涉及的接口 / 类型在**前一阶段就定义好**（如 Stage 1 已预留 `internal/netengine/` 全部接口），保证架构边界稳定；
- 但**接口存在 ≠ 功能实现**。在对应阶段验收前，不得往占位接口里塞入未经真实网络验证的逻辑；
- 禁止为了"看起来进度更快"而提前实现后阶段能力。

### 1.2 「真实网络环境验证」的定义

每个模块的验收必须满足：

| 要求 | 说明 |
| --- | --- |
| 真实内核 | 使用**真实内核二进制**（Mihomo / Xray-core / sing-box 或描述文件注册的新内核），非模拟进程 |
| 真实网络 | 在真实 Linux 网络栈中进行（真实网卡、真实路由、真实 DNS、真实远程目标），非纯内存环回桩 |
| 真实流量 | 产生真实 TCP / UDP 连接与数据包，验证转发、分流、出口行为 |
| 真实故障 | 注入真实故障（进程 kill、断网、端口占用、摘要损坏），验证恢复与 Fail-Closed |
| **禁止模拟成功状态** | 不得用硬编码返回值、mock 的 `success`、假的状态灯代替实际结果；任何"通过"必须有可复现的真实观测证据 |

### 1.3 阶段进入门槛（Gate）

进入下一阶段前必须同时满足：

1. 本阶段全部验收项**在真实网络环境通过**；
2. 本阶段**无未修复的 P0 / P1 缺陷**；
3. 架构约束 1 ~ 10 **无违背项**（尤其多核并行、统一 TUN 归属、资源隔离）；
4. 文档（本目录 6 份）与实现、API、UI **表述一致**，预留接口未被写成已实现；
5. 阶段验收清单签字留痕（写入 `GET /audit` 可查）。

---

## 2. Stage 1 — 统一管理中心 + 多核并行 + Core Adapter + Core Registry

> **状态：Phase 1 已实现（本文档集所描述的当前范围）**

### 2.1 目标

建立统一控制中心与可插拔内核体系，**不接管系统 TUN / 全局流量**。三个内核以 Worker Mode 由 NGG 独立管理，支持任意多个内核并行运行。

### 2.2 交付清单（【已实现】）

| # | 能力 |
| --- | --- |
| 1 | 架构与系统检测（平台 / 架构识别，匹配 Release 资产） |
| 2 | 三内核真实安装 / 卸载 |
| 3 | 真实版本读取（调用内核 `version` 命令 + `version_regex` 解析） |
| 4 | 独立启停重启 + **多核并行** |
| 5 | 进程状态机与健康监控（CPU / 内存 / 进程） |
| 6 | 配置导入 / 导出 / 备份 |
| 7 | 调用**内核原生配置校验** |
| 8 | 实时日志（采集 + WS 推送） |
| 9 | 端口冲突检测 |
| 10 | 升级与版本回滚 |
| 11 | 摘要校验下载（版本 / 来源 / **SHA256**） |
| 12 | 服务重启后状态恢复（SQLite） |
| 13 | 统一 REST + WS API 与 Web UI |
| 14 | 可插拔内核注册表（含 `generic` 描述文件驱动 adapter） |

### 2.3 验收标准

| 项 | 验收方式（真实环境） |
| --- | --- |
| 多核并行 | 同时 `start` 三个内核，`GET /cores` 返回 **3 个 `running`**；互相独立 stop / restart 不影响其他内核 |
| 故障隔离 | `kill -9` 其中一个内核进程，仅该内核进入 `failed → starting` 恢复，其余两个保持 `running` |
| 配置事务 | 提交一份**故意非法**的配置，`PUT /config` 返回 `422 CONFIG_INVALID`，`GET /config` 内容与提交前一致（原配置保留） |
| 原生校验 | `POST /config/validate` 的 `exit_code` / `output` 与直接执行内核校验命令的结果一致 |
| 摘要校验 | 人为替换下载文件使 SHA256 不符，`install` 返回 `DIGEST_MISMATCH`，二进制**不被落盘** |
| 版本回滚 | 升级后 `rollback`，`GET /version` 回到旧版本且进程可正常启动 |
| 状态恢复 | 重启 `nggd` 服务后，`GET /cores` 的状态 / 版本 / 端口分配与重启前一致 |
| 端口冲突 | 人为占用待分配端口，`start` 返回 `PORT_CONFLICT`，不静默成功 |
| 注册表扩展 | 在 `configs/cores.d/` 放入新描述文件 → `POST /registry/reload` → 新内核出现在 `GET /registry` 并可 install/start |
| API 安全 | 非 `127.0.0.1` 来源不可达；无 Token 请求返回 `401 UNAUTHORIZED` |
| 接口预留如实标注 | `GET /system` 的 `stage=1` / `mode="multi-core-parallel"`，`GET /health` 的 `unified_tun="reserved-stage-2"`；`internal/netengine/` 常量 `Stage2NotImplemented`；代码/UI/文档均**不宣称**已接管 TUN / DNS / 规则 |

### 2.4 真实网络环境验证要求

Stage 1 虽不接管系统流量，但**内核本身必须真实联网工作**：

- 真实下载内核 Release 资产（校验来源 + SHA256）；
- 真实启动内核进程并监听真实本地端口（SOCKS5 / HTTP CONNECT / 原生接口）；
- 通过真实本地代理端口发起真实 TCP/UDP 请求，确认内核协议能力可用；
- 真实进程故障（kill）、端口占用、服务重启，验证恢复与状态持久化。

> **禁止**：用假进程 / 假版本号 / 假日志 / 硬编码 `running` 冒充验收通过。

### 2.5 明确边界

- **【接口预留】**：`internal/netengine/` 12 个组件，零可执行逻辑；
- **【禁止】**：见第 8 节对照表，Stage 1 不得提前实现。

> **阶段更新说明**：以上 §2.5 是 **Phase 1 基线**的边界声明，保留不改。Stage 2~5 交付后，`internal/netengine/` 已落地为真实数据平面，§7 表中多数条目已转为「已实现（含能力边界）」；仍有若干【骨架】【能力边界】项未计入已完成。当前状态见 §3~§6 与 `01-architecture.md` §12A。

---

## 3. Stage 2 — NGG TUN / 会话 / Packet Processor / Protocol Dispatcher / Core Bridge

> **状态：已实现（含能力边界）** — Phase 1 时为「接口已预留，未实现」；现 `internal/netengine/` 已落地真实数据平面。能力边界见 §3.6。

### 3.1 目标

把约束 6 的**统一流量路径**落地：NGG 创建并管理**唯一 TUN**，接管系统入口流量；三个内核继续保持 Worker Mode，不创建系统级 TUN、不抢全局路由、DNS 劫持默认关闭。

### 3.2 主要交付

| 组件 | 落地内容 |
| --- | --- |
| `TunManager` | 唯一 TUN 创建 / 销毁 / 异常检测 |
| `PacketProcessor` | 包解析与重组 |
| `SessionManager` | 连接会话表、连接状态恢复 |
| `ProtocolDispatcher` | 逐连接选择内核或 DIRECT |
| `CoreAdapterBridge` | 把选定连接交给对应 `CoreAdapter` |
| `StreamAdapter` / `DatagramAdapter` | Native Stream / Datagram 接入（约束 5 后半段） |
| `NetworkResourceManager`（基础） | 防回环、防端口 / 路由冲突、远程传输绕过 TUN 走物理出口（约束 8） |

### 3.3 验收标准（**必须真实网络环境验证**）

| 项 | 验收方式 |
| --- | --- |
| 唯一 TUN | 系统仅存在 NGG 创建的 TUN；任一内核均未创建系统级默认 TUN |
| 统一路径 | 真实流量经 `TUN → PacketProcessor → SessionManager → Dispatcher → Bridge → Selected Core → Egress`，可用真实连接追踪逐跳验证 |
| 逐连接决策 | 同一时刻不同连接可被分派到不同内核或 DIRECT，且互不串流 |
| DIRECT 由 NGG 处理 | DIRECT 连接不经过任何内核，且**无 DIRECT 泄漏**（约束 9） |
| Worker Mode | 内核不写默认路由、不改全局路由表、DNS 劫持保持关闭 |
| 防回环 | 内核远程传输连接绕过 NGG TUN 走指定物理出口，无「出站→回 TUN」死循环 |
| 多核并行延续 | Stage 1 的多核并行能力不退化，任意多内核仍可同时被分派 |
| TUN 异常检测 | 真实删除 / 损坏 TUN 后触发 `system.alert` 并按 Fail-Closed 处置 |
| 崩溃恢复 | 真实 kill 内核进程，仅该内核恢复，已建立会话按连接状态恢复策略处理 |

### 3.4 退出门槛

统一路径在真实网络跑通、**无回环 / 无泄漏 / 无路由冲突**，且 Stage 1 验收项全部保持通过。

### 3.5 实际交付物与验收证据（真实运行）

| 交付物（源码） | 内容 |
| --- | --- |
| `netengine/types.go` | `Config` / `Metrics` / `Packet` / `SessionRec` / `StreamAdapterImpl`；7 个错误常量 |
| `netengine/ingress.go` + `udp.go` | 单监听统一入口（SOCKS5 / SOCKS4a / HTTP CONNECT / HTTP 绝对形式）+ 真实 UDP ASSOCIATE 中继 |
| `netengine/packet.go` / `session.go` | IPv4/IPv6 真实 L3 解析、逐连接会话表 |
| `netengine/dispatcher.go` / `bridge.go` | 逐连接决策、DIRECT/PROXY/CHAIN、Failover、Fail-Closed；真实拨号 + 握手 |
| `netengine/resources.go` + `egress_{linux,other}.go` | 资源预留、`AssertNoLoop`、Linux `SO_MARK 0x4E47` 绕过 TUN |
| `netengine/tun_{linux,other}.go` | Linux `/dev/net/tun` + TUNSETIFF + iproute2；非 Linux `ErrTUNUnsupported` |
| `netengine/netengine.go` + `selftest.go` | `Engine.Start/Stop/HandleTCP/tunLoop` + `answerDNSFromPacket`；真实端到端 `SelfTest` |
| `core/proxy.go` | `LocalProxyProvider` / `ResolveLocalProxy` / `IsHealthyEndpoint`（真实拨号） |
| `config/config.go` | 新增 `Config.Network` 段 |

**验收证据（`go test ./internal/netengine/ -v -count=1`，真实 socket，全 8 个测试通过）**：

| 测试 | 验证 |
| --- | --- |
| `TestPacketProcessorIPv4DNS` / `TestPacketProcessorIPv6TCP` | IPv4/IPv6 解析、DNS 分类、保护端口回环丢弃 |
| `TestRuleEngineMatchTypes` | 5 种规则类型命中、默认兜底、非法正则/CIDR 拒绝 |
| `TestSessionManagerAndResources` | 会话表、端口冲突、三类 `AssertNoLoop` |
| `TestEndToEndThroughRealSockets` | **实测** `path=NGG ingress -> mihomo -> echo latency=1.037ms bytes_out=156`，`Proxied>0` |
| `TestHTTPProxyThroughNGG` | `net/http` 经 NGG 访问真实 `httptest`，`200` + 响应体一致 |
| `TestFailClosedWhenKernelDown` | 内核不可达 `ok=false` 且 `Direct==0`（**无 DIRECT 泄漏**） |
| `TestSOCKS5DirectDial` | 真实 SOCKS5 客户端 + echo 大写回显 |

> 真实链路往返延迟约 **1.04ms**（`latency_ms=1.037`）、中继字节 **`bytes_out=156`**；`go test ./... -count=1` 全量通过。

### 3.6 能力边界（如实标注，不得夸大）

- **统一 TUN 仅 Linux**：需 `/dev/net/tun` + `CAP_NET_ADMIN` + iproute2；`AutoRoute` 默认只在该平台为真。
- **Windows / macOS 无 TUN**：`tun_other.go` 明确返回 `ErrTUNUnsupported`，**改用全平台可用的 SOCKS5 / HTTP CONNECT 入口**（真实测试已覆盖）。
- **TUN 读环回目前完成**：真实 L3 解析、IPv4 UDP/53 DNS 应答包构造（IP/UDP 校验和）、Fail-Closed 会话记录与 `Dropped++`。
- **TUN 内 TCP 流终止需要 userspace TCP stack，属于尚未实现的已知缺口**：当前此类包显式丢弃并计数，**绝不静默直连、绝不谎报已代理**。
- ❌ 不得写成「已实现统一 TUN 全局接管 / 全流量代理」；统一 TUN 全局接管仍是**未实现**。

---

## 4. Stage 3 — 统一规则引擎 / DNS / 出入口策略

> **状态：已实现** — Phase 1 时为「规划」；现规则 / DNS / 出入口策略已全部收敛到 NGG。能力边界见 §4.4。

### 4.1 目标

把**全局规则 / DNS / 路由策略全部收敛到 NGG**（约束 3），内核彻底不参与分流决策。

### 4.2 主要交付

- `RuleEngine`：NGG 自研统一规则（注意：这是 **Stage 3** 能力，Phase 1 **禁止**出现「NGG 自研规则分流界面」）；
- `DNSEngine`：NGG 统一 DNS（Phase 1 **禁止**出现「NGG 统一 DNS 引擎」宣称）；
- `RoutingEngine` / `EgressManager`：统一出入口策略、Failover；
- DNS 故障处理、连接状态恢复、防 DIRECT 泄漏的策略化。

### 4.3 验收标准（**必须真实网络环境验证**）

| 项 | 验收方式 |
| --- | --- |
| 策略归属 | 全部规则 / DNS / 路由配置存在于 NGG，内核配置中不含分流决策 |
| DNS 真实性 | 真实域名查询走 NGG DNS 引擎，故障时有明确 Fail-Closed 行为、不泄漏 |
| 出口策略 | 真实多目标连接按出入口策略调度，Failover 在真实断链下生效 |
| 防泄漏 | 规则 / DNS / 出口状态不确定时默认丢弃，真实抓包验证无直连泄漏 |
| 逐连接一致性 | 与 Stage 2 的 `SessionManager` 决策链一致，不出现规则与分派互相矛盾 |

### 4.4 实际交付物、验收证据与能力边界

| 交付物 | 内容 |
| --- | --- |
| `netengine/rules.go` | `RuleSet` / `ruleEngine`：**10 种规则类型**（`DOMAIN`、`DOMAIN-SUFFIX`、`DOMAIN-KEYWORD`、`DOMAIN-REGEX`、`IP-CIDR`、`DST-PORT`、`NETWORK`、`PROCESS-NAME`、`TENANT`、`MATCH`）、**4 种动作**（`PROXY` / `DIRECT` / `REJECT` / `DNS-LOCAL`）、优先级降序、`Validate()` 预校验、`Replace()` / `LoadRuleFile()` **Fail-Closed 全量替换**、`Rule.TenantID` 租户规则、`DefaultRules()` 4 条 |
| `netengine/dns.go` | miekg/dns：UDP + TCP 双监听、hosts / block、缓存 TTL（取 `min(CacheTTL, 各 RR TTL)`）、多上游顺序容错、`https://` DoH POST、`Resolve(ctx)`、`SetHosts/Reload` |
| `netengine/resources.go` | `egressManager.Dialer` 真实 `net.Dialer` + Linux `SO_MARK 0x4E47`、`routingEngine.Select` |
| `netengine/netengine.go` | `answerDNSFromPacket()`：IPv4/UDP/DNS 应答包，含 **IP 头校验和 + UDP 伪首部校验和** |

**验收证据**：`TestRuleEngineMatchTypes`（5 种类型命中 + 非法规则拒绝）、`TestPacketProcessorIPv4DNS`、`TestSessionManagerAndResources`；`go test ./... -count=1` 全绿。

**能力边界（如实标注）**：TUN 内 DNS 应答**仅覆盖 IPv4 UDP/53**（IPv6 查询、TCP DNS over TUN 不在覆盖范围）；`SO_MARK` 为 Linux 专有，非 Linux 用绑定本地地址并如实反映；TUN 内 TCP 流终止仍是 §3.6 所述**已知缺口**。

---

## 5. Stage 4 — 传统 VPN / 加密代理 / 新内核 / 后量子

> **状态：已实现（含能力边界 / 骨架）** — Phase 1 时为「规划」；协议中心 / 后量子 / 插件运行时已落地。能力边界见 §5.4。

### 5.1 目标

扩展协议与内核生态，验证「新增内核不要求重写 NGG Network Engine」（约束 7）。

### 5.2 主要交付

- 传统 VPN 协议中心（WireGuard / OpenVPN 类，作为**协议执行引擎**接入）；
- 新增加密代理协议；
- 通过 `configs/cores.d/` 描述文件接入更多内核（验证 Registry 扩展性）；
- 后量子密码（PQC）能力预研与接入。

### 5.3 验收标准（**必须真实网络环境验证**）

| 项 | 验收方式 |
| --- | --- |
| 扩展不改引擎 | 新增一个内核**仅**通过描述文件 + 标准 Adapter 完成，**未修改** `internal/netengine/` 代码 |
| 真实互通 | 新内核在真实网络下完成握手与数据传输，与既有内核并行运行 |
| VPN 协议 | 真实隧道建立、真实流量转发、真实断线重连 |
| 后量子 | 真实握手协商验证（非仅编译通过） |
| 兼容性 | 新增能力不破坏 Stage 1 ~ 3 的验收项 |

### 5.4 实际交付物、验收证据与能力边界

| 交付物 | 内容 |
| --- | --- |
| `protocols/protocols.go` | 16 个协议 Spec 目录（`vpn` 7 / `encrypted-proxy` 8 / `post-quantum` 1）、`Register` / `SetEnabled` / `List(category)`、`GenerateConfig()` 生成**真实内核原生配置**（wireguard→sing-box/mihomo/xray-core，openvpn→conf，8 种加密代理→yaml，mlkem-hybrid→json）、`ValidateConfig()` 按 json/yaml/conf 真实解析校验 |
| `pq/pq.go` | `crypto/mlkem` **ML-KEM-768（FIPS 203）** + `crypto/ecdh` **X25519** 混合交换 + HKDF-SHA256 + **AES-256-GCM** `SecureChannel`；`ServerHandshake` / `ClientHandshake` / `SelfTest` |
| `plugins/plugins.go` | goja（JS）+ wazero（WASM + WASI）运行时、`Manifest`（`kind/hooks/permissions`）、`Install/Enable/Remove/List`、`LoadManifestDir`、JS 沙箱（无 require / fs / network）、`RunSessionHooks` / `RunDNSHooks` |
| `engine/stage45.go` | `pluginHookAdapter` 把插件输出映射回 `RouteDecision`；`Stages()` 如实回报阶段状态 |

**验收证据（真实运行）**：`TestProtocolRegistryAndConfigGeneration`（5 组配置生成 + 格式校验通过、未知协议/畸形 JSON 被拒）、`TestHybridHandshakeSelfTest`（`net.Pipe()` 真实混合握手 + AEAD 往返）、`TestSecureChannelTamperDetection`、`TestJavaScriptPluginChangesDecision`、`TestRealWASMPlugin`（真实 `.wasm` 模块）、`TestBrokenPluginIsReported`；`POST /api/v1/pq/selftest` 暴露同一真实自检。

**能力边界 / 骨架（如实标注，不得夸大）**：
- **明确不重写内核协议栈**：协议中心只做能力目录、原生配置生成与格式校验；加密 / 握手 / 隧道仍由内核执行（见 `08-stage3-5-features.md` §3）。
- **后量子实现仅用于 NGG 节点间控制信道**（`GET /pq/status` 的 `note` 明确说明），**未集成进代理数据面协议**；❌ 不得写成「已完成后量子代理协议集成」。
- 插件 `permissions` 为**声明式元数据**；插件市场 / 远程分发为**骨架**，仅支持本地 `plugins.d` 与 REST 安装。

---

## 6. Stage 5 — SD-WAN / 企业网关 / 全平台

> **状态：已实现（含能力边界 / 骨架）** — Phase 1 时为「规划（最终愿景）」；SD-WAN / 多租户 / 跨平台共享核心已落地，原生 UI 客户端仍为骨架。能力边界见 §6.4。

### 6.1 目标

面向企业与全平台场景的产品化。

### 6.2 主要交付

- SD-WAN / 多 WAN 调度；
- 企业网关能力与企业多租户；
- 全平台客户端：Linux ARM64 / OpenWrt / Windows / macOS / Android / iOS / HarmonyOS / iPad / TV / 软路由（首发为 **Linux x86_64**）；
- 插件市场、JS / WASM 插件运行时。

### 6.3 验收标准（**必须真实网络环境验证**）

| 项 | 验收方式 |
| --- | --- |
| 多平台 | 每个目标平台在**真实设备 / 真实系统**上完成安装、启动、真实联网转发 |
| 多 WAN | 真实多出口链路下调度与 Failover 生效 |
| 多租户 | 真实租户隔离，配置 / 状态 / 流量互不越界 |
| 插件运行时 | JS / WASM 插件在真实沙箱中运行，隔离与权限边界经过验证 |

### 6.4 实际交付物、验收证据与能力边界

| 交付物 | 内容 |
| --- | --- |
| `sdwan/sdwan.go` | 真实 `net.DialTimeout("tcp", probe_target, 1500ms)` 探测、EWMA（RTT `0.7/0.3`、Loss `0.7`）、`ReportResult` 数据面反馈、**4 种策略**（`lowest-latency` / `weighted` / `priority` / `failover`）、`SelectLink` / `ForceFailover` / `Status` |
| `tenants/tenants.go` | 默认租户保护、`ResolveTenant`、`Allowed`（内核白名单 + 配额）、`Charge`、规则 `TenantID` 隔离 |
| `api/stage45.go` | 12 个 `GOOS/GOARCH` `buildTargets`、`POST /platforms/build` 真实 `go build` 交叉编译（`CGO_ENABLED=0`、逐目标 4 分钟超时、返回 `ok/output/path`） |
| `internal/mobile/mobile.go` | gomobile-bind 友好的共享 Go 核心 `Gateway`（`SystemInfo/Cores/NetworkStatus/Stages/SDWANStatus/Protocols/StartCore/StopCore/StartNetwork/StopNetwork/BatchStart/SelfTest/SetRules/ProxyURL/Connectable`），`Connectable()` 真实请求 `GET /health`，**no simulated connectivity** |

**验收证据（真实运行）**：`TestRealLinkProbingAndFailover`（两个真实 TCP 监听作 probe target，关闭其一后经真实失败拨号触发切换，断言 `switches>0`、每条链路 `Probes>0`）、`TestTenantIsolation`（配额、内核白名单、default 保护）、`internal/mobile` 包测试、`POST /platforms/build` 真实 `go build` 产物；`go test ./... -count=1` 全绿。

**能力边界 / 骨架（如实标注，不得夸大）**：
- `egressManager.Dialer` 中 `LinkSelector` 目前为占位（注释说明由 `RoutingEngine` 咨询），**选路结果尚未注入每个连接的出站拨号**；不得写成「已完成全流量多 WAN 调度」。
- `MaxSessions` / `AllowedProtocols` / `DefaultCore` / `RuleSetTag` 为**声明式字段**，`Charge()` 未接入 `HandleTCP` 主数据路径；不得写成「已完成完整企业级配额闭环」。
- **原生 UI 客户端为骨架，需各平台 SDK 打包**（`GET /platforms` 的 `note` 明确说明）：无 iOS / Android 原生安装包、无 platform VPN 配置下发；❌ 不得写成「已完成 iOS/Android 原生客户端」。

---

## 7. 最终愿景归属表（避免阶段错配）

> **Phase 1 基线**：以下能力在 Phase 1 / Stage 1 **明确禁止**提前实现，也不得在当时的文档、API、UI 中描述为「已有」。该历史基线保留记录。
>
> **当前版本（Stage 2~5 交付后）**：下表第三列已更新为**如实状态**；【已实现】项以真实测试为证据，【能力边界】【骨架】项**不计入已完成**，仍不得写成夸大表述。

| 愿景 / 能力 | 所属阶段 | 当前状态（含能力边界） |
| --- | --- | --- |
| 多平台客户端（ARM64 / OpenWrt / Windows / macOS / Android / iOS / HarmonyOS / iPad / TV / 软路由） | Stage 5 | 【骨架】纯 Go 核心 + 12 目标真实 `go build` 交叉编译 + gomobile 共享核心已实现；**原生 UI 客户端为骨架，需各平台 SDK 打包** |
| VPN 协议中心 | Stage 4 | 【已实现，能力边界】16 协议目录 + 真实内核原生配置生成与格式校验；**不重写内核协议栈** |
| 后量子密码 | Stage 4 | 【已实现，能力边界】ML-KEM-768 + X25519 + HKDF-SHA256 + AES-256-GCM 真实实现；**仅 NGG 节点间控制信道**，未集成代理数据面 |
| SD-WAN / 多 WAN | Stage 5 | 【已实现，能力边界】真实 TCP 探测 + EWMA + 4 策略 + 故障切换；选路结果尚未注入每个连接的出站拨号 |
| 企业多租户 | Stage 5 | 【已实现，能力边界】隔离 / 内核白名单 / 配额 / 默认租户保护；部分字段为声明式，配额未接入主数据路径 |
| 插件市场 | Stage 5 | 【骨架】运行时已实现；插件市场 / 远程分发未交付，仅本地 `plugins.d` + REST 安装 |
| JS / WASM 插件运行时 | Stage 5 | 【已实现】goja + wazero(WASI) + manifest / hook / JS 沙箱 |
| NGG 自研规则分流界面 | Stage 3 | 【已实现】10 种规则类型 / 4 种动作 / 优先级 / Fail-Closed 全量替换 / 租户规则 + REST 端点 |
| NGG 统一 DNS 引擎 | Stage 3 | 【已实现】miekg/dns：UDP/TCP 监听、hosts/block、缓存 TTL、多上游、DoH |
| 新增加密代理协议 | Stage 4 | 【已实现，能力边界】协议目录中登记能力；**由内核执行协议栈**，NGG 不重写 |
| 统一 TUN / 会话 / Packet Processor / Protocol Dispatcher / Core Bridge | Stage 2 | 【已实现数据平面，能力边界】userspace 统一入口全平台可用；**TUN 仅 Linux** 且需 `CAP_NET_ADMIN`；**TUN 内 TCP 流终止未实现**；不得写成「已实现统一 TUN 全局接管」 |
| 统一 TUN 全局接管（全平台全流量） | Stage 2 目标 | **【未实现】** |
| 后量子代理协议集成 | Stage 4 目标 | **【未实现】** |
| iOS / Android 原生客户端 | Stage 5 目标 | **【骨架 / 未实现】** |

---

## 8. 与 Phase 1 边界的对照（速查）

| 类别 | 内容 | 本文对应 |
| --- | --- | --- |
| 【已实现】Phase 1 基线 | 架构检测、三内核安装卸载、版本读取、多核并行启停、状态监控、配置事务与备份、原生校验、实时日志、端口冲突、升级回滚、摘要下载、重启恢复、REST+WS API + Web UI、可插拔注册表 | 第 2 节 |
| 【接口预留】Phase 1 基线 | `internal/netengine/` 12 个组件 + `Stage2NotImplemented` | 第 3 节入口（历史基线） |
| 【禁止】Phase 1 基线 | 第 7 节表格全部条目 | 第 7 节（历史基线） |
| 【已实现】Stage 2~5 | 统一入口 / Packet / Session / Dispatcher / Bridge、规则 / DNS、协议中心 / 后量子 / 插件、SD-WAN / 多租户 / 跨平台共享核心 + 40 个 REST 端点 | §3.5 / §4.4 / §5.4 / §6.4、`01-architecture.md` §12A |
| 【能力边界 / 骨架】Stage 2~5 | Linux TUN 限定、TUN 内 TCP 流终止缺口、后量子仅控制信道、不重写内核协议栈、选路未注入拨号、声明式租户字段、原生 UI 客户端骨架、插件市场骨架 | §3.6 / §4.4 / §5.4 / §6.4、第 7 节 |
| 【未实现】 | 统一 TUN 全局接管、后量子代理协议集成、iOS/Android 原生客户端 | 第 7 节 |

---

## 9. 约束覆盖关系复述（ADR-001）

> 早期初始化指令中的「**默认单主内核模式**」已被最高级约束第 4 条**明确覆盖并废止**。NGG 从 Stage 1 起即为**多核并行**架构，不存在「主内核 / 单主」概念。本路线图所有阶段均以「任意多个内核可同时运行、独立生命周期、故障互不株连」为基线，Stage 2 的统一 TUN 落地**不会**退回单主内核模式。
>
> `POST /cores/batch/start` 的 `exclusive=true` 是**用户主动发起的批量「单核独占启动」选项**（默认 `false` 为多核并行），属于一次操作的可选参数，不引入主内核概念、不强制互斥，因此不违背本覆盖关系（详见 `01-architecture.md` ADR-001）。

---

## 10. 相关文档

| 文档 | 内容 |
| --- | --- |
| `01-architecture.md` | 架构定位、10 条约束、ADR、Phase 1 边界表 |
| `02-api.md` | 完整 REST + WS API |
| `03-development.md` | 开发流程、新增内核、测试与阶段验收清单 |
| `04-installation.md` | 部署、配置、升级、卸载 |
| `05-troubleshooting.md` | 故障排查 |
| `07-stage2-network-engine.md` | Stage 2 统一网络引擎设计、TUN 能力边界、四种入口、Fail-Closed 决策表 |
| `08-stage3-5-features.md` | Stage 3~5 各能力的「已实现 / 骨架 / 能力边界」标注 |
| `09-stage2-5-api.md` | Stage 2~5 新增 40 个 REST 端点 |
