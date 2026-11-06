# v2.7.1 · 总控启用 / 内核启动可靠性 / VPS 节点绑定 / 用户订阅

## 1. 网关总控：一键「启用并接管」

**问题**：点「应用接管」只得到 `网关总控未启用（master.enabled=false），命令未执行`，因为开关改了表单但没保存。

**修复**：`POST /gateway/datapath/apply` 接受可选的 `{master}`，在同一请求里先持久化、再执行接管计划；前端点「应用接管」会自动把 `enabled` 置为 true 并提交当前表单。

- 未提权时仍然如实返回 `executed=false` + 命令原文（需要管理员 PowerShell 执行）。
- 语义清楚：**应用 = 保存 + 接管**，不再是「先保存、再点应用」两步。

## 2. 内核启动：先自检、再启动，出问题说清楚

**现场表现**：mihomo 正常，sing-box / Xray-core 一启动就崩，弹两条「kernel crashed, independent restart #1」。

**本机复现**：用同一版本二进制（sing-box 1.14.4 / Xray 26.10.31）按默认配置单启、并行批量启动**都正常**，所以问题来自运行环境或配置与端口漂移，而不是默认模板。

### 2.1 端口自动校准（最可能的真因）

Worker 模式端口由 NGG 分配并写进生成的配置。一旦配置里的端口与实际可用端口脱节——例如升级时旧内核进程没有随守护进程退出，仍占着旧端口——启动时只检查「新分配端口空闲」，进程真正 bind 时才发现旧端口被占用，于是崩溃重启。

现在启动前会：

1. 读出配置里**实际声明的端口**（mihomo 的 `mixed-port` / `external-controller`、sing-box 的 `listen_port`、Xray 的 `inbounds[].port`）；
2. 逐个检查是否被占用，或与其它内核的配置重复；
3. 有冲突时按分配器给出的端口**改写配置**，走正常的配置事务（备份 + 内核原生校验 + 原子替换），并记审计 `core.ports`；
4. 无冲突时不动配置（不会无故改用户内容）。

### 2.2 预检与快速失败

- 启动前先跑 `<binary> version`（3 秒超时）：被杀软隔离、架构不匹配、缺 DLL 会直接报「二进制无法执行」，不再以「started」收场。
- 启动后观察 3 秒：若进程立刻退出，返回带真实 stderr 的错误，并把状态置为 failed，**不进入崩溃重启循环**（不会再刷两条告警）。

### 2.3 诊断面板

`GET /cores/{id}/diagnostics` + 内核管理页「诊断」按钮：

| 检查项 | 内容 |
|---|---|
| 二进制存在 | 路径、大小、修改时间 |
| 二进制可执行 | `<binary> version` 的真实输出或错误 |
| 配置有效 | 内核原生校验结果 |
| 端口可用 | 每个端口的占用状态与占用 PID（运行中的内核自身占用视为正常） |
| 进程运行 | state 与 pid |

附带日志尾部 40 行与一句结论（hint），例如「端口 17920 被 PID 4464 占用」。

### 2.4 崩溃提示不再刷屏

- 后端把崩溃事件从 `system.alert` 改为 `core.state`（带 `state=failed` 与 `reason`）。
- 前端相同文案 8 秒内只提示一次；同一内核同一原因 30 秒内只提示一次。

## 3. 节点：把一台 VPS 真正绑定成可用节点

参考 remnawave 的「panel 管节点、节点跑内核、用户拿订阅」模型，出口/入口节点新增「部署到 VPS」：

1. 填协议（VLESS / Trojan / Shadowsocks）、端口、SNI；
2. NGG 生成凭据（VLESS 用 UUIDv4、Trojan 用随机口令、SS 用 `aes-256-gcm` + 随机密码）；
3. 生成一键 bash 脚本：安装 Xray 或 sing-box → 写入最小可用配置 → 自签证书（VLESS/Trojan）→ systemd 常驻 → 放通防火墙端口；
4. 粘到 VPS 上跑完，节点即可用；协议、端口、凭据与在线状态回显在节点列表。

脚本面向 Ubuntu 22.04/24.04 与 Debian 12，需要 root。生成结果持久化在 `<data>/provisions/<hostID>.json`（0600），不含 NGG 的会话凭据。

**已知边界**：脚本内含节点凭据（客户端要用），不要公开；自签证书需要客户端开启 `allowInsecure`；云厂商安全组要另行放通端口；脚本不安装 NGG Agent（CPU / 流量等指标需另装 agent）。

## 4. 用户管理：一条订阅链接，绑定节点 + 协议 + 规则 + DNS

每个用户可绑定「节点 / 协议 / 规则集 / DNS」，并拿到五条订阅链接：

| 客户端 | 链接参数 | 内容 |
|---|---|---|
| Clash / Mihomo | `?target=clash` | `proxies` + `proxy-groups` + `rules`（含 `rule-providers`） |
| Surge | `?target=surge` | 首行 `#!MANAGED-CONFIG`，`[Proxy]` / `[Proxy Group]` / `[Rule]` |
| sing-box | `?target=singbox` | 1.10+ JSON，含 `dns` / `route.rule_set` |
| v2rayN | `?target=v2ray` | 分享链接整体 base64 |
| SOCKS5 | `?target=socks` | `host:port:user:pass` |

- 节点凭据优先取该节点的 provision（部署脚本生成的那一份），否则退回节点记录的地址 / 端口 / 协议 / 密钥；缺字段时返回 422 并说明缺什么，不编造。
- DNS 三档：`off` 不写 DNS、`system` 跟随系统、`gateway` 用网关 DNS。
- 订阅走公网监听 `/sub/u/<token>`，未知 token / 禁用 / 过期 / 渲染失败统一 404（不可枚举）；轮换后旧链接立即失效。
- 响应头带 `subscription-userinfo` 与 `profile-update-interval: 24`。

**已知边界**：Surge 不支持 VLESS（仍生成该行但会警告，建议绑 Trojan/SS）；规则集的下载地址当前是占位 URL，导入前需替换或移除；v2ray 链接无法承载 SOCKS5/HTTP 与 DNS。

## 5. 升级说明

- 新增数据文件：`<data>/provisions/*.json`、`<data>/subscription-tokens.json`（0600 原子写）。
- `users.json` 形状不变；订阅 token 侧载存储，列表与详情不会带出 token。
- 内核配置若因端口冲突被自动改写，旧文件在配置备份里可回滚。
