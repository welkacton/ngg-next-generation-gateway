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
| 传输 | mTLS HTTPS :2222，TLS 1.3 | mTLS HTTPS `NODE_PORT`（**默认 2244**，见 §10），TLS 1.2+ |
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
| 「该端口上的服务不是本面板签发的节点」 | **管理端口被别的程序占用**（最常见：Remnawave 的 remnanode 占用 2222）。面板会把该端口上的实际证书 CN / 签发者附在错误里；把方案里的管理端口改成 2244 等空闲端口，重新生成脚本并在服务器上执行一次 |
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
  只对面板出口 IP 放行 2244（或方案里选的管理端口）。
- 在线部署只支持 `xray-core` 档案；mihomo / sing-box 档案仍走手动配置，API 明确返回 422。
- 端口放通只处理本机防火墙；iptables 规则不持久化，脚本会提示。
- CA 私钥在面板本机 `data/nodepki/ca.key`（0600）。**备份它**；丢了只能重新生成方案并逐台重部署。
- 不再有「节点主动上报」，因此面板离线期间节点指标不会被补录；重新上线后由下一轮 30 s 轮询补齐当前值。


---

## 10. v2.13.1 · 管理端口冲突与可诊断的失败

真实现场：一台 VPS 上本来就跑着 Remnawave，`remnanode` 占有 **2222**。
按上游默认值给 NGG 也配 2222 时，面板连过去拿到的是 remnanode 的 mTLS 证书
（`CN=MkLEM48…`，签发者 `YADMa8Y…`），旧版只回一句笼统的
「证书不匹配或已过期，重新生成方案并部署即可」—— 用户反复重新生成方案也不可能解决端口被占。

v2.13.1 的改动：

| 项 | 说明 |
| --- | --- |
| 默认管理端口 | `2222` → **`2244`**（`nodeapi.DefaultPort` / `nodedeploy.DefaultNodePort` / `ngg-node -listen` / 前端一致）。上游仍可用 2222，NGG 不再与它撞车 |
| 事前提醒 | 方案里仍用 2222 时，GET/POST `/deploy` 返回 `warning`，弹窗在生成脚本前就提示换端口 |
| 脚本预检 | 管理端口被非 NGG 进程占用时，报错直接点名「2222 通常已被 Remnawave remnanode 占用」并给出 2244 建议 |
| 错误分类 | `nodeapi.classify()` 区分 `foreign-ca` / `cert-expired` / `wrong-node` / `client-rejected`，`friendlyNodeError()` 按原因给不同处置，不再一律叫用户重新生成方案 |
| 对端证书诊断 | 连接握 TLS 失败时，`nodeapi.InspectPeer()` 以只读方式读出该端口实际证书的 CN / 签发者 / 有效期并附在错误里，一眼看出连错了服务 |
| 内核校验超时 | 第 7 步 `xray run -test` 加 45 秒 `timeout`；之前配置卡住会让整个安装停在 step 7 且没有任何输出 |

> 已经用 2222 生成过方案的节点不会自动换端口：在弹窗里把「管理端口」改成 2244，
> 点「生成安装脚本」，然后在服务器上重新执行一次脚本即可。不要用停掉其它面板服务的方式腾端口。


---

## 11. v2.13.2 · 自定义档案引用了节点上不存在的文件

第二次现场失败与端口无关（已用 2244），终端给出的是 Xray 自己的话：

```
NGG_STEP 7 用内核校验配置
Failed to start: main: failed to load config files: [/etc/ngg/xray/config.json] > infra/conf
> failed to build inbound config with tag DIRECT_Trojan
> infra/conf: failed to build TLS config
> infra/conf: failed to parse certificate > open /etc/xray-certs/panel.welkacton.ccwu.cc/…crt: no such file or directory
NGG_FAIL 7 配置校验未通过
```

`Default-Profile` 的 `DIRECT_Trojan` 入站引用了 `/etc/xray-certs/panel.welkacton.ccwu.cc/` 下的
`.crt` / `.key`。这些文件在面板（Windows）上存在与否毫无意义，Xray 会在**节点**上解析它们；
脚本因此停在第 7 步，`ngg-node` 从未安装，面板连 2244 只能超时，显示在 UI 上就是
「无法连接节点。确认脚本已执行完成…」。

