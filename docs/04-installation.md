# NGG 安装说明

> **适用版本**：Phase 1 / Stage 1
> **首发平台**：**Linux x86_64 优先**（后续 Linux ARM64 / OpenWrt / Windows / macOS / Android / iOS / HarmonyOS / iPad / TV / 软路由）
> **配置契约**：本文字段与 `internal/config/config.go` 一一对应；仓库提供无密钥模板 `configs/ngg.example.yaml`，
> 首次部署复制到 `configs/ngg.yaml`（含自动生成的 token，已在 `.gitignore` 中排除）。
> **重要**：Phase 1 **不接管系统 TUN / 全局流量**，NGG 代码路径不创建 TUN、不改全局路由、不抢 DNS（统一 TUN 属 Stage 2，见 `05-troubleshooting.md` 第 7 条）。

---

## 1. 安装方式总览

| 场景 | 方式 | 章节 |
| --- | --- | --- |
| 生产 / 日常使用 | Linux x86_64 二进制部署 + systemd（`scripts/install.sh`） | 第 2 ~ 5 节 |
| 开发 / 定制 | 从源码构建 | 第 7 节 |
| Windows / macOS | 仅开发运行 | 第 9 节 |
| 无外网环境 | 离线放置三个内核二进制 | 第 10 节 |
| 版本更新 / 移除 | 升级与卸载 | 第 11 节 |

---

## 2. 二进制部署（Linux x86_64）

仓库已提供安装脚本 `scripts/install.sh`（默认前缀 `/opt/ngg`、配置 `/etc/ngg`、数据 `/var/lib/ngg`、服务名 **`ngg.service`**）：

```bash
# 在源码树根目录、以 root 执行
sudo PREFIX=/opt/ngg CONF=/etc/ngg DATA=/var/lib/ngg bash scripts/install.sh

# 该脚本会：交叉编译 linux/amd64 二进制 → 创建系统用户 ngg →
# 安装 /opt/ngg/nggd、/etc/ngg/ngg.yaml、/etc/ngg/cores.d、web/dist →
# 生成 /etc/systemd/system/ngg.service（含 CAP_NET_ADMIN 预置，见第 4 节）→ daemon-reload

# ⚠ 重要：install.sh 复制的配置里 storage.dir / logging.dir / server.web_dir 仍是相对路径，
#   在 systemd 下相对 WorkingDirectory(/opt/ngg) 解析。生产环境请先手工改为绝对路径：
#     storage.dir:    /var/lib/ngg
#     logging.dir:    /var/lib/ngg/logs
#     server.web_dir: /opt/ngg/web/dist
#   否则数据会落在 /opt/ngg/data/，与第 3 节目录规划不一致（见 05-troubleshooting.md 第 10 条）。
# 首次启动会生成 API Token 并写回 /etc/ngg/ngg.yaml
sudo systemctl enable --now ngg
sudo systemctl status ngg
sudo journalctl -u ngg -n 50 --no-pager
sudo grep 'token:' /etc/ngg/ngg.yaml
```

脚本可通过环境变量调整：`PREFIX`、`CONF`、`DATA`、`SERVICE_USER`。

