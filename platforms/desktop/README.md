# 桌面端接法（Windows / macOS / Linux）

> **骨架 / scaffold，非成品托盘 App。** 桌面端推荐形态是「本机 `nggd` 服务模式 +
> 浏览器 Web UI + 系统代理设置」。**系统托盘 / 菜单栏 App 未实现**，本目录不提供
> 任何 GUI 客户端。Windows / macOS / Linux 的 `nggd` 二进制属于**可构建**目标。

## 1. 能力边界（务必先读）

- 已有且可构建：`cmd/nggd` 守护进程（控制面 REST + WS + Web UI），由
  `platforms/build.sh` 交叉编译，或仓库根目录 `make backend` 本机编译。
- 已有：`web/` 下的 Web UI，浏览器打开 `http://127.0.0.1:9090/?token=<TOKEN>` 使用。
- **未实现**：系统托盘 / 菜单栏 App、开机自启 GUI、原生设置面板、一键改系统代理的
  图形界面。下面的系统代理命令需要用户手动执行或自行封装成脚本/服务。
- 入口形态：Windows / macOS 用 userspace SOCKS5 / HTTP CONNECT 入口
  （默认 `127.0.0.1:18080`）+ 系统代理；Linux 既可用 userspace 入口，也可用统一 TUN
  （需 `/dev/net/tun` + `CAP_NET_ADMIN`）。DNS 默认 `127.0.0.1:18053`。

## 2. 构建与运行

```bash
# 在仓库根目录
bash platforms/build.sh windows/amd64 darwin/arm64 linux/amd64
# 产物：bin/platforms/nggd-*.exe / nggd-*

# 首次准备配置（含 Token 的配置不入库）
[ -f configs/ngg.yaml ] || cp configs/ngg.example.yaml configs/ngg.yaml

# 运行服务
./bin/platforms/nggd-linux-amd64 -config configs/ngg.yaml
# 首次启动会在控制台打印自动生成的 API Token 并写回 configs/ngg.yaml
```

浏览器管理界面：`http://127.0.0.1:9090/?token=<TOKEN>`。
控制面为 `http://127.0.0.1:9090/api/v1`，WS 为 `/api/v1/ws?token=<TOKEN>`，
所有 `/api/v1/**` 请求需 `Authorization: Bearer <token>`。

Linux 以 systemd 常驻（服务模式）请参考仓库 `docs/04-installation.md` 与
`scripts/install.sh`；macOS 可用 launchd、Windows 可用「任务计划程序 / 服务封装」
自行包装，本目录不提供这些包装文件。

## 3. 系统代理设置示例

以下示例中 `127.0.0.1:18080` 是 NGG 的 userspace 入口（SOCKS5 / HTTP CONNECT），
`127.0.0.1:18053` 是内置 DNS 监听。示例命令均需按本机实际环境调整，
`<TOKEN>` / `<服务名>` 为占位符。

### Linux（GNOME 示例）

```bash
# SOCKS5 系统代理
gsettings set org.gnome.system.proxy mode 'manual'
gsettings set org.gnome.system.proxy.socks host '127.0.0.1'
gsettings set org.gnome.system.proxy.socks port 18080

# 关闭代理
gsettings set org.gnome.system.proxy mode 'none'

# 或仅对当前终端生效（HTTP CONNECT + SOCKS5）
export HTTP_PROXY=http://127.0.0.1:18080
export HTTPS_PROXY=http://127.0.0.1:18080
export ALL_PROXY=socks5h://127.0.0.1:18080
export NO_PROXY=127.0.0.1,localhost
```

### macOS（`networksetup`，服务名用 `networksetup -listallnetworkservices` 查看）

```bash
# SOCKS5
networksetup -setsocksfirewallproxy "Wi-Fi" 127.0.0.1 18080
networksetup -setsocksfirewallproxystate "Wi-Fi" on

# HTTP CONNECT（Web Proxy）
networksetup -setwebproxy "Wi-Fi" 127.0.0.1 18080
networksetup -setsecurewebproxy "Wi-Fi" 127.0.0.1 18080

# 关闭
networksetup -setsocksfirewallproxystate "Wi-Fi" off
networksetup -setwebproxystate "Wi-Fi" off
networksetup -setsecurewebproxystate "Wi-Fi" off
```

### Windows

```cmd
rem WinHTTP 代理（管理员权限）
netsh winhttp set proxy 127.0.0.1:18080
netsh winhttp reset proxy
```

```powershell
# 当前用户 IE/系统代理设置
$key = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings'
Set-ItemProperty -Path $key -Name ProxyServer -Value '127.0.0.1:18080'
Set-ItemProperty -Path $key -Name ProxyEnable  -Value 1
# 关闭：把 ProxyEnable 设为 0
```

### DNS（可选）

NGG 内置 DNS 监听 `127.0.0.1:18053`。如需让系统 DNS 走 NGG，可在系统网络设置中
把 DNS 服务器指向 `127.0.0.1`（端口 18053 属非标准端口，通常需配合本地转发/DNSMasq
等），具体做法因平台而异，本骨架不提供自动配置。

## 4. 验证

```bash
BASE='http://127.0.0.1:9090/api/v1'
H="Authorization: Bearer <TOKEN>"

curl -H "$H" "$BASE/health"          # 连通性（共享核心 Connectable() 用的就是这个）
curl -H "$H" "$BASE/network/status"  # 查看 ingress 地址
curl -H "$H" "$BASE/cores"           # 内核列表
```

## 5. 仍需完成（未实现清单）

- 系统托盘 / 菜单栏 App（一键开关、状态图标、菜单）
- 自动配置/还原系统代理、开机自启的图形化封装
- 原生设置面板（Token 录入与 Keychain/凭据管理器存储）
- 断线重连、fail_closed 策略的桌面端产品化

## 6. 安全说明

控制面默认只监听 `127.0.0.1` 且要求 Bearer Token；`configs/ngg.yaml` 含 Token，
已在 `.gitignore` 中排除。不要把控制面改成监听非回环地址。所有示例中的
`<TOKEN>` 均为占位符。
