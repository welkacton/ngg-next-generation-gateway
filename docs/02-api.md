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
{ "ngg_version":"2.13.1", "os":"linux", "arch":"amd64", "platform":"linux/amd64",
  "go_version":"go1.27.3", "started_at":"...", "listen_addr":"127.0.0.1:9090",
  "stage":1, "mode":"multi-core-parallel", "data_dir":"data" }
```

> `ngg_version` 现在来自 `internal/version.Release`（Gen 2 / v2.13.1），不再硬编码 Phase 1 字样。
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

---

## 11. 远程主机节点（v2.3.0，入口节点 / 出口节点）

`internal/hosts` 是独立于旧 `/nodes` 协议端点库的注册表，持久化在 `data/hosts-config.json`（0600）。
一台主机 = 任意位置的 VPS / 电脑 / 软路由，角色 `role` 可为 `inbound` / `outbound` / `both`，
随时可切换；`both` 会同时出现在入站和出站视图中。

> **v2.13.0 起没有 SSH，也没有节点侧探针**：节点上跑 `ngg-node`（管理接口）+ `ngg-core`（内核），
> 面板用双向 TLS 连入节点（`/node/xray/healthcheck`、`/node/xray/start`、`/node/stats/get-system-stats`），
> 与 Remnawave 的 node API 同一套形状。地址、端口、证书都在部署方案里，见 `docs/23-v2130-node-deploy-mtls.md`。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/hosts?role=inbound\|outbound\|both\|all` | `{hosts,count,stats,path}`，每项含 `probe`（最后一次真实探测） |
| `POST` | `/api/v1/hosts` | 新增主机，保存后立即执行一次真实 TCP 探测 |
| `GET` | `/api/v1/hosts/stats` | 注册表计数 + 统一网络引擎的**真实** `bytes_in` / `bytes_out` / `active`（未启动则为缺省） |
| `POST` | `/api/v1/hosts/probe` | 并发探测所有启用主机，返回 `{checked,online,results}` |
| `POST` | `/api/v1/hosts/{id}/probe` | 单主机真实 TCP 探测 |
| `POST` | `/api/v1/hosts/{id}/role` | body `{"role":"outbound"}`，任意切换入站 / 出站 / 双向 |
| `GET` / `PUT` / `DELETE` | `/api/v1/hosts/{id}` | 读取 / 修改 / 删除 |
| `GET` / `POST` / `DELETE` | `/api/v1/hosts/{id}/deploy` | 读取 / 生成 / 丢弃节点部署方案（生成时签发节点证书，脚本由面板渲染）。**v2.13.1 起**管理端口用 2222 时返回 `warning`，旧探针方案返回 `legacy:true` 且不回传脚本 |
| `GET` | `/api/v1/hosts/{id}/deploy/script` | 下载部署脚本（`text/x-shellscript`，含节点证书与私钥） |
| `POST` | `/api/v1/hosts/{id}/node/connect` | 连入节点（双向 TLS）：健康检查 → 下发内核配置 → 读指标，结果落盘 |
| `GET` | `/api/v1/hosts/{id}/node/status` | 节点状态（读缓存 + 最近一次连接结果）：`node_url` / `online` / `metrics` / `health` / `warning` |
| `GET` | `/api/v1/hosts/node-metrics` | 所有已部署节点的指标快照 `{nodes,interval_sec}`，后端每 30 秒连入各节点采样 |
| `GET` | `/api/v1/hosts/export` | 导出全量 JSON |
| `PUT` | `/api/v1/hosts/import` | 全量替换，任一条校验失败则整个节点库保持不变 |

**Host 字段**：`id` / `name` / `internal` / `country` / `address` / `node_port` / `svc_port` /
`role` / `enabled` / `protocol` / `core_id` / `profile` / `secret_key` / `proxy_url` / `plugin` /
`provider` / `ip_addresses` / `tags` / `note` / `track_traffic` / `user_ratio` / `node_ratio` /
`created_at` / `updated_at`。

**安全与诚实边界**：

- `address` 只接受 IP 或域名（拒绝 `http://` 等 scheme），端口必须 1..65535，倍率 0.1..100；
  `id` 唯一且限 64 字符，重复或非法记录一律 422。
