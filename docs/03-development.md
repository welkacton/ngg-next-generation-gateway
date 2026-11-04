# NGG Phase 1 — 开发说明

> 本文描述**当前代码**的结构、命令与扩展方式。字段与 `internal/adapters/generic/descriptor.go`
> 中的 `Descriptor` 结构体一一对应，可直接照抄使用。

## 1. 环境要求

| 项 | 版本 / 说明 |
| --- | --- |
| Go | **1.26+**（`go.mod` 声明 `go 1.26.0`，由 `modernc.org/sqlite` 决定；开发用 1.27.3） |
| Node.js | **20+**（开发机为 22.x） |
| 数据库 | 无外部依赖，`modernc.org/sqlite` 纯 Go 内嵌，**免 CGO / 免 C 编译器** |
| 操作系统 | **Linux x86_64 优先**；Windows / macOS 用于开发运行 |
| 可选工具 | `make`、`curl`、`sha256sum`、`systemctl` |

主要依赖：`gopkg.in/yaml.v3`、`github.com/gorilla/websocket`、
`github.com/shirou/gopsutil/v4`、`modernc.org/sqlite`。HTTP 路由使用 Go 标准库
`net/http.ServeMux`（Go 1.22+ 方法 + 路径参数），无 Web 框架。

## 2. 目录结构

```
cmd/nggd/main.go                 服务入口、信号与优雅退出
pkg/contracts/types.go           共享 DTO / 能力描述契约（零内部依赖）
internal/config/config.go        NGG 自身配置（非内核配置）
internal/core/
  adapter.go                    CoreAdapter 接口、RuntimeParams、ReleaseSpec、资产模板渲染
  registry.go                   Core Plugin Registry（可插拔，非白名单）
  ports.go                      10 个模块的接口定义（接口先行）
  version.go                    真实版本解析
internal/adapters/
  mihomo/ xray/ singbox/        三个内置适配器（纯函数：参数 / 默认配置 / 资产名）
  generic/descriptor.go         描述文件驱动适配器（新增内核的扩展点）
  builtin/builtin.go            内置内核注册（只是三个注册项，不是硬编码上限）
internal/engine/
  engine.go                     Core Manager（唯一编排入口）
  supervisor.go                 真实进程监管 + 独立崩溃恢复
  version.go                    安装 / 升级 / 回滚 / 摘要校验下载器
  config.go                     配置事务（备份→原生校验→替换）
  resources.go                  端口隔离、架构检测、进程指标
  eventbus.go                   事件总线 + 日志环形缓冲
  health_unix.go / health_windows.go  平台相关信号与进程采样
internal/store/store.go          SQLite 状态持久化
internal/api/
  server.go                     REST 路由 + 本机限制 + Bearer 认证 + 结构化错误码
  handlers.go                   全部 REST 处理器
  ws.go                         WebSocket 事件推送
internal/netengine/interfaces.go  Stage 2/3 统一网络引擎【仅接口，零实现】
web/                             Vue 3 + TS + Naive UI + Vite
configs/ngg.yaml                 NGG 自身配置
configs/cores.d/                 新内核能力描述文件目录
configs/examples/kernel-template.yaml  可用模板（不会自动加载）
docs/ scripts/ Makefile
```

## 3. 构建与运行命令

| 命令 | 实际作用 |
| --- | --- |
| `make backend` / `make build` | `go build -trimpath -o bin/nggd ./cmd/nggd` |
| `make frontend` | `cd web && npm install && npm run build`（产出 `web/dist`） |
| `make test` | `go test ./... -count=1` |
| `make vet` | `go vet ./...` |
| `make dist` | `vet → test → backend → frontend` |
| `make run` | 构建并 `./bin/nggd -config configs/ngg.yaml` |
| `make dev` | `cd web && npm run dev`（Vite :5173；后端需另开终端 `make run`） |

等价原始命令：