**为什么用户一直没能退回默认模式**：部署弹窗的「配置文件」下拉框只有档案列表，
没有「不绑定」这一项；一旦选中，想回到内置 REALITY 只能找到 hover 才出现的清除图标。
placeholder 还写着「选择要绑定的配置文件（推荐）」，进一步误导。

v2.13.2 的改动：

| 项 | 说明 |
| --- | --- |
| 生成方案时预警 | `profileFileWarning()` 扫描渲染后的配置里的 `certificateFile` / `keyFile` 绝对路径（排除脚本自管的 `/etc/ngg`），把具体路径写进 `View.Warning`，弹窗在**复制脚本之前**就显示 |
| 脚本提前失败 | 第 6 步检查这些路径是否存在，缺失则 `die 6` 并逐一列出；不再等到第 7 步被 Xray 的嵌套报错淹没 |
| 显式「不绑定」 | 下拉框首项改为「不绑定 · 使用内置 REALITY 配置（推荐）」，placeholder 改为「留空＝使用内置 REALITY 配置（推荐）」，选中档案时显示证书文件风险提示 |
| 方案为准 | `loadDeployPlan` 按已保存方案的 `mode` / `profile_id` 回填下拉框，REALITY 方案不会再被卡片上的档案绑定悄悄改回自定义档案 |

**现场处置（不需要等升级也能做）**：把「配置文件」清空后重新生成脚本，或直接执行
`bin/ngg-node-01-2244.sh`（REALITY 模式，不引用任何外部文件）；若确实要用该档案，
先把 `.crt` / `.key` 放到节点服务器上对应路径，再执行脚本。


---

## 11. v2.13.2 · 自定义档案引用了节点上不存在的文件

第二次现场失败与端口无关（已用 2244），终端给出的是 Xray 自己的话：

```
NGG_STEP 7 用内核校验配置
Failed to start: main: failed to load config files: [/etc/ngg/xray/config.json] > infra/conf
> failed to build inbound config with tag DIRECT_Trojan
> infra/conf: failed to build TLS config
> infra/conf: failed to parse certificate > open /etc/xray-certs/panel.welkacton.ccwu.cc/…crt: no such file or directory
NGG_FAIL 7 配置校验未通过
```

`Default-Profile` 的 `DIRECT_Trojan` 入站引用了 `/etc/xray-certs/panel.welkacton.ccwu.cc/` 下的
`.crt` / `.key`。这些文件在面板（Windows）上存在与否毫无意义，Xray 会在**节点**上解析它们；
脚本因此停在第 7 步，`ngg-node` 从未安装，面板连 2244 只能超时，显示在 UI 上就是
「无法连接节点。确认脚本已执行完成…」。

**为什么用户一直没能退回默认模式**：部署弹窗的「配置文件」下拉框只有档案列表，
没有「不绑定」这一项；一旦选中，想回到内置 REALITY 只能找到 hover 才出现的清除图标。
placeholder 还写着「选择要绑定的配置文件（推荐）」，进一步误导。

v2.13.2 的改动：

| 项 | 说明 |
| --- | --- |
| 生成方案时预警 | `profileFileWarning()` 扫描渲染后的配置里的 `certificateFile` / `keyFile` 绝对路径（排除脚本自管的 `/etc/ngg`），把具体路径写进 `View.Warning`，弹窗在**复制脚本之前**就显示 |
| 脚本提前失败 | 第 6 步检查这些路径是否存在，缺失则 `die 6` 并逐一列出；不再等到第 7 步被 Xray 的嵌套报错淹没 |
| 显式「不绑定」 | 下拉框首项改为「不绑定 · 使用内置 REALITY 配置（推荐）」，placeholder 改为「留空＝使用内置 REALITY 配置（推荐）」，选中档案时显示证书文件风险提示 |
| 方案为准 | `loadDeployPlan` 按已保存方案的 `mode` / `profile_id` 回填下拉框，REALITY 方案不会再被卡片上的档案绑定悄悄改回自定义档案 |

