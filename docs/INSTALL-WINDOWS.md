# NGG 在 Windows 上安装（安装程序 / 服务 / 卸载）

> 本文对应仓库新增的 `installer/windows/`：一个**真正的 Windows 安装程序**（Inno Setup `.exe`）、
> 一个**便携 ZIP + PowerShell 安装脚本**，以及 `nggd.exe` 内置的 **Windows 服务支持**。
> VPS / Linux 部署见 [`DEPLOY-VPS.md`](DEPLOY-VPS.md)；全场景速查见 [`DEPLOY.md`](DEPLOY.md)。

---

## 0. 先读能力边界（避免误用）

| 项 | 状态 |
| --- | --- |
| 控制面 REST + WS + Web UI | ✅ 原生运行，浏览器访问 `http://127.0.0.1:9090/?token=<TOKEN>` |
| **Windows 服务**（开机自启、失败自动重启、优雅停止） | ✅ 已实现：`cmd/nggd/service_windows.go` 通过 `golang.org/x/sys/windows/svc` 接入 SCM |
|  userspace 统一入口（SOCKS5 / SOCKS4a / HTTP CONNECT） | ✅ 默认 `127.0.0.1:18080` |
| 内置 DNS 引擎 | ✅ 默认 `127.0.0.1:18053` |
| **统一 TUN / 全局流量接管** | ❌ Windows 明确返回 `ErrTUNUnsupported`，自动改用 userspace 入口；不需要 TUN 驱动、不需要 Wintun。点 TUN 按钮**不会**自动装虚拟网卡，见 §6.5 与 [`TUN-GLOBAL-TAKEOVER.md`](TUN-GLOBAL-TAKEOVER.md) |
| 系统托盘 App / 原生设置面板 / 一键改系统代理 | ❌ **未实现**（`platforms/desktop/` 仍为骨架），本文提供手动命令 |
| iOS / Android 等原生客户端 | ❌ 骨架，见 [`DEPLOY.md`](DEPLOY.md) 第 10 节 |

安装程序只做四件事：**放文件 → 写配置 → 注册并启动服务 → 校验健康**；不修改系统代理、不装驱动、不联网下载内核。

---

## 1. 前置条件

- Windows 10 / 11 **x64**（或 Windows Server 2016+）；Release 只提供 `windows/amd64`。
- **管理员权限**（安装 Windows 服务、写 `Program Files`、设置 ACL 都需要）。
- PowerShell 5.1（系统自带）即可；不需要 .NET SDK、不需要 Go / Node（除非从源码构建）。
- 控制面只监听 `127.0.0.1`，**不需要**开放任何防火墙端口。

> 仓库当前为 **Private**。若在线下载 Release，需要 GitHub PAT（Classic `repo` 或 Fine-grained `Contents: Read`），
> 通过 `$env:GITHUB_TOKEN` 传入；**不要**把 PAT 写进命令行历史或提交到仓库。

---

## 2. 方式 A：GUI 安装包（推荐给本机使用）

1. 从 Release 下载 `NGG-Setup-v2.1.1.exe`：
   <https://github.com/welkacton/ngg-next-generation-gateway/releases/tag/v2.1.1>
   （私有仓库需登录有权限的 GitHub 账号；同页有 `SHA256SUMS` 可校验；也可按第 4 节自行构建）。
2. 双击 → UAC 确认 → 选择安装目录（默认 `C:\Program Files\NGG`）→ 完成。
3. 安装器会自动执行 `install.ps1 -ConfigureOnly -Silent -OpenBrowser`：
   - 生成 `configs\ngg.yaml`（绝对路径 + 随机 API Token）；
   - 注册 **Windows 服务 `NGG`**（自动启动、失败自动重启 3 次）；
   - 启动并轮询 `GET /api/v1/health`；
   - 写入 `NGG-ACCESS.txt`（含带 Token 的一次性绑定地址，ACL 仅 SYSTEM + Administrators）；
   - 打开浏览器完成首次绑定。
4. 开始菜单出现：`NGG Web UI`、`NGG Access Info`、`Uninstall NGG`。

**安装完成后**

```powershell
Get-Service NGG
Restart-Service NGG
Get-Content "$env:ProgramFiles\NGG\logs\ngg-service.log" -Tail 50
Get-Content "$env:ProgramFiles\NGG\NGG-ACCESS.txt"
```

