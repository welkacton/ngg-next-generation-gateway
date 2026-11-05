# NGG 部署指南

仓库当前为 **Private（仅自己可见）**：`https://github.com/welkacton/ngg-next-generation-gateway`。
只有仓库所有者授权的 GitHub 账号可以克隆；自行部署到 VPS、家用电脑、软路由或 OpenWrt 时，
请先完成授权克隆（`git clone` 使用有权限的凭证 / SSH Key），或下载自己账号可见的 Release 产物。
许可：MIT（见 `LICENSE`）。**如需对外共享，由所有者在仓库设置中手动改为 Public。**

> **按目标平台选择专门手册**（本文是全场景速查）：
> - VPS / 云服务器「从空白机器到可用」→ **[`DEPLOY-VPS.md`](DEPLOY-VPS.md)**（一键脚本、systemd、防火墙、SSH 隧道 / HTTPS、备份回滚）
> - Windows 安装程序 / 服务 / 卸载 → **[`INSTALL-WINDOWS.md`](INSTALL-WINDOWS.md)**
> - 手工部署模板 → `deploy/systemd/ngg.service.example`、`deploy/nginx/ngg.conf.example`

---

## 0. 先读安全清单（私有仓库同样必须遵守）

- **控制 API 默认只监听 `127.0.0.1:9090` 且必须带 Bearer Token**。不要把它直接暴露到公网。
  远程管理请走 SSH 隧道：`ssh -L 9090:127.0.0.1:9090 user@your-vps`。
- 若把 `network.ingress.listen` 改成 `0.0.0.0`（局域网代理），**必须**同时设置
  `auth_user` / `auth_pass`，否则 NGG 会拒绝启用该入口（`ErrIngressInsecure`）。
- `configs/ngg.yaml`（含 API Token）、`data/`（含 SQLite、节点凭据、运行时配置）都在
  `.gitignore` 中，**永远不要提交**。
- `POST /nodes` 的 `params` 与整份原生配置里可能含密码、UUID、私钥、后量子密钥、证书路径。
  分享配置或提 Issue 前请先脱敏。`configs/examples/native-xray.sample.json` 全部使用占位符，
  **请把它当成模板，不要直接用于生产**。
- 内核下载会强制校验 GitHub Release 的 `sha256` digest、官方域名与真实 `-v` 版本，
  任一失败即拒绝安装。Release 产物请用 `SHA256SUMS` 校验。
- Linux 上启用 `network.tun.auto_route` 需要 `/dev/net/tun` + `CAP_NET_ADMIN`；其它平台
  返回 `ErrTUNUnsupported`，自动使用 userspace 入口，不假装接管全局流量。

---

## 1. 部署路径总览

| 目标 | 推荐方式 | 架构 | 备注 |
| --- | --- | --- | --- |
| VPS / 云服务器 | Docker Compose 或 systemd | linux/amd64、linux/arm64 | 最完整路径 |
| 家用电脑（Windows） | `NGG-Setup-vX.Y.Z.exe` / 便携 ZIP + `install.ps1` | Windows 服务 + Web UI | 见 `INSTALL-WINDOWS.md` |
| 家用电脑（macOS / Linux） | `scripts/quickstart.sh` 或预编译包 | 各平台原生二进制 | 控制面仅本机 |
| 软路由 / OpenWrt | `platforms/openwrt/` 的 procd init + UCI | linux/arm64、linux/mipsle 等 | 需自行交叉编译 |
| 群晖 / NAS / 小主机 | Docker | amd64 / arm64 | 用 `.env` 配 Token |
| 集群 / 多机 | 每台独立部署；SD-WAN 链路在「出口节点」页配置 | — | 不共享 `data/` |

---

## 2. Docker / Docker Compose（VPS 推荐）

```bash
git clone https://github.com/welkacton/ngg-next-generation-gateway.git
cd ngg-next-generation-gateway
cp .env.example .env
# 可选：编辑 .env 设置 NGG_TOKEN
docker compose up -d --build
docker compose logs -f ngg       # 首次启动会打印自动生成的 API Token
```

浏览器打开 `http://127.0.0.1:9090/?token=<日志里的 TOKEN>`。配置持久化在 `./runtime/configs/ngg.yaml`，数据在 `./runtime/data/`。

- Linux 上 `network_mode: host` 让 LAN 入口与 TUN 正常工作；Docker Desktop 请改用
  `docker-compose.yml` 里注释掉的 `ports` 映射。
- `NET_ADMIN` 只在启用 TUN 自动路由时需要。
- 升级：`git pull && docker compose up -d --build`（配置与数据在挂载卷里，不会丢）。

