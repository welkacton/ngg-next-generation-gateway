# NGG Stage 2~5 — REST API 文档（新增端点）

> **适用版本**：Phase 1 + Stage 2~5 当前实现
> **对应源码**：`internal/api/network.go`、`internal/api/stage45.go`、`internal/api/server.go`（`routes()` 第 96~140 行）、`internal/engine/stage45.go`、`internal/api/handlers.go`
> **配套文档**：`02-api.md`（Phase 1 的 32 个端点）、`07-stage2-network-engine.md`（Stage 2 设计）、`08-stage3-5-features.md`（Stage 3~5 能力）
> **文档状态**：以下 **40 个端点**均为 `server.go` 中实际注册的路由；请求体 / 响应体字段、HTTP 状态码、错误码全部以代码为准。

---

## 1. 通用约定

### 1.1 基址与鉴权

- Base URL：`http://127.0.0.1:9090/api/v1`（`server.listen` / `server.port` 可配，默认仅回环）。
- 所有 `/api/` 路由由 `middleware` 双重校验：**socket 绑定回环地址** + `RemoteAddr` 必须回环；非本机 → `403 FORBIDDEN: NGG control API is local-only`。
- 请求头 `Authorization: Bearer <token>`；缺失 / 错误 → `401 UNAUTHORIZED: missing or invalid API token`。
- Token 首次启动自动生成并写入 `configs/ngg.yaml`（0600），可用 `NGG_TOKEN` 覆盖。

### 1.2 统一错误格式

所有错误响应是**单字段对象**（`server.go:writeErr`）：

```json
{ "error": "ERROR_CODE: 人类可读描述" }
```

`ERROR_CODE` 由 `errorCodeFor(status, msg)` 从**真实失败文本**推导，不按端点硬编码。本批端点实际会出现的映射：

| 触发条件 | HTTP | `ERROR_CODE` |
| --- | --- | --- |
| 缺 / 错 Token | `401` | `UNAUTHORIZED` |
| 非回环来源 | `403` | `FORBIDDEN` |
| JSON 解析失败、缺必填字段（`path` / `name`） | `400` | `INVALID_ARGUMENT` |
| 规则 / 配置 / 插件 / 链路 / 租户业务校验失败 | `422` | `CONFIG_INVALID` |
| DNS 上游查询 / 域名被策略阻断 | `502` | `CORE_EXEC_FAILED` |
| 错误文本含 `not registered` 或 `not found` | `404` | `CORE_NOT_FOUND` |
| 其它 `404`（如 `unknown ... action`、`failed to load`） | `404` | `NOT_FOUND` |
| 重复启动（文本含 `already running`） | `409` | `ALREADY_RUNNING` |
| 其它 `409`（资源冲突、监听失败、引擎未初始化） | `409` | `STATE_CONFLICT` |
| 网络引擎未初始化（`503 ServiceUnavailable`） | `503` | `INTERNAL_ERROR` |
| 自检启动失败 / mkdir 失败 / panic | `500` | `INTERNAL_ERROR` |
| 无健康 WAN 链路（`503`） | `503` | `INTERNAL_ERROR` |

> `503` 不在 `errorCodeFor` 的 status 分支内，落到 `default → INTERNAL_ERROR`，这是**代码的实际行为**。错误文本若额外含 `not found` 等关键词，会优先命中对应前缀（例如 `rule X not found` 得到 `CORE_NOT_FOUND`）。

### 1.3 通用成功结构

`writeOK(w, msg)` 返回 `contracts.OperationResult`：`{"ok":true,"message":"..."}`（`data` 省略）。`writeJSON(w, 200, v)` 直接序列化对象 / 数组。

### 1.4 端点分组

| 组 | 端点数 | 小节 |
| --- | --- | --- |
| Stage 2~3 网络引擎 | 17 | §2 |
| Stage 4 阶段状态 / 协议 / 后量子 / 插件 | 13 | §3 |
| Stage 5 SD-WAN / 多租户 / 跨平台 | 10 | §4 |
| **合计** | **40** | — |

---

## 2. Stage 2~3 网络引擎端点（17 个）

### `GET /api/v1/network/status`

- 请求体：无。
- 响应 `200`：`engine.NetworkStatus()` → 若 `network==nil` 返回 `{"running":false,"stage":1}`；否则 `netengine.Engine.Status()`：

| 字段 | 含义 |
| --- | --- |
| `running` / `stage` | 运行态 / 固定 `2` |
| `fail_closed` / `failover` | 配置开关 |
| `ingress` | `{enabled, address, protocols:["socks5","socks4a","http-connect","http-absolute"]}` |
| `tun` | `{configured(=TUN.AutoRoute), active, name, mtu, stats}`；`stats` 为 `TunStats{BytesIn,BytesOut,PacketsIn,PacketsOut,Sessions,Since}` |
| `dns` | `dnsEngine.Status()` 见 §2.11 |
| `rules` / `sessions` / `plugins` | 规则数 / 会话数 / 启用插件数 |
| `endpoints` | `[]{core_id, kind, address, healthy, error?}`（真实健康探测） |
| `healthy` | 健康内核 ID 数组（真实拨号） |
| `resources` | `ResourceManager.Snapshot()`：`kind -> ["key=owner", ...]` |
| `metrics` | `Metrics.Snapshot()`：`connections, active, bytes_in, bytes_out, direct, proxied, dropped, dns_queries, dns_cache_hits, rule_matches, kernel_errors, failovers, tun_packets_in, tun_packets_out, uptime_sec` |