- `probe` 来自 `net.Dialer` 对 `address:node_port`（无管理端口时用 `svc_port`）的**真实 TCP 连接**，
  3 秒超时；探测结果**只在内存中**，进程重启后清空，绝不把历史「在线」当当前状态。
- 远端 CPU / 内存 / 网卡 / 单节点流量 NGG 未采集，接口不返回这些字段，界面显示「未采集」，
  **不生成任何模拟速度、在线用户数或流量数字**。统计卡条的下行 / 上行 / 活跃连接来自
  `GET /network/status` 的 `metrics`，是 NGG 全局真实计数，不是单节点数据。

```bash
# 登记一台 VPS 当作出站，并立即真实探测
curl -X POST -H "$H" -H 'Content-Type: application/json' \
  -d '{"id":"us01","name":"US us01","country":"US","address":"203.0.113.10","node_port":3372,"svc_port":443,"role":"outbound","enabled":true,"protocol":"vless-reality"}' \
  "$BASE/hosts"

# 把另一台改成入站
curl -X POST -H "$H" -H 'Content-Type: application/json' \
  -d '{"role":"inbound"}' "$BASE/hosts/hk01/role"

curl -H "$H" "$BASE/hosts/stats"
curl -X POST -H "$H" "$BASE/hosts/probe"
```

---

## 12. 真实流量历史（v2.4.0，网络概览）

`GET /api/v1/network/metrics/history?minutes=5|15|30` 返回后端内存中的真实采样序列。
守护进程每 2 秒从统一网络引擎的 `Metrics.Snapshot()` 采一次，最多保留 900 点（30 分钟）；
进程重启后历史清空，绝不用旧数据伪装实时状态。

| 字段 | 说明 |
| --- | --- |
| `running` | 最新采样时网络引擎是否运行；`false` 时 `note` 明确说明没有真实流量 |
| `interval_sec` / `window_minutes` / `count` | 采样间隔、请求窗口、返回点数 |
| `source` / `samples[].source` | `tun` = 图表数据来自虚拟网卡的网卡级收发字节；`engine` = 来自会话计数 |
| `tun_open` | 采样时虚拟网卡是否已创建（关闭时 `tun_*` 一律为 0，不残留上次的数值） |
| `samples[].ts` | Unix 毫秒时间戳 |
| `samples[].bytes_in` / `bytes_out` | **有效**累计字节：虚拟网卡在线时取网卡计数，否则取引擎会话计数 |
| `samples[].rate_in` / `rate_out` | 相邻真实计数差 ÷ 真实时间差，单位 B/s |
| `samples[].tun_bytes_in/out`、`tun_rate_in/out` | 虚拟网卡的原始收发字节与速率（独立计数，不与会话计数相加，避免重复统计） |
| `samples[].engine_bytes_in/out` | 统一引擎会话计数的原始值，便于对照 |
| `samples[].active` / `connections` / `direct` / `proxied` / `dropped` / `dns_queries` / `rule_matches` / `failovers` | 统一网络引擎的真实计数 |

- **不伪造**：引擎未运行时速率为 0 并标记 `running=false`；计数器归零（引擎重启）时速率按 0 处理，
  不产生虚假尖峰；第一个采样点没有前驱，速率为 0。
- **v2.4.0 计量修复**：`internal/netengine/netengine.go` 的 `relay()` 原先把两个方向的字节都累加到
  `BytesOut`，导致 `bytes_in`（下载）恒为 0。现在 `upstream → client` 计入 `BytesIn`、
  `client → upstream` 计入 `BytesOut`，并有 `TestRelayCountsDirectionsSeparately` 回归测试。
- **v2.7.4 起以虚拟网卡为权威数据源**：网卡在线时 `bytes_in/out` 就是「整机流量」，
  因为它统计的是真正跨过 NGG 适配器的 IP 包；网卡未创建时才回落到会话计数。两者**从不相加**，
  否则同一份流量会被统计两次。界面在速率区与趋势图标题明确标注当前数据源。
- 前端 `web/src/views/NetworkOverview.vue` 每 2 秒轮询该端点，用纯 SVG 双系列面积图绘制
  速率 / 累计流量 / 连接数三种视图；无采样点时显示空状态，不绘制模拟曲线。

