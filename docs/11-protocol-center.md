# 协议配置（Protocol Config）

> 适用版本：Gen 2 / v2.2.1 起；**v2.2.2 起完整配置支持双向编辑（改动自动回写分区）**。菜单名与页面标题为 **「协议配置」**（旧称「协议中心」）。

这一页只做两件事，避免「不知道从哪下手」：

| 区块 | 能做什么 | 是否可编辑 |
| --- | --- | --- |
| **内核支持协议** | 用纯文字列出 Mihomo / sing-box / Xray-core 各自支持的协议名称 | ❌ 只读，定时从内核官方文档自动同步 |
| **配置文件** | 卡片列表（类似 Clash Verge / Shadowrocket 的 Profile），点开分段编辑完整内核配置；完整配置与 6 个分区双向同步 | ✅ 可新建、复制、编辑、校验、导出、删除、设为默认、启用/停用、多选批量启停 |

旧版的可视化表单、协议 JSON 编辑器、params 输入框、单条详情卡片、批量启停开关**已全部移除**。

---

## 1. 内核支持协议（只读 · 官网实时同步）

协议清单来自**三个内核的官方文档仓库**（也就是官网文档页面 "Edit this page on GitHub" 指向的仓库）：

| 内核 | 官网 | 官方数据源 |
| --- | --- | --- |
| Mihomo | <https://wiki.metacubex.one/config/proxies/> | `MetaCubeX/Meta-Docs` → `docs/config/proxies/` |
| sing-box | <https://sing-box.sagernet.org/configuration/outbound/> | `SagerNet/sing-box` → `docs/configuration/outbound/` |
| Xray-core | <https://xtls.github.io/config/outbounds/> | `XTLS/Xray-docs-next` → `docs/config/outbounds/` |

- 服务端**每 6 小时**自动刷新一次；服务启动时立即刷新一次；页面「刷新目录」按钮可强制立刻同步。
- 前端每 60 秒只读一次目录状态；页面显示来源（官网已同步 / 本地缓存 / 内置基线）、上次同步、下次同步与间隔。
- 每个内核只显示**协议名称文字**，不含任何开关或编辑控件。翻译文件（`.en.md` / `.zh.md`）、`index.md`、变体文件会被过滤，缩写字会自动展开成可读名称（`ss` → `Shadowsocks (SS)`）。
- 结果缓存在 `<storage.dir>/protocol-catalog.json`。**网络失败时保留上一次清单并标记为 stale**，逐项显示错误；缓存与网络都不可用时才回退到明确标注的「内置基线」，绝不假装同步成功。
- 私有环境或 GitHub API 限流时，可在服务端配置 `NGG_GITHUB_TOKEN` / `GITHUB_TOKEN` 提高限额。

### 相关 API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/protocols/catalog` | 纯读缓存目录 + 同步状态；**不访问 GitHub**（前端每 60 s 轮询，刷新由 6 h 定时器或手动按钮负责） |
| POST | `/protocols/catalog/refresh` | 立即同步三个内核官网，返回最新快照 |

`GET` 返回的关键字段：`kernels[].protocols[]`（`slug` / `name` / `doc_url`）、`kernels[].count`、
`kernels[].from_cache` / `error`、`source`、`stale`、`synced_at`、`next_sync_at`、`interval_minutes`、`message`。

---

## 2. 配置文件（卡片 · 可编辑）

参考截图那种一行一张卡片的 Profile 列表：`Default-Profile` 默认存在，点卡片打开右侧编辑抽屉。

每张卡片显示：名称、默认标记、目标内核、格式、已填分区数、更新时间；行内可「编辑 / 设为默认 / 复制 / 删除」。
顶部可搜索、刷新、`+ 新建`。

### 六个分区

一个配置文件按目标内核的**官方语法**拆成 6 个可编辑分区，每个分区都有中文说明与带注释的模板。
编辑器里的顺序固定为 **协议 → 入站 → 出站 → 路由规则 → DNS → 其他 → 重建模板**（旧版本保存的文件读取时也会自动规范化到这个顺序）：

| 序 | 分区 | 含义 | Mihomo (YAML) | sing-box (JSON) | Xray-core (JSON) |
| --- | --- | --- | --- | --- | --- |
| 1 | 协议 | 代理节点 / 协议条目 | `proxies:` | `outbounds` 中的代理项 | `outbounds` 中的代理项 |
| 2 | 入站 | 本地监听入口 | `listeners:` | `inbounds:` | `inbounds:` |
| 3 | 出站 | 选择器 / 直连 / 阻断 | `proxy-groups:` | `outbounds` 中的 selector/direct/block | `outbounds` 中的 freedom/blackhole |
| 4 | 路由规则 | 分流规则 | `rules:` | `route.rules` | `routing.rules` |
| 5 | DNS | DNS 设置 | `dns:` | `dns:` | `dns:` |
| 6 | 其他 | 日志与全局 | `mode` / `log-level` | `log` | `log` |

> 注意：sing-box / Xray-core 的「协议内容」与「出站」都属于 `outbounds` 数组。
> 保存时后端会**把两个分区的同名数组真实合并**，而不是生成重复 JSON key。