- 错误：仅通用 `401` / `403`。

### `POST /api/v1/network/start`

- 请求体：无。
- 响应 `200`：`{"ok":true,"message":"NGG unified network engine started"}`。
- 错误：`409 ALREADY_RUNNING`（重复 start，`ErrAlreadyRunning` 文本含 `already running`）；`409 STATE_CONFLICT`（资源预留冲突、DNS / ingress 监听失败、引擎未初始化）。

### `POST /api/v1/network/stop`

- 请求体：无。
- 响应 `200`：`{"ok":true,"message":"NGG unified network engine stopped"}`（`Stop()` 在未运行时返回 `nil`，仍为 200）。
- 错误：`409 STATE_CONFLICT`（停止过程出错）。

### `GET /api/v1/network/config`

- 请求体：无。
- 响应 `200`：`netengine.Config`（`json` tag）：

| 字段 | 子字段 |
| --- | --- |
| `enabled` / `fail_closed` / `failover` | bool |
| `ingress` | `enabled, listen, port, auth_user, auth_pass` |
| `tun` | `name, mtu, ipv4, ipv6, auto_route, strict_route`（`TunConfig` 已带 `json`/`yaml` tag） |
| `dns` | `enabled, listen, port, upstreams, cache_ttl, hosts, block` |
| `egress` | `interface, local_addr, mark, bypass_tun` |
| `default_core` / `direct_on_failure` | string / bool |

- 错误：`503 INTERNAL_ERROR`（`network engine not initialized`）。

### `PUT /api/v1/network/config`

- 请求体（字段均可选，未提供则不改；嵌套对象按字段 patch，不会用零值覆盖未提供的子字段）：

```json
{
  "fail_closed": true,
  "failover": true,
  "default_core": "mihomo",
  "direct_on_failure": false,
  "egress_mark": 20039,
  "bypass_tun": true,
  "ingress": { "enabled": true, "listen": "127.0.0.1", "port": 18080, "auth_user": "", "auth_pass": "" },
  "dns": { "enabled": true, "listen": "127.0.0.1", "port": 18053, "upstreams": ["223.5.5.5:53"], "cache_ttl": 60, "hosts": {}, "block": [] },
  "tun": { "name": "ngg0", "mtu": 1500, "ipv4": "10.89.0.1/24", "ipv6": "fd89::1/64", "auto_route": false, "strict_route": true },
  "egress": { "interface": "eth0", "local_addr": "", "mark": 20039, "bypass_tun": true }
}
```

- 扁平字段 `egress_mark` / `bypass_tun` 是 `egress.mark` / `egress.bypass_tun` 的快捷写法，保留向后兼容。
- `ingress` / `dns` / `tun` / `egress` 的改动会进入 `requires_restart`（监听地址、端口、TUN、出口标记都要重启网络引擎才生效）。
- `ingress.enabled=true` 时校验 `port` 在 1..65535，且非回环 `listen` 必须同时提供 `auth_user` / `auth_pass`（对应 `ErrIngressInsecure`）；`dns.enabled=true` 时校验 `port`。
- 非 Linux 平台设置 `tun.auto_route=true` 会返回 `warnings`，如实说明该能力边界。
- **持久化**：变更写入 `<storage.dir>/network-config.json`（0600），服务重启时由 `engine.New` 加载并覆盖 `configs/ngg.yaml` 中的 `network` 段。主配置文件（含注释与 API Token）**不会被控制面重写**；写入失败时内存配置会回滚。
- 响应 `200`：

```json
{
  "ok": true,
  "changed": ["ingress", "dns"],
  "requires_restart": ["ingress", "dns"],
  "warnings": [],
  "persisted": true,
  "persist_path": "data/network-config.json",
  "config": { "...完整 Config..." }
}
```

- 错误：`400 INVALID_ARGUMENT`（JSON 非法 / 端口越界 / 非回环入口缺认证）；`503 INTERNAL_ERROR`（引擎未初始化）；`500 INTERNAL_ERROR`（持久化失败，内存已回滚）。

### `GET /api/v1/network/rules`

- 请求体：无。
- 响应 `200`：`{"rules":[Rule...],"default_action":"DIRECT","default_core":"..."}`。`Rule` 字段：`id, type, value, action, core_id, chain, tenant_id, priority, enabled, comment`。
- 错误：`503 INTERNAL_ERROR`（引擎未初始化）。

### `POST /api/v1/network/rules`

- 请求体：单个 `Rule` 对象。

```json
{ "id":"r-example", "type":"DOMAIN-SUFFIX", "value":".example.com",
  "action":"PROXY", "core_id":"mihomo", "priority":50, "enabled":true, "comment":"demo" }
```

