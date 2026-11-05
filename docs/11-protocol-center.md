# 协议中心：按内核分类、可视化配置编辑

> 适用版本：Gen 2 / v2.1.2
> 界面位置：一级菜单「配置引擎 → 协议中心」；后端 `internal/protocols`、`internal/api/protocols.go`。
> 端点总表见 [`02-api.md`](02-api.md) 第 51~54b 行。

---

## 1. 目录只包含三个内核支持的协议

界面顶部没有大段说明文字，只有五个切换按钮（各带真实计数），一次只看一类：

| 视图 | 筛选规则 |
| --- | --- |
| Mihomo 支持协议（默认） | `kernels` 含 `mihomo` |
| sing-box 支持协议 | `kernels` 含 `sing-box` |
| Xray-core 支持协议 | `kernels` 含 `xray-core` |
| 自定义协议 | `custom === true` |
| 全部 | 上述三内核支持的协议合集 + 自定义协议 |

**普通未加密协议（`plaintext`）与普通代理协议（`plain-proxy`）已从目录移除**：
`GET /protocols` 默认排除这两类，只有显式 `?include_all=true` 才会返回（保留 API 兼容，界面不再展示）。
同理，仅由 `external-helper` / `ngg-native` 提供、不属于三个内核的条目也不出现在目录里。
多核共用的协议会同时出现在多个内核分类，这是真实的支持关系，不是重复数据。

表格列固定宽度 + 省略号、无左右固定列，完整 Spec JSON 由行内「详情」展开查看。
三个大栏目（现在合并为两个：协议目录 / 配置与协议编辑）标题加粗放大，端点作为灰色副标题。

---

## 2. 三个内核的配置语法各不相同（按官方文档）

`internal/protocols/schema.go` 是每个内核字段定义、中文含义与官方文档链接的单一来源；
`GET /protocols/schema` 把它交给前端渲染可视化表单，`outboundConfig()` 负责把**同一组可视化字段**
映射成各内核自己的官方语法。没有任何字段是 NGG 自创的。

| 内核 | 语法 | 同一概念的字段差异 | 官方文档 |
| --- | --- | --- | --- |
| Mihomo / Clash.Meta | `proxies:` YAML 数组，每项 `{name,type,server,port,...}` | 端口 `port`，加密 `cipher`，TLS 是顶层 `tls`，传输层 `network` | <https://wiki.metacubex.one/config/proxies/> |
| sing-box | JSON `outbound` 对象，TLS / 传输层是嵌套的 `tls` / `transport` | 端口 `server_port`（下划线），加密 `method`，SNI `tls.server_name` | <https://sing-box.sagernet.org/configuration/outbound/> |
| Xray-core | JSON `outbounds[]`，协议参数在 `settings.vnext[].users[]`，传输层独立在 `streamSettings` | 地址 `address`，端口 `settings.vnext[].port`，UUID `users[].id`，SNI `tlsSettings.serverName`（大驼峰） | <https://xtls.github.io/config/outbounds/> |

`TestThreeKernelGrammarsAreDistinctAndValid` 用同一组参数在三个内核各生成一份配置，
逐一断言语法特征（`server_port` / `vnext` / `proxies:` 等）互不串味且都能被对应解析器通过。

---

## 3. 合并后的编辑区：可视化优先，代码在后

原来的「配置生成与自定义编辑」和「自定义协议 · JSON 编辑」两张卡片已合并成一张
**「配置与协议编辑」**，内部上下两块：

### ① 可视化编辑（点击式）

- 模式切换：`修改现有协议` / `新建自定义协议`；点目录里的「配置」会**自动滚动定位**到这里并短暂高亮。
- 选择协议、选择内核（切换内核会**换一整套字段**，因为语法不同）。
- 顶部一行写明当前内核的语法与官方文档链接。
- 字段由 `GET /protocols/schema` 渲染：文本 / 数字 / 开关 / 下拉四种控件；
  每个字段都显示**原生字段名**（mono 蓝底）、中文名称、必填标记，以及一行
  「这个参数是干什么的」的中文注释。
- 按钮：`生成配置` / `新建协议（含配置）` 或 `保存协议与配置` / `清除已存配置` / `重置字段`。
- 新建模式走 `POST /protocols/preview`：协议还没注册也能真实预览配置，不伪造「已保存」状态。

### ② 代码编辑（带注释）

- 两个切换页：`配置源码 (yaml/json)` / `协议定义 JSON`。
- 默认**显示字段注释**：配置源码由 `protocols.Annotate` 在每个字段上方插入
  `//`（JSON）或 `#`（YAML/CONF）注释，文件头写明内核、语法、官方文档；
  协议定义 JSON 顶部有一段逐字段说明（`id` / `kernels` / `params` / `config_content` / `enabled` …）。