```bash
curl -H "$H" "$BASE/network/metrics/history?minutes=5"
# {"running":true,"source":"tun","tun_open":true,"interval_sec":2,
#  "window_minutes":5,"count":19,
#  "samples":[{"ts":...,"source":"tun","bytes_in":39849802,"bytes_out":1653,
#              "rate_in":8388608,"rate_out":350,
#              "tun_bytes_in":39849802,"tun_bytes_out":1653,...}],
#  "note":"数据源：NGG 虚拟网卡（网卡级真实收发字节）"}
```

## 13. 认证 `/api/v1/auth`（v2.6.0，登录门禁）

控制台不再使用浏览器端 API Token：首次打开必须手动创建管理员，之后只有登录窗口。
凭据以 Argon2id（m=64 MiB, t=3, p=2，随机盐）存于 `<data>/auth.json`（0600），
会话令牌为 32 字节随机串，磁盘上只保存其 SHA-256。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/auth/state` | 免鉴权。返回 `setup_required / authenticated / username / policy` |
| POST | `/auth/setup` | 免鉴权，**只能成功一次**：`{username,password}`，成功后直接下发会话 Cookie（201） |
| POST | `/auth/login` | `{username,password,remember}`，5 次失败锁定 60 秒（429） |
| POST | `/auth/logout` | 吊销当前会话并清 Cookie |
| POST | `/auth/password` | `{old_password,new_password}`；改密会吊销全部会话 |

```bash
# 首次创建管理员（只需一次）
curl -c jar.txt -X POST -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"your-strong-password"}' \
  http://127.0.0.1:9090/api/v1/auth/setup