- 响应 `200`：`{"ok":true,"rule":{...}}`。
- 错误：`400 INVALID_ARGUMENT`（JSON 非法）；`422 CONFIG_INVALID`（`Validate()` 失败：缺 `id`、重复 ID、未知 `type` / `action`、`PROXY` 缺 `core_id` 与 `chain`、非法正则、非法 CIDR）；`503 INTERNAL_ERROR`。
- 类型 / 动作枚举见 `08-stage3-5-features.md` §1（10 种类型、4 种动作）。

### `PUT /api/v1/network/rules`

- 请求体（**Fail-Closed 全量替换**：全部规则校验通过才生效）：

```json
{ "rules":[ { "id":"r1","type":"MATCH","action":"PROXY","core_id":"mihomo","priority":10,"enabled":true } ],
  "default_action":"DIRECT", "default_core":"mihomo" }
```

- 响应 `200`：`{"ok":true,"count":1}`。
- 错误：`400 INVALID_ARGUMENT`；`422 CONFIG_INVALID`（任一规则非法或 ID 重复，原子拒绝）；`503 INTERNAL_ERROR`。

### `DELETE /api/v1/network/rules/{id}`

- 路径参数：`id`（`r.PathValue("id")`）。
- 响应 `200`：`{"ok":true,"message":"rule removed"}`。
- 错误：`404 CORE_NOT_FOUND`（`rule <id> not found` 含 `not found`）；`503 INTERNAL_ERROR`。

### `POST /api/v1/network/rules/load`

- 请求体：`{"path":"/abs/or/relative/rules.yaml"}`（`.yaml` / `.yml` / `.json`）。
- 响应 `200`：`{"ok":true,"loaded":4,"path":"..."}`（返回 `RuleFile` 中的规则数）。
- 错误：`400 INVALID_ARGUMENT`（`path` 缺失或 JSON 非法）；`422 CONFIG_INVALID`（读文件 / YAML 解析 / 规则校验失败）。

### `GET /api/v1/network/dns`

- 请求体：无。
- 响应 `200`：`dnsEngine.Status()` → `{enabled, running, listen, port, upstreams, hosts, blocked, cache_entries}`（`hosts` / `blocked` / `cache_entries` 为条目数）。
- 错误：仅通用 `401` / `403`。

### `POST /api/v1/network/dns/resolve`

- 请求体：`{"name":"www.example.com","type":"A"}`（`type` 可省略，缺省 `"A"`；可填 `"AAAA"`）。
- 响应 `200`：`{"ok":true,"name":"...","type":"A","answers":["93.184.216.34"]}`。
- 错误：`400 INVALID_ARGUMENT`（`name` 缺失 / JSON 非法）；`502 CORE_EXEC_FAILED`（上游查询失败或 `domain X blocked by NGG DNS policy`）。

### `POST /api/v1/network/dns/hosts`

- 请求体：`{"hosts":{"internal.test":"10.0.0.9"},"block":["ads.example"]}`（两个字段均可省）。
- 响应 `200`：`{"ok":true,"hosts":1,"block_added":1}`（会清空 DNS 缓存）。
- 错误：`400 INVALID_ARGUMENT`（JSON 非法）。

### `GET /api/v1/network/sessions`

- 请求体：无。响应 `200`：`SessionManager.List(200)` → `[]SessionRec`（最多 200 条）。
- `SessionRec` **无 `json` tag**，字段为 Go 导出名：`Session`（内含 `ID, Network, Src, Dst, Domain, Rule, CoreID, CreatedAt, FailClosed, TenantID, Tags`）、`TenantID`、`Tags`、`BytesIn`、`BytesOut`、`State`（`establishing|active|closed|failed`）、`LastError`。
- 错误：仅通用 `401` / `403`。

### `GET /api/v1/network/endpoints`

- 请求体：无。响应 `200`：`CoreBridge.Endpoints()` → `[]{core_id, kind, address, healthy, error?}`；`kind ∈ socks5|http-connect|mixed|transparent`，`healthy` 来自真实 TCP 拨号。
- 错误：仅通用 `401` / `403`。

### `POST /api/v1/network/selftest`

- 请求体（可空 body；`ContentLength>0` 才解析）：`{"core_id":"mihomo"}`（`core_id` 为空表示走 DIRECT 路径自检）。服务端 30s 超时。
- 响应 `200`（成功）：

```json
{ "ok":true, "target":"127.0.0.1:54321", "proxy":"127.0.0.1:18080",
  "core_id":"mihomo", "bytes":78, "latency_ms":1.037,
  "path":"NGG ingress -> mihomo -> echo", "echo":"NGG-SELFTEST:...", "sessions":1 }
```

- 响应 `200`（自检判定失败，HTTP 仍为 200）：`{"ok":false,"target":...,"proxy":...,"core_id":...,"error":...,"fail_closed":true}`（SOCKS5 拨号失败）或 `{"ok":false,"error":"echo payload mismatch","got":...,"want":...}`。
- 错误：`400 INVALID_ARGUMENT`（JSON 非法）；`500 INTERNAL_ERROR`（echo server 启动 / 写 / 读失败、ingress 未监听、临时规则安装失败）。
- **真实自检**：起本地 echo server → 经 NGG 入口 + 选定内核 → 校验字节逐字回显，绝不伪造成功。实测 `latency_ms≈1.04`、`bytes_out=156`。

