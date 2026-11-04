# OpenWrt / 软路由接入

> **骨架 / scaffold，非成品固件或 ipk 包。** 本目录只提供 procd init 脚本与 UCI
> 配置示例，以及交叉编译/接入说明。OpenWrt 的 **ipk 打包、LuCI 界面、内核二进制的
> 交叉编译与安装、开机自启集成测试均未提供/未实现**。需要 OpenWrt SDK 完成后续打包。

## 1. 能力边界（务必先读）

- `cmd/nggd` 是纯 Go（含纯 Go 的 SQLite / gopsutil 依赖），`CGO_ENABLED=0` 下
  可交叉编译到 OpenWrt 常见架构，属于**可构建**目标。
- 本目录**不含** `nggd` 二进制、内核二进制、`web/dist` 静态资源、Makefile 或
  ipk 控制文件（`control` / `postinst` 等）。这些必须由打包步骤提供。
- 控制面 REST/WS 默认只监听 `127.0.0.1` 并要求 Bearer Token；LuCI 管理界面未实现。

## 2. 目录

```
openwrt/
├── README.md
└── files/etc/
    ├── init.d/ngg        # procd init 脚本（真实可执行结构）
    └── config/ngg        # UCI 配置示例
```

安装到设备时的目标路径：

```
files/etc/init.d/ngg   -> /etc/init.d/ngg      （需 chmod +x）
files/etc/config/ngg   -> /etc/config/ngg
```

## 3. 交叉编译 `nggd`

在仓库根目录执行（OpenWrt 常见架构示例）：

```bash
bash platforms/build.sh linux/arm64 linux/arm linux/mipsle
# 产物：
#   bin/platforms/nggd-linux-arm64
#   bin/platforms/nggd-linux-arm
#   bin/platforms/nggd-linux-mipsle
```

`build.sh` 使用 `GOOS=linux GOARCH=<arch> CGO_ENABLED=0 go build -trimpath
-ldflags="-s -w" -o ... ./cmd/nggd`。若目标是 32 位 MIPS 且需软浮点，可能还要
设置 `GOMIPS=softfloat` 之类的环境变量，请按实际设备调整。

把产物安装为 `/usr/bin/nggd`，并把仓库 `web/dist`（`make frontend` 生成）放到
`/usr/share/ngg/web`，再安装内核二进制与 `configs/cores.d` 描述文件。

## 4. procd init 与 UCI

`/etc/init.d/ngg` 是标准 procd 脚本：

- `USE_PROCD=1`，`START=95` / `STOP=10`；
- `start_service()` 读取 `/etc/config/ngg`，用 `procd_open_instance` 拉起
  `nggd -config <config_file>`，配置 `respawn`、stdout/stderr、`pidfile` 与
  配置文件变更监视；
- 首次启动且 YAML 不存在时，`prepare_config()` 由 UCI 字段生成
  `/etc/ngg/ngg.yaml`（权限 0600）；**已存在则绝不覆盖**，因为 `nggd` 会把
  自动生成的 API Token 写回该文件（`internal/config.Save`）；
- `service_triggers()` 注册 reload trigger，`reload_service()` 重启实例；
- 若 `tun=1` 但 `/dev/net/tun` 不存在，会通过 `logger` 打印告警但不阻止启动
  （退化为 userspace SOCKS5/HTTP CONNECT 入口）。

启用与常用命令：

```bash
/etc/init.d/ngg enable
/etc/init.d/ngg start
/etc/init.d/ngg restart
/etc/init.d/ngg reload
logread | grep ngg          # 查看日志（脚本 stdout/stderr 已交给 procd）
```

## 5. UCI 字段与 YAML 的对应关系

| UCI | 对应 `configs/ngg.example.yaml` / `internal/config` | 默认值 |
| --- | --- | --- |
| `main.listen` | `server.listen` | `127.0.0.1` |
| `main.port` | `server.port` | `9090` |
| `main.token` | `server.token`（留空则由 nggd 自动生成） | 空 |
| `main.web_dir` | `server.web_dir` | `/usr/share/ngg/web` |
| `main.storage_dir` | `storage.dir` | `/etc/ngg/data` |
| `main.descriptor_dir` | `cores.descriptor_dir` | `/etc/ngg/cores.d` |
| `main.port_base` / `main.port_stride` | `cores.port_base` / `cores.port_stride` | `17890` / `10` |
| `network.enabled` | `network.enabled` | `1` |
| `network.fail_closed` | `network.fail_closed` | `1` |
| `network.default_core` | `network.default_core` | `mihomo`（示例，需替换为已注册内核 id） |
| `network.ingress_listen` / `ingress_port` | `network.ingress.listen` / `port` | `127.0.0.1` / `18080` |
| `network.dns_listen` / `dns_port` | `network.dns.listen` / `port` | `127.0.0.1` / `18053` |
| `network.tun` | 统一 TUN 开关（提示用） | `1` |

注意 `token` 的生效方式：脚本生成 YAML 时写入 UCI 的值，并在 UCI 显式设置了
token 时通过 `NGG_TOKEN` 环境变量覆盖（`config.Load` 支持该变量）。留空时由
`nggd` 首次启动自动生成并写回 YAML；之后修改 UCI 的 token 不会自动同步到已存在的
YAML，请手工维护或用环境变量注入。

## 6. TUN 与入口约束

- Linux 统一 TUN 需要 `/dev/net/tun`（`kmod-tun`）且进程具备 `CAP_NET_ADMIN`；
  procd 默认以 root 启动可满足，缺失时脚本会打印告警。
- 数据面默认入口 `127.0.0.1:18080`（SOCKS5 / HTTP CONNECT）、DNS
  `127.0.0.1:18053`。若要把 ingress 改成局域网地址供其它设备使用（例如电视端，
  见 `platforms/tv/`），按 `internal/netengine` 约束**必须**同时配置
  `auth_user` / `auth_pass`，否则会被 `ErrIngressInsecure` 拒绝。
- 控制面 REST 保持仅本机，不要对局域网开放（架构约束 ADR-005）。

## 7. 仍需完成（未实现清单）

- OpenWrt SDK 下的 ipk 打包（Makefile、`control`、`postinst`、`conffiles`）
- LuCI 管理界面、`rpcd` 接口
- 内核二进制的交叉编译、安装与升级流程
- 开机自启、固件升级后配置保留、TUN/防火墙（nftables）规则集成
- UCI -> YAML 的双向同步与 Token 轮换

## 8. 安全说明

`configs/ngg.yaml` 含 API Token，脚本生成时已设为 `0600`；UCI 中的 `token` 同样
属于敏感信息，示例为空。控制面默认只监听 `127.0.0.1` 并要求 `Bearer <token>`，
所有示例占位符不代表真实凭据。