**现场处置（不需要等升级也能做）**：把「配置文件」清空后重新生成脚本，或直接执行
`bin/ngg-node-01-2244.sh`（REALITY 模式，不引用任何外部文件）；若确实要用该档案，
先把 `.crt` / `.key` 放到节点服务器上对应路径，再执行脚本。


---

## 12. v2.13.3 · 让自定义档案真正可部署

v2.13.2 只是把「档案引用了不存在的证书」提前报出来，部署仍然会停在第 6 步。真正的修法是让
profile 模式和内置模式一样自包含：

| 问题 | 处理 |
| --- | --- |
| 档案引用 `/etc/xray-certs/...` 下的 `.crt` / `.key`，节点上没有 | 第 6 步按 `certificateFile` / `keyFile` 成对解析，缺失就 `openssl req -x509` 签发**同域名自签证书**（ECDSA P-256，3650 天，SAN=DNS:<文件名>），已存在则跳过（幂等，不覆盖用户后来放的真证书）；`NGG_NODE_READY` 增加 `tls_self_signed=1`，并逐条 NOTE 说明客户端需允许不安全证书 |
| 面板填的数据端口（6691）与档案实际监听端口（33927 / 43804 / 22363 / 54076）不一致 | 第 6 步从 `$CONFIG` 解析所有 `"port": N` 得到 `CFG_PORTS`；第 11 步按这些端口放通，第 13 步只要其中**任一**在监听即通过；结尾与 `NGG_NODE_READY` 打印 `ports=` |
| 非 profile 模式引用外部证书 | 仍然 `die 6`（内置模式不该出现这种引用，出现就是配置错了） |

**注意**：证书只是「能不能启动」的问题。一个 `clients: []` 全空的档案即使部署成功，也没有任何
客户端能连上——那属于「协议配置」内容问题，不是部署问题。


---

## 13. v2.13.4 · 单元模板 `${NODE_DIR}` 未定义，旧单元没被覆盖

第三次现场日志里最关键的一行不是 Xray 的报错，而是：

```
NGG_STEP 9 写入节点证书并启动 ngg-node.service
[NGG] 证书已写入：CN = NGG Node, CN = 01
-bash: NODE_DIR: unbound variable
NGG_OK 9
NGG_NOTE systemctl enable ngg-node.service 失败，重启后 API 不会自动拉起
...
systemd[1]: Started ngg-node.service.
NGG Node vps-main (reality :3372).
xray[1973291]: Xray 26.10.31 ...
NGG_FAIL 10 节点 API 未进入 active（当前：inactive）
```

`ngg-node.service` 的 `Description` 还是 **v2.12 的 `NGG Node vps-main (reality :3372)`**，进程是 Xray。
也就是说新单元**根本没写进去**，服务器上跑的还是旧单元。

根因：API 单元模板里 `ReadWritePaths=${NODE_DIR} /run` 把模板占位符 `{{NODE_DIR}}` 误写成了
shell 变量 `${NODE_DIR}`，而脚本里没有这个变量、又开着 `set -u`，heredoc 展开时直接
`unbound variable`；脚本头是 `set -uo pipefail`（没有 `-e`），所以错误被吞掉，继续打印 `NGG_OK 9`。

修复：

| 项 | 说明 |
| --- | --- |
| 占位符 | `ReadWritePaths={{NODE_DIR}} /run`，渲染后是 `ReadWritePaths=/etc/ngg /run` |
| 变量 | 脚本显式定义 `NODE_DIR="{{NODE_DIR}}"`，即使别处再写 `${NODE_DIR}` 也不会炸 |
| 写入校验 | 两个 `cat > /etc/systemd/system/*.service` 之后都加 `[ -s ... ] || die 9 / die 10`，写不进去立刻停，不再静默继续 |
| 回归测试 | `TestScriptWritesUnitsWithResolvedPaths` 断言不出现 `${NODE_DIR}`、`NODE_DIR` 有定义、单元里是解析后的绝对路径、两处写入校验存在 |

> 旧单元与新 API 单元同名（`ngg-node.service`），修复后 `cat >` 会直接覆盖，`daemon-reload` +
> `restart` 即可把 :3372 的 Xray 换成 :2244 的 ngg-node。