### `POST /api/v1/network/packettest`

- 请求体：无。内部用 `syntheticDNSPacket()` 构造真实 IPv4/UDP/53 包喂给 `PacketProcessor`。
- 响应 `200`：

```json
{ "action":"dns", "parse_error":"<nil>",
  "packet": { "version":4, "protocol":"udp", "src":"10.89.0.2",
              "dst":"10.89.0.1", "src_port":12345, "dst_port":53, "is_dns":true } }
```

- 错误：`400 INVALID_ARGUMENT`（`Process()` 返回错误，例如目标端口被登记为保护端口时回环被拒）。

---

## 3. Stage 4 端点（13 个）

### `GET /api/v1/stages`

- 请求体：无。响应 `200`：`engine.Stages()`（下列数值为**新引擎初始态的示例值**，实际值由运行时注册表 / 规则 / 插件 / 租户 / 链路数量决定）：

```json
{
  "stage1": { "name":"统一管理中心 / 多内核引擎", "status":"complete", "kernels":3 },
  "stage2": { "name":"统一 TUN / 会话 / Packet Processor / Dispatcher / Core Bridge",
              "status":"implemented",
              "note":"Linux TUN requires CAP_NET_ADMIN; userspace ingress is fully active on all platforms",
              "active":true },
  "stage3": { "name":"统一规则引擎 / DNS / 出入口策略", "status":"implemented", "rules":4 },
  "stage4": { "name":"VPN 协议中心 / 后量子 / 插件运行时", "status":"implemented", "protocols":16, "plugins":0 },
  "stage5": { "name":"SD-WAN / 多租户 / 跨平台骨架", "status":"implemented", "tenants":1, "links":0 }
}
```

- 错误：仅通用 `401` / `403`。

### `GET /api/v1/protocols`

- 查询参数：`category`（可选；`vpn` / `encrypted-proxy` / `post-quantum`；空则返回全部）。
- 响应 `200`：`[]Spec`（按 ID 排序，内置 16 个）。`Spec` 字段：`id, name, category, transport, default_port, ports, kernels, features, pq_capable, requires_root, description, enabled`。
- 错误：仅通用 `401` / `403`。

### `GET /api/v1/protocols/{id}`

- 路径参数：`id`。响应 `200`：`Spec`。
- 错误：`404 CORE_NOT_FOUND`（`protocol <id> not found`）。

### `POST /api/v1/protocols/{id}/enable`

- 请求体：`{"enabled":true}`。
- 响应 `200`：`{"ok":true,"protocol":{...Spec...}}`。
- 错误：`400 INVALID_ARGUMENT`（JSON 非法）；`404 CORE_NOT_FOUND`（`protocol "<id>" not registered` 含 `not registered`）。

### `POST /api/v1/protocols/{id}/config`

- 请求体：`{"kernel":"sing-box","params":{"server":"198.51.100.1","port":51820,"private_key":"...","peer_public_key":"..."}}`。
- 响应 `200`：`{"ok":true,"protocol":"wireguard","kernel":"sing-box","format":"json","content":"{...}","validated":true}`（`format ∈ json|yaml|conf`，`content` 为**真实生成且经格式校验**的内核原生配置）。
- 错误：`400 INVALID_ARGUMENT`（JSON 非法 / `no config generator for protocol "<id>" on kernel "<kernel>"`）；`422 CONFIG_INVALID`（`ValidateConfig` 解析失败）。
- **能力边界**：只生成配置片段并做格式校验，**不重写内核协议栈、不执行握手**。

### `GET /api/v1/pq/status`

- 响应 `200`：

```json
{ "suite":"NGG-HYBRID-X25519-MLKEM768-HKDFSHA256-AES256GCM",
  "mlkem":"ML-KEM-768 (FIPS 203, crypto/mlkem)", "classical":"X25519 (crypto/ecdh)",
  "kdf":"HKDF-SHA256", "aead":"AES-256-GCM", "key_bytes":32,
  "implemented":true, "go_version":"go1.27.3",
  "note":"用于 NGG 节点间控制信道；内核数据面协议仍由各内核自行实现" }
```

- 错误：仅通用 `401` / `403`。

### `POST /api/v1/pq/selftest`

- 请求体：无。响应 `200`：`pq.SelfTest()` → `{ok, suite, mlkem, classical, kem, aead, encapsulation_key_bytes, ciphertext_bytes, key_bytes}`（`ciphertext_bytes=1088`，`key_bytes=32`）。真实 `net.Pipe()` 混合握手 + AES-256-GCM 往返。
- 错误：`500 INTERNAL_ERROR`（握手 / 加解密失败）。

### `GET /api/v1/plugins`

- 响应 `200`：`[]Plugin` → `{manifest, loaded, error?}`。`Manifest` 字段：`id, name, kind, source, hooks, permissions, enabled, description`。
- 错误：仅通用 `401` / `403`。

### `POST /api/v1/plugins`

- 请求体：`Manifest` 对象。

