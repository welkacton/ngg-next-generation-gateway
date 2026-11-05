# NGG Phase 1 — REST / WebSocket API 文档

> 本文与 `internal/api/handlers.go`、`pkg/contracts/types.go` 一一对应。
> 所有响应字段均为**实际实现**，不包含规划中但未实现的字段。

## 1. 通用约定

### 1.1 基址与鉴权

- Base URL：`http://127.0.0.1:9090/api/v1`（`server.listen` / `server.port` 可配，默认仅回环）。
- 所有 `/api/` 路由要求请求头 `Authorization: Bearer <token>`。
- WebSocket 因浏览器无法设置自定义头，可改用查询参数 `?token=<token>`（仅 `/api/v1/ws` 生效）。
- Token 首次启动自动生成并写入 `configs/ngg.yaml`（文件权限 0600），也打印到服务日志；可用环境变量 `NGG_TOKEN` 覆盖。
- 服务端在 socket 层绑定回环地址，并在中间件二次校验 `RemoteAddr` 必须是回环；非本机请求返回 `403`。
- 缺 Token / Token 错误 → `401 {"error":"UNAUTHORIZED: missing or invalid API token"}`。

开发时前端 Vite 通过 proxy 把 `/api` 转发到 `http://127.0.0.1:9090`。

### 1.2 统一错误格式

所有错误响应是**单字段对象**：

```json
{ "error": "ERROR_CODE: 人类可读描述" }
```

`ERROR_CODE` 由真实失败原因推导（`internal/api/server.go` 的 `errorCodeFor`），可用前缀做客户端分支。

### 1.3 HTTP 状态码

| 状态码 | 含义 | 常见错误码 |
| --- | --- | --- |
| `200` | 成功（安装、导入、保存配置等也返回 200） | — |
| `400` | 请求体 / 参数不合法 | `INVALID_ARGUMENT` |
| `401` | 未认证 / Token 无效 | `UNAUTHORIZED` |
| `403` | 非本机来源 | `FORBIDDEN` |
| `404` | 资源不存在 / 内核未注册 / 内核未安装时读版本 / 进程未运行读 stats | `CORE_NOT_FOUND`、`NOT_FOUND` |
| `409` | 状态冲突（启停重启 / 卸载） | `CORE_NOT_INSTALLED`、`ALREADY_RUNNING`、`CORE_EXEC_FAILED`、`STATE_CONFLICT` |
| `422` | 配置校验未通过 / 备份回滚失败 | `CONFIG_INVALID`、`BACKUP_NOT_FOUND` |
| `502` | 上游下载、摘要、版本或内核执行失败 | `DOWNLOAD_FAILED`、`DIGEST_MISMATCH`、`VERSION_NOT_FOUND`、`CORE_EXEC_FAILED` |
| `500` | 未分类内部错误 | `INTERNAL_ERROR` |

> Phase 1 不返回 `201`；创建类操作统一返回 `200`。也不返回 `503`——SQLite 使用单连接 + WAL + busy_timeout，不向上游暴露锁错误。

### 1.4 错误码总表（实际会出现的）

| 错误码 | 触发条件 |
| --- | --- |
| `UNAUTHORIZED` | Token 缺失或无效 |
| `FORBIDDEN` | 非回环来源 |
| `INVALID_ARGUMENT` | JSON 解析失败、缺 `version` / `ids` / `backup_id`、缺 multipart `file` |
| `CORE_NOT_FOUND` | 内核 id 未注册，或错误文本含 "not found" |
| `CORE_NOT_INSTALLED` | 内核未安装时 start / restart / 读版本 / 原生配置校验 |
| `ALREADY_RUNNING` | 重复 start |
| `NOT_RUNNING` | 进程未运行时读实时状态 |
| `PORT_CONFLICT` | 启动前端口冲突检测发现占用 |
| `ARCH_UNSUPPORTED` | 适配器未声明当前 GOOS/GOARCH 支持 |
| `CONFIG_INVALID` | 内核原生配置校验失败、配置事务被拒 |
| `BACKUP_NOT_FOUND` | 备份不存在 / 备份完整性校验失败 |
| `VERSION_NOT_FOUND` | 目标版本无资产、版本解析失败、无已存版本可回滚 |
| `DIGEST_MISMATCH` | SHA256 与 GitHub Release Assets API digest 不一致 |
| `DOWNLOAD_FAILED` | 下载失败、来源主机不在白名单、上游无 digest 而拒绝安装 |
| `CORE_EXEC_FAILED` | 原生二进制执行失败 / 启动超时 |
| `STATE_CONFLICT` | 其它状态冲突 |
| `INTERNAL_ERROR` | 未分类错误 / panic |

