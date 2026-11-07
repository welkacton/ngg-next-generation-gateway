# NGG 模块驱动架构（按 10 条规划重建）

> 本文件是**重新规划后的架构基线**。此前基于主机直连 / 部署脚本的那套流程不再作为演进方向。
> 约束：**现有 `internal/*` 各域与既有页面一律不改写、不删除**；本架构通过新增一个
> `internal/module` 聚合域 + 引用 ID 组合现有能力实现，避免破坏存量数据。

---

## 1. 目标与边界

用户要的是「**一个模块 = 一份可下发的完整网络能力**」：

```
节点 + 三内核 + 协议组 + 规则 + DNS + 策略组 + NGG 主内核
        ↓ 打包成 Module
用户选择 Module → 生成订阅链接
        ↓ 按客户端转换（链接管理）
手机订阅 → 解析出模块全部内容 → NGG 主内核统一调度
```

| 做 | 不做 |
| --- | --- |
| 新增 `internal/module` 聚合域、模块 API、模块管理页 | 不改 `hosts` / `core` / `profiles` / `rulesets` / `users` 的既有实现 |
| 引用现有域的 ID，组合成模块 | 不重写订阅渲染器，复用 `internal/subscription` 的 6 种 target |
| 新增凭据自动生成（API 密码 / 后量子 / mTLS） | 不再沿用旧的节点部署脚本体系 |
| 主内核以「调控器」身份读取模块并编排 | 不在本期实现真实 TUN 数据面改造 |

---

## 2. 分层

```
┌──────────────────────────────────────────────────────────────┐
│ 控制台：节点 / 内核 / 协议组 / 规则 / DNS / 策略组 / 模块 / 用户 / 链接 │
└───────────────────────────┬──────────────────────────────────┘
                            │ /api/v1/modules/*
┌───────────────────────────▼──────────────────────────────────┐
│ Module 聚合域（新增）                                          │
│   Module{ id, master, nodes[], kernels[], protocol_groups[],    │
│           rules[], rulesets[], dns, policy_groups[] }          │
│   Store(0600 JSON) · Validate · Credentials · Render           │
└───────────────────────────┬──────────────────────────────────┘
                            │ 引用 ID（不复制数据）
┌───────────────────────────▼──────────────────────────────────┐
│ 既有域：hosts · core · profiles · rulesets · users · subscription│
│ 加密：pq.KeyPair · nodepki CA · crypto/rand                     │
└───────────────────────────┬──────────────────────────────────┘
                            │ MasterKernel.Apply(module)
┌───────────────────────────▼──────────────────────────────────┐
│ NGG 主内核（总控）：按模块启停内核、下发协议/规则/DNS/策略、    │
│ 选择出口节点、监控并自动重调度                                 │
└──────────────────────────────────────────────────────────────┘
```

---

## 3. 领域模型

`Module` 是**唯一聚合根**；其余 10 项都是它的内嵌值对象或 ID 引用，不单独持久化，
保证「用户选一个模块」就能拿到全部内容，不需要客户端拼装 6 个接口。

```go
type Module struct {
    ID, Name, Note   string
    Enabled          bool
    Master           MasterConfig      // 规划 10：NGG 主内核总控
    Nodes            []NodeSpec        // 规划 1
    Kernels          []KernelSlot      // 规划 2
    ProtocolGroups   []ProtocolGroup   // 规划 3
    Rules            []RuleSpec        // 规划 4
    RuleSets         []RuleSetRef      // 规划 4
    DNS              DNSConfig         // 规划 5
    PolicyGroups     []PolicyGroup     // 规划 6
    Links            []LinkSpec        // 规划 9
    CreatedAt, UpdatedAt time.Time
}
```

**关键设计**：模块被用户引用时做一次 `Snapshot()` 快照，写入订阅令牌。
之后修改模块不会让已发出的订阅悄悄变化；需要显式「重新下发」才更新。

---

## 4. 十条规划逐条落地

