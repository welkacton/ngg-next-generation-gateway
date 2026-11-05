# GitHub 规则集挂载

> 适用版本：Gen 2 / v2.2.1 起。入口在 **规则** 页顶部的「GitHub 规则集」卡片。

可以把 GitHub 上的分流规则仓库（例如 [`blackmatrix7/ios_rule_script`](https://github.com/blackmatrix7/ios_rule_script)）
**自动识别里面所有分流规则分类**，然后批量侧挂载成规则集，或直接把每条规则导入实时规则表。

---

## 1. 它是怎么发现的

以 `blackmatrix7/ios_rule_script` 为例，仓库结构为：

```
rule/Clash/Google/Google.list          ← 分类 Google 的规则
rule/Clash/YouTube/YouTube.list
rule/Clash/OpenAI/OpenAI.list
... 668+ 个分类
```

NGG 用**一次** GitHub git-trees API 调用
(`/repos/<owner>/<repo>/git/trees/<branch>?recursive=1`) 拿全仓库文件树，然后：

1. 只保留 `rule/<格式>/<分类>/<文件>` 两段结构的条目；
2. 只接受 `.list` / `.yaml` / `.yml` / `.txt` / `.conf`；
3. 过滤 `README`、翻译/变体文件（`*_No_Resolve` / `*_Classical`）；
4. 每个分类优先选与分类同名的 `.list`，其次 `.yaml`；
5. 生成 `raw.githubusercontent.com` 下载地址，缓存到 `<storage.dir>/ruleset-github/`。

**实测**：`blackmatrix7/ios_rule_script@master` 共 14,865 个文件，Clash 目录下识别出 **689 个 `.list` / 668 个分类**。
其它格式目录（Surge / QuantumultX / Loon / Shadowrocket / AdGuard）可切换。

内置预设仓库：

| 仓库 | 默认分支 | 说明 |
| --- | --- | --- |
| `blackmatrix7/ios_rule_script` | `master` | 668+ 分类，六套格式目录 |
| `Loyalsoldier/clash-rules` | `release` | 自动生成的大陆/海外域名与 IP 规则集 |

也可以直接填任意 `owner/name` + 分支。GitHub API 限流时可在服务端设置
`NGG_GITHUB_TOKEN` / `GITHUB_TOKEN`；限流 / 仓库不存在 / 目录里没有可识别文件都会返回明确错误，不会伪造清单。

---

## 2. 两种挂载方式

在页面上可搜索分类、点「全选搜索结果」，或直接点 **「全选全部（668）」** 一次选中目录里的每一个分类；
「显示全部」可展开完整列表。全选后点「加载所选」，前端会**自动按每批 50 个分批调用**后端并显示批次进度，
因此 668 个分类也能一次性全部加载完成，同时后端仍保留单次 50 的安全上限与并发上限 4（避免打爆 GitHub）。

然后选择：

### `mount` 侧挂载为规则集

每个分类变成一条 `source=url` 的规则集，ID 形如 `gh:blackmatrix7-ios_rule_script:google`。
后端会**真实下载并解析**分类文件，规则集栏里能看到实际识别条数，之后可随时「重新拉取」更新。
这是"把规则集挂在规则上"的方式 —— 规则表本身不变，需要时再点「应用」。

### `import` 直接导入规则表（自动添加全部分流规则）

下载 → 解析 → 逐条写入实时规则表，ID 形如 `gh:google:1`，`Comment` 记录来源仓库与分类。
可统一指定动作（`PROXY` / `DIRECT` / `REJECT`）与执行内核 `core_id`。
**重复导入是幂等的**（按 ID 去重），单条非法规则会被跳过而不是伪装成成功。

### `both` 两者都做

先挂载规则集，再把规则导入规则表。

`mode=import` 且 `action=PROXY` 时必须提供 `core_id`（或规则表已设置默认内核），否则返回
`CONFIG_INVALID`，因为 NGG 不允许出现没有执行内核的 PROXY 规则。

---

## 3. 如实反馈

响应逐分类返回 `recognised`（真实解析出的规则条数）、`rules_added`、`mounted`、`ruleset_id`、
`error`。例如某分类文件只有注释、没有任何规则时，会返回「解析失败: 未从内容中识别出任何规则」，
**不会**显示成 0 条成功。汇总字段：`categories / recognised / rules_added / mounted / failed / message`。

失败常见原因：网络不可达、GitHub 限流、分类文件为空、规则语法不被 NGG 解析器支持。

---

## 4. API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/rulesets/github/presets` | 内置预设仓库 |
| GET | `/rulesets/github/catalog?repo=&branch=&format=&refresh=` | 读缓存目录；`refresh=true` 强制同步 |
| POST | `/rulesets/github/catalog/refresh` | body `{repo,branch,format}` 强制同步 |
| POST | `/rulesets/github/mount` | body `{repo,branch,format,categories[],mode,action,core_id,enabled}` |

`categories[]` 只接受**已缓存目录里存在的分类名**，防止任意 URL 注入。

`POST /rulesets/github/mount` 示例：

```json
{
  "repo": "blackmatrix7/ios_rule_script",
  "branch": "master",
  "format": "Clash",
  "categories": ["Google", "YouTube", "OpenAI"],
  "mode": "both",
  "action": "DIRECT",
  "core_id": "mihomo",
  "enabled": true
}
```

---

## 5. 测试覆盖

`internal/rulesets/github_test.go` 与 `internal/api/rulesets_github_test.go` 用本地 HTTP server
模拟 git-trees 与 raw 文件，真实断言：分类发现与去重、`.list` 优先、磁盘缓存标注、
限流错误、端到端挂载、规则集出现在注册表、导入后规则出现在实时规则表、重复导入幂等、
空分类失败上报、未知分类与超量批次被拒绝。