**手动部署**等价步骤：

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin ngg
sudo mkdir -p /etc/ngg/cores.d /var/lib/ngg /var/lib/ngg/logs /opt/ngg/web
sudo install -m 0755 bin/nggd /opt/ngg/nggd
sudo install -m 0600 configs/ngg.example.yaml /etc/ngg/ngg.yaml
sudo cp -r web/dist /opt/ngg/web/ 2>/dev/null || true
sudo cp configs/cores.d/*.yaml /etc/ngg/cores.d/ 2>/dev/null || true
# 编辑 /etc/ngg/ngg.yaml：把 storage.dir / logging.dir / server.web_dir 改为绝对路径
sudo chown -R ngg:ngg /etc/ngg /var/lib/ngg /opt/ngg/web
# 将第 4 节 unit 内容保存为 /etc/systemd/system/ngg.service
sudo systemctl daemon-reload && sudo systemctl enable --now ngg
```

> 服务仅监听 `127.0.0.1`，中间件还会二次校验 `RemoteAddr` 必须是回环；非本机来源返回 `403`（ADR-005）。默认端口 **9090**。

---

## 3. 目录规划

**源码树默认**（`configs/ngg.yaml` 的 `storage.dir` / `logging.dir` / `server.web_dir` 均为**相对路径**，相对启动时的进程工作目录解析）：

| 路径 | 用途 |
| --- | --- |
| `configs/ngg.yaml` | NGG 主配置（含自动生成的 token，权限 `0600`） |
| `configs/cores.d/` | 内核能力描述文件（`*.yaml` / `*.yml` / `*.json`） |
| `data/ngg.db` | SQLite 状态持久化（desired_state / 备份索引 / 安装记录 / 审计） |
| `data/cores/<id>/` | **每个内核独立目录**：`bin/`、`versions/`、`config.*`、`backups/`、`.staging/` |
| `data/logs/` | 日志目录 |
| `web/dist/` | 前端构建产物（`server.web_dir`） |
| `bin/nggd` | 后端二进制 |

**生产部署**（`scripts/install.sh` 默认）：

| 路径 | 用途 | 归属 |
| --- | --- | --- |
| `/opt/ngg/nggd` | 服务二进制 | `root:root` |
| `/opt/ngg/web/dist/` | 前端产物（若构建时存在） | `ngg:ngg` |
| `/etc/ngg/ngg.yaml` | 主配置（含 token） | `ngg:ngg`，`0600` |
| `/etc/ngg/cores.d/` | 新增内核描述文件 | `ngg:ngg` |
| `/var/lib/ngg/ngg.db` | SQLite 状态持久化 | `ngg:ngg` |
| `/var/lib/ngg/cores/<id>/` | 每个内核独立目录 | `ngg:ngg` |

> **systemd 部署务必使用绝对路径**：`scripts/install.sh` 生成的 unit 里 `WorkingDirectory=/opt/ngg`，若配置仍为 `storage.dir: data`，SQLite 会落在 `/opt/ngg/data/ngg.db` 而不是 `/var/lib/ngg/ngg.db`。请把 `storage.dir` / `logging.dir` / `server.web_dir` 改为绝对路径（见第 2 节），否则会出现「重启后状态看似丢失」（`05-troubleshooting.md` 第 10 条）。
>
> 说明：NGG 内部已用 `filepath.Abs` 规范化**内核目录**（`internal/engine/config.go` / `version.go` 构造函数），因此相对路径**已不是功能缺陷**，只是数据落点与第 3 节规划不一致；`ngg.db` 仍由 `config.DBPath()` 用 `storage.dir` 拼接，落点依赖进程工作目录。

**内核独立原则**（约束 4）：每个内核独立安装、独立存储、独立运行，例如：

```
data/cores/
├── mihomo/     { bin/mihomo, config.yaml, backups/, versions/, .staging/ }
├── xray-core/  { bin/xray,   config.json, backups/, versions/, .staging/ }
└── sing-box/   { bin/sing-box, config.json, backups/, versions/, .staging/ }
```

> 三个内置内核 id 为 `mihomo` / `xray-core` / `sing-box`（见 `pkg/contracts/types.go`），**不是** `xray` / `singbox`。
> 源码树 `configs/cores.d/` 与生产 `/etc/ngg/cores.d/` 是**同一机制**，由 `cores.descriptor_dir` 指定路径，改配置即可切换。

---

## 4. systemd unit 示例

`scripts/install.sh` 生成的是 `/etc/systemd/system/ngg.service`（**服务名 `ngg`，不是 `nggd`**）：

```ini
[Unit]
Description=NGG Next-Generation Gateway (Phase 1)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=ngg
WorkingDirectory=/opt/ngg
ExecStart=/opt/ngg/nggd -config /etc/ngg/ngg.yaml
Restart=on-failure
RestartSec=3
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE
NoNewPrivileges=false

[Install]
WantedBy=multi-user.target
```

**关于权限的如实说明**

- 脚本预置了 `CAP_NET_ADMIN CAP_NET_BIND_SERVICE` 与 `NoNewPrivileges=false`，这是**为 Stage 2 统一 TUN / 低端口绑定预留**的能力；
- **Phase 1 代码路径完全不使用它们**：NGG 不创建 TUN、不改全局路由、不抢 DNS，内核一律 Worker Mode（默认配置显式 `tun.enable: false` / `dns.enable: false`）；
- 若追求最小权限，Phase 1 可移除这两项能力并设 `NoNewPrivileges=true`、`ProtectSystem=strict` + `ReadWritePaths=/var/lib/ngg /etc/ngg`——**不影响 Phase 1 任何功能**；
- 若参考旧资料把内核自身配置成开 TUN / 劫持 DNS / 抢默认路由，那是**违背约束 2**，且与脚本预置能力无关。

`main.go` 支持的 flag：`-config`（配置文件路径，默认 `configs/ngg.yaml`）、`-no-restore`（跳过启动时的状态恢复）、`-version`（打印版本退出）。

常用运维命令：

```bash
sudo systemctl start|stop|restart|status ngg
sudo journalctl -u ngg -f
```

---

## 5. 首次启动与 Token

- `config.Load` 在 `server.token` 为空时**生成 32 字节随机 Token**，并**写回配置文件本身**（`configs/ngg.yaml`，权限 `0600`，不是单独的 token 文件），同时在启动日志打印 `NGG API Token: <token>`；
- 后续重启**复用**配置文件中的 token，不会重新生成；首次写回由 `yaml.v3` 完成，会把整个配置文件**重排为 4 空格缩进**（手工注释位置 / 缩进变化属正常现象）；
- 可用环境变量 **`NGG_TOKEN`** 覆盖（适合容器 / 自动化）；
- 轮换 Token：清空配置文件中的 `token: ""` 并重启（生成新值，旧 Token 立即失效）；
- 所有 `/api/v1/**` 请求都需 `Authorization: Bearer <token>`；WebSocket 因浏览器无法设置自定义头，可用 `?token=<token>`（仅 `/api/v1/ws` 生效）。见 `02-api.md`。

```bash
TOKEN=$(sudo awk '/^[[:space:]]*token:/{print $2}' /etc/ngg/ngg.yaml)
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:9090/api/v1/system
```

> 配置文件含 Token，**不要**提交到版本库，也不要放宽到 `0777`。前端首次可用 `http://127.0.0.1:9090/?token=<TOKEN>` 完成一次性绑定。

---

## 6. 主配置 `configs/ngg.yaml` 字段说明

以下为仓库实际配置（**五个顶层分组，没有 `auth` / `store` / `log` / `web` / `system` 分组**）：

```yaml
# 控制 API（仅本机 + Bearer Token，ADR-005）
server:
  listen: 127.0.0.1        # 固定本机；socket 层与中间件双重限制
  port: 9090               # REST / WS 端口
  token: ""                # 留空 → 首次启动自动生成并写回本文件（0600）
  web_dir: web/dist        # 前端构建产物目录

# 状态持久化（modernc.org/sqlite，纯 Go 免 CGO，ADR-003）
storage:
  dir: data                # SQLite 为 <dir>/ngg.db；内核目录为 <dir>/cores/<id>/

# 内核管理
cores:
  descriptor_dir: configs/cores.d   # 描述文件目录（生产可改 /etc/ngg/cores.d）
  port_base: 17890                  # PortAllocator 起始端口
  port_stride: 10                   # 每个内核占用的端口块跨度（5 个端口：mixed/socks/http/api/dns）

# 进程监管
supervisor:
  stop_timeout: 5s                  # 优雅停止超时
  restart_max: 3                    # 单内核独立崩溃恢复最大重试次数
  restart_backoff: 2s               # 重试退避

# 日志
logging:
  level: info                       # 日志级别
  dir: data/logs                    # 日志目录
  ring_capacity: 4000               # 内存日志环形缓冲条数（WS 推送 / GET /logs 来源）
```

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `server.listen` | `127.0.0.1` | **不可改为公网地址**（约束 9 / ADR-005） |
| `server.port` | `9090` | REST / WS 端口 |
| `server.token` | `""` | 空则自动生成并写回本文件 |
| `server.web_dir` | `web/dist` | 前端产物目录 |
| `storage.dir` | `data` | 运行数据根目录，须可写且**持久**（见 `05` 第 10 条） |
| `cores.descriptor_dir` | `configs/cores.d` | 新增内核描述文件目录 |
| `cores.port_base` | `17890` | 端口分配起点；`Validate()` 要求 `>=1024` 且 `port_base + 200*port_stride <= 65535` |
| `cores.port_stride` | `10` | 每内核端口块跨度，要求 `>=5`（每个角色一个端口） |
| `supervisor.stop_timeout` | `5s` | Go duration 字符串 |
| `supervisor.restart_max` | `3` | 独立崩溃恢复重试上限 |
| `supervisor.restart_backoff` | `2s` | 退避 |
| `logging.level` | `info` | 日志级别 |
| `logging.dir` | `data/logs` | 日志目录 |
| `logging.ring_capacity` | `4000` | 环形缓冲条数 |
| `build.go_binary` | `""` | 交叉编译用的 Go 工具链绝对路径。服务以 systemd/daemon 启动时 PATH 常不含 `go`，且 `-trimpath` 构建的 `runtime.GOROOT()` 为空；此时必须显式配置，或设置环境变量 `NGG_GO_BINARY`。未找到编译器时 `POST /platforms/build` 返回 `503` 并给出明确原因 |

**环境变量覆盖**（优先级高于配置文件）：`NGG_LISTEN`、`NGG_PORT`、`NGG_DATA_DIR`、`NGG_TOKEN`。

> `storage.dir` / `logging.dir` / `server.web_dir` 为**相对路径**时相对进程工作目录解析；源码树开发用默认值即可，systemd 部署务必改为绝对路径（见第 2、3 节）。

> **下载安全是代码内置、不可通过配置关闭的**：`internal/engine/version.go` 强制校验 GitHub Release Assets API 的 `digest: sha256:...`、下载 URL 主机白名单（HTTPS + `github.com` / `*.githubusercontent.com`）、以及解包后执行二进制读取到的真实版本；无 digest / 摘要不符 / 版本不符一律 Fail-Closed 拒绝安装。配置文件里**没有** `verify_sha256` 之类的开关。

---

## 7. 从源码构建

环境：**Go 1.26+**（`go.mod` 声明 `go 1.26.0`，由 `modernc.org/sqlite` 决定）、**Node 20+**。

```bash
git clone <仓库地址> ngg && cd ngg

make build            # 仅后端 → bin/nggd
make frontend         # 前端 → web/dist
make test             # go test ./... -count=1
make vet              # go vet ./...
make dist             # vet + test + 后端 + 前端，完整分发构建
make run              # 构建后端并 ./bin/nggd -config configs/ngg.yaml

# 开发模式（注意：make dev 仅启动前端 Vite）
bash scripts/dev.sh   # 同时启动后端 :9090 与 Vite :5173
# 或：终端 A 跑 make run，终端 B 跑 make dev

# 本地运行
./bin/nggd -config configs/ngg.yaml
```

> 纯 Go SQLite（免 CGO）意味着**无需 gcc / C 工具链**，交叉编译：
> `bash scripts/build.sh linux/arm64 windows/amd64`（默认 `linux/amd64`，内部用 `CGO_ENABLED=0`）。

---

## 8. 前端构建产物放置

```bash
cd web && npm install && npm run build      # 生成 web/dist
```

- 路径由 **`server.web_dir`** 指定（默认 `web/dist`），需与 `configs/ngg.yaml` 一致；生产部署由 `scripts/install.sh` 复制到 `$PREFIX/web/dist/`；
- 若 `web/dist/index.html` 不存在，后端 `handleStatic` 会返回一个**如实的状态页**（提示「Web 管理界面尚未构建」并给出构建命令），不会伪造仪表盘；
- 开发模式下 Vite dev server 通过 proxy 把 `/api` 转发到 `http://127.0.0.1:9090`；
- 后端 REST `/api/v1` 与静态资源由同一 `127.0.0.1:9090` 端口提供。

---

## 9. Windows / macOS 开发运行

> 仅用于**开发**，不是生产首发平台（首发为 Linux x86_64）。

```bash
# 后端
go run ./cmd/nggd -config configs/ngg.yaml
# 或：make run

# 前端
cd web && npm install && npm run dev
```

| 项 | 说明 |
| --- | --- |
| 配置路径 | 使用源码树 `configs/ngg.yaml`；必要时把 `storage.dir`、`logging.dir`、`server.web_dir` 改为本地可写目录 |
| 端口 | 默认 `9090`；`cores.port_base` / `port_stride` 需避开系统占用端口 |
| TUN | Phase 1 **不创建 TUN**，Windows / macOS 上**无需**安装 TUN 驱动或管理员权限 |
| 内核二进制 | 需准备对应平台的内核资产（`platforms` / `asset_templates` 中声明），否则 `ARCH_UNSUPPORTED` |
| 监听 | 仍为 `127.0.0.1` + Bearer Token |

---

## 10. 离线安装三个内核二进制

> **`POST /cores/{id}/install` 的请求体只有 `{version}` 一个字段**（省略即最新稳定版），**不支持**指定本地资产路径参数。因此离线场景采用「在有网侧下载并校验 → 直接把二进制放到内核独立目录 → 重启让 NGG 识别」的方式。

### 10.1 有网侧：下载并校验

在能访问 GitHub 的机器上，按各适配器 `release.asset_templates` 渲染出的资产名下载，并校验官方摘要：

```bash
# 资产名示例（以 linux/amd64 为准，实际由 adapter 的 asset_templates + arch_map 决定）
#   mihomo   : mihomo-linux-amd64-v1.19.33.gz
#   xray-core: Xray-linux-64.zip            （另有 Xray-linux-64.zip.dgst）
#   sing-box : sing-box-1.14.4-linux-amd64.tar.gz
curl -L -O https://github.com/<owner>/<repo>/releases/download/<tag>/<asset>
sha256sum <asset>          # 与 GitHub Release Assets API 的 digest 比对，必须一致
```

解包取出可执行文件（`zip` / `gz` / `tar.gz`），得到 `mihomo`、`xray`、`sing-box` 三个二进制。

### 10.2 目标机：放到内核独立目录

`versionManager.BinaryPath(id)` 为 `<storage.dir>/cores/<id>/bin/<BinaryName(goos)>`；`IsInstalled` 只检查该文件是否存在。因此：

```bash
DATA=data        # 生产为 /var/lib/ngg
sudo install -D -m 0755 ./mihomo   "$DATA/cores/mihomo/bin/mihomo"
sudo install -D -m 0755 ./xray     "$DATA/cores/xray-core/bin/xray"
sudo install -D -m 0755 ./sing-box "$DATA/cores/sing-box/bin/sing-box"

# 可选：同时放入版本目录，使 POST /cores/{id}/rollback 可回退到该版本
sudo install -D -m 0755 ./mihomo "$DATA/cores/mihomo/versions/v1.19.33/mihomo"
```

### 10.3 重启并确认

```bash
sudo systemctl restart ngg     # 或重跑 ./bin/nggd -config configs/ngg.yaml
```

启动时 `Engine.New` 会对每个已注册内核自动 `ports.Allocate(id)` 分配独立端口块、`configs.Ensure(id)` 生成 Worker Mode 基线配置，并把状态置为 `installed`。

```bash
curl -H "$H" "$BASE/cores"                    # 三个内核 installed=true
curl -H "$H" "$BASE/cores/mihomo/version"     # 真实执行二进制读到的版本
curl -X POST -H "$H" "$BASE/cores/mihomo/start"
```

### 10.4 校验要点

- **SHA256 / 来源 / 版本三重校验必须在有网侧完成**：直接落盘会**绕过** `versionManager.Install` 的下载校验链路，因此不要跳过有网侧的摘要比对；
- 二进制文件名必须与 Adapter 的 `binary` / `BinaryName(goos)` 一致（Windows 为 `.exe`）；
- 三个内核分别落到 `mihomo/` / `xray-core/` / `sing-box/`，互不覆盖；
- 这种离线放置不会在 SQLite `installed` 表写入记录，但 `IsInstalled` 与 `Restore` 均以磁盘二进制为准，**不影响**启动与状态恢复；
- 若后续需要完整的版本链与升级能力，联网后走一次 `POST /cores/{id}/install` 即可纳入标准管理。

---

## 11. 升级与卸载

### 11.1 升级 NGG 自身

```bash
sudo cp /etc/ngg/ngg.yaml /etc/ngg/ngg.yaml.bak
sudo cp /var/lib/ngg/ngg.db /var/lib/ngg/ngg.db.bak

sudo systemctl stop ngg
sudo install -m 0755 bin/nggd /opt/ngg/nggd
sudo systemctl start ngg
sudo journalctl -u ngg -n 50 --no-pager
```

- **不要**删除 `storage.dir`（默认 `data/`、生产 `/var/lib/ngg/`），否则 SQLite 状态 / 版本链 / 备份丢失；
- 升级后用 `GET /system`、`GET /cores` 核对版本与状态恢复。

### 11.2 升级 / 回滚单个内核

优先用 API（见 `02-api.md` 第 5 节），**不影响其他内核**。注意 `rollback` 的 `version` 字段**必填**，且必须是 `versions/<tag>/` 下已存的版本。

> Phase 1 端到端验收已通过的真实版本：**mihomo 1.19.33** / **xray-core 26.10.31** / **sing-box 1.14.4**。示例中的 tag 请以 `GET /cores/{id}/versions` 返回的 `tag_name` 为准。

```bash
curl -X POST -H "$H" -H 'Content-Type: application/json' \
  -d '{"version":"v1.19.33"}' "$BASE/cores/mihomo/upgrade"
curl -X POST -H "$H" -H 'Content-Type: application/json' \
  -d '{"version":"v1.19.33"}' "$BASE/cores/mihomo/rollback"        # 回滚到 versions/ 下已存的版本
curl -X POST -H "$H" -H 'Content-Type: application/json' \
  -d '{"backup_id":"mihomo-20261104T140000.000000000-abcd"}' \
  "$BASE/cores/mihomo/config/rollback"                             # 回滚配置
```

### 11.3 卸载单个内核

```bash
curl -X DELETE -H "$H" "$BASE/cores/xray-core"     # 仅移除 xray-core
```

> `DELETE /cores/{id}` 会移除 `bin/` 与 `versions/`，但**保留配置与备份**（响应 message 明确说明）；运行中的内核会先被停止。

### 11.4 完整卸载 NGG

> **此操作不可逆，执行前请确认已备份**（配置、状态、内核二进制）。

```bash
sudo systemctl stop ngg
sudo systemctl disable ngg
sudo rm -f /etc/systemd/system/ngg.service
sudo systemctl daemon-reload

# 确认后再删除（路径按实际 PREFIX/CONF/DATA）
sudo rm -rf /opt/ngg /etc/ngg /var/lib/ngg
sudo userdel ngg 2>/dev/null || true
```

---

## 12. 安装后自检

- [ ] `GET /api/v1/system` 返回 `200`，`stage=1`、`mode="multi-core-parallel"`
- [ ] `GET /health` 的 `status="ok"`，且 `unified_tun="reserved-stage-2"`（**统一 TUN 属 Stage 2 预留**）
- [ ] `GET /registry` 可见三个内置内核（`builtin=true`、`source="builtin"`）
- [ ] 三内核（`mihomo` / `xray-core` / `sing-box`）`install` 成功且 SHA256 校验通过
- [ ] `POST /cores/batch/start`（`exclusive=false`）后 `GET /cores` 出现**多个 `running`**（多核并行）
- [ ] 重启 `ngg` 后状态 / 版本 / 端口分配恢复
- [ ] 非 `127.0.0.1` 来源返回 `403`，无 Token 返回 `401`
- [ ] Web UI 可打开（`web/dist` 已构建），且未出现「已接管 TUN / DNS / 规则」等表述

---

## 13. 相关文档

| 文档 | 内容 |
| --- | --- |
| `01-architecture.md` | 架构、约束、ADR |
| `02-api.md` | 完整 REST + WS API（32 个端点） |
| `03-development.md` | 源码构建、`configs/cores.d/` 描述文件字段、测试 |
| `05-troubleshooting.md` | 安装 / 运行故障排查 |
| `06-roadmap.md` | 五阶段路线图与验收标准 |
