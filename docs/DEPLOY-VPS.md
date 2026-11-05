# NGG 部署到 VPS / 云服务器（完整步骤）

> 本文是**从一台空白 VPS 到可访问管理界面**的完整本地部署手册，覆盖 Ubuntu / Debian / Rocky / AlmaLinux
> 的 systemd 原生安装、Docker Compose、私有仓库 Release 下载、防火墙、HTTPS 反向代理、备份与回滚。
> Windows 安装请见 [`INSTALL-WINDOWS.md`](INSTALL-WINDOWS.md)；其它场景速查见 [`DEPLOY.md`](DEPLOY.md)。

---

## 0. 先明确安全边界（必读）

| 约束 | 原因 |
| --- | --- |
| 控制 API 只监听 `127.0.0.1:9090`，且所有 `/api/` 请求必须带 Bearer Token | 代码里 socket 层与中间件**双重**校验回环地址，非回环直接 403。把 `server.listen` 改成 `0.0.0.0` 不是「开放远程管理」，而是直接失效 |
| 远程管理走 **SSH 隧道**，或 **Nginx + HTTPS + Basic Auth** 反向代理到本机 9090 | 这是唯一两种被支持的远程访问方式 |
| 云安全组 / 防火墙只放行 22、可选的 80/443，**绝不放行 9090 / 18080 / 18053** | 9090 是控制面，18080 是代理入口，18053 是 DNS |
| `configs/ngg.yaml`（含 Token）与 `data/`（含节点凭据、私钥）不入库、不外传 | 已 gitignore；备份文件请加密保存 |
| 仓库当前为 **Private** | 下载 Release 资产需要 GitHub PAT；不要把 PAT 写进脚本或提交 |

> `network.ingress.listen` 若改为 `0.0.0.0` 开放局域网代理，**必须**同时设置 `auth_user` / `auth_pass`，
> 否则 NGG 会主动拒绝启用该入口（`ErrIngressInsecure`）。

---

## 1. 准备 VPS

**最低配置**：1 vCPU / 1 GB RAM / 10 GB 磁盘；x86_64 或 ARM64 均可（Release 提供 amd64 / arm64 / arm / 386）。

```bash
# 以 Ubuntu 22.04/24.04、Debian 12 为例
ssh root@<VPS_IP>
adduser nggadmin && usermod -aG sudo nggadmin     # 用非 root 账号日常运维
apt update && apt upgrade -y
apt install -y curl tar ca-certificates openssl
timedatectl set-timezone Asia/Shanghai           # 可选

# Rocky / AlmaLinux 9 等价命令
# dnf install -y curl tar openssl ca-certificates
```

确认系统使用 systemd：`systemctl --version`。Docker 方案见第 3 节，可跳过后续 systemd 步骤。

**私有仓库凭据**（只在本机使用，不要写进仓库）：

1. GitHub → Settings → Developer settings → Personal access tokens；
2. Classic PAT 勾选 `repo`（或 Fine-grained PAT 给该仓库 `Contents: Read`）；
3. 在 VPS 上 `export GITHUB_TOKEN='ghp_xxx'`，用 `sudo --preserve-env=GITHUB_TOKEN ...` 传给脚本。
   脚本也支持 `--token`，但命令行参数会留在 shell history 与进程列表里，**优先用环境变量**。

---

## 2. 方式 A：一键脚本安装（推荐，VPS 上无需 Go / Node）

`scripts/deploy-vps.sh` 直接安装 GitHub Release 里的**预编译包**，不在服务器上编译源码。

### 2.1 取得脚本

```bash
# 有权限的账号克隆（脚本与模板都在仓库里）
git clone https://github.com/welkacton/ngg-next-generation-gateway.git
cd ngg-next-generation-gateway

# 或者只取脚本（私有仓库需把 PAT 放进请求头）
curl -fL -H "Authorization: Bearer $GITHUB_TOKEN" \
  -o deploy-vps.sh \
  https://raw.githubusercontent.com/welkacton/ngg-next-generation-gateway/main/scripts/deploy-vps.sh
chmod +x deploy-vps.sh
```

### 2.2 安装

