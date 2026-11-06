# NGG 部署到 VPS / 云服务器（完整步骤）

> 本文是**从一台空白 VPS 到可访问管理界面**的完整本地部署手册，覆盖 Ubuntu / Debian / Rocky / AlmaLinux
> 的 systemd 原生安装、Docker Compose、私有仓库 Release 下载、防火墙、HTTPS 反向代理、备份与回滚。
> Windows 安装请见 [`INSTALL-WINDOWS.md`](INSTALL-WINDOWS.md)；其它场景速查见 [`DEPLOY.md`](DEPLOY.md)。

---

## 0. 先明确安全边界（必读）

| 约束 | 原因 |
| --- | --- |
| 控制 API 只监听 `127.0.0.1:9090`，且所有 `/api/` 请求必须带 Bearer Token | 代码里 socket 层与中间件**双重**校验回环地址，非回环直接 403。把 `server.listen` 改成 `0.0.0.0` 不是「开放远程管理」，而是直接失效 |
| 远程管理走 **SSH 隧道**，或 **Nginx 公网入口（随机秘密路径 + Cookie 门禁 + HTTPS）** 反代到本机 9090 | 这是唯二被支持的远程访问方式；**不要**给 Nginx 加 `auth_basic`，它会覆盖 Web UI 的 Bearer 头导致 401 |
| 云安全组 / 防火墙只放行 22、可选的 80/443，**绝不放行 9090 / 18080 / 18053** | 9090 是控制面，18080 是代理入口，18053 是 DNS |
| `configs/ngg.yaml`（含 Token）与 `data/`（含节点凭据、私钥）不入库、不外传 | 已 gitignore；备份文件请加密保存 |
| 仓库当前为 **Private** | 下载 Release 资产需要 GitHub PAT；不要把 PAT 写进脚本或提交 |

> `network.ingress.listen` 若改为 `0.0.0.0` 开放局域网代理，**必须**同时设置 `auth_user` / `auth_pass`，
> 否则 NGG 会主动拒绝启用该入口（`ErrIngressInsecure`）。

---

## 1. 准备 VPS

**最低配置**：1 vCPU / 1 GB RAM / 10 GB 磁盘；x86_64 或 ARM64 均可（Release 提供 amd64 / arm64 / arm / 386）。

**支持的发行版**（`deploy-vps.sh` 会自动读 `/etc/os-release` 并选择包管理器，无需手工区分）：

| 家族 | 发行版 | 包管理器 |
| --- | --- | --- |
| Debian | Debian 11/12/13、Ubuntu 20.04~24.04、Linux Mint、Pop!_OS、Kali、Deepin、UOS、Raspberry Pi OS | `apt-get` |
| RHEL | RHEL 8/9、CentOS Stream、Rocky、AlmaLinux、Fedora、Amazon Linux、Oracle Linux | `dnf`（老版本自动回退 `yum`） |
| SUSE | openSUSE Leap / Tumbleweed、SLES | `zypper` |
| Arch | Arch Linux、Manjaro、EndeavourOS | `pacman` |
| Alpine | Alpine Linux | **无 systemd，请改用 `deploy-vps.sh docker`** |

脚本还会自动处理这些差异：`/sbin/nologin` vs `/usr/sbin/nologin`、`sites-enabled` vs `conf.d`、
`apache2-utils` vs `httpd-tools`、`ufw` vs `firewalld`、`python3` 缺失等。用 `--no-install-deps`
可禁止自动装包，改为自己准备依赖。

```bash
ssh root@<VPS_IP>
adduser nggadmin && usermod -aG sudo nggadmin     # 建议用非 root 账号日常运维
# 依赖（curl / tar / openssl / nginx / certbot）由脚本自动安装，无需手工执行
timedatectl set-timezone Asia/Shanghai           # 可选
systemctl --version                              # 确认是 systemd
```

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

# 在线安装指定 Release（脚本自动识别 amd64 / arm64 / arm / 386 与发行版）
sudo --preserve-env=GITHUB_TOKEN ./scripts/deploy-vps.sh install --version v2.6.4