# 之后所有请求带上会话 Cookie
curl -b jar.txt http://127.0.0.1:9090/api/v1/system
```

除 `/auth/*` 外的所有 `/api/*` 都要求「会话 Cookie 或 `Authorization: Bearer <ngg.yaml 的 token>`」，
且仍然只接受回环地址。静态前端资源不需要认证，否则登录页无法加载。

## 14. 链路编排 `/api/v1/pipeline`（v2.6.0）

把配置引擎六个二级页面串成一条可执行代理链路，顺序固定：

```
协议配置 → 入口节点 → 规则配置 → DNS配置 → 出口节点 → 用户管理
```

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/pipeline` | 当前链路 + 六段校验结果（`steps[]`，含 `status/value/detail/fix_hint/page`） |
| PUT | `/pipeline` | 保存链路参数（端口/策略/引用 id 会做真实校验，非法返回 422） |
| POST | `/pipeline/validate` | 重新校验（可传 `{config}` 做 dry-run） |
| POST | `/pipeline/render` | 返回生成的 mihomo YAML 与告警，不落盘 |
| POST | `/pipeline/apply` | 校验 → 渲染 → `mihomo -t` 原生校验 → 备份替换 → 重启内核 |
| POST | `/pipeline/test` | 真实 SOCKS5 CONNECT（默认目标 `www.gstatic.com:80`，可传 `{target}`） |

链路顺序与语义：

1. 客户端 → 入口 `listen:port`（mixed = SOCKS5 + HTTP）
2. 若选了入口节点：`dialer-proxy` 先连入口 VPS
3. 出口节点：`select / url-test / fallback` 策略组
4. 规则命中 `DIRECT / REJECT` 时提前分流，未命中走 `MATCH` 到出口组
5. 出口为空时链路直连（会在校验里给出 warn）

`direct_fallback` 只在 `select` 模式下生效——自动测速模式下把 `DIRECT` 放进组里会被延迟胜出，
等于绕过 VPS，所以代码层面禁止。

## 15. TUN 虚拟网卡 `/api/v1/network/tun`（v2.6.0）

Windows 上由 NGG 独占创建 wintun 网卡，并用 gVisor 用户态协议栈把 TCP 流量接入统一调度器；
Linux 使用内核 TUN + 独立路由表。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/network/tun/status` | 网卡真实状态、驱动签名信息、计数（收发包 / 字节 / 会话） |
| POST | `/network/tun/driver/install` | 下载官方 wintun 包 → 校验 SHA-256 → 解压 → Authenticode 校验 → 放到 `nggd.exe` 同目录 |
| POST | `/network/tun/attach` | `{route_all:false}` 创建网卡；`true` 时额外接管 `0.0.0.0/0` |
| POST | `/network/tun/detach` | 移除路由、销毁网卡 |

驱动来源固定为 `https://www.wintun.net/builds/wintun-0.14.1.zip`，
SHA-256 `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`，
解压后还会用 `WinVerifyTrust` 校验 Authenticode（WireGuard LLC）。NGG 不随包分发该 DLL（GPL-2.0）。

前置条件（后端会如实上报，不满足时拒绝创建而不是伪造网卡）：

- 进程具备提升令牌（Windows 服务以 LocalSystem 运行即满足）
- `wintun.dll` 与 `nggd.exe` 同目录（点一次「下载并安装驱动」即可）

验收脚本：以管理员运行 `scripts/verify-tun.ps1`（`-ExecutionPolicy Bypass`）。它会用临时实例
真实创建网卡、只对 `1.1.1.1/32` 加一条临时路由并发出一次真实 HTTP 请求、读回收发计数，
随后删除路由并销毁网卡，结果写入 `data/tun-verify/report.json`。

收发路径（v2.7.4 修复）：`channel.Endpoint.Read()` 是**非阻塞**的，队列为空时立即返回 nil。
旧实现用它做主机的写回循环，协程在启动瞬间（队列必然为空）就退出了，于是网卡「只收不发」：
Windows 状态里显示「已发送 16 MB / 已接收 0」，浏览器全部 `ERR_CONNECTION_TIMED_OUT`。
现在写回循环阻塞在 `ReadContext(ctx)` 上，stack 产出的每个包都会真实写入适配器，
并且有注入真实 IP 包的回归测试（`TestTunPumpSendsStackRepliesToDevice`，旧代码必失败）。

DNS 解析也走引擎的出口拨号器：接管默认路由后，未绑定网卡的 UDP socket 会把 DNS 查询送回 TUN 自己，
形成「查询 → 进隧道 → 再查询」的死循环。`TunDevice.dialer()` 复用 `egress.Dialer()` 的
`IP_UNICAST_IF` 绑定，确保查询从物理网卡发出。

UDP 处理策略（v2.7.5 改）：**不再静默丢弃**。

- `:53` → 交给 `network.dns.upstreams[0]`，走固定网卡的出口拨号器。
- 其它 UDP（QUIC / HTTP-3 / 游戏）→ 先问调度器，`DIRECT` 决策**直接做用户态 UDP 转发**
  （一条 socket 双向中继，空闲 90s 回收），并计入 `udp_flows` 与字节计数。
- 只有被判定为需要代理的 UDP 才会立刻关闭（本版未实现 SOCKS5 UDP ASSOCIATE）。
  立刻关闭而不是挂 30 秒，是为了让浏览器马上回落到 TCP，而不是等 QUIC 超时——
  旧实现每个非 DNS 流都空占一个 gVisor endpoint 30 秒，正是「延迟高、像丢包」的来源之一。

---

## v2.6.4 新增端点

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET / POST / DELETE | `/rulesets/github/presets` | GitHub 规则集来源列表 / 自定义新增 / 删除（内置不可删，持久化 `ruleset-github/presets.json`） |
| POST | `/network/rules/delete` | 规则批量删除 `{ids, all}`，返回剩余规则与删除数 |
| GET | `/rulesets/syntax` | 三内核官方规则集字段 schema 与示例 |
| GET | `/profiles/dns-schema` | 三内核官方 DNS 字段 schema 与示例 |
| POST | `/network/components/toggle` | 八项键位逐项开关（4 个组件 + 4 个策略），组件类返回 `requires_restart`；`GET /network/status.components` 自 v2.7.5 起只列 4 个真实组件 |
| GET / POST / PUT | `/nodes/{id}/metrics`、`/nodes/keys/generate`、`/nodes/keys/pair`、`/nodes/{id}/probe`、`/nodes/{id}/config-file` | 节点采集、密钥生成配对、真实 TCP 探测、配置文件 |

`GET /network/status` 增加 `default_core`、`direct_on_failure`、`bypass_tun` 与 `components`；`PUT /network/config` 对 `default_core` 做注册表白名单校验。
详见 [`15-v264-features.md`](15-v264-features.md)（本版能力说明与验证口径）。

---

## v2.6.5 新增端点：网关中心

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/gateway/config` | WAN / LAN / 防火墙配置；PPPoE 密码只返回 `password_set`，不回显明文 |
| PUT | `/gateway/config` | 校验并持久化（0600 原子写），返回 `changed` / `persisted` / `warnings` |
| GET | `/gateway/status` | 真实网卡列表、默认上行接口与地址、默认网关（Linux）、防火墙状态 |

配置结构：`wan{mode,interface,address,gateway,mtu,dns[],ipv6,username,password_set}`、
`lan{interface,address,netmask,dhcp_enabled,dhcp_start,dhcp_end,lease_minutes,domain}`、
`firewall{default_inbound,rules[],port_forwards[],upnp,conntrack}`。

### v2.6.7 网关中心扩展

`GET/PUT /gateway/config` 的 `config` 增加：

- `wans[]`：固定 6 个 WAN 口，字段 `id,name,enabled,mode,interface,address,gateway,mtu,dns[],ipv6,username,password_set,route,weight,priority`；`route` 为 `local`（本地直出）或 `proxy`（配置引擎国际代理）。
- `dns`：`mode(forward|recursive), servers[], fallback[], doh, cache_enabled, cache_size, cache_ttl, local_domain, hosts, blocklist[], dnssec, log_queries`。
- `wan` 保留为 `wans[0]` 的兼容字段。

`GET /gateway/status` 增加 `wan_links: [{id,interface,route,address,online}]`。

---

## v2.7.0 新增端点：远程出口节点 / 网关总控 / 客户端接入

### 远程代理节点（Agent）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/agents` | 节点列表 + 实时指标 + 公网监听配置 |
| POST | `/agents` | 创建节点，一次性返回 token、`agent.yaml` 与三平台安装脚本 |
| GET / PUT / DELETE | `/agents/{id}` | 读取 / 更新 / 删除节点 |
| POST | `/agents/{id}/rotate-token` | 轮换 token（旧 token 立即失效） |
| GET | `/agents/{id}/metrics` | 最近采样（内存环形，进程重启清空） |
| GET | `/agents/{id}/install?os=windows\|linux\|docker` | 安装脚本文本 |
| GET / PUT | `/agents/server` | 插件接入监听配置（默认启用，端口 9191） |

节点记录字段：`id,name,address,port,role,status,online,bound,os,arch,hostname,agent_version,last_seen,metrics`；
指标：`cpu_pct,cpu_cores,load1,mem_total,mem_used,swap_total,swap_used,disk_total,disk_used,disks[],interfaces[],rx_bytes,tx_bytes,rx_rate,tx_rate,uptime_sec,sampled_at,source`。
`source:"unavailable"` 表示插件尚未上报，此时数值为 0 且不参与展示。

公网监听（默认 `0.0.0.0:9191`，不走控制面鉴权，用节点 token）：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/v1/agent/report` | Header `X-NGG-Agent-Token`，上报系统指标 |
| GET | `/v1/agent/ping` | 连通性自检 |
| GET | `/sub/{token}` | 客户端订阅（sing-box JSON） |

### 网关总控

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET / PUT | `/gateway/master` | 虚拟网卡接管、分流策略、上行绑定 |
| GET | `/gateway/datapath` | 数据面状态（网卡是否存在、metric、默认路由是否经该网卡） |
| POST | `/gateway/datapath/apply` | 生成并（在 Windows 提权时）执行接管命令 |
| POST | `/gateway/datapath/revert` | 撤销接管 |
| POST | `/gateway/master/split-rules` | 把分流清单写入规则引擎（幂等，`tag=gateway-master`） |

`PUT /gateway/master` 与 `apply` 均返回 `warnings` / `commands`；非 Windows 或未提权时 `executed:false`，命令原样返回供人工执行，绝不上报「已接管」。

### 客户端接入

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/clients` | 客户端列表 + 接入设置 |
| POST | `/clients` | 创建客户端，一次性返回凭据 |
| PUT / DELETE | `/clients/{id}` | 更新（启用 / 配额 / 过期）/ 删除 |
| POST | `/clients/{id}/rotate` | 轮换凭据 |
| GET | `/clients/{id}/config?format=singbox\|mihomo\|wireguard\|socks` | 生成配置 |
| GET / PUT | `/clients/access` | 对外地址、端口、协议、TLS |

配置格式：sing-box JSON、Mihomo YAML、WireGuard `.conf`、SOCKS5 参数；订阅地址 `<public_url>/sub/<credential>`。

---

## v2.7.1 新增 / 变更端点

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/gateway/datapath/apply` | body 可带 `{master}`：先持久化再执行接管计划（一步完成「启用并接管」） |
| GET | `/cores/{id}/diagnostics` | 内核诊断：二进制 / 版本输出 / 配置校验 / 端口占用 / 日志尾部 / 五项检查 / hint |
| GET / POST | `/hosts/{id}/provision` | 读取 / 生成 VPS 部署脚本（VLESS / Trojan / Shadowsocks），返回 endpoint 与诚实 notes |
| GET | `/users/{id}/subscription` | 用户订阅：token、绑定信息、五条链接（首次调用自动签发 token） |
| PUT | `/users/{id}/subscription` | 绑定节点、协议、规则集、DNS |
| POST | `/users/{id}/subscription/rotate` | 轮换 token（旧链接立即失效） |

公网监听新增（不经会话鉴权）：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/sub/u/{token}?target=clash\|surge\|singbox\|v2ray\|socks` | 用户订阅正文；未知 / 禁用 / 过期统一 404 |

事件变更：内核崩溃恢复不再发 `system.alert`，改为 `core.state`（`payload.state=failed`、`payload.reason`），前端据此去重提示。

启动行为变更：`POST /cores/{id}/start` 先执行 `<binary> version` 预检，并在启动后观察 3 秒；进程立刻退出时返回 409 风格错误并携带真实 stderr，不进入崩溃重启循环。端口与配置不一致且存在冲突时，启动前会按分配器端口改写配置（走备份 + 原生校验）。

## 16. 控制中心 `/api/v1/gateway/control`（v2.7.2）

「接管全局」与「总控设置」合并为一个按序编排的页面（网关中心 → 控制中心），
接口也按同样的顺序返回三（或四）个步骤，状态全部来自真实探测：

```
① 启动 NGG 虚拟网卡  →  ② 强制接管全部流量  →  ③ WAN 出口（本地运营商）
```

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/gateway/control` | 聚合状态：`master` / `datapath` / `tun` / `adapters` / `egress` / `wans` / `steps` / `stale_adapters` |
| POST | `/gateway/control/start` | 按序执行：清理旧网卡 → 建卡 → 接管默认路由 → 绑定出口；逐步返回 `steps[]` |
| POST | `/gateway/control/stop` | 反向执行：撤销默认路由 → 销毁网卡 → `master.enabled=false` |
| GET | `/network/adapters` | 真实网卡列表：`virtual`（按系统描述识别 wintun/tap/tun/vpn…）、`is_ngg`、`default`（默认路由出口）、`ipv4` |
| PUT | `/network/config` | `{egress:{interface}}` 现在**立即重绑定**（`egress_applied=true`），不再要求重启引擎 |

`start` 的请求体（全部可选）：

```json
{
  "adapter_name": "NGG",
  "adapter_metric": 1,
  "capture_all": true,
  "bypass_private": true,
  "auto_start": false,
  "egress_interface": "以太网",
  "wan_id": "wan1"
}
```

行为约定（都已实现并有测试覆盖）：

- TUN 网卡默认名是 **NGG**（旧版为 `ngg0`）；`adapter_name` 与 `network.tun.name` 会自动对齐，
  改名时会先销毁旧网卡再重建。
- 若检测到上次接管遗留的、仍在同一 TUN 网段上的网卡（`stale_adapters`），
  `start` 会先删除其默认路由并禁用；未提权时给出可直接复制执行的命令。
- 接口优先级 `0` 会被规范为 `1`：`netsh ... metric=0` 的含义是「自动」，会让物理网卡赢回默认路由。
- **路由命令幂等**（v2.7.4）：「已经存在」不是错误。Windows 对已存在的路由仍返回退出码 1
  （`对象已存在。` / `The object already exists.`），以前会让「一键启动」在第 2 步失败并回滚 `enabled`；
  现在 `add route` 遇到该输出按成功处理，`delete route` 遇到「找不到元素」同样视为已完成，
  结果里通过 `idempotent[]` 如实列出被幂等处理的命令。
- 任一步骤失败时 `ok=false`，且 `master.enabled` 会回退为 `false`——开关不谎报未生效的接管。
- `wan_id` 与 `egress_interface` 同时给出时，会把该接口回写到对应 WAN 配置，WAN 设置页与
  控制中心描述同一个上行。

## 17. 数据面性能与丢包归因（v2.7.5）

`GET /api/v1/network/status` 的 `metrics` 新增：

| 字段 | 说明 |
| --- | --- |
| `drop_reasons` | 丢弃连接按原因拆分：`dial` 直连失败 / `no_kernel` 无可用内核 / `blocked` 规则拦截 / `loop` 环路拒绝 / `udp` UDP 不可代理 / `session` 会话创建失败 / `rule` 规则错误 |
| `udp_flows` | 已建立的 UDP 中继流数量（TUN 直连 UDP，含 QUIC） |

**为什么之前的「丢弃连接」是虚高的**：`protocolDispatcher` 与 `Engine.reject()` 都会给同一个失败会话
`Dropped.Add(1)`，控制台因此显示约两倍于真实值的丢包。现在只在 `reject()` 计数一次，
并用 `DropError{Reason}` 把原因带出来，界面在「连接与策略」里按原因列出。

TUN 数据面的其它调整：

- 只有 `DIRECT` 的 UDP 会被中继；需要代理的 UDP 立即关闭（见 §15）。
- `udp_flows` 为空闲 90 秒回收的流，不再让每个 QUIC 尝试空占 30 秒。
- 连接失败时 `DropError` 会带上具体阶段，便于区分「规则拦的」和「拨号失败的」。

## 18. 节点管理合并与配置文件绑定部署（v2.8.0）

### 18.1 入口节点 / 出口节点 → 节点管理

`/inbound` 与 `/outbound` 合并为 **`/nodes`（节点管理）**，菜单位于「协议配置」正下方：

- 角色成为同一份节点库上的字段（`inbound` / `outbound` / `both`），卡片上可一键切换；
- 旧地址 `/inbound`、`/outbound` 保留为 **跳转到 `/nodes`**，书签不会 404；
- 后端接口不变：`GET /hosts?role=inbound|outbound|both|all`，节点管理页固定用 `all`。

### 18.2 部署脚本可绑定「协议配置」档案

`POST /hosts/{id}/provision` 新增可选字段 `profile_id`：

```json
{ "profile_id": "edge-vless", "port": 8443, "sni": "edge.example.com" }
```

- 传 `profile_id` 时，后端用 `profiles.Render()` 渲染该档案，脚本**安装档案对应的内核**
  （mihomo / sing-box / xray-core）并把渲染后的内容**原样写入**该内核的配置文件；
- `port` / `sni` 缺省时从内容推断（首个 `port:` / `"port":` / `mixed-port:`，以及 `serverName` / `sni`），
  仅用于放通防火墙与自检，不会改写配置；
- 脚本先跑内核自带校验（`xray run -test` / `sing-box check` / `mihomo -t`），失败即中止；
- 结果里新增 `profile_id` / `profile_name` / `kernel`，并随 provision 持久化，控制台可再次显示绑定；
- 不传 `profile_id` 时保持原来的「按协议生成最小配置」模式（向后兼容）。

### 18.3 删掉的冗余面板

节点详情抽屉里的「配置文件 / 采集数据 / 密钥配对 / 连接测试」四张卡已删除：
它们与「协议配置」「网络概览」「控制中心」重复，且密钥配对与采集依赖未安装的 agent，
在节点页展示只会误导。节点页现在只保留节点本身的属性与真实探测结果。
