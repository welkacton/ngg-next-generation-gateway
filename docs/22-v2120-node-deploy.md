# v2.12.0 · 节点部署：控制面 → 节点（Remnawave 的职责拆分 + 哪吒的探针通道）

## 1. 参考架构与差异

Remnawave 把「面板」和「节点」拆成两个进程：面板生成 Xray 配置并下发，节点只跑内核、回报系统信息与流量；
节点主动去面板拉配置、推状态。

NGG 沿用同一套职责拆分，但把**信息通道**换成了[哪吒探针](https://github.com/nezhahq/nezha)的模式：
节点上跑一个只做出站连接的探针（`ngg-probe`），每 10 秒把真实指标 + 节点状态 POST 到面板的
`/v1/agent/report`。面板**从不连进节点**——不需要入站管理端口，不需要 SSH，节点在 NAT 后面也能上报。

| 能力 | Remnawave | 哪吒 | NGG v2.12.0 |
| --- | --- | --- | --- |
| 面板 → 节点 | HTTPS :2222 + `SECRET_KEY` | 面板不连入 | **面板不连入**（部署脚本可选经已保存的 SSH 推一次，仅执行不读状态） |
| 节点 → 面板 | 拉配置 + 推状态（同一通道） | gRPC 长连接上报 | HTTP POST `/v1/agent/report`，头 `X-NGG-Agent-Token` |
| 节点身份 | 容器 env 里的 `SECRET_KEY` | 面板签发的 secret | `/etc/ngg/node.json`（身份 / 内核路径）+ `/etc/ngg/agent.conf`（探针编号与令牌，0600） |
| 身份校验 | 全量比对 | secret 比对 | 面板只存令牌的 SHA-256，上报时恒定时间比对 |
| 配置下发 | 面板推配置并调 node API 重启 | — | 面板渲染脚本 → 节点执行 → 内核自校验 → systemd 重启 |
| 节点状态 | node API 返回 system / stats | 探针上报 | 探针上报：CPU / 内存 / 磁盘 / 网卡 / 累计流量 + 单元状态 / 端口监听 / 配置指纹 |
| 失败处理 | 面板标记离线 | 面板标记离线 | 3× 上报间隔没收到 → `offline`；从未收到 → `never`（不编造状态） |

这样做的直接好处：**面板不需要公网入口来读节点，节点也不需要额外开放管理端口**；
SSH 从「状态采集通道」退化为「可选的一次性执行通道」。

## 2. 部署脚本：12 步，每步都有真实输出

`POST /api/v1/hosts/{id}/deploy` 渲染一个自包含 bash 脚本（约 33 KB），随后可选地**在节点上真正执行**。
每一步都向 stdout 打一行机器可读标记，面板据此渲染进度，不做任何模拟：

```
NGG_STEP <n> <标题>    步骤开始
NGG_OK <n>             步骤完成
NGG_FAIL <n> <原因>    中止（退出码 1）
NGG_NOTE <文本>        需要知道但不是失败（如「端口由 ngg-node 自己占用，将重启」）
NGG_NODE_READY k=v     最后一行：节点已就绪（含 probe=up|down|off）
```

| 步骤 | 做什么 | 关键取舍 |
| --- | --- | --- |
| 1 预检 | root / systemd / 架构 / 端口占用 / 磁盘 | 端口被别人占用时**直接中止**并给出占用进程；由 `ngg-node.service` 自己占用时只提示并重启 |
| 2 依赖 | 只装缺少的 curl / openssl / unzip / ca-certificates | 依赖齐全时**跳过 apt**，1 核小机器上省 20–40 秒 |
| 3 内核 | 有可用 Xray 就复用，否则只下载二进制到 `/usr/local/bin/ngg-xray` | 不跑官方 `install-release.sh`，避免它创建/覆盖 `xray.service` 和 `/usr/local/etc/xray` |
| 4 清单 | 写 `/etc/ngg/node.json`（含运行时发现的内核路径） | 0600；探针靠它知道自己是哪个节点、跑什么内核 |
| 5 凭据 | REALITY 用 `xray x25519 -i` **反推公钥比对**；TLS 生成自签证书 | 密钥编码若与内核不一致，这里就会失败，而不是等客户端连不上才发现 |
| 6 配置 | 写 `/etc/ngg/xray/config.json` | 私有网段用字面 CIDR 黑洞，不依赖 `geoip.dat` |
| 7 校验 | `xray run -test` | 失败即中止，不留半启动状态 |
| 8 启动 | 注册 `ngg-node.service` 并等待 active | 独立单元，不动机器上任何已有服务 |
| 9 放通 | ufw（仅已启用时）/ firewalld / iptables | iptables 规则不持久化，脚本如实提示；云安全组需人工放通 |
| 10 探针 | 写 `/etc/ngg/agent.conf`（0600）+ 装 `ngg-probe` + `ngg-probe.service` | 装完立刻 `curl /v1/agent/ping` 验证可达；不可达只记 NOTE，探针按 1s→60s 退避重试 |
| 11 CLI | 安装 `/usr/local/bin/ngg-node` | params / status / probe / logs / restart / uninstall（`probe` 看上报日志） |
| 12 自检 | 端口监听 + 单元状态 + 配置指纹 + 上报状态 | 前三项不过即中止；上报状态写进 `NGG_NODE_READY` 的 `probe=` |

脚本的立场：**只做这一件事**。不修改 `/usr/local/etc/xray`、不重启别人的单元、不改 sshd、
不装 docker、不改防火墙默认策略。重复执行是幂等的（复用同一套凭据覆盖配置）。

面板上报地址留空时的行为是明确的：**停掉旧探针**（`systemctl disable --now ngg-probe`）并记 NOTE，
不会留下一个继续往旧地址推数据的进程。

## 3. 节点探针（`ngg-probe`）

| 项 | 说明 |
| --- | --- |
| 形态 | POSIX sh 脚本（约 240 行），`/usr/local/bin/ngg-probe`，`ngg-probe.service` 守护 |
| 配置 | `/etc/ngg/agent.conf`（0600）：`PANEL_URL` / `AGENT_ID` / `AGENT_TOKEN` / `INTERVAL` |
| 采集 | `/proc/stat`（CPU 增量）、`/proc/meminfo`、`/proc/net/dev`、`/proc/uptime`、`/proc/loadavg`、`df -PkT /`、`/sys/class/net/*` |
| 节点段 | `/etc/ngg/node.json` + `sha256sum` 配置 + `systemctl is-active` + 端口监听（`ss`，无 `ss` 时读 `/proc/net/tcp`） |
| 上报 | `POST {PANEL_URL}/v1/agent/report`，JSON 体 ≤ 2 MiB；面板按 agent 记录落盘 |
| 退避 | 成功按 `INTERVAL`（10 s）；失败 1→2→4…→60 s；401 单独提示「令牌已轮换，请重新部署」 |
| 手工调试 | `NGG_PROBE_ONCE=1 /usr/local/bin/ngg-probe`（单次上报，退出码即结果） |

为什么用 shell 而不是第二个 Go 二进制：节点侧只需要 curl（第 2 步已装），不必为它再发一个
Linux 资产（本仓只发布 `.exe`），也不必让节点能访问 GitHub。数据源与 Go agent 在 Linux 上用的是同一批
`/proc` 文件，同样是实测值而不是记忆值。

## 4. 三种传输 + 档案模式

| mode | 说明 | 客户端要求 |
| --- | --- | --- |
| `reality`（默认） | VLESS + REALITY，SNI 默认 `www.microsoft.com` | 填 pbk / sid / sni + `fp=chrome`，**不需要** allowInsecure |
| `vless-tls` | VLESS + 自签证书 | 需 allowInsecure |
| `trojan-tls` | Trojan + 自签证书 | 需 allowInsecure |
| `profile` | 把「协议配置」档案内容**原样**写入，内核必须是 xray-core | 由档案决定 |

`profile` 模式只接受 xray-core：mihomo / sing-box 档案仍走「生成部署脚本」手动执行，
API 会明确返回 422 而不是悄悄换内核。

## 5. API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/hosts/{id}/deploy` | 已保存的方案 + 最近一次运行（404 = 还没生成过） |
| POST | `/api/v1/hosts/{id}/deploy` | 生成方案（含探针身份）；`execute=true` 时立即在节点执行（202 + job） |
| GET | `/api/v1/hosts/{id}/deploy/script` | 下载脚本（`text/x-shellscript`，含节点凭据） |
| GET | `/api/v1/hosts/{id}/deploy/job` | 轮询执行进度（实时步骤 / 备注 / 失败原文） |
| GET | `/api/v1/hosts/{id}/node/status` | **读探针上报**（不再 SSH）：`status` / `last_seen` / `metrics` / `node` / `warning` |

请求体：

```json
{ "mode": "reality", "port": 3372, "sni": "", "profile_id": "", "panel_url": "http://203.0.113.5:9191", "rotate": false, "execute": true }
```

- `panel_url` 是**指针语义**：字段缺省＝沿用已保存的地址；显式传 `""`＝方案不再上报（并停掉旧探针）。
  未传时后端回落到「远程节点 → 服务设置」的 `public_url`。
- 生成方案时面板会给该节点分配一个探针身份（`agent_id` + 令牌），同一条记录也会出现在
  「远程节点」列表里 —— 一套身份，两个视图。
- `rotate=false`（默认）时复用已保存的 UUID / REALITY 密钥 / 探针令牌；`rotate=true` 才轮换，
  轮换后节点需要重新部署才会恢复上报（令牌只在生成时下发一次）。
- 执行是**后台任务**：HTTP 立刻返回 202，前端每 1.5 秒轮询 job，避免安装内核耗时超过客户端超时。

`node/status` 的响应字段（都是实测值，没有则留空而不是填 0）：

| 字段 | 含义 |
| --- | --- |
| `installed` | 面板有该节点的部署方案 |
| `verified` | 探针曾用面板签发的令牌成功上报过（等价于「节点确实持有这个令牌」） |
| `online` / `status` | `online` / `offline` / `never`，按 3× 上报间隔判定 |
| `metrics` | CPU / 内存 / 磁盘 / 网卡 / 累计流量 / 运行时长 / `sampled_at` / `source=ngg-probe` |
| `node` | 节点自报：`unit_state` / `listening` / `config_sha256` / `kernel_version` / `collected_at` |
| `warning` | 一句话说明「为什么现在看不到状态」 |

## 6. 与已有服务共存（实测）

目标 VPS 上本来就跑着一整套 Remnawave（`remnawave` / `remnawave-db` / `remnawave-redis` /
`remnanode` / caddy），443 与 80 被 caddy 占用，2222 是 Remnawave 的节点端口。NGG 的部署：

- 用 3372（节点登记的端口），**不碰 443**；
- 单元名 `ngg-node.service` / `ngg-probe.service`、目录 `/etc/ngg`，与 `/usr/local/etc/xray` 完全隔离；
- 部署前后 `docker ps` 全部 healthy，caddy 仍在 443。

## 7. 真实验证（2026-11-07，50.118.185.194 · Xray 26.10.31）

| 项 | 结果 |
| --- | --- |
| 部署耗时 | 首次 60 s 级（下载内核），复用内核后 **4.9 s** |
| 步骤日志 | **12/12 `done`**，`NGG_NODE_READY … probe=up` |
| systemd | `ngg-node.service` + `ngg-probe.service` 均 active + enabled，节点监听 `*:3372` |
| 内核 | `Xray 26.10.31`，`xray run -test` 通过，配置指纹 `688c7839…` |
| 上报 | 探针每 10 s 上报，`verified=true`、`status=online`，`source=ngg-probe` |
| 上报内容 | CPU 7.8%（1 核，load 0.32）/ 内存 1.16 GiB · 1.96 GiB / 磁盘 7.67 GiB · 19.68 GiB / 累计 ↓90.3 GB ↑84.5 GB / uptime 12 天 |
| 节点段 | `unit_state=active`、`listening=true`、`kernel_version=Xray 26.10.31`、`config_sha256=688c7839…` |
| 断连判定 | 停掉探针后 30 s 内 `online` → `offline`（`last_seen` 保持不变，不伪造时间） |
| 恢复 | 重新部署后自动回到 `online`，`last_seen` 继续前进 |
| 模式切换 | `vless-tls` + 3373 部署成功（自签证书、换端口、单元重启），再切回 `reality` + 3372 |

端到端测试时面板在 NAT 后面（198.64.67.2:9191 从节点不可达），因此用一条 SSH 反向隧道把
面板的 9191 映射到节点 loopback，让「节点出站 → 面板」这条链路真正跑通。隧道只存在于测试过程，
产品本身不使用它：生产环境把 `public_url` 配成节点能访问到的地址即可。

## 8. 安全

- 脚本包含**节点自己的凭据**（UUID / REALITY 私钥 / 探针令牌），因此：不入仓库、脚本下载接口要求登录、
  记录文件 0600、前端只拿 `PublicSpec()`（私钥与令牌被剥离）。
- 面板凭据从不进入脚本；SSH 密码仍只存在于本机 `data/host-monitor.json`（0600），不回显、不入日志。
- 探针令牌在面板侧只存 SHA-256，上报时恒定时间比对；`401` 只回一句「token 无效」，
  无法用来枚举 agent id。
- 探针只做出站连接，节点上不新增任何监听端口（`ngg-probe.service` 不需要 `ReadWritePaths` 之外的特权，
  以 `NoNewPrivileges` + `ProtectSystem=full` 运行）。
- `rotate=true` 才轮换凭据，默认不轮换，避免误操作让线上客户端全部失效。

## 9. 已知边界

- **节点必须能访问面板上报地址**：这是探针模式的前提。面板在 NAT / 无公网入口时，
  需要填一个节点可达的地址（反代、隧道或公网 IP），否则节点状态只会显示「从未上报」。
- 在线部署（`execute=true`）仍依赖已保存的 SSH 凭据；没配 SSH 的节点用「生成部署脚本」手动执行。
  注意这只是**执行**通道，读状态永远走探针。
- 只支持 Ubuntu / Debian + systemd；其他发行版脚本会在预检阶段明确拒绝。
- 端口放通只处理本机防火墙，云安全组需要人工放通（脚本会提示具体端口）。
- 探针用 shell 采集 `/proc`，网卡 `addresses` 依赖 `ip` 命令（缺失时为空数组，其余字段不受影响）。
- REALITY 未启用 `xtls-rprx-vision`（`flow`），换取客户端参数更简单；需要 Vision 时改配置后
  `ngg-node restart` 即可，脚本不做隐式改写。