纯 Docker：

```bash
docker build -t ngg:local .
docker run -d --name ngg --network host \
  -e NGG_TOKEN=your-own-secret \
  -v "$PWD/runtime/configs:/app/configs" -v "$PWD/runtime/data:/app/data" \
  --cap-add NET_ADMIN ngg:local
```

---

## 3. systemd（无 Docker 的 VPS）

**推荐：一键安装 Release 预编译包（VPS 上不需要 Go / Node，自动写绝对路径配置与 Token）**

```bash
export GITHUB_TOKEN='ghp_xxx'     # 私有仓库下载 Release 需要 PAT
sudo --preserve-env=GITHUB_TOKEN ./scripts/deploy-vps.sh install --version v2.2.0
sudo ./scripts/deploy-vps.sh status
# 完整参数与手工等价步骤见 docs/DEPLOY-VPS.md
```

`deploy-vps.sh` 支持 `install / upgrade / backup / restore / status / doctor / uninstall`，
并能可选配置 ufw/firewalld、Nginx 公网入口（`--public`：公网 IP + 端口 + 16 位随机后缀 +
Cookie 门禁）与 `--domain --with-tls` 的 certbot HTTPS。**不要**给 Nginx 加 Basic Auth，
它会覆盖 Web UI 的 Bearer 头导致 401。

**备选：从源码构建安装**（需要 VPS 上有 Go 1.26+、Node 20+）

```bash
git clone https://github.com/welkacton/ngg-next-generation-gateway.git
cd ngg-next-generation-gateway
cd web && npm ci && npm run build && cd ..     # 生成 Web UI
sudo bash scripts/install.sh                   # 安装到 /opt/ngg，配置 /etc/ngg/ngg.yaml
sudo systemctl enable --now ngg
journalctl -u ngg -f                           # 查看 Token 与日志
```

> `install.sh` 复制的配置仍是**相对路径**；生产环境务必把 `storage.dir`、`logging.dir`、`server.web_dir`
> 改成绝对路径（见 `04-installation.md` 第 2 节）。一键脚本已自动处理这一点。

---

## 4. 本地电脑 / 一键启动

```bash
git clone https://github.com/welkacton/ngg-next-generation-gateway.git
cd ngg-next-generation-gateway
bash scripts/quickstart.sh                 # 源码构建（需 Go 1.26+、Node 20+）
# 或
bash scripts/quickstart.sh --release v2.2.0    # 下载官方 Release 预编译包
```

脚本会：构建/下载 → 复制 `configs/ngg.example.yaml` 为 `configs/ngg.yaml` → 启动。
首次启动自动生成 Token 并写入本地配置（0600）。

**Windows 用户请用安装程序而不是手跑 exe**：双击 `NGG-Setup-vX.Y.Z.exe`，或解压
`NGG-vX.Y.Z-windows-amd64.zip` 后以管理员运行 `install.ps1`。它会写好绝对路径配置、随机 Token、
注册 Windows 服务、启动并做健康检查。完整步骤见 [`INSTALL-WINDOWS.md`](INSTALL-WINDOWS.md)。
仅在开发调试时才需要 `bin\nggd.exe -config configs\ngg.yaml`。

---

## 5. 从 GitHub Release 获取预编译包

发布 tag（`v*`）时 CI 会为 linux amd64/arm64/386/arm、windows amd64、darwin amd64/arm64、freebsd amd64
构建包含 `nggd` + `web-dist` + 配置模板 + 文档 + `scripts/` + `deploy/` + `installer/` 的 tar.gz，
并额外产出 **`NGG-<tag>-windows-amd64.zip`（便携包）** 与 **`NGG-Setup-<tag>.exe`（Inno Setup 安装程序）**，
全部附 `SHA256SUMS` 与逐包 `.sha256`。`deploy-vps.sh` / `install.ps1` 下载时都会校验摘要。

```bash
# 校验（示例）
curl -LO https://github.com/welkacton/ngg-next-generation-gateway/releases/download/v2.2.0/ngg-v2.2.0-linux-amd64.tar.gz
sha256sum -c ngg-v2.2.0-linux-amd64.tar.gz.sha256
tar -xzf ngg-v2.2.0-linux-amd64.tar.gz && cd ngg
cp configs/ngg.example.yaml configs/ngg.yaml
./nggd -config configs/ngg.yaml
```

---

## 6. 软路由 / OpenWrt

见 `platforms/openwrt/README.md`：提供 procd init 脚本与 UCI 配置。步骤概要：

