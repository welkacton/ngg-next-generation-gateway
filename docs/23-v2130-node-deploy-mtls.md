# v2.13.0 · 节点部署：Remnawave 原生模型（面板 ↔ 节点，双向 TLS，零 SSH）

> 这一版把 v2.11.0 的「SSH 只读采集」和 v2.12.0 的「哪吒式探针上报」**整条链路删除**，
> 按 [Remnawave](https://github.com/remnawave) 的 `remnawave/node` 契约重新实现：
> **面板主动连入节点**，用双向 TLS 下发配置、启停内核、读取节点自报的真实指标。
> 本文是当前唯一有效的节点部署说明；`docs/21-v2110-node-monitor.md` 与
> `docs/22-v2120-node-deploy.md` 描述的是已移除的实现。

---

## 1. 为什么改成 Remnawave 的形状

Remnawave 把「面板」和「节点」拆成两个进程：面板持有全部配置，节点只跑内核并暴露一个
**HTTPS 管理接口**。上游源码里这个接口的关键事实是：

- `remnawave/node` 在 `NODE_PORT`（默认 **2222**）起 HTTPS 服务，
  `requestCert: true`、`rejectUnauthorized: true`、`minVersion: TLSv1.3` —— 真正的门锁是
  **客户端证书**（mTLS），不是共享的 `SECRET_KEY` / `INTERNAL_REST_TOKEN`。
- 路由表（`libs/contract/api/routes.ts`，`ROOT = '/node'`）：
  `GET /node/xray/healthcheck`、`POST /node/xray/start`、`POST /node/xray/stop`、
  `GET /node/stats/get-system-stats`。
- 所有响应统一包一层 `{"response": …}`。
- `StartXrayCommand.Request = { internals: { forceRestart, … }, xrayConfig: Record<string, unknown> }`；
  响应含 `isStarted / version / error / nodeInformation / system`。
- `GET /stats` 返回 `{ info: { arch, cpus, cpuModel, memoryTotal, hostname, … },
  stats: { memoryFree, memoryUsed, uptime, loadAvg, interface } }`。

NGG 保留这套**路由、动词、语义、响应包装**，只把运行形态换掉：

| 维度 | Remnawave | NGG v2.13.0 |
| --- | --- | --- |
| 节点侧进程 | Docker 容器 `remnanode` | 单个静态 Go 二进制 `ngg-node`（无 Docker） |
| 传输 | mTLS HTTPS :2222，TLS 1.3 | mTLS HTTPS `NODE_PORT`（默认 2222），TLS 1.2+ |
| 信任根 | 外部 PKI / 容器注入证书 | 面板自签 ECDSA P-256 CA（`data/nodepki`），按节点签发 |
| 节点身份 | 容器环境 | 服务端证书 `CN = 节点 ID`，面板把对端 CN 钉死 |
| 内核 | 同一容器内管理 Xray | 独立 `ngg-core.service`，由 `ngg-node` 通过 systemctl 重启 |
| 配置下发 | 面板 POST `/node/xray/start` | 同左；先 `-test` 再原子替换，失败不碰线上配置 |
| 指标 | `/node/stats/get-system-stats` | 同左（gopsutil 实测 + 1 s 网卡速率采样） |
| 面板 → 节点 | 需要节点管理端口可达 | 同左；**节点不需要能访问面板** |
| SSH | 不需要 | **完全没有**（代码里也没有 SSH 客户端） |

为什么不复用共享 token：节点管理端口一旦开在公网，任何持有同一 token 的人都能推配置、启停内核。
证书让「每个节点只认自己的身份」，也让面板能发现「这个地址后面不是我以为的那台节点」。

## 2. 信任模型（`internal/nodepki`）

面板第一次启动时在 `data/nodepki/`（目录 0700、文件 0600）建立 CA，之后**只读**：

```
ca.crt / ca.key        ECDSA P-256 CA，10 年；CA 半缺失是硬错误，绝不重建（重建会让所有已部署节点失效）
panel.crt / panel.key  CN=ngg-panel，只含 ClientAuth
<node>.crt / <node>.key  按节点签发，CN=节点 ID，SAN=节点地址 + 127.0.0.1 + ::1，5 年，Server+ClientAuth
```

- **节点侧**：`tls.RequireAndVerifyClientCert` + `VerifyConnection` 里比对客户端证书 CN，
  只接受 `ngg-panel` 或它自己的节点 ID。同一 CA 下的**兄弟节点不能驱动另一台节点**。
- **面板侧**：除验证 CA 链与主机名外，还把服务端证书的 CN 钉死到期望的节点 ID；
  不一致时报「确认该地址指向的是这台节点」，而不是把配置推给别的机器。
- 回环 SAN 是必需的：部署脚本第 13 步用 `https://127.0.0.1:<NODE_PORT>/…` 自检刚装好的服务，
  证书里没有 `127.0.0.1` 会在这一步失败。
- 节点证书/私钥只以两种形式存在：生成脚本时的一次性内存，以及**渲染好的脚本本身**。
  `Spec` 里这三个字段标了 `json:"-"`，不会重复落进记录；记录文件
  `data/nodedeploy/<id>.json`（0600）保存方案元数据 + 脚本，脚本下载接口要求登录且不在列表视图回显。
  面板连入节点只需要 **CA + 面板客户端证书**（都在 `nodepki`），因此重启面板后旧方案仍可连接。
  旧 v2.12 的方案没有 `node_port`，面板会明确提示「旧版本（SSH / 探针）生成的」并要求重新生成方案。

## 3. 节点 API 契约（`internal/nodeapi`）

面板与节点共用一份 Go 类型定义（`internal/nodeapi/types.go`），客户端在 `client.go`：

| 方法 | 路径 | 请求 | 响应（去掉 `response` 外壳） |
| --- | --- | --- | --- |
| GET | `/node/xray/healthcheck` | — | `isAlive / xrayInternalStatusCached / xrayVersion / nodeVersion` + `nodeId / coreActive / uptimeSec / configSha256 / checkedAt` |
| POST | `/node/xray/start` | `{"internals":{"forceRestart":true},"xrayConfig":{…}}` | `isStarted / version / error / system` |
| POST | `/node/xray/stop` | — | 停止结果 |
| GET | `/node/stats/get-system-stats` | — | `{info, stats}` + NGG 扩展 `memoryTotal / cpuPercent / diskTotal / diskUsed / diskMount / interfaces / sampledAt` |

客户端按超时分级（health 8 s / stats 10 s / start 45 s / stop 20 s），响应体上限 1 MiB；
错误按 `unreachable | tls | auth | node | protocol` 归类并翻译成中文可操作提示
（连不上 / 证书不匹配 / 未授权 / 节点执行失败 / 响应格式不对）。

`POST /node/xray/start` 的处理顺序（`internal/nodesvc/service.go`）：

1. 把 `xrayConfig` 写到 `config.json.tmp`；
2. `xray run -test -c <tmp>`；失败 → 删 tmp，返回 `isStarted=false` + 校验输出，**线上配置不变**；
3. 校验通过 → 备份旧配置为 `.bak`，`rename` 原子提交；
4. `systemctl restart ngg-core.service`，轮询 `is-active` 最多 20 s，失败时附上 `journalctl -u … -n 40` 尾部；
5. `!forceRestart` 且配置哈希与运行哈希一致且内核 active → **跳过重启**（轮询不会打断在线连接）。

> 这是两个 systemd 单元的唯一理由：重启 `ngg-core` 时，`ngg-node` 自己还活着，能把结果报回面板。
> 如果 API 和内核在同一个进程/单元里，一次 restart 就会让面板看到连接断开而不是「启动失败原因」。

指标采集（`internal/nodesvc/stats.go`）用 gopsutil 读真实 CPU / 内存 / 磁盘 / 负载 / 运行时长 / 网卡计数；
后台每 1 秒采样一次算每网卡 rx/tx 速率，避免首次请求一律回 `0 B/s`；标题网卡取当前最忙的一张。
所有值来自节点自己，面板不估算、不填充 0。

## 4. 部署脚本：13 步（`internal/nodedeploy`）

`POST /api/v1/hosts/{id}/deploy` 每次都**签发一张新证书**并渲染一份自包含 bash 脚本
（约 27 KB / 637 行），含该节点的证书、私钥、CA 与内核配置。所有替换值都经过白名单校验
（节点名禁 `"` `'` `$` 反引号 `\` 换行，地址只允许 `[A-Za-z0-9._:-]`，摘要只允许 64 位 hex，
PEM 内容检查 heredoc 分隔符冲突），避免把用户输入拼成可执行代码。

| 步骤 | 做什么 | 关键取舍 |
| --- | --- | --- |
| 1 预检 | root / systemd / 架构 / 发行版 / 磁盘 / **两个端口**占用 | 数据端口与管理端口不能相同；被别的进程占用即中止并给出占用者 |
| 2 依赖 | 只装缺的 curl / openssl / unzip / ca-certificates / **iproute2** | `ss` 用于端口检查；依赖齐全时跳过 apt |
| 3 内核 | 复用已有 Xray，否则下载到 `/usr/local/bin/ngg-xray` | 不跑官方 `install-release.sh`，不碰 `/usr/local/etc/xray` 与别人的 `xray.service` |
| 4 清单 | `/etc/ngg/node.json` | 节点 ID / 内核路径等运行信息 |
| 5 凭据 | REALITY 用 `xray x25519 -i` 反推公钥比对；TLS 生成自签证书 | 密钥编码不一致在这里就失败 |
| 6 配置 | `/etc/ngg/xray/config.json` | 私有网段用字面 CIDR，不依赖 `geoip.dat` |
| 7 校验 | `xray run -test` | 失败即中止 |
| 8 节点服务 | 下载 `ngg-node-linux-<arch>` 并 **SHA256 强校验** | 摘要不匹配直接丢弃、不执行；开发构建没有摘要时只按 HTTPS 校验并明确写 NOTE |
| 9 迁移 + 证书 | 清掉 v2.12 的旧 `ngg-node.service` 与 `ngg-probe`，写 `ca.crt` / `node.crt` / `node.key`（0600）+ `ngg-node.service` | 旧单元判定严格匹配 `ExecStart=/usr/local/bin/(ngg-)?xray run`，不会误杀同名新单元 |
| 10 内核单元 | 写 `ngg-core.service`，先起 API 再起内核 | `wait_active` 两者都 active 才算过 |
| 11 放通 | ufw / firewalld / iptables 放通两个端口 | 云安全组需人工放通；**管理端口建议只对面板 IP 开放** |
| 12 CLI | `/usr/local/bin/nggctl` | `status / params / api / logs / core-logs / restart / uninstall` |
| 13 双向自检 | 带客户端证书 `curl --cert --key --cacert https://127.0.0.1:$NODE_PORT/node/xray/healthcheck` 必须返回 `"isAlive":true`；**不带客户端证书必须失败** | 只测「能通」会漏掉证书根本没生效的情况 |

成功后最后一行：`NGG_NODE_READY node=<id> port=<数据端口> node_port=<管理端口> mode=<…> listen=<…> api=<…> config_sha=<…>`。

脚本的立场不变：**只做这一件事**。不改 sshd、不装 Docker、不改防火墙默认策略、不重启别人的单元。

## 5. 面板侧 API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/hosts/{id}/deploy` | 已保存方案 + 最近一次连接结果（404 = 没生成过） |
| `POST` | `/api/v1/hosts/{id}/deploy` | 生成 / 重新生成方案（签发新证书）；`{mode,port,node_port,sni,profile_id,rotate}` |
| `DELETE` | `/api/v1/hosts/{id}/deploy` | 丢弃方案 |
| `GET` | `/api/v1/hosts/{id}/deploy/script` | 下载脚本（`text/x-shellscript`，含节点私钥，需登录） |
| `POST` | `/api/v1/hosts/{id}/node/connect` | 连入节点：health → 可选下发配置 → stats，结果落盘 |
| `GET` | `/api/v1/hosts/{id}/node/status` | 读缓存 + 最近一次连接结果：`node_url / online / metrics / health / warning` |
| `GET` | `/api/v1/hosts/node-metrics` | 全部已部署节点的指标快照 `{nodes,interval_sec}` |

- `connect` 的失败会标在真实阶段上（`health / config / start / stats`），不会笼统说「部署失败」。
- 后台每 **30 秒** 轮询一次所有已部署节点（8 s 超时、6 并发），**轮询从不下发配置**：
  30 秒定时器不能把线上内核反复重启。
- `node/status` 如实区分「在线 / 离线 / 从未连上」，缓存里没有数据时不编造指标。

已移除的端点（不再存在）：`GET/PUT/DELETE /hosts/{id}/monitor*`、`GET /hosts/{id}/deploy/job`、
`POST …/deploy` 的 `execute=true` 参数与 `panel_url` 字段。

## 6. 界面流程（节点管理 → 部署 / 连接）

1. **新增节点**：填地址 / 端口 / 角色，保存后做一次真实 TCP 探测。
2. **部署 / 连接**：四段式弹窗 —— ① 部署方案（配置文件、数据端口、管理端口）→
   ② 在服务器上执行（复制 / 下载脚本）→ ③ 连接节点并下发配置 → ④ 节点信息。
3. 脚本在服务器上以 root 执行一次；**面板不会自己去连 SSH**。
4. 点「连接节点并下发配置」后，卡片与弹窗显示节点实测 CPU / 内存 / 磁盘 / 累计流量 / 实时速率 /
   运行时长 / 节点版本 / 内核版本 / `ngg-core` 状态；连不上时显示具体错误而不是 0。

## 7. 发布与完整性

发布流水线（`.github/workflows/release.yml`）分两段：

1. `node-binaries`（ubuntu）交叉编译 `./cmd/ngg-node` → `ngg-node-linux-amd64` /
   `ngg-node-linux-arm64`，算出 SHA256 作为 job outputs；
2. `build`（windows）**先等上一步**，把两个摘要通过
   `-ldflags "-X github.com/ngg-project/ngg/internal/nodedeploy.NodeBinSHA256AMD64=… -X …ARM64=…"`
   注入 `nggd.exe`；摘要缺失时直接拒绝构建。

面板生成的脚本里 `NODE_BIN_BASE` 指向**当前版本 tag** 的 Release 资产，按架构拼 `-amd64/-arm64`，
下载后比对内嵌摘要。`publish` 阶段再次核对资产摘要与 job outputs 一致，防止「面板里的摘要」和
「实际发布的二进制」不是同一份。CI 也会交叉编译两个架构并做 `-version` 冒烟。

Release 资产：Windows 安装包 / 独立 `.exe`（一如既往）**加上两个 Linux 节点二进制**；
其余格式仍不发布。`SHA256SUMS` 只列**实际发布的资产**（不列 Inno Setup 的中间文件），
每个资产另有一个同名 `.sha256`，所以 `sha256sum -c SHA256SUMS` 能整份通过。

> 开发构建没有注入摘要，脚本会打印一条 NOTE 说明「只按 HTTPS 校验来源」而不是假装校验过；
> 发布版必定带摘要，第 8 步摘要不一致时直接丢弃下载文件并中止。

## 8. 排障

| 现象 | 原因 / 处理 |
| --- | --- |
| 「该方案是旧版本（SSH / 探针）生成的，没有节点管理端口」 | v2.12 及更早的方案没有 mTLS 材料：点「丢弃方案」→ 重新生成 → **在服务器上重新执行一次脚本** |
| `unreachable` / 连接超时 | 管理端口没放通，或云安全组没放行；确认 `nggctl status`，并确认面板 IP 在允许列表里 |
| `tls` / 证书 CN 不一致 | 地址指向了别的节点；或服务器上的证书是旧方案签的 → 重新生成方案并重新执行脚本 |
| `auth` / 401 403 | 客户端证书不是面板签发的；检查 `/etc/ngg/node/` 下的 `ca.crt`、`node.crt` |
| 连接成功但 `core_active=false` | 看 `nggctl core-logs`；配置 `-test` 通过后仍起不来时，节点会返回 journal 尾部 |
| 指标是 0 / 缺失 | 轮询还没跑满一个周期，或节点刚启动；卡片会显示「未连接」，不会用 0 冒充 |
| 第 8 步摘要失败 | 面板是开发构建（没注入摘要）会写 NOTE；发布版出现不一致说明下载被劫持或版本不匹配，**脚本已删除未执行的文件** |
| 升级后旧 `ngg-probe.service` 还在 | 第 9 步会 `systemctl disable --now` 并删除；也可手动 `systemctl disable --now ngg-probe && rm -f /etc/systemd/system/ngg-probe.service /usr/local/bin/ngg-probe` |

## 9. 已知边界

- 面板必须能连到节点的 `NODE_PORT`；面板在无公网入口、节点也没有可达地址时，两个方向都不通。
  这是 Remnawave 模型的固有前提，v2.12 的探针模式能绕过它但已按需求移除。
- 脚本只支持 systemd 的 Debian / Ubuntu 系；其他发行版在预检阶段明确拒绝。
- 管理端口默认对所有人开放到 TCP 层（mTLS 会拒绝无证书的握手），**强烈建议**在云安全组里
  只对面板出口 IP 放行 2222。
- 在线部署只支持 `xray-core` 档案；mihomo / sing-box 档案仍走手动配置，API 明确返回 422。
- 端口放通只处理本机防火墙；iptables 规则不持久化，脚本会提示。
- CA 私钥在面板本机 `data/nodepki/ca.key`（0600）。**备份它**；丢了只能重新生成方案并逐台重部署。
- 不再有「节点主动上报」，因此面板离线期间节点指标不会被补录；重新上线后由下一轮 30 s 轮询补齐当前值。