> Phase 1 **不会**出现 `ALREADY_INSTALLED`（重复安装被当作升级处理）与 `SQLITE_BUSY`；
> 这两个编码保留给后续阶段。

---

## 2. 数据模型

### 2.1 `CoreStatus`

```json
{
  "id": "mihomo",
  "display_name": "Mihomo (Clash.Meta)",
  "state": "running",
  "installed": true,
  "version": "1.19.33",
  "pid": 12345,
  "binary_path": "data/cores/mihomo/bin/mihomo",
  "config_path": "data/cores/mihomo/config.yaml",
  "ports": [17890, 17891, 17892, 17893, 17894],
  "auto_start": true,
  "message": "",
  "restarts": 0,
  "updated_at": "2026-11-04T14:00:00Z",
  "capabilities": {
    "protocols": ["shadowsocks", "vmess", "..."],
    "inbound_types": ["mixed", "socks", "http", "..."],
    "features": ["rule", "clash-api", "..."],
    "worker_mode": true,
    "native_stream": false,
    "native_datagram": false
  }
}
```

`state` 枚举：`not_installed` / `installed` / `starting` / `running` / `stopping` / `stopped` / `failed` / `unknown`。
状态**由真实 OS 进程与二进制存在性推导**，不是维护用的假标志。

### 2.2 其它模型

| 模型 | 关键字段 |
| --- | --- |
| `SystemInfo` | `ngg_version, os, arch, platform, go_version, started_at, listen_addr, stage(1), mode("multi-core-parallel"), data_dir` |
| `CoreDescriptor` | `id, display_name, description, homepage, repository, capability, builtin, source` |
| `VersionInfo` | `version, raw, channel("installed"/"available"), detected_at, asset_url, digest_url` |
| `LogEntry` | `seq, core_id, stream("stdout"/"stderr"/"ngg"), line, time` |
| `ProcessStats` | `pid, cpu_percent, memory_rss, memory_vms, threads, uptime_sec, sampled_at` |
| `ConfigBackup` | `id, core_id, path, sha256, size, reason, created_at` |
| `PortAllocation` | `core_id, ports[], names{port:role}` |
| `AuditRecord` | `id, action, core_id, detail, created_at` |
| `OperationResult` | `ok, message, data?` |

---

## 3. 端点总表

> **Stage 2~5 新增端点见本节末尾的「Stage 2~5 新增端点」表（40 个）**；逐端点的方法 / 路径 / 请求体 / 响应体字段 / 错误码与 curl 示例见 **`09-stage2-5-api.md`**。下表 1~32 为 Phase 1 原有端点（保留不改）。

| # | 方法 | 路径 | 说明 |
| --- | --- | --- | --- |
| 1 | GET | `/system` | 系统 / 架构 / 版本 / 阶段信息 |
| 2 | GET | `/health` | 健康检查与运行内核数 |
| 3 | GET | `/cores` | 全部内核状态 |
| 4 | GET | `/cores/{id}` | 单个内核状态 |
| 5 | POST | `/cores/{id}/start` | 启动 |
| 6 | POST | `/cores/{id}/stop` | 停止 |
| 7 | POST | `/cores/{id}/restart` | 重启 |
| 8 | POST | `/cores/{id}/install` | 安装 / 升级（可选 version） |
| 9 | DELETE | `/cores/{id}` | 卸载（保留配置与备份） |
| 10 | POST | `/cores/{id}/upgrade` | 升级到指定版本 |
| 11 | POST | `/cores/{id}/rollback` | 回滚到已存版本 |
| 12 | GET | `/cores/{id}/versions` | 可用版本列表（GitHub Releases） |
| 13 | GET | `/cores/{id}/version` | 已安装真实版本 |
| 14 | POST | `/cores/batch/start` | 多核并行 / 单核独占启动 |
| 15 | POST | `/cores/batch/stop` | 批量停止 |
| 16 | GET | `/cores/{id}/config` | 读取当前有效配置 |
| 17 | PUT | `/cores/{id}/config` | 备份→原生校验→替换 |
| 18 | POST | `/cores/{id}/config/validate` | 仅调用原生校验，不改配置 |
| 19 | GET | `/cores/{id}/config/backups` | 备份列表 |
| 20 | POST | `/cores/{id}/config/rollback` | 配置回滚（含完整性校验 + 原生校验） |
| 21 | GET | `/cores/{id}/config/export` | 导出配置文件 |
| 22 | POST | `/cores/{id}/config/import` | 导入配置文件（multipart `file`） |
| 23 | GET | `/cores/{id}/logs` | 日志（按 seq 增量） |
| 24 | DELETE | `/cores/{id}/logs` | 清空该内核日志 |
| 25 | GET | `/cores/{id}/stats` | 真实 CPU / 内存 / 线程 / 运行时长 |
| 26 | GET | `/registry` | 已注册内核描述列表 |
| 27 | POST | `/registry/reload` | 重新扫描 `cores.d` 描述文件 |
| 28 | GET | `/ports` | 内核隔离端口分配 |
| 29 | GET | `/ports/check?ports=17890,17891` | 真实端口占用探测 |
| 30 | GET | `/backups` | 全部配置备份 |
| 31 | GET | `/audit?limit=50` | 审计日志 |
| 32 | GET | `/ws` | WebSocket 事件流 |