# 「先用公网 IP + 端口 + 16 位随机后缀访问」（最常用）
sudo --preserve-env=GITHUB_TOKEN ./scripts/deploy-vps.sh install --version v2.6.4 \
  --public --web-port 8080 --configure-firewall

# 之后把域名 A 记录指向 VPS，再补 SSL（相同或再次执行 install 均可）
sudo --preserve-env=GITHUB_TOKEN ./scripts/deploy-vps.sh install --version v2.6.4 \
  --domain ngg.example.com --with-tls --email you@example.com --configure-firewall

# 离线 / 内网：在有网机器下载 Release 包与 SHA256SUMS，scp 到 VPS 后
sudo ./scripts/deploy-vps.sh install --file /root/ngg-v2.6.4-linux-amd64.tar.gz --public
```

`--public` 会打印形如 `http://203.0.113.7:8080/9f3ac1b27e4d6058` 的地址，并把秘密路径、
域名、Token 一起写到 `/etc/ngg/web-gate.txt`（`0600`）。**首次在浏览器打开这个完整秘密路径**
会写入 Cookie 并跳转到 `/`，之后 Web UI 的 `/assets`、`/api/v1` 等绝对路径才能正常加载。
换浏览器 / 清 Cookie 后需要再打开一次秘密路径。

> `--file` 时脚本会在同目录寻找 `SHA256SUMS` 或 `<包名>.sha256` 并校验；
> 自构建包可加 `--skip-checksum`（不推荐）。

### 2.3 脚本实际做了什么（可审计）

1. 下载 `ngg-<tag>-linux-<arch>.tar.gz` + `SHA256SUMS`，**校验 SHA256 通过才解包**；私有仓库自动回退到 GitHub API 资产接口；
2. 创建系统用户 `ngg` 与目录 `/opt/ngg`、`/etc/ngg`、`/var/lib/ngg`、`/var/log/ngg`；
3. 安装 `nggd`、`web/dist`、`cores.d` 描述文件；
4. 从模板生成 `/etc/ngg/ngg.yaml`：**全部改为绝对路径**、生成 32 字节随机 API Token、权限 `0600`；
5. 生成 systemd unit（含 `CAP_NET_ADMIN` 供 TUN、`ProtectSystem=full` + `ReadWritePaths` 最小可写面）；
6. 探测发行版并自动安装缺失依赖（curl / tar / openssl / python3 / nginx / certbot / 防火墙工具）；
7. 可选配置 ufw/firewalld 与 Nginx 公网入口（16 位随机秘密路径 + Cookie 门禁 + WebSocket；`--with-tls` 用 certbot 签发证书并生成 443 站点）；
8. `systemctl enable --now ngg`，轮询 `/api/v1/health` 直到通过；
9. 打印本机地址、`IP:端口/随机后缀`、域名/HTTPS 地址与 SSH 隧道命令。

### 2.4 常用参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--version vX.Y.Z` | — | Release tag；`install` / `upgrade` 必填（除非 `--file`） |
| `--file PATH` | — | 本地 tar.gz，离线部署 |
| `--token TOKEN` | `$GITHUB_TOKEN` | GitHub PAT（私有仓库） |
| `--public` | 关 | 启用公网入口：`http://公网IP:端口/16位随机后缀` |
| `--web-port N` | 8080 | 公网入口监听端口 |
| `--public-ip IP` | 自动探测 | 手动指定公网 IP（NAT / 内网穿透场景） |
| `--domain FQDN` | — | 绑定域名（同时启用 Nginx，`server_name` + 80 监听） |
| `--with-tls` / `--email` | — | `certbot certonly --webroot` 签发证书并生成 443 站点 |
| `--configure-firewall` | 关 | ufw/firewalld 放行 22/80/443/`WEB_PORT`；防火墙未启用时只写规则、不擅自激活 |
| `--no-install-deps` | 关 | 禁止自动装包，自行准备依赖 |
| `--enable-tun` | 关 | 打开 `network.tun.autoroute`（需 `/dev/net/tun`） |
| `--port N` | 9090 | NGG 控制面端口（**仅本机监听**） |
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