```bash
export GITHUB_TOKEN='ghp_xxx'

# 在线安装指定 Release（脚本自动识别 amd64 / arm64 / arm / 386）
sudo --preserve-env=GITHUB_TOKEN ./scripts/deploy-vps.sh install --version v1.0.0

# 可选：配置防火墙 + Nginx 反向代理 + Let's Encrypt
sudo --preserve-env=GITHUB_TOKEN ./scripts/deploy-vps.sh install --version v1.0.0 \
  --domain ngg.example.com --with-tls --email you@example.com --configure-firewall

# 离线 / 内网：在有网机器下载 Release 包与 SHA256SUMS，scp 到 VPS 后
sudo ./scripts/deploy-vps.sh install --file /root/ngg-v1.0.0-linux-amd64.tar.gz
```

> `--file` 时脚本会在同目录寻找 `SHA256SUMS` 或 `<包名>.sha256` 并校验；
> 自构建包可加 `--skip-checksum`（不推荐）。

### 2.3 脚本实际做了什么（可审计）

1. 下载 `ngg-<tag>-linux-<arch>.tar.gz` + `SHA256SUMS`，**校验 SHA256 通过才解包**；私有仓库自动回退到 GitHub API 资产接口；
2. 创建系统用户 `ngg` 与目录 `/opt/ngg`、`/etc/ngg`、`/var/lib/ngg`、`/var/log/ngg`；
3. 安装 `nggd`、`web/dist`、`cores.d` 描述文件；
4. 从模板生成 `/etc/ngg/ngg.yaml`：**全部改为绝对路径**、生成 32 字节随机 API Token、权限 `0600`；
5. 生成 systemd unit（含 `CAP_NET_ADMIN` 供 TUN、`ProtectSystem=full` + `ReadWritePaths` 最小可写面）；
6. 可选配置 ufw/firewalld 与 Nginx（Basic Auth + WebSocket + certbot）；
7. `systemctl enable --now ngg`，轮询 `/api/v1/health` 直到通过；
8. 打印访问地址、Token 与 SSH 隧道命令。

### 2.4 常用参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--version vX.Y.Z` | — | Release tag；`install` / `upgrade` 必填（除非 `--file`） |
| `--file PATH` | — | 本地 tar.gz，离线部署 |
| `--token TOKEN` | `$GITHUB_TOKEN` | GitHub PAT（私有仓库） |
| `--domain FQDN` | — | 启用 Nginx 反向代理 |
| `--with-tls` / `--email` | — | 调用 certbot 申请证书并强制 HTTPS |
| `--configure-firewall` | 关 | ufw/firewalld 放行 22/80/443；防火墙未启用时只写规则、不擅自激活 |
| `--enable-tun` | 关 | 打开 `network.tun.autoroute`（需 `/dev/net/tun`） |
| `--port N` | 9090 | 通过 `NGG_PORT` 覆盖控制面端口 |
| `--skip-checksum` | 关 | 跳过 SHA256（仅自构建包） |
| `PREFIX` / `CONF` / `DATA` / `LOGDIR` | `/opt/ngg` / `/etc/ngg` / `/var/lib/ngg` / `/var/log/ngg` | 环境变量覆盖路径 |

### 2.5 首次访问

```bash
sudo ./scripts/deploy-vps.sh status          # 服务状态 + 健康检查 + 带 Token 的地址
sudo journalctl -u ngg -f                    # 实时日志
sudo grep '^[[:space:]]*token:' /etc/ngg/ngg.yaml
```

**推荐 SSH 隧道**（本机执行，不需要开放任何额外端口）：

```bash
ssh -L 9090:127.0.0.1:9090 nggadmin@<VPS_IP>
# 浏览器打开 http://127.0.0.1:9090/?token=<TOKEN> 完成一次性绑定
```

---

## 3. 方式 B：Docker Compose（已有容器环境时）

```bash
git clone https://github.com/welkacton/ngg-next-generation-gateway.git
cd ngg-next-generation-gateway
cp .env.example .env
# 可选：在 .env 里设置 NGG_TOKEN（留空则首次启动自动生成并写回 runtime/configs/ngg.yaml）
docker compose up -d --build
docker compose logs -f ngg          # 日志里会打印自动生成的 API Token
```

- Linux 上 `network_mode: host` 保证 LAN 入口与 TUN 正常；Docker Desktop 需改用 `docker-compose.yml` 里注释掉的 `ports`。
- 配置持久化在 `./runtime/configs/ngg.yaml`，数据在 `./runtime/data/`，升级不会丢。
- 升级：`git pull && docker compose up -d --build`。
- 远程访问同样走 SSH 隧道或宿主机 Nginx 反代到 `127.0.0.1:9090`。