浏览器打开 `NGG-ACCESS.txt` 里的 `Web UI` 地址（形如 `http://127.0.0.1:9090/?token=<64位十六进制>`）。
首次绑定后 Token 保存在浏览器本地；也可随时从 `configs\ngg.yaml` 读取。

---

## 3. 方式 B：便携 ZIP（推荐给自动化 / 无 GUI 场景）

> ⚠️ **文档里出现 `C:\path\to\...` 时那是占位符，必须换成你解压后的真实目录。**
> 直接复制占位符会得到 `cd : 找不到路径`；在 `C:\Users\Admin>` 下直接执行 `.\install.ps1`
> 会得到「-File 形式参数的实际参数不存在」——因为当前目录里根本没有这个文件。

### 3.1 最省事：双击 `install.bat`

1. 下载 `NGG-v2.1.1-windows-amd64.zip` → **右键 → 全部解压**（不要直接在压缩包里双击）；
2. 进入解压出来的文件夹，确认里面有 `nggd.exe`、`install.ps1`、`install.bat`、`web-dist\`；
3. **双击 `install.bat`** → UAC 点「是」→ 自动以管理员身份安装并打开浏览器。

这一步不需要手敲任何命令，也不需要自己开管理员 PowerShell。

### 3.2 手工命令（把路径换成你自己的）

假设解压到 `C:\Users\Admin\Downloads\NGG-v2.1.1-windows-amd64`：

```powershell
# 1) 先进入真实目录（按需改盘符/路径）
Set-Location "$env:USERPROFILE\Downloads\NGG-v2.1.1-windows-amd64"

# 2) 确认文件在眼前（这一步能避免 90% 的路径错误）
Get-ChildItem | Select-Object Name

# 3) 以管理员身份运行的 PowerShell 里执行
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Source . -OpenBrowser
```

如果不知道解压到哪了，用资源管理器地址栏复制路径，或：

```powershell
Get-ChildItem -Path "$env:USERPROFILE\Downloads" -Filter "install.ps1" -Recurse -ErrorAction SilentlyContinue |
  Select-Object -ExpandProperty DirectoryName
```

> `-File .\install.ps1` 用的是**当前目录**；`cd` 失败时后面的命令仍在 `C:\Users\Admin` 执行，
> 所以务必先 `Set-Location` 成功并看到 `install.ps1` 存在。

静默安装（不弹等待提示）：

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Source . -Silent
```

在线下载并安装（**必须在一个含 `install.ps1` 的目录里执行**，私有仓库需 PAT）：

```powershell
$env:GITHUB_TOKEN = 'ghp_xxx'
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Version v2.1.1 -Silent -OpenBrowser
```

脚本会用 `SHA256SUMS` 校验下载包；若 Release 没有该文件会给出明确警告。

### 3.3 直接双击 EXE（方式 A）其实更简单

如果你只想要「下载 → 双击 → 完成」，用 `NGG-Setup-v2.1.1.exe`（Inno Setup 安装包），
它会自己放好文件、注册服务、启动、打开浏览器，**完全不需要 PowerShell 命令**。
便携 ZIP 是给自动化/静默部署用的。

### install.ps1 参数

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `-InstallDir` | `%ProgramFiles%\NGG` | 安装目录 |
| `-Source PATH` | 自动探测 | 已解压的 Release 包 / 仓库根目录（含 `nggd.exe`） |
| `-Version vX.Y.Z` | — | 在线下载 `windows-amd64.zip` 并安装 |
| `-Token TOKEN` | `$env:GITHUB_TOKEN` | GitHub PAT（私有仓库） |
| `-ConfigureOnly` | 关 | 文件已由安装器放好，只做配置 + 服务（Inno Setup 内部使用） |
| `-NoService` | 关 | 改用「任务计划程序」开机启动（**无控制台日志**，不推荐） |
| `-NoAutostart` | 关 | 不注册服务/计划任务，仅放文件与配置；适合测试或手动运行 |
| `-NoStart` | 关 | 配置与服务都就绪但不启动、不做健康检查 |
| `-Port N` | 9090 | 修改 `server.port`（只改第一处，不影响 ingress/dns 端口） |
| `-Force` | 关 | 覆盖已有 `configs\ngg.yaml` 并**轮换 Token**（旧 Token 立即失效） |
| `-OpenBrowser` | 关 | 安装完成后打开带 Token 的 Web UI |
| `-Silent` | 关 | 不等待回车、不交互确认 |