| # | 规划 | 实体 | 自动生成 / 规则 | API | 页面 |
| --- | --- | --- | --- | --- | --- |
| 1 | 节点管理：新建**空白**节点，只填 IP + 端口，其余自动生成；部署后连接探测主机信息 | `NodeSpec{Address, Port, APIPassword, PQPublic, PQFingerprint, MTLS{CA, Cert, Key, Fingerprint}, Probe}` | 新建时一次性生成：32 字节 API 密码、后量子密钥对（`pq.GenerateKeyPair`）、由面板 CA 签发的 mTLS 证书（`nodepki`）。完整凭据与安装脚本保存在面板本地 0600 文件里（供探测与脚本重下载），列表视图不回显，脚本下载端点需登录 | `POST /modules/{id}/nodes` · `POST /modules/{id}/nodes/{nid}/deploy` · `POST /modules/{id}/nodes/{nid}/probe` | 节点管理（新建表单只有 IP/端口，其余展示为只读的自动生成卡片） |
| 2 | 内核管理：三内核可同时 / 单独 / 多个启动，NGG 主内核自动调控 | `KernelSlot{Kernel, Enabled, AutoStart, ProtocolGroupID}` | 组合方式用位集表达：`start_mode ∈ {single, multiple, all}`；主内核按模块决定哪些槽位 active | `POST /modules/{id}/kernels/start\|stop` （body 支持 `ids[]` 或 `all`） | 内核管理（三个槽位 + 批量/单独启停） |
| 3 | 协议管理：协议组可一对三内核，也可三协议组各对应一内核同时启用 | `ProtocolGroup{ID, Name, KernelIDs[], Protocols[], Enabled}` | 校验内核 ID 必须出现在 `Kernels` 中；一个组允许绑定 1~3 个内核 | `CRUD /modules/{id}/protocol-groups` | 协议管理（组编辑器 + 内核多选） |
| 4 | 规则管理：新建规则、启停、配置规则集 | `RuleSpec` + `RuleSetRef` | 规则可挂到规则集；启停用 `enabled`，不删除 | `CRUD /modules/{id}/rules` · `/rulesets` | 规则管理 |
| 5 | DNS 管理：防泄密、加密解析、国内外自动分流 | `DNSConfig{LeakProtection, Encrypt, Domestic[], Foreign[], Strategy}` | `strategy=geo` 时按域名/IP 归属选择国内或国外上游；`encrypt ∈ {doh,dot,doq,none}` | `PUT /modules/{id}/dns` | DNS 管理 |
| 6 | 策略管理：策略组可绑定规则集 | `PolicyGroup{ID, Name, RuleSetIDs[], Mode}` | 校验引用的规则集存在 | `CRUD /modules/{id}/policy-groups` | 策略管理 |
| 7 | 模块管理：打包上述 6 项 + NGG 主内核 | `Module` | `Validate()` 做引用完整性；`Snapshot()` 冻结 | `CRUD /api/v1/modules` · `POST /modules/{id}/validate` | 模块管理（六段组合 + 主内核卡） |
| 8 | 用户管理：选模块保存 → 生成订阅链接 | `User.ModuleID` + `SubscriptionToken` | 保存时生成不可猜测 token，绑定模块快照 | `POST /modules/{id}/users` 或用户页选模块 → `GET /api/v1/sub/{token}` | 用户管理 |
| 9 | 链接管理：按客户端转成专属可识别链接 | `LinkSpec{Client, Target, URL, QR}` | 复用 `subscription.Targets`（clash/surge/singbox/v2ray/socks/substore），一个 token 派生多种链接 | `GET /api/v1/modules/links?token=...&client=...` | 链接管理（客户端下拉 + 二维码） |
| 10 | 手机订阅 → 识别模块信息 → 主内核总控 | `MasterConfig{Mode, AutoRegulate, PollInterval}` | 主内核读取快照：启停内核槽、下发协议/规则/DNS/策略、选出出口节点、异常自动重调度 | `POST /modules/{id}/master/apply` · `GET /modules/{id}/master/status` | 控制中心（总控状态） |

---

## 5. 关键链路

### 5.1 新建空白节点（规划 1）

```
用户输入：address=50.118.185.194, port=6691
      │
      ├─ crypto/rand → APIPassword (base64url, 32B)
      ├─ pq.GenerateKeyPair() → PQPublic / 指纹（私钥仅本次响应返回）
      ├─ nodepki.CA.IssueNode(nodeID, address) → mTLS cert/key（私钥仅本次返回）
      └─ 落库只存：地址、端口、API 密码哈希、后量子公钥/指纹、证书指纹
```

部署产物是一份自包含安装脚本，节点启动后以 mTLS + API 密码 + 后量子握手三重校验接入；
面板用「连接探测」拉取 CPU / 内存 / 磁盘 / 网卡等真实主机信息，探测失败如实写错误不填 0。

### 5.2 用户 → 订阅 → 客户端（规划 8/9/10）