---

## 4. 方式 C：纯手工 systemd 安装（不使用脚本）

适合需要逐条审计或自定义路径的场景。以下命令以 root 执行，路径与脚本默认值一致。

```bash
# 1) 下载并校验 Release
export VERSION=v1.0.0 ARCH=amd64 TOKEN=ghp_xxx
BASE="https://github.com/welkacton/ngg-next-generation-gateway/releases/download/${VERSION}"
curl -fL -o /tmp/ngg.tgz "$BASE/ngg-${VERSION}-linux-${ARCH}.tar.gz"
curl -fL -o /tmp/SHA256SUMS "$BASE/SHA256SUMS"
awk -v n="ngg-${VERSION}-linux-${ARCH}.tar.gz" '$2==n{print $1"  /tmp/ngg.tgz"}' /tmp/SHA256SUMS | sha256sum -c -
mkdir -p /tmp/ngg-pkg && tar -xzf /tmp/ngg.tgz -C /tmp/ngg-pkg
PKG=/tmp/ngg-pkg/ngg

# 2) 用户与目录
useradd --system --no-create-home --shell /usr/sbin/nologin ngg
install -d -m 0755 /opt/ngg /opt/ngg/web /etc/ngg /etc/ngg/cores.d /var/lib/ngg /var/log/ngg
install -m 0755 "$PKG/nggd" /opt/ngg/nggd
cp -r "$PKG/web-dist" /opt/ngg/web/dist
cp -r "$PKG/configs/cores.d/." /etc/ngg/cores.d/

# 3) 配置：改为绝对路径 + 预置 Token（避免服务用户写回配置文件）
TOKEN_API=$(openssl rand -hex 32)
sed -e "s#^\([[:space:]]*dir:[[:space:]]*\)data[[:space:]]*\$#\1/var/lib/ngg#" \
    -e "s#^\([[:space:]]*dir:[[:space:]]*\)data/logs[[:space:]]*\$#\1/var/log/ngg#" \
    -e "s#^\([[:space:]]*web_dir:[[:space:]]*\).*\$#\1/opt/ngg/web/dist#" \
    -e "s#^\([[:space:]]*descriptor_dir:[[:space:]]*\).*\$#\1/etc/ngg/cores.d#" \
    -e "s#^\([[:space:]]*token:[[:space:]]*\).*\$#\1\"$TOKEN_API\"#" \
    "$PKG/configs/ngg.example.yaml" > /etc/ngg/ngg.yaml
chown -R ngg:ngg /etc/ngg /var/lib/ngg /var/log/ngg /opt/ngg/web
chmod 0600 /etc/ngg/ngg.yaml
echo "API Token: $TOKEN_API"    # 立即保存到密码管理器

# 4) unit（可直接复制 deploy/systemd/ngg.service.example）
cp deploy/systemd/ngg.service.example /etc/systemd/system/ngg.service
systemctl daemon-reload
systemctl enable --now ngg

# 5) 自检
curl -fsS -H "Authorization: Bearer $TOKEN_API" http://127.0.0.1:9090/api/v1/health
```

> **必须改成绝对路径**：`storage.dir` / `logging.dir` / `server.web_dir` / `cores.descriptor_dir`。
> systemd 的 `WorkingDirectory=/opt/ngg`，相对路径会把 SQLite 写到 `/opt/ngg/data/`，与备份目录规划不一致。

---

## 5. 远程管理：SSH 隧道 / Nginx + HTTPS

### 5.1 SSH 隧道（推荐，零暴露）

```bash
ssh -N -L 9090:127.0.0.1:9090 nggadmin@<VPS_IP>      # 保持窗口打开
# 浏览器: http://127.0.0.1:9090/?token=<TOKEN>
```

也可用 VS Code / Termius 的端口转发，效果相同。

### 5.2 Nginx + Basic Auth + HTTPS（需要域名）

```bash
apt install -y nginx apache2-utils certbot python3-certbot-nginx
htpasswd -bcB /etc/ngg/htpasswd ngg '<随机强密码>'
cp deploy/nginx/ngg.conf.example /etc/nginx/sites-available/ngg.conf
sed -i 's/ngg.example.com/<你的域名>/' /etc/nginx/sites-available/ngg.conf
ln -sfn /etc/nginx/sites-available/ngg.conf /etc/nginx/sites-enabled/ngg.conf
nginx -t && systemctl reload nginx
certbot --nginx -d <你的域名> --redirect
# 一键脚本等价命令：deploy-vps.sh install --domain <域名> --with-tls --email <邮箱>
```