### 3.1 Stage 2~5 新增端点（40 个，追加于 Phase 1 的 32 个端点之后）

> 完整字段、响应体、错误码与 curl 示例见 **`09-stage2-5-api.md`**。下表编号 33~72 接续上表；路径均为 `/api/v1` 前缀下的相对路径，鉴权 / 错误格式与 §1 一致。

| # | 方法 | 路径 | 说明 | 阶段 |
| --- | --- | --- | --- | --- |
| 33 | GET | `/network/status` | 统一网络引擎状态（含 metrics / endpoints / healthy / resources） | Stage 2 |
| 34 | POST | `/network/start` | 启动网络引擎（全量资源回滚） | Stage 2 |
| 35 | POST | `/network/stop` | 停止网络引擎 | Stage 2 |
| 36 | GET | `/network/config` | 读取 `netengine.Config` | Stage 2 |
| 37 | PUT | `/network/config` | 更新配置（返回 `changed` / `requires_restart`） | Stage 2 |
| 38 | GET | `/network/rules` | 规则列表 + 默认动作 / 默认内核 | Stage 3 |
| 39 | POST | `/network/rules` | 新增单条规则 | Stage 3 |
| 40 | PUT | `/network/rules` | Fail-Closed 全量替换规则 | Stage 3 |
| 41 | DELETE | `/network/rules/{id}` | 删除规则 | Stage 3 |
| 42 | POST | `/network/rules/load` | 从 YAML/JSON 文件加载规则集 | Stage 3 |
| 43 | GET | `/network/dns` | DNS 引擎状态 | Stage 3 |
| 44 | POST | `/network/dns/resolve` | 真实域名解析（A / AAAA） | Stage 3 |
| 45 | POST | `/network/dns/hosts` | 设置 hosts / block 策略 | Stage 3 |
| 46 | GET | `/network/sessions` | 会话表（最多 200 条） | Stage 2 |
| 47 | GET | `/network/endpoints` | 内核本地端点 + 真实健康探测 | Stage 2 |
| 48 | POST | `/network/selftest` | 真实端到端自检（echo 逐字回显） | Stage 2 |
| 49 | POST | `/network/packettest` | 喂真实 IPv4/UDP/53 包给 PacketProcessor | Stage 2 |
| 50 | GET | `/stages` | 五个阶段的如实实现状态 | Stage 4 |
| 51 | GET | `/protocols` | 协议目录；默认排除 plaintext / plain-proxy，可按 `kernel` / `category` / `group` / `search` / `enabled` 过滤，`include_all=true` 返回全部 | Stage 4 |
| 51a | GET | `/protocols/meta` | 大类 / 分类 / 内核 / 配置格式元数据 | Stage 4 |
| 51a2 | GET | `/protocols/schema` | 三个内核的字段定义、中文含义与官方文档链接 | Stage 4 |
| 51b | POST | `/protocols` | 新建自定义协议（原始 JSON Spec，缺省 `enabled` 视为 true） | Stage 4 |
| 51c | POST | `/protocols/toggle` | 单条 / 多选 / 全部启停（`ids` 或 `all` + `enabled`） | Stage 4 |
| 51d | POST | `/protocols/validate` | 校验手写 JSON / YAML / CONF（自动剥离整行注释），不落盘 | Stage 4 |
| 51d2 | POST | `/protocols/annotate` | 给配置加/去逐字段注释，返回带注释文本与纯净文本 | Stage 4 |
| 51d3 | POST | `/protocols/preview` | 未注册协议的真实配置预览（可视化新建用），附 `annotated_content` | Stage 4 |
| 52 | GET | `/protocols/{id}` | 单个协议 Spec | Stage 4 |
| 52a | PUT | `/protocols/{id}` | 更新自定义协议；内置协议仅更新可编辑状态 | Stage 4 |
| 52b | DELETE | `/protocols/{id}` | 删除自定义协议（内置协议拒绝删除） | Stage 4 |
| 53 | POST | `/protocols/{id}/enable` | 启用 / 禁用单条协议 | Stage 4 |
| 54 | POST | `/protocols/{id}/config` | 生成并校验内核原生配置片段；`regenerate` 强制回内置模板 | Stage 4 |
| 54a | PUT | `/protocols/{id}/config` | 保存编辑后的配置为该协议的覆盖模板 | Stage 4 |
| 55 | GET | `/protocols/catalog` | **只读**协议名称目录（三内核官网定时同步）+ 同步状态；详见 `docs/11-protocol-center.md` | v2.2.1 |
| 55a | POST | `/protocols/catalog/refresh` | 立即从三个内核官方文档同步最新协议清单 | v2.2.1 |
| 56 | GET | `/profiles/options` | 配置文件：内核选项、六个分区定义、官方文档链接 | v2.2.1 |
| 56a | GET / POST | `/profiles` | 配置文件卡片列表 / 新建 | v2.2.1 |
| 56b | POST | `/profiles/template` | 取某内核带中文注释的起始模板（不落盘） | v2.2.1 |
| 56c | POST | `/profiles/validate` | 逐段 + 合并后真实语法校验，返回完整配置文本 | v2.2.1 |
| 56d | GET / PUT / DELETE | `/profiles/{id}` | 读取 / 更新 / 删除配置文件 | v2.2.1 |
| 56e | POST | `/profiles/{id}/render` `…/duplicate` `…/activate` | 生成完整配置 / 复制 / 设为默认 | v2.2.1 |
| 56f | POST | `/profiles/split` | **完整配置反向拆分**：`{profile, content}` → 6 个分区 + `changed` / `warnings`；纯计算不落盘，语法错误返回 422；详见 `docs/11-protocol-center.md` | v2.2.2 |
| 57 | GET | `/rulesets/github/presets` | 内置 GitHub 规则集预设仓库 | v2.2.1 |
| 57a | GET | `/rulesets/github/catalog` | 已缓存的 GitHub 分流规则分类目录 | v2.2.1 |
| 57b | POST | `/rulesets/github/catalog/refresh` | 真实抓取 git-trees 并重建分类目录 | v2.2.1 |
| 57c | POST | `/rulesets/github/mount` | 批量 `mount` / `import` / `both`，逐分类返回真实识别条数与错误；单次上限 50，前端全选时自动分批；详见 `docs/12-github-rulesets.md` | v2.2.1 |
| 58 | POST | `/cores/{id}/install` | **异步后台安装**：立即返回 202 + `task_id`，下载/校验/版本检查在后台执行，进度走 `core.install` WebSocket 事件；`?wait=1` 保留同步模式 | v2.2.1 |
| 58a | GET | `/cores/install/status` | 轮询安装任务：`?id=` 查单个、`?core=` 查该内核最近一次、无参数列全部 | v2.2.1 |
| 58b | POST | `/profiles/toggle` | 配置文件批量启停 `{ids, all, enabled}` | v2.2.1 |
| 54b | DELETE | `/protocols/{id}/config` | 清除覆盖模板，恢复内置生成 | Stage 4 |
| 55 | GET | `/pq/status` | 后量子套件信息与用途边界 | Stage 4 |
| 56 | POST | `/pq/selftest` | 真实混合握手 + AEAD 往返自检 | Stage 4 |
| 57 | GET | `/plugins` | 插件列表 | Stage 4 |
| 58 | POST | `/plugins` | 安装插件（JS / WASM manifest） | Stage 4 |
| 59 | POST | `/plugins/reload` | 重载 `plugins.d` 目录 | Stage 4 |
| 60 | POST | `/plugins/{id}/enable` | 启用 / 禁用插件 | Stage 4 |
| 61 | POST | `/plugins/{id}/test` | 运行 session hook 并返回决策输出 | Stage 4 |
| 62 | DELETE | `/plugins/{id}` | 删除插件 | Stage 4 |
| 63 | GET | `/sdwan/status` | SD-WAN 状态（策略 / 链路 / 切换次数） | Stage 5 |
| 64 | GET | `/sdwan/links` | WAN 链路列表 | Stage 5 |
| 65 | POST | `/sdwan/links` | 新增 WAN 链路 | Stage 5 |
| 66 | DELETE | `/sdwan/links/{id}` | 删除 WAN 链路 | Stage 5 |
| 67 | POST | `/sdwan/{action}` | `start` / `stop` / `failover` / `policy` | Stage 5 |
| 68 | GET | `/tenants` | 租户列表 | Stage 5 |
| 69 | POST | `/tenants` | 新增租户 | Stage 5 |
| 70 | DELETE | `/tenants/{id}` | 删除租户 | Stage 5 |
| 71 | GET | `/platforms` | 构建矩阵与客户端能力边界 | Stage 5 |
| 72 | POST | `/platforms/build` | 真实 `go build` 交叉编译 | Stage 5 |