每个分区模板里都用 `#`（YAML）或 `//`（JSON）逐行写明字段名含义。注释只是阅读辅助：
**校验和落盘前会自动剥离整行注释**，且不会误伤字符串里的 `https://`。切换内核后请点「重建模板」，
避免把 A 内核语法写进 B 内核。

### 编辑流程

`选内核 → 编辑分区 → 校验语法 → 保存`。编辑抽屉里有这条 4 步提示、分区 tab、字段说明、官方文档链接，
以及「编辑完整配置」（合并 6 个分区后的 YAML / JSON，**可直接编辑**、可复制或下载）、「校验语法」、「保存」。

- 校验逐段做真实解析，再校验合并后的完整配置；语法错误不会写入磁盘。
- 默认配置文件不能删除，需先把另一份设为默认。
- **启用 / 停用**：每张卡片可单独启停，也可勾选多张后「启用选中 / 停用选中 / 全部启用 / 全部停用」。不同内核的配置文件可同时启用，各自只对应自己的内核，不存在互斥。状态一次落盘，写入失败整体回滚。
- 页面左右保留 26px 留白，避免内容顶到侧栏。
- 所有数据保存在 `<storage.dir>/profiles.json`（0600，原子写 + 失败回滚）；新建时自动生成 `Default-Profile`。

### 完整配置 ↔ 分区：双向编辑

「编辑完整配置」打开的是**可编辑**的完整配置窗口，不再是只读预览。两条方向共用同一份语法规则：

| 方向 | 入口 | 后端 |
| --- | --- | --- |
| 分区 → 完整配置 | 打开窗口 / 保存后重新打开 | `Render()` 合并 6 个分区 |
| 完整配置 → 分区 | 窗口内编辑（自动）或「立即同步到分区」 | `Split()` 拆回 6 个分区 |

- 窗口顶部默认开启 **「自动同步到分区」**：停止输入约 650 ms 后自动调用 `POST /profiles/split`，把整份配置按内核顶层键拆回 6 个分区，**立即更新抽屉里的分段编辑器**。状态栏显示 `已同步 / 同步中 / 待同步 / 未同步`，下方 6 个胶囊显示各分区行数，被本次改动更新的分区会高亮。
- **语法错误时不覆盖分区**：错误原因直接显示在状态栏，原分区保持不变；关闭窗口时可选择「放弃修改并关闭」或继续编辑。自动同步请求不会弹全局错误提示，避免打字时刷屏。
- 可以关掉自动同步，改用「立即同步到分区」手动触发；「保存并关闭」会先强制同步再保存。该拆分接口是**纯计算、不落盘**，只有点「保存」才写入 `profiles.json`。
- 拆分规则：Mihomo 的 YAML 按顶层键归位（含注释随键保留）；sing-box / Xray-core 的 `outbounds` 按「节点 / 选择器·直连·阻断」重新分到「协议内容」与「出站」两段，`Render` 会合并回**同一个数组**（顺序可能变化，tag 引用不受影响）；未识别的顶层键统一放入「其他」并给出提示，绝不丢弃。JSON 分段里的说明注释在重建后不再保留，配置内容不受影响。
- 后端 `Split()` 是 `Render()` 的逆运算；单元测试对三个内核验证 `Render → Split → Render` 配置语义完全一致，并覆盖改动传播、未知键、非法语法、`outbounds` 分组。

### 相关 API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/profiles` | 卡片列表 + `active` 默认 ID |
| POST | `/profiles` | 新建（不传 `sections` 时自动生成模板） |
| GET/PUT/DELETE | `/profiles/{id}` | 读取 / 更新 / 删除 |
| POST | `/profiles/template` | 取某内核的带注释起始模板（不落盘） |
| POST | `/profiles/validate` | 逐段 + 合并后语法校验，返回完整配置文本 |
| POST | `/profiles/split` | **反向拆分**：`{profile, content}` → 6 个分区、`changed`、`warnings`；纯计算不落盘，语法错误返回 422 |
| POST | `/profiles/{id}/render` | 由已保存内容生成完整配置 |
| POST | `/profiles/{id}/duplicate` | 复制 |
| POST | `/profiles/{id}/activate` | 设为默认 |
| POST | `/profiles/toggle` | 批量启停：`{ids:[], all:bool, enabled:bool}`，返回变更数与全量列表 |
| GET | `/profiles/options` | 内核选项、分区定义、官方文档链接 |

---

## 3. 与旧版的关系

- 旧的 `/protocols`（协议注册表 CRUD、启停、schema、preview、annotate）**仍保留在后端**，供其它模块与 API 兼容使用，但**新页面不再调用、不再展示**。
- 旧的 `GET /protocols` 默认排除明文与普通代理分组；`include_all=true` 可取全部。
- 页面文件仍是 `web/src/views/Protocols.vue`，路由仍是 `/protocols`，只有菜单与标题改为「协议配置」。

## 4. 边界

- 配置文件是**配置内容编辑器**，不会自动把配置下发给正在运行的内核；下发仍走既有的内核配置管理流程。
- 目录同步依赖 GitHub API；完全离线环境只能显示缓存或内置基线，状态栏会如实标注。
- 统一 TUN / 全局流量接管仍未实现，见 [`TUN-GLOBAL-TAKEOVER.md`](TUN-GLOBAL-TAKEOVER.md)。