访问 `https://<域名>/?token=<TOKEN>`：

- Nginx 的 Basic Auth 是**额外一层**，NGG 的 Bearer Token 仍然必需；Web UI 首次用 `?token=` 绑定后会在本地保存。
- WebSocket `/api/v1/ws` 需要 `Upgrade` 头，模板已配置；若日志出现大量 WS 断连，先检查这两行。
- 浏览器若同时弹出 Basic Auth 与 NGG 登录，先完成 Basic Auth，再粘贴 Token。
- **不要**把 `server.listen` 改成 `0.0.0.0` 来「省掉 Nginx」：中间件会拒绝所有非回环请求。

---

## 6. 防火墙与云安全组

```bash
# ufw（Ubuntu / Debian）
ufw allow OpenSSH
ufw allow 80/tcp && ufw allow 443/tcp
ufw status                       # 确认规则后再 ufw enable，避免把自己锁在门外
# 注意：不要 ufw allow 9090

# firewalld（Rocky / AlmaLinux）
firewall-cmd --permanent --add-service=ssh --add-service=http --add-service=https
firewall-cmd --reload
```

云厂商控制台的安全组同样只放行 22（建议限制来源 IP）、80、443。
`deploy-vps.sh install --configure-firewall` 会添加上述规则，但**不会擅自启用**原本关闭的 ufw。

---

## 7. 内核与统一 TUN

- 三个内核（mihomo / xray-core / sing-box）**不随 Release 附带**，首次在 Web UI「内核引擎 → Cores」页安装，
  NGG 会强制校验 GitHub 官方域名 + SHA256 digest + 真实 `-v` 版本，任一失败即拒绝安装。
- 离线放置内核二进制的方法见 [`04-installation.md`](04-installation.md) 第 10 节。
- 统一 TUN 仅 Linux 可用，需要 `/dev/net/tun`、`CAP_NET_ADMIN` 与 iproute2：

```bash
ls -l /dev/net/tun || modprobe tun
sudo ./scripts/deploy-vps.sh install --version v1.0.0 --enable-tun   # 或手工把 autoroute 改为 true
```

- **已知能力边界**：TUN 读环回目前完成真实 L3 解析、IPv4 UDP/53 DNS 应答与 Fail-Closed 记录；
  **TUN 内 TCP 流终止需要 userspace TCP 协议栈，尚未实现**。TCP 数据面由 `127.0.0.1:18080` 的
  SOCKS5 / HTTP CONNECT userspace 入口承载。请勿把 NGG 当作已完成「全局 TUN 全流量接管」。
- Windows / macOS 明确返回 `ErrTUNUnsupported`，自动使用 userspace 入口。

---

## 8. 运维：升级 / 备份 / 恢复 / 卸载

```bash
# 升级（自动先备份 → 替换二进制与 Web UI → 健康检查失败自动回滚二进制；配置与数据不动）
sudo --preserve-env=GITHUB_TOKEN ./scripts/deploy-vps.sh upgrade --version v1.0.1

# 备份（/etc/ngg + /var/lib/ngg + web/dist + unit，含凭据，请加密保存）
sudo ./scripts/deploy-vps.sh backup
sudo ./scripts/deploy-vps.sh backup --output /root/ngg-before-change.tar.gz

# 恢复
sudo ./scripts/deploy-vps.sh restore --file /var/backups/ngg/ngg-backup-20261105T101500.tar.gz

# 体检（路径 / 权限 / 服务 / 端口 / TUN / 磁盘）
sudo ./scripts/deploy-vps.sh doctor
sudo ./scripts/deploy-vps.sh status

# 卸载（默认交互确认；--keep-data 保留配置与数据）
sudo ./scripts/deploy-vps.sh uninstall --keep-data --yes
```

手工等价操作：

| 目标 | 命令 |
| --- | --- |
| 升级 | `systemctl stop ngg` → 覆盖 `/opt/ngg/nggd` 与 `/opt/ngg/web/dist` → `systemctl start ngg` |
| 备份 | `tar -czf ngg-backup.tgz -C / etc/ngg var/lib/ngg opt/ngg/web/dist etc/systemd/system/ngg.service` |
| 回滚配置 | Web UI「配置管理」按备份 ID 回滚，或 `PUT /api/v1/cores/{id}/config/rollback` |
| 回滚内核版本 | `POST /api/v1/cores/{id}/rollback`（`version` 必填） |
| Token 轮换 | 清空 `/etc/ngg/ngg.yaml` 的 `token: ""` → `systemctl restart ngg` → 读取新 Token |