```
用户选 Module ──► 保存 ──► Module.Snapshot() ──► token
                                              │
      ┌───────────────────────────────────────┴────────────────┐
      ▼                                                        ▼
GET /api/v1/sub/{token}?target=clash              /api/v1/modules/links?client=shadowrocket
      │                                                        │
      └──────────────► 手机订阅 ◄──────────────────────────────┘
                           │
                           ▼
              客户端拿到模块内全部节点/协议/规则/DNS/策略
                           │
                           ▼
              NGG 主内核（总控）按 AutoRegulate 调度
```

---

## 6. 凭据与安全

| 项 | 生成 | 存储 | 展示 |
| --- | --- | --- | --- |
| API 密码 | `crypto/rand` 32B | `NodeSpec` 存 SHA-256；`mnodes.json`(0600) 存明文供探测 | 创建时显示一次，脚本下载端点可取 |
| 后量子密钥 | `pq.GenerateKeyPair()` | 公钥 + 指纹；`mnodes.json`(0600) | 公钥/指纹创建时显示 |
| mTLS 证书 | `nodepki` 面板 CA 签发，CN = 节点 ID | 证书 + 指纹；私钥在 `mnodes.json`(0600) | 私钥创建时显示一次 |
| 订阅 token | `crypto/rand` 24B base64url | token 哈希 | 用户页可见（可轮换） |

模块快照、订阅内容都是敏感数据：文件 0600、接口需登录、列表视图不回显私钥。

---

## 7. 分期路线与验收

| 期 | 范围 | 验收 |
| --- | --- | --- |
| **P1（本轮已完成）** | **规划 1**：`internal/mnode` 空白节点（只填 IP+端口）、三类凭据自动生成、自包含安装脚本、mTLS 连接探测；`internal/module` 聚合类型/校验/快照作为共用基础；`/api/v1/mnodes*` 6 个端点；新菜单「模块架构 → ① 节点管理」 | `go build/vet/test ./...`、`vue-tsc`、`vite build`、安装脚本 `bash -n` 全绿 |
| **P2（已完成）** | **规划 2**：内核管理，三内核 single / multiple / all 启停，直接调用现有 `engine.BatchStart/BatchStop`，逐条返回真实结果 |
| **P3（已完成）** | **规划 3~6**：协议组（一对三内核）、规则 + 规则集、DNS 防泄密/加密/国内外分流、策略组绑定规则集 |
| **P4（已完成）** | **规划 7~9**：模块打包（六项 + 主内核 + 快照）、用户选模块生成订阅 token、按客户端派生专属深链 |
| **P5（已完成）** | **规划 10**：主内核读取模块、计算调控计划并真实启停内核、下发协议/规则/DNS/策略、记录调控状态 |：空白节点表单（仅 IP+端口）、自动凭据卡片、部署脚本、连接探测；三内核槽位与单/多/全启停 | 新建节点只填两项；批量启停接口返回逐个状态 |
| P3 | 规划 3/4/5/6：协议组、规则/规则集、DNS 分流、策略组的模块内 CRUD | 一个组绑 3 内核；策略组绑规则集通过 Validate |
| P4 | 规划 8/9：用户选模块、订阅令牌、链接管理（多客户端 + 二维码） | 保存用户即产出 6 种 target 链接 |
| P5 | 规划 10：主内核调控器，读取快照并编排，异常自动重调度 | 模块 apply 后内核状态与模块声明一致 |

每期独立构建、测试、发版；**每期都不改动现有域实现**。

---

## 8. 风险与边界

- P1 的 Module 是**配置聚合**，不是数据面重构；主内核真正接管转发需要 P5。
- 后量子密钥用于节点接入握手，不等同于替换 TLS 证书体系，两者叠加使用。
- 订阅快照保证「已发出的链接不被静默修改」，代价是改模块后需显式重新下发。
- 现有 `/api/v1/hosts*`、`/cores*`、`/pipeline*` 等接口保持原样，模块只引用其 ID。


---

## 9. 本轮（规划 1）实际交付