```json
{ "id":"route-js", "name":"Route by domain", "kind":"javascript",
  "source":"var result={}; if(ngg.input.domain.indexOf('example')>=0){result.core_id='sing-box';}",
  "hooks":["session","dns"], "permissions":["decision:write","log"],
  "enabled":true, "description":"demo" }
```

- 响应 `200`：`{"ok":true,"plugin":{...Manifest...},"loaded":true}`。
- 错误：`400 INVALID_ARGUMENT`（JSON 非法）；`422 CONFIG_INVALID`（编译失败：缺 `id`、缺 `source`、非法 JS、未知 `kind`、WASM 魔数缺失 / 未导出 `ngg_hook()`）。失败插件仍入库但 `loaded=false` 并带 `error`，通过 `GET /plugins` 可查。

### `POST /api/v1/plugins/reload`

- 请求体：无。扫描 `<storage.dir>/plugins.d` 下的 `*.yaml|*.yml|*.json` manifest。
- 响应 `200`：`{"loaded":2,"problems":["bad.yaml: ..."],"dir":"data/plugins.d"}`。
- 错误：仅通用 `401` / `403`（目录不存在时 `loaded=0`，不报错）。

### `POST /api/v1/plugins/{id}/enable`

- 请求体：`{"enabled":true}`。
- 响应 `200`：`{"ok":true,"plugin":{...Manifest...}}`。
- 错误：`400 INVALID_ARGUMENT`（JSON 非法）；`404 CORE_NOT_FOUND`（`plugin "<id>" not found`）；`404 NOT_FOUND`（`plugin "<id>" failed to load: ...`，即加载失败的插件不可启用）。

### `POST /api/v1/plugins/{id}/test`

- 请求体：`{"input":{"domain":"www.example.com","hook":"session","core_id":"mihomo"}}`（`input` 可省，缺省 `{"domain":"example.com","hook":"session","core_id":"mihomo"}`）。
- 响应 `200`：`{"ok":true,"result":{...}}`（`result` 为插件可写的 `core_id` / `direct` / `block` / `tags` 合并结果）。
- 错误：`400 INVALID_ARGUMENT`（JSON 非法）；`422 CONFIG_INVALID`（插件 hook 执行出错）。

### `DELETE /api/v1/plugins/{id}`

- 响应 `200`：`{"ok":true,"message":"plugin removed"}`（同时关闭 WASM runtime）。
- 错误：`404 CORE_NOT_FOUND`（`plugin "<id>" not found`）；`404 NOT_FOUND`（`unknown plugin action`）。

---

## 4. Stage 5 端点（10 个）

### `GET /api/v1/sdwan/status`

- 响应 `200`：`{"policy":"lowest-latency","active":"wan1","running":true,"switches":1,"interval_ms":3000,"links":[...]}`（`links` 为 `[]Link`）。
- 错误：仅通用 `401` / `403`。

### `GET /api/v1/sdwan/links`

- 响应 `200`：`[]Link`。`Link` 字段：`id, name, interface, gateway, probe_target, weight, cost, priority, enabled, healthy, rtt_ms, loss_pct, probes, failures`。
- 错误：仅通用 `401` / `403`。

### `POST /api/v1/sdwan/links`

- 请求体：`Link` 对象（`id` 与 `probe_target` 必填；`weight<=0` 自动置 1）。

```json
{ "id":"wan1", "name":"Primary", "interface":"eth0", "gateway":"192.0.2.1",
  "probe_target":"198.51.100.1:443", "weight":10, "cost":1, "priority":10, "enabled":true }
```

- 响应 `200`：`{"ok":true,"link":{...Link...}}`。
- 错误：`400 INVALID_ARGUMENT`（JSON 非法）；`422 CONFIG_INVALID`（`link needs id and probe_target`）。

### `DELETE /api/v1/sdwan/links/{id}`

- 响应 `200`：`{"ok":true,"message":"link removed"}`。
- 错误：`404 CORE_NOT_FOUND`（`link not found` 含 `not found`）。

### `POST /api/v1/sdwan/{action}`

`action` 由 `r.PathValue("action")` 取得，四种取值：

| `action` | 请求体 | 响应 `200` |
| --- | --- | --- |
| `start` | 无 | `{"ok":true,"message":"SD-WAN probing started"}` |
| `stop` | 无 | `{"ok":true,"message":"SD-WAN probing stopped"}` |
| `failover` | 无 | `{"ok":true,"active":{...Link...}}`（`ForceFailover` 立即置当前链路不健康并重选） |
| `policy` | `{"policy":"weighted"}` | `{"ok":true,"policy":"weighted"}` |

- `policy` 枚举：`lowest-latency`（默认）、`weighted`、`priority`、`failover`。
- 错误：`400 INVALID_ARGUMENT`（`policy` 的 JSON 非法）；`404 NOT_FOUND`（`unknown SD-WAN action: <action>`）；`503 INTERNAL_ERROR`（`failover` 时 `no healthy WAN link available (fail-closed)`）。

### `GET /api/v1/tenants`

- 响应 `200`：`[]Tenant`。字段：`id, name, enabled, max_sessions, quota_bytes, used_bytes, allowed_cores, allowed_protocols, default_core, rule_set_tag`。默认含 `default` 租户。
- 错误：仅通用 `401` / `403`。

### `POST /api/v1/tenants`