```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o nggd-linux-arm64 ./cmd/nggd
# 复制到设备，安装 platforms/openwrt/files/etc/init.d/ngg 与 config/ngg
/etc/init.d/ngg enable && /etc/init.d/ngg start
```

低性能设备建议关闭 Web UI 之外的额外功能，并按需裁剪 `docs/08-stage3-5-features.md` 中的可选模块。

---

## 7. 自定义自己的配置

### 完整内核原生配置

「入口节点 / 出口节点 → 原生配置」模式支持直接粘贴整份 Xray-core / sing-box JSON 或 Mihomo YAML
（含 `log` / `inbounds` / `outbounds` / `routing`）。可以带 `@long-text:` 或
`<long_text_quote>...</long_text_quote>` 包裹，NGG 会自动剥离。

四个动作：

| 动作 | 说明 |
| --- | --- |
| 读取内核当前配置 | 从 `GET /cores/{id}/config` 载入当前有效配置 |
| 仅原生校验 | 调用内核自身的校验器（`xray -test`、`sing-box check`、`mihomo -t`），不改动文件 |
| 校验并应用 | **备份 → 原生校验 → 通过才替换**（`PUT /cores/{id}/config`），失败保留原配置 |
| 解析为节点 / 导入节点库 | 把 inbounds/outbounds/routing 解析成可编辑的节点与路由视图 |

示例模板：`configs/examples/native-xray.sample.json`（全部占位符）。解析时会把
`vless` + `mlkem768x25519plus...` 识别为 ML-KEM-768 混合后量子节点，`freedom` → DIRECT，
`socks` → SOCKS5，并保留原始 `settings` / `streamSettings` / 证书路径等参数。

### 自定义部署配置

- 复制 `configs/ngg.example.yaml` → `configs/ngg.yaml`，按需修改端口、目录、入口认证、Fail-Closed 策略。
- 新内核放 `configs/cores.d/*.yaml`（见 `docs/03-development.md`）。
- 规则/DNS/SD-WAN/插件示例数据在 `configs/examples/`。
- 从 Web UI 修改的网络配置写入 `data/network-config.json`，节点写入 `data/nodes-config.json`，
  都不改写含 Token 与注释的主配置文件。

---

## 8. 端口与目录

| 端口 | 用途 | 默认绑定 |
| --- | --- | --- |
| 9090 | REST + WebSocket 控制面 | 127.0.0.1 |
| 18080 | 统一 userspace 入口（SOCKS5 / SOCKS4a / HTTP CONNECT / HTTP 绝对形式） | 127.0.0.1 |
| 18053 | NGG DNS 引擎（UDP + TCP） | 127.0.0.1 |
| 17890~ | 三内核隔离端口块（每内核 5 个） | 127.0.0.1 |

```
data/                 运行时数据（gitignored）
  ngg.db              SQLite 状态
  cores/<id>/         内核二进制、配置、备份
  network-config.json 界面修改的网络配置
  nodes-config.json   协议节点定义（可能含凭据）
runtime/              Docker Compose 挂载目录（gitignored）
platforms/            各平台接入骨架
```

---

## 9. 升级与备份

> systemd 原生部署的一键运维命令见 [`DEPLOY-VPS.md`](DEPLOY-VPS.md) 第 8 节：
> `deploy-vps.sh upgrade / backup / restore / status / doctor / uninstall`（升级失败会自动回滚二进制）。

- 升级代码：`git pull` → 重新构建 → 重启服务；内核二进制与配置在 `data/`，不会丢。
- 备份：打包 `configs/ngg.yaml` 与 `data/`（含密钥，请加密保存）。
- 配置回滚：Web UI「配置管理」页按备份 ID 回滚，或「原生配置」页重新应用旧配置。
- 内核版本回滚：`POST /api/v1/cores/{id}/rollback`。

---

## 10. 已知能力边界（部署前请确认）

- **iOS / iPadOS / Android / HarmonyOS / Android TV 原生 App 未实现**，`platforms/` 只有
  共享 Go 核心与接入骨架，需各平台 SDK 自行打包。
- **TUN 上的 TCP 流终止需 userspace TCP 协议栈**，属于尚未实现的已知缺口；当前 TUN 读环回
  完成真实 L3 解析、IPv4 UDP/53 DNS 应答与 Fail-Closed 记录。TCP 数据面走 userspace 入口。
- 后量子能力用于 NGG 节点间控制信道；内核数据面的 PQ 支持取决于内核版本。
- SD-WAN 探测与选路真实可用，但选路结果尚未注入每条连接的出站拨号器。
- 协议中心不重写内核协议栈；传统 VPN 由外部 helper 执行。