---

## 4. 系统与状态

### `GET /system`
```json
{ "ngg_version":"2.2.2", "os":"linux", "arch":"amd64", "platform":"linux/amd64",
  "go_version":"go1.27.3", "started_at":"...", "listen_addr":"127.0.0.1:9090",
  "stage":1, "mode":"multi-core-parallel", "data_dir":"data" }
```

> `ngg_version` 现在来自 `internal/version.Release`（Gen 2 / v2.2.2），不再硬编码 Phase 1 字样。
> 代际字段与仓库更新端点见 [`10-updates.md`](10-updates.md) / [`GENERATIONS.md`](GENERATIONS.md)。

### `GET /health`
```json
{ "status":"ok", "cores_running":2, "running":["mihomo","sing-box"],
  "uptime_sec":312, "registered":3, "subscribers":1, "stage":1,
  "unified_tun":"reserved-stage-2" }
```
`unified_tun` 明确标注统一 TUN 是 Stage 2 预留，**不是**已启用功能。

### `GET /cores` / `GET /cores/{id}`
返回 `CoreStatus[]` / `CoreStatus`。未注册 id → `404 CORE_NOT_FOUND`。

---

## 5. 内核生命周期

所有操作经 `REST → Core Manager → Core Adapter → 原生进程`。UI 无法执行 Shell。

