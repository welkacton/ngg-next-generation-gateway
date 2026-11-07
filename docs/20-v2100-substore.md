# v2.10.0 · Sub-Store 互通

> 目标：NGG 与 [Sub-Store](https://github.com/sub-store-org/Sub-Store) 双向兼容 —— 既能把 NGG 的节点交给 Sub-Store 转换成 QX / Loon / Stash / Egern 等格式，也能把 Sub-Store 里已有的节点导回节点管理，且**不破坏原有 5 种订阅格式的任何行为**。

## 1. 为什么是「双向」

Sub-Store 是订阅管理层：它自己不产生节点，只负责把一份节点列表转换成各客户端支持的格式。NGG 与它的关系因此有两个方向：

| 方向 | 入口 | 产出 |
| --- | --- | --- |
| NGG → Sub-Store | 用户管理 → 订阅抽屉 → **Sub-Store** 链接（`/sub/u/<token>?target=substore`） | Sub-Store 可直接消费的 plain JSON（节点数组） |
| Sub-Store → NGG | 节点管理 → **从 Sub-Store 导入**（`/api/v1/substore/parse`、`/import`） | 节点库里的远程主机记录 |

## 2. 输出：`target=substore`

响应体就是 Sub-Store 自己 `target=json` 输出使用的 **plain JSON**：一个 proxy 对象数组，键名使用 Sub-Store / Clash.Meta 的归一化拼写。

```json
[
  {
    "type": "vless",
    "name": "US-01",
    "server": "us01.example.com",
    "port": 443,
    "uuid": "8f0da0b2-…",
    "tls": true,
    "servername": "www.bing.com",
    "network": "ws",
    "ws-opts": { "path": "/ray", "headers": { "Host": "www.bing.com" } },
    "udp": true
  }
]
```

- `Content-Type: application/json; charset=utf-8`（与 sing-box 共用同一分支）。
- Sub-Store 会把这份内容识别为 **Clash Proxy JSON**，所以「新建订阅 → 填 NGG 链接」即可，不需要任何转换脚本。
- 该格式**只描述节点**：NGG 的规则集与 DNS 不会随它传递，`Build` 会在 `warnings` 里明确说明，而不是静默丢弃。
- 五种旧格式（clash / surge / singbox / v2ray / socks）的渲染路径、链接形状与告警文案完全未改动，`Targets` 只是在末尾追加了 `substore`。

## 3. 输入：解析对方的导出内容

`internal/substore` 是一个纯解析/生成包（不依赖 API 层），`Parse` 按以下顺序嗅探格式，任一命中即停止：

| 顺序 | 格式 | 识别依据 |
| --- | --- | --- |
| 1 | Sub-Store / Clash JSON | 首个非空字符为 `{` 或 `[`，且可反序列化 |
| 2 | Clash / mihomo YAML | YAML 能解析为对象/数组（标量会被拒绝，交给第 3 步） |
| 3 | base64 订阅 | 整段是 base64，解码后是合法 UTF-8 文本 |
| 4 | 节点链接列表 | 含 `://` 的行 |

### 3.1 兼容的格式差异

| 差异 | 处理 |
| --- | --- |
| JSON5 行注释 `//`、块注释 `/* */`、尾逗号 | 解析前剥离，字符串内的注释符号不误伤 |
| 顶层是数组 / 是带 `proxies` 的对象 / 是单个 proxy 对象 / 是组合订阅的 `list` | 四种都接受；单个对象会附带「按 1 个节点解析」的告警 |
| UTF-8 BOM、CRLF | 去掉 BOM 后按 `\n` 统一切分 |
| `port` 是字符串 `"443"`、`tls` 是字符串 `"true"`、`udp` 是 `1` | 统一归一化（`str` / `num` / `boolean` 三个取值器） |
| 字段别名：`cipher`↔`method`、`sni`↔`servername`↔`peer`、`network`↔`net`、`uuid`↔`id`、`password`↔`pass`/`auth`、`ws-path`↔`ws-opts.path`、`ws-headers`↔`ws-opts.headers.Host` | 全部识别 |
| 类型别名：`ss`/`shadowsocks`、`socks`/`socks5`/`socks5h`、`http`/`https`/`h2`、`hy2`/`hysteria2`、`wg`/`wireguard` | 归一到 Sub-Store 规范名 |
| `vmess://base64(json)` 使用 `ps/add/id/net/tls` 这套 v2rayN 词汇 | 单独映射，不与 Clash 键名混用 |
| `ss://` 的 userinfo 可能是 base64、也可能直接是 `method:password` | 两种都试 |
| trojan 条目没有写 `tls: true` | Trojan 本身只能跑 TLS，按 TLS 处理 |
| 同一 `server:port:type` 重复出现（组合订阅常见） | 折叠成一条，并计一次 skipped |
| 节点链接里缺端口 | 只有协议有默认端口时才补（http 80 / https 443 / socks 1080），其余报错 |

### 3.2 异常处理

- **单条失败不影响整份文档**：坏条目进 `skipped` 并附一条告警（含序号与截断后的原文），其余照常解析。
- **不会硬塞协议**：`vmess / hysteria2 / tuic / wireguard / ssr / anytls` 等能解析但不属于 NGG 节点模型的类型，会以 `type` 保留原义、在导入阶段逐条说明「协议 X 暂不支持导入 NGG 节点」后跳过。
- **上限**：单文档 4 MiB、最多 2000 条、base64 嵌套最多 3 层（超过即报 `无法识别的订阅格式`，不递归爆栈）。
- **空内容 / 垃圾内容**：分别返回 `订阅内容为空` 与 `无法识别的订阅格式（支持 Sub-Store JSON / Clash YAML / base64 / 节点链接）`，HTTP 422。
- **预览不泄密**：`Proxy` 的 `MarshalJSON` 用 `has_secret` 替换 `uuid / password / username`，预览接口不会把凭据回传给浏览器。

## 4. API

### `POST /api/v1/substore/parse`

只解析，不落库。

```json
{ "text": "[{…}]", "url": "http://127.0.0.1:3000/api/sub/name?target=json", "role": "outbound" }
```

`text` 优先于 `url`（粘贴的内容不会触发任何网络请求）。`url` 只接受 `http/https`，15 秒超时，读取上限 4 MiB + 1 字节（让 Parse 报「超限」而不是把文档截断成半份）。私有地址是允许的 —— Sub-Store 通常就自建在同网段。

响应：

```json
{
  "dry_run": true, "format": "json", "source": "text", "bytes": 812,
  "parsed": 3, "skipped": 1,
  "hosts": [{ "id": "hk-01-8388", "name": "HK-01", "address": "1.2.3.4", "svc_port": 8388, "protocol": "ss", "role": "outbound", "note": "从 Sub-Store 导入 · HK-01" }],
  "failed": [], "warnings": ["JP-vmess：协议 vmess 暂不支持导入 NGG 节点，已跳过"]
}
```

### `POST /api/v1/substore/import`

同一个请求体，`dry_run: true` 只返回预览；否则逐条写入节点库：

- `id` 由名称生成（CJK 名称会退化为地址），与已有 id 冲突时追加 `-2` / `-3`，因此同一份订阅可以重复导入成第二批。
- `node_port` 统一写 3372（与「新增节点」表单默认值一致），响应里明说可在详情中改。
- 凭据按节点模型组装：Shadowsocks = `method:password`，SOCKS5/HTTP = `username:password`，VLESS = uuid。
- 单个节点写入失败进 `failed`（不回滚已成功的），全军覆没才返回 422。

## 5. 界面

- **用户管理 → 订阅抽屉 → 客户端**：新增 `Sub-Store` 行（复制链接 / 下载 `.json`），并说明 DNS 与规则需要在 Sub-Store 的脚本操作里配置。
- **节点管理 → 从 Sub-Store 导入**：订阅地址 / 粘贴内容切换、目标角色、`解析预览` → 格式 / 识别 / 跳过 / 可导入四枚标签 + 节点列表 + 告警（最多展示 12 条），确认后才落库。

## 6. 验证

```bash
go test ./internal/substore/ -count=1     # 格式嗅探、别名、JSON5、base64、URI、上限、去重、密钥不外泄
go test ./internal/subscription/ -count=1 # 六种 target 全部可渲染，substore 正文可被 Parse 回读
go test ./internal/api/ -run TestSubStore -count=1
```

回归关注点：`Targets` 追加不改动原有顺序，`Links` 只新增 `substore` 字段，旧客户端读到的 JSON 结构不变。