- 请求体：`Tenant` 对象（`id` 必填；`max_sessions<=0` 自动置 1000）。

```json
{ "id":"acme", "name":"Acme", "enabled":true, "max_sessions":1000,
  "quota_bytes":1073741824, "allowed_cores":["mihomo"],
  "allowed_protocols":["shadowsocks"], "default_core":"mihomo", "rule_set_tag":"acme" }
```

- 响应 `200`：`{"ok":true,"tenant":{...Tenant...}}`。
- 错误：`400 INVALID_ARGUMENT`（JSON 非法）；`422 CONFIG_INVALID`（`tenant id is required`）。

### `DELETE /api/v1/tenants/{id}`

- 响应 `200`：`{"ok":true,"message":"tenant removed"}`。
- 错误：`404 CORE_NOT_FOUND`（`tenant not found or protected`，含 `not found`；删除 `default` 同样走此路径）。

### `GET /api/v1/platforms`

- 响应 `200`：

```json
{ "host":"windows/amd64", "go":"go1.27.3",
  "targets":["linux/amd64","linux/arm64","linux/386","linux/arm","linux/mipsle",
             "windows/amd64","windows/arm64","darwin/amd64","darwin/arm64",
             "freebsd/amd64","android/arm64","ios/arm64"],
  "clients":["Android","iOS","iPadOS","HarmonyOS","Windows","macOS","Linux","Android TV","OpenWrt / 软路由"],
  "note":"纯 Go 核心 + gomobile 绑定；原生 UI 客户端为骨架，需各平台 SDK 打包" }
```

- 错误：仅通用 `401` / `403`。`note` 为**如实能力边界**：原生 UI 客户端仍是骨架。

### `POST /api/v1/platforms/build`

- 请求体（可空 body）：`{"targets":["linux/amd64","linux/arm64"]}`；缺省时编译 4 个目标 `linux/amd64, linux/arm64, windows/amd64, darwin/arm64`。
- 响应 `200`：

```json
{ "ok":true, "go_binary":"<实际使用的 go 可执行文件绝对路径>", "results":[
  {"target":"linux/amd64","ok":true,"output":"ok","path":"bin/platforms/nggd-linux-amd64"},
  {"target":"ios/arm64","ok":false,"output":"<真实 go build 错误输出，截断 400 字符>"} ] }
```

- 真实执行 `go build -trimpath -ldflags="-s -w"`，逐目标设 `GOOS/GOARCH/CGO_ENABLED=0`，单目标 4 分钟超时，产物落在 `bin/platforms/nggd-<goos>-<goarch>[.exe]`；格式错误的 target 返回 `ok:false, output:"invalid target"`，**不伪装成功**。
- 编译器解析顺序：`build.go_binary` → `NGG_GO_BINARY` → `PATH` 中的 `go` → `runtime.GOROOT()/bin/go` → 常见安装路径（`C:/Users/admin/.workbuddy/toolchains/go/bin/go.exe` 等）；全部找不到时返回 `500` 并提示配置方式。响应中的 `go_binary` 是**真实使用**的编译器路径。
- 错误：`400 INVALID_ARGUMENT`（JSON 非法）；`500 INTERNAL_ERROR`（创建输出目录失败）；`503`（工具链未找到，响应体为 `{"error":"...: Go toolchain not found: ..."}`）。

---

## 5. curl 示例