## 3. 方式 B：Docker Compose（容器环境 / Alpine / 无 systemd）

一键（脚本会自动生成 `.env` 和随机 Token，兼容 `docker compose` v2 与旧版 `docker-compose`）：

```bash
git clone https://github.com/welkacton/ngg-next-generation-gateway.git
cd ngg-next-generation-gateway
./scripts/deploy-vps.sh docker                     # 不需要 root（需在当前用户属于 docker 组）
sudo ./scripts/deploy-vps.sh docker --public --web-port 8080   # 需要公网入口时加 sudo
docker compose logs -f ngg
```

手工等价步骤：

```bash
cp .env.example .env
# 可选：在 .env 里设置 NGG_TOKEN（留空则首次启动自动生成并写回 runtime/configs/ngg.yaml）
docker compose up -d --build
docker compose logs -f ngg          # 日志里会打印自动生成的 API Token
```

- Linux 上 `network_mode: host` 保证 LAN 入口与 TUN 正常；Docker Desktop 需改用 `docker-compose.yml` 里注释掉的 `ports`。
- 配置持久化在 `./runtime/configs/ngg.yaml`，数据在 `./runtime/data/`，升级不会丢。
- 升级：`git pull && docker compose up -d --build`。
- 远程访问同样走 SSH 隧道或宿主机 Nginx 公网入口（第 5 节）；`docker --public` 会自动配置宿主机 Nginx。
- **Alpine / OpenRC** 没有 systemd，`install` 会明确拒绝并提示改用本节的 `docker` 子命令。

---

## 4. 方式 C：纯手工 systemd 安装（不使用脚本）

适合需要逐条审计或自定义路径的场景。以下命令以 root 执行，路径与脚本默认值一致。

```bash
# 1) 下载并校验 Release
export VERSION=v2.6.4 ARCH=amd64 TOKEN=ghp_xxx
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

### 5.2 公网 IP + 端口 + 16 位随机后缀（阶段一）

```bash
sudo --preserve-env=GITHUB_TOKEN ./scripts/deploy-vps.sh install --version v2.6.4 \
  --public --web-port 8080 --configure-firewall
```

脚本会：

1. 生成 16 位十六进制随机后缀（如 `9f3ac1b27e4d6058`），写入 `/etc/ngg/web-gate.txt`（0600）；
2. 生成 Nginx 站点：监听 `0.0.0.0:8080`，**只有** `/<后缀>` 这条路径返回 `Set-Cookie` 并 302 到 `/`；
   其它路径在没有该 Cookie 时一律返回 404；
3. 反代到 `127.0.0.1:9090`（NGG 仍然只监听回环，中间件照常校验）；
4. 打印 `http://<公网IP>:8080/<16位后缀>`。

**访问方式**：先在浏览器打开这个**完整秘密路径**，Cookie 写入后自动跳到 `/`，此时粘贴
`?token=<TOKEN>` 完成绑定。换浏览器 / 清 Cookie 后重开一次秘密路径即可。

> 为什么用 Cookie 门禁而不是把 Web UI 挂到 `/<后缀>/` 子路径？Vite 产物里的 `/assets/...`、
> `/api/v1/...` 都是绝对路径，挂子路径会 404；Cookie 方案不需要重新编译前端。
>
> ⚠️ **不要在 Nginx 上加 `auth_basic`**：浏览器会把 Web UI 的 `Authorization: Bearer <token>`
> 头替换成 Basic 凭据，Nginx 会直接返回 401，请求到不了 NGG。这是早期版本的一个坑，现已移除。

### 5.3 绑定域名 + SSL 证书（阶段二）

域名 A 记录指向 VPS 公网 IP、80/443 放行后：

```bash
sudo --preserve-env=GITHUB_TOKEN ./scripts/deploy-vps.sh install --version v2.6.4 \
  --domain ngg.example.com --with-tls --email you@example.com --configure-firewall
# 或仅补证书（脚本会重新生成站点配置，秘密路径会更换并写入 web-gate.txt）
```