---

## 11. 兼容 3X-UI / Remnawave 导出的 Xray 配置

NGG 的「入口节点 / 出口节点 → 原生配置」模式接受**整份 Xray-core JSON**，因此
现有面板生成的配置可以直接粘贴，无需改写容器格式。

| 来源 | 特点 | NGG 中的处理 |
| --- | --- | --- |
| [3X-UI](https://github.com/MHSanaei/3x-ui) 默认配置 | 单个 Shadowsocks inbound，`freedom` / `blackhole` outbound，`routing.rules: []` | 解析为 1 个 `shadowsocks` 入口 + `direct` / `reject` 出口；`settings.method`、`clients`、`network` 原样保留在 `params.settings` |
| [Remnawave Node](https://docs.rw/overview/quick-start/) | Node 是内置 Xray-core 的容器；REALITY / VLESS / Shadowsocks inbound；`ext:geo-*.dat:*` 扩展 geosite/geoip 规则；文件日志与证书挂载 | 解析 `inboundTag` / `outboundTag`（支持数组），并把 `domain` / `ip` 匹配条件保留在路由视图的 `matchers` 中；REALITY / TLS 参数保留在 `params.streamSettings` |
| 自定义 Xray 多入口 | 多 `inbounds` 按 `routing.rules` 绑定不同 `outbounds` | 全部解析为可编辑节点 + 路由绑定表 |

内置模板（`GET /api/v1/nodes/templates`，界面「载入示例」按钮）：

- `configs/examples/3x-ui-default.sample.json`
- `configs/examples/native-xray.sample.json`（多入口 / 多出口 / 后量子 VLESS）
- `configs/examples/remnawave-xray.sample.json`（REALITY + ext geosite 规则）

### 使用 Remnawave / 3X-UI 配置时的注意点

1. **证书路径**：Remnawave 文档用 `/var/lib/remnawave/configs/xray/ssl/` 挂载 `privkey.key` /
   `fullchain.pem`。把配置粘到 NGG 时，请改成自己容器内可访问的路径，并在
   `docker-compose.yml` 增加只读挂载（见下方注释示例）。REALITY 传输不需要证书。
2. **扩展 geo 文件**：`ext:geo-zapret.dat:zapret` 这类文件需挂载到 Xray 的数据目录，
   **不要整目录挂载**（会覆盖默认 geosite/geoip），按文件逐个挂载。
3. **文件日志**：`log.error` / `log.access` 指向容器路径时需挂载卷并配置 logrotate，否则日志会撑满磁盘。
4. **服务端路由 ≠ 客户端路由**：Remnawave 文档明确指出，服务端 `routing` 只控制服务端流量，
   客户端的 DIRECT/代理路由要在客户端侧配置。NGG 的 `routing` 绑同样只描述 NGG 侧的出口选择。
5. **凭据**：面板生成的 `clients[].id` / `password` / `decryption` 都是真实凭据。粘贴后它们会保存在
   `data/nodes-config.json`（本地 0600、不入库），分享配置前务必脱敏。

```yaml
# docker-compose.yml 片段：证书与扩展 geo 文件（按需启用）
    volumes:
      - ./runtime/configs:/app/configs
      - ./runtime/data:/app/data
      # - ./certs/privkey.key:/etc/ngg-certs/privkey.key:ro
      # - ./certs/fullchain.pem:/etc/ngg-certs/fullchain.pem:ro
      # - ./geo/geo-zapret.dat:/usr/local/share/xray/geo-zapret.dat:ro
      # - ./geo/ip-zapret.dat:/usr/local/share/xray/ip-zapret.dat:ro
      # - ./runtime/logs:/var/log/remnanode
```

### 与这些面板的定位差异（如实说明）

Remnawave 是「Panel + Node 分离、集中管理多节点、用户与订阅分发」的体系，依赖 PostgreSQL / Redis
与反向代理；3X-UI 是单机 Xray 管理面板。NGG 则是**单机统一网络网关**：一个进程统一创建 TUN /
入口、统一规则与 DNS、并行调度多个内核。NGG 参考它们的**配置格式与部署安全实践**，
但**当前不实现**订阅分发、用户管理、多机集中管控或 Panel/Node 分离架构；
企业多租户目前也只是声明式字段。请勿把 NGG 当作这些面板的替代品。

参考文档：[Remnawave Quick Start](https://docs.rw/overview/quick-start/) ·
[3X-UI](https://github.com/MHSanaei/3x-ui)