### `POST /cores/{id}/start` · `/stop` · `/restart`
无请求体。成功：`{"ok":true,"message":"started|stopped|restarted","data":null}`。

启动前置检查（任一失败即 `409`，Fail-Closed）：
1. 二进制真实存在；2. 未在其它内核占用端口；3. 适配器声明支持当前 GOOS/GOARCH；
4. 隔离端口块 5 个端口的 TCP/UDP 均可绑定；5. 配置文件存在；
6. **调用内核原生校验器通过**（`mihomo -t -f`、`xray -test -config`、`sing-box check -c`）。

停止会先标记 `stopping`，向真实进程发终止信号，超时后强杀，再写回 `desired_state`。

### `POST /cores/{id}/install`
```json
{ "version": "v1.19.33" }   // 可省略或传 {}，表示最新版本
```
响应：`{"ok":true,"message":"installed 1.19.33","data":{VersionInfo}}`。

安装链路（真实执行，非模拟）：
查询官方 GitHub Release → 按平台渲染资产名 → 读取 Assets API 的 `digest: sha256:...`
→ 校验下载 URL 主机白名单（HTTPS + github.com / *.githubusercontent.com）
→ 流式下载并计算 SHA256 → 比对（Xray 额外校验 `.dgst`）
→ 解包（zip / gz / tar.gz / raw）→ **执行下载到的二进制 `-v` 并解析真实版本**
→ 版本与请求的 tag 比对 → 原子替换 `bin/<binary>`，旧版本保存在 `versions/<tag>/` 供回滚。

任何一步失败都不会替换当前有效二进制。运行中升级会先停内核、装完再自动启动。

### `DELETE /cores/{id}`
卸载二进制与 `versions/`，**保留**配置与备份；运行时会先停止。

