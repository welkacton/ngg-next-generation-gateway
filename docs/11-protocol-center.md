# 协议中心：全内核协议目录、配置编辑与批量启停

> 适用版本：Gen 2 / v2.1.1（本页随协议中心重构新增）
> 界面位置：一级菜单「配置引擎 → 协议中心」；后端 `internal/protocols`、`internal/api/protocols.go`。
> 端点总表见 [`02-api.md`](02-api.md) 第 51~54b 行。

## 1. 目录包含什么

协议中心现在覆盖**三大类**，每条都标注真实的内核支持、加密方式、配置形态与端口：

| 大类 `group` | 含义 | `category` | 示例 |
| --- | --- | --- | --- |
| `encrypted` **加密协议** | 由内核或 helper 提供密码学保护的协议 | `vpn` / `encrypted-proxy` / `post-quantum` | WireGuard、OpenVPN、IPsec/IKEv2、L2TP/IPsec、SSTP、OpenConnect、SSH 隧道、Shadowsocks(AEAD)、VMess、VLESS、VLESS+REALITY、Trojan、Hysteria2、TUIC、AnyTLS、Mieru、Snell、ShadowTLS、NaïveProxy、ML-KEM-768 混合后量子 |
| `plaintext` **普通未加密协议** | 协议本身不加密，仅限可信内网 | `plaintext` | DIRECT、REJECT、HTTP 明文代理、SOCKS5 / SOCKS4、DNS(UDP/53)、TCP/UDP 端口转发 |
| `plain-proxy` **普通代理协议** | 标准代理编排，安全性由链路上的上游决定 | `plain-proxy` | HTTP CONNECT 隧道、上游 HTTP / SOCKS5 代理、代理链 Relay、PAC、系统代理入口、ws:// 明文 WebSocket、gRPC h2c |

**诚实边界**：`kernels` 字段表示「该内核有原生 outbound / 监听器，或存在外部 helper 配置生成路径」。
NGG 不重新实现任何内核的协议栈；`external-helper` 类协议只生成 helper 参数文件并由 NGG 管理生命周期。
`config_kind` 说明生成物形态：

| `config_kind` | 生成物 | 原因 |
| --- | --- | --- |
| `outbound` | 内核原生 outbound（mihomo 的 `proxies:` YAML / sing-box、xray-core 的 JSON） | 大多数代理协议 |
| `policy` | NGG 策略片段（YAML） | DIRECT / REJECT 等是内核的**规则动作**，不是 outbound |
| `listener` | 入站监听片段（JSON） | DNS、端口转发、PAC、系统代理入口是 inbound |
| `helper` | `key=value` 文本（conf） | OpenVPN / IPsec 等由外部程序执行 |

每条内置协议都在 `TestEveryBuiltinGeneratesValidConfigOnEveryKernel` 里对
`mihomo / sing-box / xray-core / ngg-native / external-helper` 五个目标逐一生成并用
JSON / YAML / CONF 解析器校验，避免目录里出现「只有名字、生成就报错」的假条目。

## 2. 界面布局：五大类视图

目录顶部是六个切换按钮（一次只显示一类，避免一屏几十行看不过来），每个都带真实计数：

| 视图 | 筛选规则 |
| --- | --- |
| **Mihomo 支持协议**（默认） | `kernels` 含 `mihomo` |
| **sing-box 支持协议** | `kernels` 含 `sing-box` |
| **Xray-core 支持协议** | `kernels` 含 `xray-core` |
| **VPN 代理协议** | `category` ∈ `vpn` / `encrypted-proxy` / `post-quantum` |
| **自定义协议** | `custom === true`，即 JSON 编辑器里新建的条目 |
| 全部 | 全部内置与自定义协议 |

前四类维度不同，多核共用的协议会**同时出现在多个内核分类**，这是刻意保留的真实支持关系，不是重复数据；
切到「全部」可一览。视图内还可以按「加密 / 未加密 / 普通代理」小类、启用状态、关键词再筛，并按小类排序。
批量工具条的「本类全部启用 / 停用」只作用于当前视图可见的协议，不会误改其他分类。

表格列精简为 `选择 / 展开 / 协议 / 小类·配置形态 / 内核支持 / 加密方式 / 启用 / 操作`，
每列固定宽度并启用省略号 + tooltip，去掉了会造成文字互相覆盖的左右固定列；
完整 Spec JSON 改为行内「详情」展开查看，**不再单独占用一个「单条详情」卡片**。
三个大栏目（协议目录 / 配置生成与自定义编辑 / 自定义协议 JSON 编辑）标题加粗放大，端点作为灰色副标题。

## 3. 配置生成与自定义编辑

界面「配置生成与自定义编辑」卡片：

1. 点目录里的「配置」会自动填入 ID 与首选内核，并**自动滚动定位**到这张卡片（短暂高亮）；
2. **不再单独提供 `params` 输入框**：server / port / 密钥等参数已内置在协议定义的 `params` 里，
   生成结果本身就能直接编辑，单独再填一份参数是冗余的。需要自定义就改生成结果：
   未消费的键会原样带入，不会静默丢弃；