---

## 9. 故障排查

| 现象 | 原因 / 处理 |
| --- | --- |
| `curl: (22) 404` 下载 Release | 私有仓库未带 PAT；设置 `GITHUB_TOKEN` 或 `--token`。脚本会自动回退 GitHub API 资产接口 |
| SHA256 校验失败 | 包损坏或被替换；重新下载，不要加 `--skip-checksum` 硬闯 |
| `systemctl status ngg` 显示 `203/EXEC` | `/opt/ngg/nggd` 不存在或不可执行，重新 `install`；确认 `ExecStart` 路径 |
| 启动即退出、日志 `load config` | `/etc/ngg/ngg.yaml` 路径错误或 YAML 语法错误；`nggd -version` 可先验证二进制 |
| 健康检查 401 | Token 不对；`grep token /etc/ngg/ngg.yaml`，注意脚本已把 Token 写入配置文件 |
| 健康检查 403 | 请求来源不是回环；确认没有把 `server.listen` 改成 `0.0.0.0`，Nginx 必须反代到 `127.0.0.1` |
| Web UI 显示「尚未构建」 | 包内 `web-dist` 未复制到 `/opt/ngg/web/dist`，或 `server.web_dir` 仍是相对路径 |
| 重启后「状态丢失」 | `storage.dir` 仍是相对路径 `data`，SQLite 落在 `/opt/ngg/data/`；改为 `/var/lib/ngg` |
| Nginx 502 | 后端没起来；`systemctl status ngg`、`journalctl -u ngg -n 80` |
| WebSocket 反复断开 | 反代缺少 `Upgrade` / `Connection "upgrade"` 头，或 `proxy_read_timeout` 太短 |
| TUN 不生效 | 检查 `/dev/net/tun`、`CAP_NET_ADMIN`、`autoroute: true`；容器需 `--cap-add NET_ADMIN` |
| 端口被占用 | `ss -ltnp | grep -E '9090|18080|18053'`；改 `--port` 或配置里的端口 |
| 服务写配置失败 | `ProtectSystem=full` 下 `/etc` 只读；确认 `ReadWritePaths=/etc/ngg ...`，且配置属主为 `ngg` |

---

## 10. 验收清单

- [ ] `systemctl is-active ngg` = `active`，`systemctl is-enabled ngg` = `enabled`
- [ ] `curl -H "Authorization: Bearer <TOKEN>" http://127.0.0.1:9090/api/v1/health` 返回 200
- [ ] 浏览器通过 SSH 隧道或 `https://<域名>/?token=<TOKEN>` 打开管理界面，Web UI 不是「尚未构建」状态页
- [ ] `ss -ltn` 里 9090 只绑定 `127.0.0.1`，公网扫描不到 9090 / 18080 / 18053
- [ ] `/etc/ngg/ngg.yaml` 权限 `0600`，四个路径字段均为绝对路径
- [ ] `GET /api/v1/cores` 可见三个内置内核；安装后多核可并行启动
- [ ] `sudo ./scripts/deploy-vps.sh doctor` 无关键 FAIL（TUN 可选）
- [ ] 已执行一次 `backup` 并把备份文件加密存放到 VPS 之外
- [ ] Token、Basic Auth 密码、PAT 已保存到密码管理器，未出现在仓库 / 聊天 / 截图里

---

## 11. 相关文档

| 文档 | 内容 |
| --- | --- |
| [`DEPLOY.md`](DEPLOY.md) | 全场景部署速查（Docker / systemd / 软路由 / 3X-UI / Remnawave 配置兼容） |
| [`INSTALL-WINDOWS.md`](INSTALL-WINDOWS.md) | Windows 安装包 / 服务 / 卸载 |
| [`04-installation.md`](04-installation.md) | Phase 1 基线安装说明、配置字段、离线内核、systemd unit 细节 |
| [`05-troubleshooting.md`](05-troubleshooting.md) | 更细的故障排查 |
| [`07-stage2-network-engine.md`](07-stage2-network-engine.md) | 统一网络引擎与 TUN 能力边界 |