```bash
# 后端
go build -o bin/nggd ./cmd/nggd
./bin/nggd -config configs/ngg.yaml -no-restore   # -no-restore 跳过状态恢复

# 交叉编译（免 CGO，直接可用）
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o bin/nggd-linux-arm64 ./cmd/nggd
# 或
bash scripts/build.sh linux/amd64 linux/arm64 windows/amd64

# 前端
cd web && npm install && npm run dev     # 开发
cd web && npm run build                  # 产物 web/dist
```

首次启动会生成随机 API Token 并写回 `configs/ngg.yaml`（0600），同时打印到控制台。
用 `http://127.0.0.1:9090/?token=<TOKEN>` 打开管理界面完成一次性绑定。

## 4. 开发流程（项目硬性要求）

1. **先定义接口**：`internal/core/ports.go` 定义模块端口，`internal/core/adapter.go` 定义
   `CoreAdapter`，`internal/netengine/interfaces.go` 预留 Stage 2/3 接口。
2. **先写测试**：每个模块配套 `*_test.go`，接口契约先被测试固定。
3. **再实现代码**：实现放在 `internal/engine` / `internal/store` / `internal/api`。
4. **每次修改必须通过**：`go build ./...` → `go vet ./...` → `go test ./...` →
   `npm run build`（前端改动时）。
5. **不得用模拟状态冒充真实功能**：状态来自真实 OS 进程与真实二进制；
   版本来自执行 `-v` 并解析；配置校验调用内核自身；CPU/内存来自 gopsutil；
   端口冲突来自真实 bind 探测。
6. **新增功能若属 Stage 2+，只准在 `internal/netengine` 加接口，不准提前堆实现。**

### 单元测试清单

| 文件 | 覆盖 |
| --- | --- |
| `internal/core/version_test.go` | 三个内核**真实** `-v` 输出解析、无法解析时拒绝伪造版本 |
| `internal/core/adapter_test.go` | RuntimeParams 端口校验、按 OS 的 Release 资产名渲染、架构映射 |
| `internal/core/registry_test.go` | 注册 / 去重 / 内置保护 / 描述文件加载与模板渲染 |
| `internal/adapters/builtin/adapters_test.go` | 三内核能力契约、真实资产名、真实版本输出、Worker Mode 默认配置可解析 |
| `internal/engine/engine_test.go` | 事件总线、日志环形缓冲、端口隔离、真实 OS 子进程启停与日志捕获、摘要/URL 校验 |
| `internal/engine/config_integration_test.go` | **真实 Mihomo 二进制**跑完整配置事务 + 真实 zip 解包（需环境变量，否则 skip） |
| `internal/store/store_test.go` | SQLite desired_state / 备份 / 安装记录 / 审计往返 |
| `internal/api/api_test.go` | REST 鉴权、系统/注册表/内核/端口端点、真实 not_installed 状态、静态页 |

启用真实内核集成测试：

```bash
NGG_TEST_MIHOMO_BINARY=/abs/path/to/mihomo \
NGG_TEST_ARCHIVE=/abs/path/to/mihomo-windows-amd64-v1.19.33.zip \
  go test ./... -count=1
```

## 5. 如何新增一个内核（Core Plugin Registry）

**不需要改 NGG Network Engine，不需要重新编译，不需要把内核写死成第四个。**
只需往 `configs/cores.d/` 放一个 `*.yaml` / `*.yml` / `*.json` 能力描述文件，
然后重启服务或调用 `POST /api/v1/registry/reload`。

### 5.1 完整可用模板

模板文件：`configs/examples/kernel-template.yaml`。核心结构如下（**字段名以代码为准**）：