### `POST /cores/{id}/upgrade` · `/rollback`
请求体：`{"version":"v1.19.33"}`（必填）。`upgrade` 等价于带版本的 install；
`rollback` 从 `versions/<version>/` 恢复并执行 `-v` 验证。

### `GET /cores/{id}/versions` · `/version`
`/versions` 返回 GitHub 最近 25 个 release（`channel:"available"`）；
`/version` 执行本机二进制返回 `channel:"installed"` 的真实版本。

### `POST /cores/batch/start`
```json
{ "ids": ["mihomo","xray-core","sing-box"], "exclusive": false }
```
响应：`{"results":[{"id":"mihomo","ok":true,"message":"ok"}, ...]}`。

- `exclusive:false`（默认）：**多核并行**，各内核在独立 goroutine 中同时启动。
- `exclusive:true`：单核模式，先停止其它正在运行的内核，再启动指定内核。
- 一个内核失败**不会**取消其它内核的启动结果。

### `POST /cores/batch/stop`
`{"ids":[...]}` → 同样的 `results` 结构。

---

## 6. 配置管理

> **强制事务**：备份当前有效配置（记录 SHA256）→ 写候选到 `.staging/<同名文件>`
> → 调用内核原生校验 → 通过才原子替换。校验失败保留原配置字节不变。

### `GET /cores/{id}/config`
```json
{ "content":"...", "path":"data/cores/mihomo/config.yaml",
  "sha256":"...", "size":412, "updated_at":"2026-11-04T14:00:00Z" }
```

### `POST /cores/{id}/config/validate`
请求：`{"content":"..."}`。
响应（**HTTP 200**，校验失败也返回 200，靠 `ok` 判断）：
```json
{ "ok": true, "output": "$ mihomo.exe -t -f ...\nconfiguration file ... test is successful" }
```
失败时额外带 `error` 字段，`output` 含所有原生校验尝试的完整输出。

### `PUT /cores/{id}/config`
请求：`{"content":"...","reason":"manual-edit"}`（`reason` 可选，默认 `api.update`）。
成功：
```json
{ "ok":true, "message":"配置已备份、原生校验通过并替换生效",
  "backup": { "id":"mihomo-20261104T140000.000000000-abcd", "core_id":"mihomo",
              "path":"...","sha256":"...","size":412,"reason":"manual-edit","created_at":"..." } }
```
失败 → `422 CONFIG_INVALID`（或 `BACKUP_NOT_FOUND`），原配置不变。

### `GET /cores/{id}/config/backups` · `GET /backups`
返回 `ConfigBackup[]`（后者返回全部内核）。

### `POST /cores/{id}/config/rollback`
请求：`{"backup_id":"..."}`。
流程：校验备份归属 → 校验备份文件 SHA256 完整性 → 走完整 Apply 事务（会再生成一份"回滚前"备份）。
成功：`{"ok":true,"message":"配置已回滚并通过原生校验","data":null}`。

### `GET /cores/{id}/config/export`
返回原始配置文件二进制，`Content-Disposition: attachment; filename="config.yaml"`。

### `POST /cores/{id}/config/import`
`multipart/form-data`，字段名 `file`，上限 16 MiB。走完整 Apply 事务（reason=`api.import`）。

---

## 7. 日志、监控、注册表、端口、审计

### `GET /cores/{id}/logs?after=<seq>&limit=<n>`
返回 `LogEntry[]`。`after` 为该内核上次最大 `seq`，实现增量拉取；`limit` 默认 200，最大 5000。
实时推送请用 WebSocket。`DELETE /cores/{id}/logs` 清空该内核日志缓冲。

### `GET /cores/{id}/stats`
返回 `ProcessStats`，数据来自真实 OS 进程（gopsutil），进程不存在 → `404`。
服务每 5 秒也会通过 WS 主动推送 `core.stats`。

### `GET /registry`
返回 `CoreDescriptor[]`，含 `builtin:true` 的三个内核与任何 `cores.d` 描述文件注册的
`builtin:false` 内核。`source` 为 `builtin` 或 `descriptor:<filename>`。

### `POST /registry/reload`
```json
{ "loaded": 1, "problems": ["bad.yaml: ..."] }
```
只注册**新增**描述文件；已存在条目不会被替换，重载不会打断运行中的内核。

### `GET /ports` · `GET /ports/check?ports=...`
`/ports` 返回 `PortAllocation[]`，每内核 5 个互斥端口（mixed / socks / http / api / dns）。
`/ports/check` 返回 `{"conflicts":[17890]}`，探测真实 TCP + UDP 绑定。