| 文件 | 内容 |
| --- | --- |
| `internal/module/{types,credentials,validate,store,snapshot}.go` | 聚合类型、三类凭据自动生成、引用完整性校验、内核单/多/全启停解析、快照冻结 |
| `internal/mnode/{store,deploy,probe}.go` | 空白节点 Store（0600）、自包含安装脚本（Python3 mTLS 服务 + systemd）、连接探测客户端 |
| `internal/api/mnode.go` + `server.go` 接线 | `GET/POST /api/v1/mnodes`、`GET/DELETE /api/v1/mnodes/{id}`、`GET .../script`、`POST .../probe` |
| `web/src/views/ModuleNodes.vue` | 新建表单**只有 IP + 端口**、一次性凭据弹窗、安装脚本复制/下载、连接探测结果网格 |
| `web/src/api/ngg.ts` / `router/index.ts` / `App.vue` | 类型契约、路由 ` /module-nodes`、菜单「模块架构 → ① 节点管理」 |
| `internal/mnode/mnode_test.go`、`internal/module/module_test.go` | 凭据生成、注入防护、探测解析、脚本 `bash -n`、Store 持久化、快照冻结等回归 |

**验证**：`go build ./...`、`go vet ./...`、`go test -p 2 ./... -count=1`、`npx vue-tsc --noEmit`、`npm run build` 全部通过。

**已知边界**：节点端目前用内嵌 Python3 HTTPS 服务做探测（不依赖 Docker / SSH / 既有 ngg-node）；
后量子在探测阶段以「公钥指纹」校验，完整 ML-KEM 握手在规划 10 的主内核总控里接入。
**规划 2~10 已按顺序全部完成，见下方 §10。**


---

## 10. 规划 2~10：顺序实现记录

| 规划 | 后端 | API | 前端 |
| --- | --- | --- | --- |
| ② 内核管理 | `mcatalog.Kernel` + `ResolveStart` + `SetKernelStates` | `POST /mcat/kernels/{start\|stop}` → `engine.BatchStart/BatchStop` | 三内核卡 + 单/多/全模式 + 启停结果与状态 |
| ③ 协议管理 | `mcatalog.ProtocolGroup`，绑定内核 ID 校验 | `PUT/DELETE /mcat/items/protocol-groups` | 组名 + 内核多选 + 协议条目编辑 |
| ④ 规则管理 | `Rule` / `RuleSet`，启停开关 | `PUT/DELETE /mcat/items/rules\|rule-sets` | 规则与规则集并列两栏 |
| ⑤ DNS 管理 | `DNSPolicy` + `ResolveDNS`（国内域名表 + CN IP CIDR + 防泄密拒绝明文） | `PUT /mcat/items/dns`、`POST /mcat/dns/resolve` | 策略编辑 + 域名/IP 分流试算 |
| ⑥ 策略管理 | `PolicyGroup` 绑定规则集（悬挂引用拒绝） | `PUT/DELETE /mcat/items/policy-groups` | 模式 + 规则集多选 |
| ⑦ 模块管理 | `BuildModule()` 从 2~6 + 规划 1 节点 + 主内核打包，`Validate()` + `Snapshot()` | `GET/POST /mcat/modules`、`POST .../validate`、`DELETE` | 九宫格选择 + 模块列表 + 校验 |
| ⑧ 用户管理 | `User{ModuleID, Token}`，token 自动生成 | `PUT /mcat/items/users` | 用户名 + 选模块，保存即得 token |
| ⑨ 链接管理 | `BuildLinks()`：`clash://`、`sing-box://`、`surge://`、`shadowrocket://` 深链 | `POST/GET /mcat/users/{id}/links` | 用户 + 客户端多选 → 专属链接 + 复制 |
| ⑩ 总控 | `Regulate()` 计算启停/下发动作，`MasterState` 记录 | `GET/POST /mcat/master` | 选模块 → 调控 → 展示动作与状态 |

**统一入口**：菜单「模块架构」下 ①~⑩ 共 10 项；②~⑩ 共用 `ModuleWorkbench.vue`，按步骤条顺序切换。
**数据**：`module-catalog.json`（2~9）、`modules.json`（7 的聚合根）、`mnodes.json`（1 的节点），均为 0600 独立文件。
**真实能力**：内核启停与总控都调用现有 `engine.BatchStart/BatchStop`；引擎未初始化或内核未安装时逐条返回真实原因，不伪造成功。

**验证**：`go build/vet/test -p 2 ./...`、`npx vue-tsc --noEmit`、`npm run build` 全绿；
`TestMCatalogFlow2To10` 走真实 HTTP handler 从「建规则集 → 协议组 → DNS 试算 → 打包模块 → 用户 → 链接 → 总控」整条链路通过。

**已知边界**：客户端订阅内容渲染仍复用 `internal/subscription`，本期只生成可识别的深链与 token，**未改动现有订阅渲染器**；
手机侧拿到模块全量信息后的解析由客户端负责，主内核总控在面板侧完成。