3. 「生成配置」返回按目标内核生成的真实片段；若该协议已保存过自定义配置，则返回保存的版本；
4. 生成结果在下方大文本框里**可以直接改**，配 `格式化内容 / 校验 / 保存为该协议配置 / 清除已保存 / 复制 / 下载 / 还原本次生成`；
5. 「保存」前一定调用 `POST /protocols/validate` 做格式感知的真实语法校验，语法错误**不会写入**；
6. 「重新生成模板」带 `regenerate: true`，忽略覆盖并回到内置模板；「清除已保存」彻底删除覆盖。

保存的配置写入 `Spec.config_content` / `config_format`，与启用状态一起持久化到
`<storage.dir>/protocols-config.json`（权限 `0600`）。重启后仍在；升级二进制不会丢。
写入失败时内存状态会回滚，不会出现「界面显示已启用、磁盘却没写进去」。

## 4. 自定义协议与 JSON 编辑

界面「自定义协议 · JSON 编辑」卡片可粘贴 / 编辑完整 Spec JSON：

```json
{
  "id": "my-corp-proxy",
  "name": "Corp Proxy",
  "category": "plain-proxy",
  "group": "plain-proxy",
  "security": "自定义 / 由上游决定",
  "transport": "TCP",
  "config_kind": "outbound",
  "default_port": 9999,
  "kernels": ["mihomo"],
  "features": ["custom"],
  "description": "在 JSON 编辑器中按需修改字段后保存",
  "enabled": true,
  "params": { "server": "127.0.0.1", "port": 9999 },
  "config_content": "proxies:\n  - name: corp\n    type: http\n    server: 127.0.0.1\n    port: 9999\n",
  "config_format": "yaml"
}
```

- `group`：`encrypted` / `plaintext` / `plain-proxy`；`category`：`vpn` / `encrypted-proxy` / `post-quantum` / `plaintext` / `plain-proxy`。
- `config_kind`：`outbound` / `policy` / `listener` / `helper`。
- `config_content` 可直接写一份自定义模板（JSON / YAML / CONF），生成时优先返回它。
- 必填 `id` 与非空 `kernels` 数组；`id` 冲突会被拒绝。
- 新建时若 JSON 未写 `enabled`，服务端默认为 **true**（可立即使用）；显式写 `false` 则默认停用。
- **内置协议不可删除，结构字段只读**：JSON 编辑器对内置协议只保存 `enabled` / `params` / `name` / `description` / `config_content` / `config_format`。
  这样升级二进制时代码里的目录修正仍能生效；需要完全自定义结构请新建一条自定义协议。

## 5. 单条 / 多选 / 本类全部启停

- 目录首列是多选 checkbox，工具条提供 `启用选中 / 停用选中 / 本类全部启用 / 本类全部停用` 并显示已选数量；
- 每行「启用」开关是单条操作，走 `POST /protocols/{id}/enable`；
- 批量与全部走 `POST /protocols/toggle`（`{"ids":[...],"enabled":true}` 或 `{"all":true,"enabled":false}`），
  **只翻转 `enabled` 并一次落盘**，不做全量替换、不触网、不重新生成目录；持久化失败整体回滚；
- **启用状态是真实生效的**：停用后的协议调用 `/config` 会返回 400 `is disabled; enable it first`，
  重新启用后立即恢复。对应测试 `TestDisabledProtocolCannotGenerate`。

## 6. 全站西文字体

Web UI 的全部西文字形（含 Naive UI 组件、表格、代码/配置编辑器）统一使用
**SF Mono Nerd Font Ligaturized**（[上游仓库](https://github.com/shaunsingh/SFMono-Nerd-Font-Ligaturized)）。

- 字体文件：上游 `.otf` 用 fontTools + brotli 无损转封装为 `.woff2`，放在 `web/public/fonts/`，
  体积从 16.4 MB 降到 5.0 MB；4 个字重（400 / 500 / 600 / 700）按需加载并 `font-display: swap` 预加载。
- `main.css` 里 `@font-face` 定义 `SF Mono Nerd Font`；`:root` 与 `theme.ts` 的
  `fontFamily` / `fontFamilyMono` 都把它放在首位，后面跟 `'PingFang SC'` / `'Microsoft YaHei'`
  作为中文回退（该字体没有 CJK 字形，中文自然落到系统字体）。
- 正文启用 `calt` / `liga` 连字；**JSON / YAML / 配置编辑器里关闭连字**，保证 `!=`、`=>` 等字符按原样显示，
  编辑配置时不会被合成字形误导。
- 许可说明见 `web/public/fonts/NOTICE.txt`：SF Mono 字形受 Apple 字体许可约束，
  Nerd Fonts 补丁为 MIT。公开发布或商用分发前请复核上游许可。