```yaml
id: my-core                       # 必填，唯一；决定 data/cores/<id>/ 与 API 路径
display_name: My Core
description: 通过标准 Core Adapter 契约注册的第四个内核
homepage: https://example.com
repository: example/my-core       # 展示用

capability:                       # 协议能力注册（约束 5）
  protocols: [socks5, example-protocol]
  inbound_types: [mixed, socks]
  features: [native-config-check]
  worker_mode: true               # 必须 true：禁止自建系统 TUN / DNS 劫持
  local_tcp_endpoint: "127.0.0.1:{{.MixedPort}}"   # Stage 2 预留
  native_stream: false            # Stage 2 预留
  native_datagram: false          # Stage 2 预留

platforms:                        # 可选；省略则使用 StandardPlatforms()
  - os: linux
    arch: amd64
  - os: windows
    arch: amd64

binary:                           # GOOS -> 可执行文件名
  windows: my-core.exe
  default: my-core

commands:
  version: ["version"]                       # 必填，纯参数无副作用
  run: ["run", "-c", "{{.Config}}"]          # 必填
  validate: ["check", "-c", "{{.Config}}"]   # 必填：调用内核原生配置校验
  env: []                                    # 可选，追加环境变量

config:
  file_name: config.json                     # 必填
  default: |                                 # 必填，Go text/template
    {"listen":"127.0.0.1","port":{{.MixedPort}}}

release:
  repository: example/my-core                # 必填 owner/repo
  asset_templates:                           # 按 GOOS 选择；也可用单一 asset_template
    windows: "my-core-{version}-windows-{arch}.zip"
    linux: "my-core-{version}-linux-{arch}.tar.gz"
  digest_template: "{asset}.dgst"            # 可选，上游独立摘要文件
  digest_format: api                         # api | dgst+api | sums
  archive: auto                              # auto | zip | gz | tar.gz | none
  binary_in_archive: "my-core*"              # 包内二进制匹配 glob
  os_map: {windows: win, darwin: mac}        # 可选，GOOS 映射
  arch_map: {amd64: x86_64, arm64: aarch64}  # 可选，GOARCH 映射
  arch_map_by_os:                            # 可选，按 OS 覆盖 arch_map
    android: {arm64: arm64-v8a}
  version_prefix: "v"                        # 可选，{vversion} 前缀

control_api:
  type: none                                 # clash | none
  url: "http://127.0.0.1:{{.APIPort}}"
  secret: ""

version_regex: "my-core ([0-9]+\\.[0-9]+\\.[0-9]+)"   # 可选；失败会回退通用 semver 提取
```

### 5.2 模板变量

| 上下文 | 可用变量 |
| --- | --- |
| `commands.run` / `validate` / `env` | `{{.Config}}`（当前有效配置绝对路径）、`{{.Dir}}`（其所在目录） |
| `config.default` | `{{.MixedPort}}` `{{.SocksPort}}` `{{.HTTPPort}}` `{{.APIPort}}` `{{.DNSPort}}` `{{.Secret}}` `{{.Dir}}` `{{.Config}}` |
| `control_api.url` | `{{.APIPort}}` `{{.Secret}}` |
| `release.asset_template(s)` | `{os}` `{arch}` `{version}` `{vversion}` |
| `release.digest_template` | `{asset}` 以及上面全部资产占位符 |