脚本用 `certbot certonly --webroot -w /var/www/html -d <域名>` 签发证书
（不改写 Nginx 配置，避免破坏门禁），成功后追加一个 443 server 块：

```
https://ngg.example.com/<16位后缀>   → Cookie 门禁 → 127.0.0.1:9090
http://<公网IP>:8080/<16位后缀>      → 仍可用（HTTP，仅建议临时使用）
```

手工等价步骤见 `deploy/nginx/ngg.conf.example`；证书路径为
`/etc/letsencrypt/live/<域名>/fullchain.pem` 与 `privkey.pem`。

- WebSocket `/api/v1/ws` 需要 `Upgrade` / `Connection "upgrade"` 头，模板已配置。
- 续期：`certbot renew` 由发行版的 systemd timer / cron 自动执行，`nginx -t` 可随时校验。
- **不要**把 `server.listen` 改成 `0.0.0.0` 来「省掉 Nginx」：中间件会拒绝所有非回环请求。

---

## 6. 防火墙与云安全组

```bash
# ufw（Debian / Ubuntu）
ufw allow OpenSSH
ufw allow 80/tcp && ufw allow 443/tcp && ufw allow 8080/tcp
ufw status                       # 确认规则后再 ufw enable，避免把自己锁在门外
# 注意：不要 ufw allow 9090

# firewalld（RHEL / Rocky / Alma / Fedora / openSUSE / Arch）
firewall-cmd --permanent --add-service=ssh --add-service=http --add-service=https
firewall-cmd --permanent --add-port=8080/tcp
firewall-cmd --reload
```

云厂商控制台的安全组同样只放行 22（建议限制来源 IP）、80、443、`WEB_PORT`。
`deploy-vps.sh install --configure-firewall` 会按发行版选择 ufw/firewalld 添加上述规则，
但**不会擅自启用**原本关闭的 ufw。

---

## 7. 内核与统一 TUN

- 三个内核（mihomo / xray-core / sing-box）**不随 Release 附带**，首次在 Web UI「内核引擎 → Cores」页安装，
  NGG 会强制校验 GitHub 官方域名 + SHA256 digest + 真实 `-v` 版本，任一失败即拒绝安装。
- 离线放置内核二进制的方法见 [`04-installation.md`](04-installation.md) 第 10 节。
- 统一 TUN 仅 Linux 可用，需要 `/dev/net/tun`、`CAP_NET_ADMIN` 与 iproute2：

```bash
ls -l /dev/net/tun || modprobe tun
sudo ./scripts/deploy-vps.sh install --version v2.6.4 --enable-tun   # 或手工把 autoroute 改为 true
```

- **已知能力边界**：TUN 读环回目前完成真实 L3 解析、IPv4 UDP/53 DNS 应答与 Fail-Closed 记录；
  **TUN 内 TCP 流终止需要 userspace TCP 协议栈，尚未实现**。TCP 数据面由 `127.0.0.1:18080` 的
  SOCKS5 / HTTP CONNECT userspace 入口承载。请勿把 NGG 当作已完成「全局 TUN 全流量接管」。
- Windows / macOS 明确返回 `ErrTUNUnsupported`，自动使用 userspace 入口。
- **`--enable-tun` 的真实含义**：只是把 `autoroute` 打开、让 NGG 尝试创建/配置 TUN；由于 TCP 流终止
  未实现，开启后 TCP 包会被 Fail-Closed 丢弃。**不要**把它当「已经全局代理」用于生产。
  完整缺口与实施计划见 [`TUN-GLOBAL-TAKEOVER.md`](TUN-GLOBAL-TAKEOVER.md)。

---

## 8. 运维：升级 / 备份 / 恢复 / 卸载