```bash
TOKEN='<从 configs/ngg.yaml 读取>'
BASE='http://127.0.0.1:9090/api/v1'
H="Authorization: Bearer $TOKEN"
JH='Content-Type: application/json'

# ---------- Stage 2~3：网络引擎 ----------
curl -H "$H" "$BASE/network/status"
curl -X POST -H "$H" "$BASE/network/start"
curl -X POST -H "$H" "$BASE/network/stop"

curl -H "$H" "$BASE/network/config"
curl -X PUT -H "$H" -H "$JH" \
  -d '{"fail_closed":true,"failover":true,"default_core":"mihomo"}' "$BASE/network/config"

curl -H "$H" "$BASE/network/rules"
curl -X POST -H "$H" -H "$JH" \
  -d '{"id":"r-example","type":"DOMAIN-SUFFIX","value":".example.com","action":"PROXY","core_id":"mihomo","priority":50,"enabled":true}' \
  "$BASE/network/rules"
curl -X PUT -H "$H" -H "$JH" \
  -d '{"rules":[{"id":"r1","type":"MATCH","action":"PROXY","core_id":"mihomo","priority":10,"enabled":true}],"default_action":"DIRECT"}' \
  "$BASE/network/rules"
curl -X DELETE -H "$H" "$BASE/network/rules/r-example"
curl -X POST -H "$H" -H "$JH" -d '{"path":"./rules.yaml"}' "$BASE/network/rules/load"

curl -H "$H" "$BASE/network/dns"
curl -X POST -H "$H" -H "$JH" -d '{"name":"www.example.com","type":"A"}' "$BASE/network/dns/resolve"
curl -X POST -H "$H" -H "$JH" \
  -d '{"hosts":{"internal.test":"10.0.0.9"},"block":["ads.example"]}' "$BASE/network/dns/hosts"

curl -H "$H" "$BASE/network/sessions"
curl -H "$H" "$BASE/network/endpoints"
curl -X POST -H "$H" -H "$JH" -d '{"core_id":"mihomo"}' "$BASE/network/selftest"
curl -X POST -H "$H" "$BASE/network/packettest"

# ---------- Stage 4：阶段 / 协议 / 后量子 / 插件 ----------
curl -H "$H" "$BASE/stages"
curl -H "$H" "$BASE/protocols?category=vpn"
curl -H "$H" "$BASE/protocols/wireguard"
curl -X POST -H "$H" -H "$JH" -d '{"enabled":true}' "$BASE/protocols/wireguard/enable"
curl -X POST -H "$H" -H "$JH" \
  -d '{"kernel":"sing-box","params":{"server":"198.51.100.1","port":51820,"private_key":"x","peer_public_key":"y"}}' \
  "$BASE/protocols/wireguard/config"

curl -H "$H" "$BASE/pq/status"
curl -X POST -H "$H" "$BASE/pq/selftest"

curl -H "$H" "$BASE/plugins"
curl -X POST -H "$H" -H "$JH" \
  -d '{"id":"route-js","kind":"javascript","hooks":["session"],"enabled":true,"source":"var result={core_id:\"sing-box\"};"}' \
  "$BASE/plugins"
curl -X POST -H "$H" "$BASE/plugins/reload"
curl -X POST -H "$H" -H "$JH" -d '{"enabled":true}' "$BASE/plugins/route-js/enable"
curl -X POST -H "$H" -H "$JH" -d '{"input":{"domain":"www.example.com"}}' "$BASE/plugins/route-js/test"
curl -X DELETE -H "$H" "$BASE/plugins/route-js"

# ---------- Stage 5：SD-WAN / 多租户 / 跨平台 ----------
curl -H "$H" "$BASE/sdwan/status"
curl -H "$H" "$BASE/sdwan/links"
curl -X POST -H "$H" -H "$JH" \
  -d '{"id":"wan1","name":"Primary","probe_target":"198.51.100.1:443","weight":10,"cost":1,"enabled":true}' \
  "$BASE/sdwan/links"
curl -X DELETE -H "$H" "$BASE/sdwan/links/wan1"
curl -X POST -H "$H" "$BASE/sdwan/start"
curl -X POST -H "$H" "$BASE/sdwan/failover"
curl -X POST -H "$H" -H "$JH" -d '{"policy":"lowest-latency"}' "$BASE/sdwan/policy"
curl -X POST -H "$H" "$BASE/sdwan/stop"

curl -H "$H" "$BASE/tenants"
curl -X POST -H "$H" -H "$JH" \
  -d '{"id":"acme","name":"Acme","enabled":true,"max_sessions":1000,"quota_bytes":1073741824,"allowed_cores":["mihomo"]}' \
  "$BASE/tenants"
curl -X DELETE -H "$H" "$BASE/tenants/acme"

curl -H "$H" "$BASE/platforms"
curl -X POST -H "$H" -H "$JH" \
  -d '{"targets":["linux/amd64","windows/amd64"]}' "$BASE/platforms/build"
```

---

## 6. 能力边界提醒

- `POST /network/selftest` 的 DIRECT 自检路径是**真实直连**（`openDirect`），走 `PROXY` 时经真实内核本地端点；`ok:false` 是合法业务结果（HTTP 仍为 200）。
- `POST /protocols/{id}/config` 只做**配置生成 + 格式校验**，不启动隧道、不重写内核协议栈。
- `GET /pq/status` 的 `note` 与 `mlkem-hybrid` 协议一样，明确后量子实现**仅用于 NGG 节点间控制信道**。
- `GET /platforms` 的 `note` 明确**原生 UI 客户端为骨架**，需各平台 SDK 打包。
- 上述端点不提供「统一 TUN 全局接管」开关：TUN 只在 Linux 且 `TUN.AutoRoute=true` 时由 `Engine.Start` 尝试打开，Windows/macOS 返回 `ErrTUNUnsupported` 并改用 userspace 入口。

---

## 附录 A · 配置引擎节点与规则编辑（追加）

### 规则单条编辑

| 方法 | 路径 | 请求体 | 响应 |
| --- | --- | --- | --- |
| `PUT` | `/api/v1/network/rules/{id}` | `Rule` 对象（`id` 以路径参数为准，保持不可变） | `{"ok":true,"rule":{...}}` |

- 后端调用 `RuleSet.Update`，先做 `Rule.Validate()`，再按优先级重新排序；不存在的 id 返回 `422`。
- 与现有 `POST /network/rules`（新增）、`DELETE /network/rules/{id}`（删除）、`PUT /network/rules`（全量替换）配合，规则表支持完整增删改。

### 入口 / 出口协议节点