### 5.3 字段说明（与代码一致）

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `id` | ✅ | 唯一标识；不可与已注册内核重复 |
| `capability.*` | ⬜ | 能力注册；`worker_mode` 强烈建议 `true` |
| `platforms` | ⬜ | `{os, arch}` 矩阵；`ArchDetector.SupportedBy` 用它在启动前校验，不匹配返回 `ARCH_UNSUPPORTED` |
| `binary` | ⬜ | GOOS → 文件名；省略时回退 `<id>` / `<id>.exe` |
| `commands.version` | ✅ | 版本命令参数；输出交给 `version_regex` / 通用 semver 解析 |
| `commands.run` | ✅ | 运行参数；**不得**包含开启内核 TUN / DNS 劫持的参数 |
| `commands.validate` | ✅ | 内核原生配置校验；配置事务强制调用 |
| `commands.env` | ⬜ | 追加的环境变量 |
| `config.file_name` | ✅ | 有效配置文件名；候选配置会写到 `.staging/<同名文件>` 后再校验 |
| `config.default` | ✅ | 首次生成的 Worker Mode 基线配置 |
| `release.repository` | ✅ | `owner/repo`；下载来源白名单仍强制 GitHub 官方域名 + HTTPS |
| `release.asset_template(s)` | ✅ | 至少一个；`asset_templates[GOOS]` 优先 |
| `release.digest_template` | ⬜ | 上游独立摘要资产；**即使省略**，NGG 仍会验证 GitHub Release Assets API 的 `digest: sha256:...` |
| `release.digest_format` | ⬜ | `api`（默认）/ `dgst+api` / `sums` |
| `release.archive` | ⬜ | `auto`（按扩展名推断）/ `zip` / `gz` / `tar.gz` / `none` |
| `release.binary_in_archive` | ⬜ | 包内二进制 glob；Windows 只匹配 `.exe` |
| `release.version_prefix` | ⬜ | `{vversion}` 前缀，如 `v` |
| `control_api` | ⬜ | 原生控制 API，Stage 2 的 Core Adapter Bridge 会用 |
| `version_regex` | ⬜ | 首个捕获组优先；失败回退通用提取；仍失败则**报错**，绝不伪造版本 |

### 5.4 校验与验收

```bash
# 1) 放描述文件
cp configs/examples/kernel-template.yaml configs/cores.d/my-core.yaml
# 编辑成真实上游信息

# 2) 重载注册表（不会打断正在运行的内核）
curl -X POST -H "$H" "$BASE/registry/reload"
# → {"loaded":1,"problems":[]}； descriptors 里 source 会是 "descriptor:my-core.yaml"

# 3) 按常规流程安装 / 启动 / 校验
curl -X POST -H "$H" -H 'Content-Type: application/json' -d '{}' "$BASE/cores/my-core/install"
curl -X POST -H "$H" "$BASE/cores/my-core/start"
curl -H "$H" "$BASE/cores/my-core/version"
curl -H "$H" "$BASE/cores/my-core/stats"
```

验收清单：注册表出现新内核 → 真实版本能读出 → 独立端口块 → 原生配置校验通过
→ 真实进程 PID / CPU / 内存 → 日志与 WS 事件 → 与另外三个内核同时运行互不干扰
→ 停止 / 卸载 / 回滚均独立生效。

## 6. 代码规范

- Adapter 必须**纯函数化**：只计算描述符、参数、默认配置、资产名；不启动进程、不写文件、不访问网络。
- 所有副作用集中在 Core Manager / Supervisor / VersionManager / ConfigManager。
- UI 不允许执行 Shell；HTTP 层只调用 `Engine` 的公开方法。
- 所有内核状态由真实 OS 进程与二进制存在性推导；禁止 `running=true` 之类的布尔伪造。
- 配置变更必须走 `Apply` 事务；禁止直接写有效配置文件。
- 错误统一用 `writeErr`，带机器可读 `ERROR_CODE:` 前缀（见 `02-api.md`）。
- 新增 Stage 2+ 能力只准加接口，不准加未验证实现。

## 7. 阶段验收清单（Phase 1）

- [x] 项目结构与技术设计文档
- [x] Core Adapter 通用接口 + 测试
- [x] 三个内核适配器 + 测试
- [x] 进程管理（真实进程、独立崩溃恢复）
- [x] 安装与版本管理（SHA256 + 来源 + 版本三重校验）
- [x] 配置管理与原生校验（强制事务）
- [x] 日志与监控（真实 CPU / 内存 / 端口探测）
- [x] Web 管理界面（Vue 3 + TS + Naive UI）
- [x] 自动化测试（`go test ./...` 全绿 + 真实二进制集成测试）
- [x] 完整集成验收（真实 API 安装三内核、多核并行启动、配置事务、状态恢复）
- [x] 安装 / 开发 / API / 故障排查文档