- 「显示字段注释」开关可切换纯源码；`POST /protocols/annotate` 负责加/去注释。
- `格式化 / 校验 / 保存 / 复制 / 下载` 齐全。**注释只是阅读辅助**：
  `ValidateConfig` 与 `SaveConfig` 都会用 `StripComments` 先去掉整行 `//` / `#` 注释，
  持久化与下发给内核的永远是纯净的 JSON/YAML/CONF。
  `StripComments` 只去掉「行首 trim 后就是注释符」的整行，因此 `https://` 这类字符串里的 `//` 不会被破坏（有测试覆盖）。
- 底部「字段释义」表按当前内核逐条列出 `字段名 / 中文名 / 含义与作用`，并附官方文档链接；
  切换内核时整张表会换成该内核的字段集。

---

## 4. 自定义协议

可视化表单的「新建自定义协议」模式可填 `id / name / kernels / category / group / default_port / 描述`，
加上当前字段值作为 `params`，再把生成的纯配置写入 `config_content`，一次 `POST /protocols` 完成创建。
代码编辑页切到「协议定义 JSON」也能直接粘贴 / 编辑完整 Spec：

```json
{
  "id": "my-corp-proxy",
  "name": "Corp Proxy",
  "category": "encrypted-proxy",
  "group": "encrypted",
  "config_kind": "outbound",
  "default_port": 9999,
  "kernels": ["mihomo"],
  "enabled": true,
  "params": { "server": "127.0.0.1", "port": 9999 },
  "config_content": "proxies:\n  - name: corp\n    type: http\n    server: 127.0.0.1\n    port: 9999\n",
  "config_format": "yaml"
}
```

- `group`：`encrypted` / `plaintext` / `plain-proxy`；`config_kind`：`outbound` / `policy` / `listener` / `helper`。
- 必填 `id` 与非空 `kernels`；新建时缺省 `enabled` 由 API 按原始 JSON 是否有该键判定为 `true`。
- **内置协议不可删除、结构字段只读**：只保存 `enabled` / `params` / `name` / `description` / `config_content` / `config_format`，
  这样升级二进制时代码里的目录修正仍能生效；需要改结构请新建协议。
- 所有用户状态持久化到 `<storage.dir>/protocols-config.json`（`0600`），写入失败会回滚内存。

---

## 5. 单条 / 多选 / 本类全部启停

- 目录首列多选 checkbox，工具条 `启用选中 / 停用选中 / 本类全部启用 / 本类全部停用`，显示已选数量；
- 每行开关走 `POST /protocols/{id}/enable`；批量走 `POST /protocols/toggle`，只翻转 `enabled`、一次落盘、失败回滚；
- **停用真实生效**：停用后 `/config` 返回 400 `is disabled; enable it first`，重新启用立即恢复。

---

## 6. 全站西文字体

Web UI 的全部西文字形统一使用 **SF Mono Nerd Font Ligaturized**（[上游仓库](https://github.com/shaunsingh/SFMono-Nerd-Font-Ligaturized)）。

- 上游 `.otf` 用 fontTools + brotli 转 `.woff2`（16.4 MB → 5.0 MB）放在 `web/public/fonts/`，4 个字重按需加载。
- `:root` 与 `theme.ts` 的 `fontFamily` / `fontFamilyMono` 都把它放首位，中文回退系统字体。
- 正文启用 `calt` / `liga` 连字；**代码编辑器关闭连字**，保证 `!=`、`=>` 按原样显示。
- 许可见 `web/public/fonts/NOTICE.txt`（SF Mono 字形受 Apple 许可约束，公开/商用分发前请复核）。

---

## 7. 相关端点

| 端点 | 用途 |
| --- | --- |
| `GET /protocols` | 目录；默认排除 plaintext / plain-proxy，支持 `kernel`、`group`、`search`、`enabled`、`include_all` |
| `GET /protocols/schema` | 三个内核的字段定义 + 中文含义 + 官方文档链接 |
| `POST /protocols/preview` | 未注册协议的真实配置预览（可视化新建时用） |
| `POST /protocols/{id}/config` | 生成配置，附 `annotated_content` 与 `doc_url` |
| `POST /protocols/annotate` | 给代码加/去字段注释，返回纯净文本 |
| `POST /protocols/validate` | 格式感知校验（自动剥离整行注释） |
| `PUT / DELETE /protocols/{id}/config` | 保存 / 清除该协议的自定义配置 |
| `POST / PUT / DELETE /protocols` | 新建 / 更新 / 删除自定义协议 |
| `POST /protocols/toggle` | 单条 / 多选 / 全部启停 |