### 脚本实际做了什么（可审计）

1. 定位 `nggd.exe`、`web-dist`、`configs\cores.d`、`docs`、`scripts`；
2. 复制到安装目录，并复制 `install.ps1` / `uninstall.ps1` 到 `installer\`；
3. 从 `configs\ngg.example.yaml` 生成 `configs\ngg.yaml`：
   `storage.dir` / `logging.dir` / `server.web_dir` / `cores.descriptor_dir` 全部改为**绝对路径**，
   并预置 32 字节随机 Token（避免服务账号回写配置文件）；
4. 用 `icacls` 把含 Token 的配置文件与 `NGG-ACCESS.txt` 限制为 `SYSTEM` + `Administrators`；
5. `New-Service -Name NGG -StartupType Automatic`，设置失败重启策略；
6. 启动并轮询 `/api/v1/health`（最多 40 秒），失败时打印 `logs\ngg-service.log` 最后 40 行；
7. 打印访问地址、Token、日志路径。

> **日志位置**：Windows 服务没有控制台，`nggd.exe` 在检测到由 SCM 启动时会自动把日志写到
> `<安装目录>\logs\ngg-service.log`（超过 8 MiB 滚动为 `.old`）。直接双击 `nggd.exe` 控制台运行时
> 日志仍输出到 stderr，行为不变。

---

## 4. 方式 C：从源码自行构建安装包

需要 **Go 1.26+**、**Node 20+**、可选 **Inno Setup 6**。

```powershell
# 仓库根目录，普通 PowerShell 即可（不需要管理员，只是产包）
powershell -ExecutionPolicy Bypass -File .\installer\windows\build-installer.ps1 -Version v2.1.1
```

产物在 `out\`：

| 文件 | 说明 |
| --- | --- |
| `NGG-v2.1.1-windows-amd64.zip` | 便携包（内含 `install.ps1`，解压即用） |
| `NGG-Setup-v2.1.1.exe` | Inno Setup GUI 安装包（仅当找到 `ISCC.exe`） |
| `*.sha256` | 校验文件 |

找不到 Inno Setup 时脚本会明确提示并**仍然产出可用的 ZIP**；安装方式：
从 <https://jrsoftware.org/isdl.php> 安装，或 `choco install innosetup -y`。

CI（`.github/workflows/release.yml`）在打 `v*` tag 时会自动完成这一步，把 ZIP 与 EXE 附到 Release。

---

## 5. 升级 / 卸载

**升级**（保留 `configs\ngg.yaml`、`data\`、`logs\`）：

```powershell
Stop-Service NGG
Copy-Item <新包>\nggd.exe "$env:ProgramFiles\NGG\nggd.exe" -Force
Copy-Item <新包>\web-dist\* "$env:ProgramFiles\NGG\web\dist" -Recurse -Force
Start-Service NGG
Get-Content "$env:ProgramFiles\NGG\logs\ngg-service.log" -Tail 30
```

或直接重跑 `install.ps1 -Source <新包目录>`：它会保留已有配置与 Token、重建服务并做健康检查。
**不要**用 `-Force`，除非你明确想轮换 Token。

**卸载**（GUI 安装包：设置 → 应用 → NGG → 卸载；便携包：脚本）：

```powershell
powershell -ExecutionPolicy Bypass -File "$env:ProgramFiles\NGG\installer\uninstall.ps1"
powershell -ExecutionPolicy Bypass -File .\uninstall.ps1 -KeepData -Silent   # 无人值守
powershell -ExecutionPolicy Bypass -File .\uninstall.ps1 -BackupPath C:\ngg-backup.zip
```

- 默认先把 `configs\` / `data\` / `logs\` 打包到 `%ProgramData%\NGG\backups\ngg-backup-<时间>.zip`（含 Token 与节点凭据，请加密保存）；
- 停止并删除 `NGG` 服务（或 `NGG Gateway` 计划任务）；
- 删除安装目录；`-KeepData` 只删程序文件，保留配置与数据（GUI 卸载默认采用此行为）；
- 不会改动系统内置账号 LocalSystem，也不会自动还原系统代理。

**Token 轮换**：清空 `configs\ngg.yaml` 里的 `token: ""` → `Restart-Service NGG` → 从新生成的配置或
`NGG-ACCESS.txt` 读取新 Token。旧 Token 立即失效。

---

## 6. 可选：系统代理与局域网入口

默认所有监听都在 `127.0.0.1`，**无需防火墙规则**。若要让本机应用走 NGG：

```powershell
# 当前用户系统代理（用完记得关）
$key = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings'
Set-ItemProperty -Path $key -Name ProxyServer -Value '127.0.0.1:18080'
Set-ItemProperty -Path $key -Name ProxyEnable  -Value 1
# 还原
Set-ItemProperty -Path $key -Name ProxyEnable -Value 0