### `GET /audit?limit=50`
返回 `AuditRecord[]`（倒序），记录 start/stop/install/config.apply/config.rejected/restore 等动作。

---

## 8. WebSocket `/api/v1/ws?token=<TOKEN>`

连接成功后立即收到一条 `system.alert` 欢迎事件。服务端每 25 秒发 Ping。
推送信封：

```json
{ "type":"core.state|core.log|core.stats|core.install|system.alert",
  "core_id":"mihomo",
  "payload": { ... },
  "timestamp":"2026-11-04T14:00:00Z" }
```

| type | payload |
| --- | --- |
| `core.state` | `CoreStatus`（或配置事件 `{event:"config.applied",detail:{...}}`） |
| `core.log` | `LogEntry` |
| `core.stats` | `ProcessStats` |
| `core.install` | `{message,version}` 安装进度 |
| `system.alert` | `{message}` 崩溃恢复 / 服务告警 |

客户端订阅是非阻塞的：慢客户端会被丢弃事件而不是阻塞整个事件总线。前端实现指数退避自动重连。

---

## 9. curl 示例

```bash
TOKEN='<从 configs/ngg.yaml 读取>'
BASE='http://127.0.0.1:9090/api/v1'
H="Authorization: Bearer $TOKEN"

curl -H "$H" "$BASE/system"
curl -H "$H" "$BASE/cores"
curl -H "$H" "$BASE/registry"

# 安装三个内核（留空 version = 最新稳定版）
curl -X POST -H "$H" -H 'Content-Type: application/json' -d '{}' "$BASE/cores/mihomo/install"
curl -X POST -H "$H" -H 'Content-Type: application/json' -d '{}' "$BASE/cores/xray-core/install"
curl -X POST -H "$H" -H 'Content-Type: application/json' -d '{}' "$BASE/cores/sing-box/install"

# 多核并行启动（三个内核同时运行）
curl -X POST -H "$H" -H 'Content-Type: application/json' \
  -d '{"ids":["mihomo","xray-core","sing-box"],"exclusive":false}' "$BASE/cores/batch/start"

# 单核独占模式
curl -X POST -H "$H" -H 'Content-Type: application/json' \
  -d '{"ids":["mihomo"],"exclusive":true}' "$BASE/cores/batch/start"

curl -H "$H" "$BASE/cores/mihomo/stats"
curl -H "$H" "$BASE/cores/mihomo/logs?limit=100"
curl -H "$H" "$BASE/cores/mihomo/config" | jq -r .content > /tmp/mihomo.yaml
curl -X POST -H "$H" -H 'Content-Type: application/json' \
  -d '{"content":"mixed-port: 17890\n"}' "$BASE/cores/mihomo/config/validate"
curl -X POST -H "$H" -H 'Content-Type: application/json' \
  -d '{"ids":["mihomo","xray-core","sing-box"]}' "$BASE/cores/batch/stop"

# WebSocket（需 wscat / websocat）
wscat -c "ws://127.0.0.1:9090/api/v1/ws?token=$TOKEN"
```

---

## 10. 安全与边界（再次强调）

- UI 只能调用本 API；API 只能调用 Core Manager；Core Manager 只能经 Core Adapter 操作内核。
- 内核在 Worker Mode 运行：默认配置显式关闭 TUN / DNS 劫持，不修改全局路由。
- Phase 1 **不创建任何系统级 TUN**，也不接管系统流量；统一 TUN / 规则 / DNS / Dispatcher
  仅在 `internal/netengine/interfaces.go` 预留接口，属 Stage 2/3。
- 下载 Fail-Closed：无官方 digest、主机不在白名单、摘要不符、版本不符，一律拒绝安装。

> **Stage 2~5 更新**：以上为 **Phase 1 基线**的安全边界（保留不改）。当前版本已新增 40 个端点（见 §3.1 与 `09-stage2-5-api.md`），`internal/netengine/` 已落地为真实数据平面；统一 TUN 仍**仅 Linux + `CAP_NET_ADMIN`** 可用，Windows/macOS 返回 `ErrTUNUnsupported` 并改用全平台 userspace 入口，TUN 内 TCP 流终止仍为已知缺口。详见 `07-stage2-network-engine.md` / `08-stage3-5-features.md`。