节点是「配置哪个加密协议、由哪个内核执行、用什么参数」的定义，持久化在
`<storage.dir>/nodes-config.json`（0600）。NGG 生成内核原生配置片段，实际握手/隧道由内核执行。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/nodes?kind=inbound\|outbound` | 节点列表：`{"nodes":[...],"count":n,"path":"..."}` |
| `POST` | `/api/v1/nodes` | 新增节点（body 为单个 `Node`） |
| `GET` | `/api/v1/nodes/{id}` | 单个节点 |
| `PUT` | `/api/v1/nodes/{id}` | 修改节点（ID 不可变） |
| `DELETE` | `/api/v1/nodes/{id}` | 删除节点 |
| `GET` | `/api/v1/nodes/protocols?kind=inbound\|outbound` | 该方向可用的协议目录 |
| `GET` | `/api/v1/nodes/export` | 导出全部节点 JSON |
| `PUT` | `/api/v1/nodes/import` | `{"nodes":[...]}` 全量 JSON 替换（全或不做） |
| `POST` | `/api/v1/nodes/{id}/config` | `{"kernel":"mihomo"}` → 生成并格式校验内核原生配置片段 |

`Node` 字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` / `name` | string | 必填，id 唯一且编辑时不可变 |
| `kind` | `inbound` / `outbound` | 必填 |
| `protocol` | string | 必须存在于协议目录，且支持该 kind |
| `address` / `port` | string / int | 入口监听地址或出口服务器；port=0 时套用协议默认端口 |
| `enabled` / `core_id` | bool / string | 启用状态；指定执行内核 |
| `params` | object | 协议参数（密码、UUID、cipher、private_key、TLS、obfs 等），自由 JSON |
| `tags` / `description` | array / string | 标签与说明 |

协议目录覆盖 Shadowsocks / VMess / VLESS+REALITY / Trojan / Hysteria2 / TUIC / AnyTLS /
Mieru / Snell / NaiveProxy / SOCKS5 / HTTP / WireGuard / OpenVPN / IPsec-IKEv2 / L2TP /
PPTP / SSTP / OpenConnect / SSH / ML-KEM-768 混合后量子 / DIRECT / REJECT。
其中 VPN 类多为 `outbound` only，`mixed` 仅 `inbound`；不匹配的组合返回 `422`。

**校验规则（Fail-Closed）**：未知协议、方向不匹配、端口越界、重复 id、JSON 参数不可序列化都会被拒绝；
`import` 逐条校验，任一条失败则整个集合保持不变，写文件失败同样回滚内存。

**诚实边界**：节点定义用于生成并校验内核原生配置片段，供运维合并进内核完整配置；
当前 userspace 统一入口仍为单一监听端口，多协议 inbound 的实际监听由内核 Worker Mode 承担，
NGG 不重写任何内核协议栈。`params` 中的凭据以明文保存在本地 0600 文件，不上传仓库。

```bash
# 新增一个加密入口节点
curl -X POST -H "$H" -H 'Content-Type: application/json' \
  -d '{"id":"in-ss","name":"SS 入口","kind":"inbound","protocol":"shadowsocks","address":"0.0.0.0","port":8388,"enabled":true,"params":{"cipher":"aes-256-gcm","password":"secret"}}' \
  "$BASE/nodes"

# 生成 sing-box 原生配置片段
curl -X POST -H "$H" -H 'Content-Type: application/json' \
  -d '{"kernel":"sing-box"}' "$BASE/nodes/in-ss/config"

# 编辑一条规则
curl -X PUT -H "$H" -H 'Content-Type: application/json' \
  -d '{"type":"DOMAIN-SUFFIX","value":".example.com","action":"PROXY","core_id":"mihomo","priority":50,"enabled":true}' \
  "$BASE/network/rules/r-example"
```

### 整份内核原生配置端点（追加）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/nodes/templates` | 内置示例模板：`3x-ui-default` / `native-xray` / `remnawave-xray`，返回 `{id,name,kernel,content}` |
| `POST` | `/api/v1/nodes/parse` | body `{kernel,content}` → 解析整份 Xray/sing-box JSON 或 Mihomo YAML；返回 `nodes` / `routing` / `detected` / `warnings`，只读不落库 |
| `POST` | `/api/v1/nodes/from-native` | 同上解析后把节点合并进节点库（Fail-Closed） |
| `GET` | `/api/v1/cores/{id}/config` | 读取内核当前有效配置（完整 JSON/YAML） |
| `POST` | `/api/v1/cores/{id}/config/validate` | 调用内核自身校验器，不写文件 |
| `PUT` | `/api/v1/cores/{id}/config` | **备份 → 原生校验 → 通过才替换**；失败保留原配置，响应含 `backup` |

- `content` 支持 `@long-text:"..."` 与 `<long_text_quote>...</long_text_quote>` 包裹，后端自动剥离。
- 解析时 `vless` + `mlkem768x25519plus...` → `mlkem-hybrid`，`freedom` → `direct`，`blackhole` → `reject`，
  `socks` → `socks5`；`inboundTag` / `outboundTag` 支持数组；`domain` / `ip` 条件保留在 `routing[].matchers`。
- 无法映射到协议目录的条目会被跳过并写入 `warnings`，不伪装成功。
- 现场验收脚本：`node scripts/e2e-native.js`（需服务运行）。

```bash
curl -X POST -H "$H" -H 'Content-Type: application/json' \
  -d '{"kernel":"xray-core","content":"{\"inbounds\":[...]}"}' "$BASE/nodes/parse"
```