# WinHTTP（管理员）
netsh winhttp set proxy 127.0.0.1:18080
netsh winhttp reset proxy
```

> 入口是 SOCKS5 / SOCKS4a / HTTP CONNECT 三合一；浏览器扩展若只支持 HTTP 代理，也用同一个 18080 端口。
> 内置 DNS 在 `127.0.0.1:18053`（非标准端口），如需系统级接管要另外做本地转发，安装器不代劳。

**开放给局域网**（必须同时设置账号密码，否则 NGG 会拒绝启用该入口）：

1. 编辑 `configs\ngg.yaml` 的 `network.ingress`：`listen: 0.0.0.0`，并填 `auth_user` / `auth_pass`；
2. `Restart-Service NGG`；
3. 管理员 PowerShell 放行端口（**只放行 18080，绝不放行 9090**）：

```powershell
New-NetFirewallRule -DisplayName 'NGG Ingress 18080' -Direction Inbound -Protocol TCP -LocalPort 18080 -Action Allow
# 移除：Remove-NetFirewallRule -DisplayName 'NGG Ingress 18080'
```

控制面 `9090` 永远保持 `127.0.0.1`，中间件会拒绝任何非回环来源（403）。远程管理请在 VPS 上走
SSH 隧道 / Nginx HTTPS（见 [`DEPLOY-VPS.md`](DEPLOY-VPS.md) 第 5 节），Windows 本机不需要。

### 6.5 关于「打开 TUN 按钮就自动装虚拟网卡、接管全部流量」

**目前做不到，且按钮不会假装成功。** 如实说明：

- Windows 版 `nggd.exe` 的 `tun_other.go` 明确返回 `ErrTUNUnsupported`，**没有 Wintun/TAP 驱动、没有虚拟网卡**；
- Linux 虽有真 TUN，但 `tunLoop()` 目前只做真实 L3 解析 + IPv4 UDP/53 DNS 应答，**TUN 里的 TCP 流终止需要 userspace TCP/IP 协议栈，尚未实现**，这类包会被 Fail-Closed 丢弃（不泄漏、不伪装成功）；
- 真正决定「这个连接走哪个内核」的是 NGG 的 `RuleEngine → Dispatcher → CoreAdapterBridge → EgressManager`，不是虚拟网卡本身；虚拟网卡只负责把裸 IP 包交给 NGG。

要实现「按钮 → 自动装虚拟网卡 → 自动接管全部流量 → 自动决定去向」，需要补齐：
Wintun 签名驱动 + 路由/DNS 事务式接管与还原 + `gVisor netstack` 之类的 userspace TCP/IP 栈 +
与现有决策链接线 + Windows 防回环 + 失败自动回滚。**这是一次数据面开发，不是改个开关。**
完整设计、代码级现状与 P0~P4 分阶段验收标准见 [`TUN-GLOBAL-TAKEOVER.md`](TUN-GLOBAL-TAKEOVER.md)。

在那之前，Windows 想让大部分应用走 NGG，可用系统代理过渡（**不是全局接管**，不遵守系统代理的程序和 UDP 会绕过）：

```powershell
powershell -ExecutionPolicy Bypass -File .\installer\windows\set-system-proxy.ps1 -Enable
powershell -ExecutionPolicy Bypass -File .\installer\windows\set-system-proxy.ps1 -ShowStatus
powershell -ExecutionPolicy Bypass -File .\installer\windows\set-system-proxy.ps1 -Disable   # 完整还原
```

脚本会把 `127.0.0.1:18080` 设为 WinINET/WinHTTP 代理，并先把原设置备份到
`%LOCALAPPDATA%\NGG\proxy-backup.json`，`-Disable` 时精确还原。

---

## 7. 故障排查

| 现象 | 原因 / 处理 |
| --- | --- |
| `cd : 找不到路径 "C:\path\to\..."` | 复制了文档占位符。换成解压后的真实目录，例如 `Set-Location "$env:USERPROFILE\Downloads\NGG-v2.1.1-windows-amd64"`；或直接用 `install.bat` / `NGG-Setup-v2.1.1.exe` |
| `-File 形式参数的实际参数 ".\install.ps1" 不存在` | 当前目录里没有 `install.ps1`。先 `Set-Location` 到解压目录并 `Get-ChildItem` 确认；`-File` 是相对**当前目录**解析的 |
| 安装脚本报 `Administrator privileges are required` | 用「以管理员身份运行」打开 PowerShell；更简单是双击 `install.bat`（会自动提权） |
| 服务启动失败 / 错误 1053 | 看 `<安装目录>\logs\ngg-service.log`；最常见是 `-config` 路径不对、配置文件 ACL 不允许 SYSTEM 读取、或 YAML 语法错误 |
| 服务起来了但健康检查 401 | Token 不匹配；从 `NGG-ACCESS.txt` 或 `configs\ngg.yaml` 复制，注意别带多余引号 |
| 健康检查 403 | 请求不是回环来源；确认没有把 `server.listen` 改成 `0.0.0.0` |
| 端口被占用 | `Get-NetTCPConnection -LocalPort 9090`；改 `-Port` 或配置里的 `server.port` 后重启服务 |
| Web UI 显示「尚未构建」 | 包内 `web-dist` 没复制到 `web\dist`；重新运行安装脚本或手动复制后 `Restart-Service NGG` |
| 在线安装 404 / 401 | 私有仓库未带 PAT：`$env:GITHUB_TOKEN='ghp_xxx'`；脚本会回退 GitHub API 资产接口 |
| 下载包校验失败 | 重新下载；不要跳过校验 |
| 找不到 `NGG-ACCESS.txt` | 只在安装成功时生成；若丢失，从 `configs\ngg.yaml` 读取 Token 自行拼 `http://127.0.0.1:9090/?token=<TOKEN>` |
| 任务计划模式没有日志 | `-NoService` 启动的进程无控制台；改用默认 Windows 服务模式 |
| 卸载后配置还在 | `-KeepData` / GUI 卸载会保留用户数据；备份在 `%ProgramData%\NGG\backups\`，可手工删除 |

---

## 8. 验收清单

- [ ] `Get-Service NGG` = `Running` / `Automatic`
- [ ] `Invoke-WebRequest -Headers @{Authorization='Bearer <TOKEN>'} http://127.0.0.1:9090/api/v1/health` 返回 200
- [ ] `http://127.0.0.1:9090/?token=<TOKEN>` 能打开完整 Web UI（不是「尚未构建」状态页）
- [ ] `logs\ngg-service.log` 持续写入启动日志；`Get-Content ... -Tail 30` 无 panic
- [ ] `configs\ngg.yaml` 四个路径字段都是绝对路径，ACL 仅 SYSTEM + Administrators
- [ ] `Get-NetTCPConnection -LocalPort 9090` 只绑定 `127.0.0.1`
- [ ] 在 Web UI「内核引擎」安装并并行启动内核后，Stage 2 自检通过（userspace 入口路径）
- [ ] Token、PAT、备份 ZIP 已存入密码管理器 / 加密保存，未提交、未截图外发

---

## 9. 相关文档

| 文档 | 内容 |
| --- | --- |
| [`DEPLOY-VPS.md`](DEPLOY-VPS.md) | VPS / systemd / Docker / HTTPS / 备份回滚 |
| [`DEPLOY.md`](DEPLOY.md) | 全场景部署速查与 3X-UI / Remnawave 配置兼容 |
| [`04-installation.md`](04-installation.md) | Phase 1 基线安装说明、配置字段、离线内核 |
| [`05-troubleshooting.md`](05-troubleshooting.md) | 通用故障排查 |
| [`TUN-GLOBAL-TAKEOVER.md`](TUN-GLOBAL-TAKEOVER.md) | **TUN 一键全局接管的可行性、缺口与 P0~P4 实施计划** |
| [`platforms/desktop/README.md`](../platforms/desktop/README.md) | 桌面端系统代理示例与未实现清单 |