```bash
# 升级（自动先备份 → 替换二进制与 Web UI → 健康检查失败自动回滚二进制；配置与数据不动）
sudo --preserve-env=GITHUB_TOKEN ./scripts/deploy-vps.sh upgrade --version v2.6.4

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
| 浏览器一直 404，打不开面板 | 没先访问 `http://IP:端口/<16位后缀>` 设置门禁 Cookie；从 `/etc/ngg/web-gate.txt` 取回秘密路径 |
| Web UI 报 401、`/api/v1/*` 全失败 | Nginx 上加了 `auth_basic`；移除它（会覆盖 Bearer 头）。改用秘密路径 + Cookie + NGG Token |
| certbot 签发失败 | 域名 A 记录未生效 / 80 被占用 / 云安全组没放行 80；先用 `curl -I http://<域名>/.well-known/acme-challenge/test` 验证 |
| `Unknown lvalue` / `useradd: invalid shell` | 老发行版差异；脚本已自动探测 `/sbin/nologin` 与 systemd 版本，必要时用 `--no-install-deps` 手工准备 |
| Alpine 提示无 systemd | 改用 `./scripts/deploy-vps.sh docker` |
| WebSocket 反复断开 | 反代缺少 `Upgrade` / `Connection "upgrade"` 头，或 `proxy_read_timeout` 太短 |
| TUN 不生效 | 检查 `/dev/net/tun`、`CAP_NET_ADMIN`、`autoroute: true`；容器需 `--cap-add NET_ADMIN` |
| 端口被占用 | `ss -ltnp | grep -E '9090|18080|18053'`；改 `--port` 或配置里的端口 |
| 服务写配置失败 | `ProtectSystem=full` 下 `/etc` 只读；确认 `ReadWritePaths=/etc/ngg ...`，且配置属主为 `ngg` |

---

## 10. 验收清单

- [ ] `systemctl is-active ngg` = `active`，`systemctl is-enabled ngg` = `enabled`
- [ ] `curl -H "Authorization: Bearer <TOKEN>" http://127.0.0.1:9090/api/v1/health` 返回 200
- [ ] 浏览器通过 SSH 隧道 / `http://IP:端口/<后缀>` / `https://<域名>/<后缀>` 打开管理界面，Web UI 不是「尚未构建」状态页
- [ ] `ss -ltn` 里 9090 只绑定 `127.0.0.1`，公网扫描不到 9090 / 18080 / 18053
- [ ] `/etc/ngg/ngg.yaml` 权限 `0600`，四个路径字段均为绝对路径
- [ ] 若启用 `--public`：先打开秘密路径 → 跳转 `/` → 用 Token 绑定成功；直接访问 `/` 应返回 404
- [ ] 若启用 `--with-tls`：`nginx -t` 通过，`https://<域名>/<后缀>` 证书有效，HTTP 仅用于 ACME
- [ ] `GET /api/v1/cores` 可见三个内置内核；安装后多核可并行启动
- [ ] `sudo ./scripts/deploy-vps.sh doctor` 无关键 FAIL（TUN 可选）
- [ ] 已执行一次 `backup` 并把备份文件加密存放到 VPS 之外
- [ ] Token、秘密路径、PAT 已保存到密码管理器，未出现在仓库 / 聊天 / 截图里

---

## 11. 相关文档

| 文档 | 内容 |
| --- | --- |
| [`DEPLOY.md`](DEPLOY.md) | 全场景部署速查（Docker / systemd / 软路由 / 3X-UI / Remnawave 配置兼容） |
| [`INSTALL-WINDOWS.md`](INSTALL-WINDOWS.md) | Windows 安装包 / 服务 / 卸载 |
| [`04-installation.md`](04-installation.md) | Phase 1 基线安装说明、配置字段、离线内核、systemd unit 细节 |
| [`05-troubleshooting.md`](05-troubleshooting.md) | 更细的故障排查 |
| [`07-stage2-network-engine.md`](07-stage2-network-engine.md) | 统一网络引擎与 TUN 能力边界 |
| [`TUN-GLOBAL-TAKEOVER.md`](TUN-GLOBAL-TAKEOVER.md) | TUN 一键全局接管的可行性、缺口与实施计划 |
