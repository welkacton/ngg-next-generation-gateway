# v2.7.0 · 远程出口节点 / 网关总控 / 客户端接入

三个新能力，都是「真实可用 + 诚实边界」：能采到的数据来自真实采集，采不到就显示不可用，不伪造。

## 1. 出口节点：远程代理节点（Agent 插件）

原来「出口节点」只能登记一台机器的地址与端口，看不到机器的真实负载。v2.7.0 增加第二种节点：**远程代理节点**。

### 流程

1. 出口节点页 →「远程代理节点」→「+ 添加远程节点」，填名称 / IP / 管理端口 / 角色。
2. 创建后一次性返回 **token**、`agent.yaml` 与三平台安装脚本（Windows / Linux / Docker）。
3. 在目标机器上执行脚本，插件启动后每 10 秒把系统指标推给 NGG。
4. 页面显示在线状态、CPU、内存、磁盘、网卡、上下行速率、运行时长；详情抽屉里有最近 60 个采样与网卡 / 磁盘明细。

### 插件采集字段

| 分类 | 字段 | 来源 |
|---|---|---|
| CPU | 使用率、核数、load1 | gopsutil `cpu.Percent` / `cpu.Counts` / `load.Avg` |
| 内存 | 总量、已用、swap 总量 / 已用 | `mem.VirtualMemory` / `mem.SwapMemory` |
| 磁盘 | 各挂载点容量 / 已用 | `disk.Partitions` + `disk.Usage` |
| 网卡 | 名称、MAC、MTU、up、地址、累计收 / 发 | `net.Interfaces` + `net.IOCounters` |
| 流量 | 收 / 发速率 | 相邻两次采样的差值 ÷ 间隔 |
| 其它 | 主机名、OS、架构、运行时长、插件版本 | `host.Info` / `host.Uptime` |

### 安全

- 每台节点一个独立 token，只存 SHA-256，比较用常数时间。
- token 只在创建 / 轮换时返回一次，界面提示「只显示一次」。
- 插件日志不打印 token；`agent.yaml` 写盘权限 0600（Windows 用 ACL 限制目录）。
- 公网监听（默认 `0.0.0.0:9191`）只暴露三个路径：上报、ping、订阅；控制面 API 仍然只监听回环。

### 端口与网络

- `9191` 是**节点管理端口**，需要目标机器能出站访问 NGG（插件主动上报，适合 NAT 后的家用电脑）。
- 若 NGG 不在公网，需要在路由器上把该端口映射到 NGG 主机，或使用内网穿透。

## 2. 链式代理（出口节点多跳）

出口节点可以配置最多 8 跳 SOCKS5 / HTTP 上游，按顺序串联：

```
NGG → SOCKS5 A → HTTP B → 目标站点
```

- 编辑入口：主机节点表单里的「链式代理」区块。
- 持久化在节点记录里，密码不回显。
- mihomo 渲染时用 `dialer-proxy` 串联；sing-box / Xray 骨架暂不含链式代理（渲染结果里会写明）。

## 3. 网关总控：虚拟网卡 + 智能分流

### 目标架构

```
应用流量
   ↓
NGG 虚拟网卡（接口优先级 0，最高）
   ↓
NGG 分流决策（域名 / 网段 / 规则）
   ├── 本地直出 → 物理网卡 / WAN（route=local）
   └── 国际代理 → 链路编排 → 出口节点 → 目标
```

### 配置项

| 项 | 说明 | 默认 |
|---|---|---|
| `enabled` | 启用总控 | false |
| `adapter_name` | 虚拟网卡名 | `NGG` |
| `adapter_metric` | 接口优先级（越小越优先） | `0` |
| `capture_all` | 接管全部流量 | true |
| `auto_start` | 开机自动接管 | false |
| `bypass_private` | 局域网 / 保留地址直连 | true |
| `split.mode` | `smart` / `global-proxy` / `direct` | `smart` |
| `split.default` | 未命中规则的默认去向 | `local` |
| `split.proxy_domains` 等 4 个列表 | 域名与网段分流清单 | 空 |
| `uplink.mode` | `gateway-wan` / `pipeline` | `gateway-wan` |
| `uplink.wan_id` | 绑定哪个 WAN 口 | `wan1` |
| `uplink.pipeline_id` | 绑定哪条链路编排 | 空（默认链路） |

### 数据面

- 「应用接管」会生成并尽量执行：
  - Windows：`netsh interface ipv4 set interface "<NGG>" metric=0`、`netsh interface ipv4 add route 0.0.0.0/0 interface=<NGG> nexthop=<tunIP> metric=0 store=active`
  - Linux：`ip link set dev NGG up`、`ip route replace default dev NGG metric 0`
- **只有 Windows 且进程提权（管理员 / LocalSystem）时才真正执行**；其它情况返回 `executed=false` 与原样命令，界面用折叠块展示，让你自己决定是否执行。
- 状态卡用真实数据：网卡是否存在、接口 metric（读不到显示「—」）、默认路由是否经该网卡。

### 分流规则

「写入规则引擎」把四个清单转成 NGG 规则（`DOMAIN-SUFFIX` / `IP-CIDR` → `PROXY` / `DIRECT`），标记 `tag=gateway-master`，重复写入幂等。写入后需要内核重载才生效。

## 4. 客户端接入：iPhone / Windows / Mac 连回家

### 流程

1. 网关中心 →「客户端接入」→ 设置对外地址与端口、协议（SOCKS5 / HTTP / WireGuard）。
2. 「+ 新增客户端」选择平台，生成一次性凭据。
3. 「配置」抽屉选择格式，复制或下载：
   - **sing-box**：iOS（sing-box App）、Android、Windows、macOS 通用。
   - **Mihomo**：Clash Verge / ClashX 等。
   - **WireGuard**：iOS / macOS / Windows 官方客户端（需 NGG 侧同时运行 WireGuard 入站）。
   - **SOCKS5**：手动参数。
4. 订阅地址 `http://<host>:9191/sub/<token>` 可直接导入支持订阅的客户端。

### 边界

- 生成的是**客户端配置**，服务端仍需 NGG 的 ingress（SOCKS5/HTTP）或 WireGuard 入站真实监听并放通端口，否则配置正确但连不上。
- WireGuard 配置里的**服务端公钥目前是占位值**：NGG 还没有读取本机 WireGuard 入站公钥的接口，需要手动替换成真实公钥才能建立隧道。
- 未设置对外地址时，配置生成会明确报错，不会编造域名。
- 订阅走公网监听端口，建议只在内网或已有 TLS 终结时使用；不要把它直接暴露在无防护的公网。

## 5. 升级与兼容

- 旧 `gateway-config.json`、`hosts.json` 自动兼容（缺字段补默认，`chain` 为 null 视为直连）。
- 新增数据文件：`agents.json`、`agent-server.json`、`clients.json`，全部 0600 + 原子写。
- 相关端点见 [`02-api.md`](02-api.md)，版本节奏见 [`GENERATIONS.md`](GENERATIONS.md)。
