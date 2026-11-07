# 系统更新栏与版本代际（Gen 2）

> 官方文档：`internal/version/version.go` 是代际与 Release 的单一事实来源；
> `internal/update` 负责真实的 GitHub Release 检查、校验下载与更新交接。
> 界面顶栏的「Gen 2 · v2.13.0」徽章与「系统更新」按钮读取的都是这两个模块的真实数据。

---

## 1. 界面里的更新栏

顶栏右侧：

| 元素 | 行为 |
| --- | --- |
| `Gen 2 · v2.13.0` 徽章 | 来自 `GET /api/v1/update/status`，显示代际、版本号与代际名称 |
| 「系统更新」胶囊按钮 | 打开更新面板；发现新版本时显示蓝点与「有新版本」；私有仓库缺 Token 时显示「需要 GitHub Token」 |
| 更新面板 | 当前版本 / 仓库 / 平台、**GitHub Token 输入**、**「立即更新（一键完成）」**、分步进度、单步排查按钮、自动检查/自动应用开关、Release 说明、校验结果、日志路径 |

**一键更新（推荐）**：点「立即更新」→ 二次确认 → 自动依次执行
`检查 Release → 下载并强制 SHA256 校验 → 备份/替换/重启服务 → 轮询 /system 直到版本号变化 → 自动刷新页面`。
每一步都在面板里显示状态（等待 / 进行中 / 完成 / 失败）；失败会停在对应步骤并给出可操作原因，
不会跳步、不会伪造「已是最新」。

**手动 / 排查**：下面仍保留「检查更新」「下载更新」「应用更新」三个单步按钮，用于定位问题。
「应用更新」成功后同样会等待新版本并自动刷新。

**自动检查**：开关默认开启，界面打开时与每 6 小时各静默调用一次
`POST /api/v1/update/check`。开关保存在 `localStorage` 的 `ngg_auto_update_check`。
检查失败（例如私有仓库缺 Token）不弹骚扰提示，只在顶栏胶囊上给出「需要 GitHub Token」引导。

**自动应用更新**：开关**默认关闭**，开启时会弹出风险确认。开启后，一旦自动检查发现新版本，
界面会真实执行 `download`（含 SHA256 校验）→ `apply`，结果和日志路径显示在面板里；
下载校验或脚本未就绪时明确提示失败，不伪装成功。状态保存在 `ngg_auto_update_apply`。

> 「立即更新」仍需一次二次确认（会短暂中断服务）；自动应用默认关闭，生产环境建议手动确认。

> **首次升级引导**：v2.1.0 及更早的二进制里更新模块只认服务端环境变量，
> **没有**「界面填 Token」和「立即更新」按钮。已装 v2.1.0 的机器请任选其一完成一次引导：
> 1. 直接双击 `NGG-Setup-v2.7.5.exe` 覆盖安装（推荐，配置、API Token、数据都会保留；安装程序会先停服务再替换 `nggd.exe`）；
> 2. 或临时给 NGG 服务配置 `NGG_GITHUB_TOKEN` 并重启服务，用旧版的三步按钮更新一次。
>
> **已经装了 v2.1.1 及以上版本的机器可以直接在 Web 界面点「立即更新」升到 v2.7.5** ——
> 这是首次真正可用的端到端一键更新：填 Token → 检查 → SHA256 校验下载 → 替换重启 → 自动刷新。

---

## 2. 后端 API

所有端点与其它 `/api/v1/**` 一样：只监听 `127.0.0.1`、必须带 Bearer Token。

| 端点 | 说明 |
| --- | --- |
| `GET /api/v1/update/status` | 代际、Release、仓库、OS/Arch、安装目录、更新暂存目录、更新脚本是否就绪、`token_configured`（服务端是否已配 PAT，**不返回 Token 值**） |
| `POST /api/v1/update/check` | 真实请求 GitHub Releases API（`/releases/latest`，无 latest 时回退列表取最新非 draft），返回 tag、发布时间、说明、资产列表、是否有新版本、当前平台匹配的资产 |
| `POST /api/v1/update/download` | body `{"tag":"v2.7.5","asset":"NGG-Setup-v2.7.5.exe"}`（省略则使用最近一次检查结果）；下载资产与 `SHA256SUMS`，**校验通过才落盘**到 `<data>/updates/<tag>/` |
| `POST /api/v1/update/apply` | 校验暂存文件与 `SHA256SUMS` 存在后，启动平台更新脚本；无脚本 / 未下载 / 摘要缺失都会明确报错，不会伪装成功 |

`check` 返回示例（真实字段，非模拟）：

```json
{
  "current": "2.1.2",
  "latest": "v2.7.5",
  "update_available": true,
  "source": "github-api",
  "checked_at": "2026-11-05T03:00:00Z",
  "release": { "tag_name": "v2.7.5", "published_at": "...", "body": "...", "assets": [] },
  "asset": { "name": "NGG-Setup-v2.7.5.exe", "size": 15176447, "url": "..." },
  "status": { "generation": 2, "generation_label": "Gen 2", "release": "2.1.2",
              "platform": "windows/amd64", "script_ready": true, "token_configured": false }
}
```

**私有仓库（重要）**：GitHub 对私有仓库的匿名请求返回 404。PAT 有两种提供方式：

1. **界面内填写（推荐给 Windows / 本机 Web 控制台）**：在更新面板输入有 `repo` / `Contents: Read` 权限的
   PAT，保存在**本机浏览器** `localStorage` 的 `ngg_github_token`；前端只把它作为
   `X-GitHub-Token` 请求头附加到 `/update/*` 请求。后端用 `update.WithToken` 把 Token 放进**单次请求的
   context**，用完即弃：**不写磁盘、不写日志、不写进响应**，也不会出现在 `/update/status` 里。
2. **服务端环境变量**：`NGG_GITHUB_TOKEN` / `GITHUB_TOKEN`，适合无人值守的 VPS。界面会显示「服务端已配置」。

请求级 Token 优先于环境变量；并发请求之间互不串扰。检查失败时面板显示明确错误，不显示「已是最新」。
通过域名 HTTPS 远程管理 VPS 时 PAT 会经过网络，请使用最小权限、可随时吊销的 Token。

**状态码与文案**：401/403 提示 Token 无效或权限不足；404 提示这是私有库、请在面板填 Token。

**版本比较**：`CompareVersions` 解析 `vX.Y.Z` 逐级比较；解析失败才回退字典序，不伪造布尔值。
当前平台匹配资产的优先级：Windows → `NGG-Setup-<tag>.exe` > 便携 ZIP > tarball；
Linux → `ngg-<tag>-linux-<arch>.tar.gz`。

---

## 3. 应用更新脚本

`POST /apply` 不会在 Go 进程里替换正在运行的二进制，而是交接给随安装包分发的脚本：

| 平台 | 脚本 | 行为 |
| --- | --- | --- |
| Windows | `<安装目录>\scripts\ngg-update.ps1` | 后端以独立进程启动（不绑定 HTTP 请求 context，脚本能在本服务停止后继续）。脚本：等 2 秒 → 校验 SHA256 → 备份 `nggd.exe` 与 `web\dist` → 停服务 → 静默运行新的 Inno Setup 安装包（或解压 ZIP 覆盖） → 读配置里的 Token 轮询 `/api/v1/health` → 失败自动回滚并重启 |
| Linux / systemd | `<prefix>/scripts/ngg-update.sh` | systemd 的 cgroup 可能在停服务时杀掉子进程，因此 `/apply` **不假装已在后台应用**，而是返回 `started=false` 与一条精确的 root 命令；脚本执行同样的备份 → 替换 → 重启 → 健康检查 → 失败回滚 |

脚本日志：`<data>/updates/apply-<tag>.log`；直接手工运行 Windows 脚本时默认写 `<安装目录>\logs\ngg-update.log`。

> **如实边界**：Windows 的全自动应用已在代码层实现并随安装包分发，但需要管理员权限与真实
> Release 才能端到端验证；Linux 明确返回手工命令而不谎报「后台已更新」。两者都要求 Release 附带
> `SHA256SUMS`，否则拒绝下载/应用。

---

## 4. 打包要求

- Release 必须附带 `SHA256SUMS`，且每个资产名与校验文件中的名称一致；
- **从 v2.7.4 起只发布 Windows `.exe`**：`NGG-Setup-<tag>.exe`（安装包，唯一推荐格式）、
  `NGG-Core-<tag>-windows-amd64.exe`（裸守护进程）、`NGG-Agent-<tag>-windows-amd64.exe`；
- 不再产出 `.zip` / `.tar.gz`，也不再构建 Linux / macOS / FreeBSD（VPS 请从源码构建）；
- 安装包内必须含 `scripts/`，否则 `/update/status` 会显示 `script_ready=false`；
- `release.yml` 在 `windows-latest` 上打 `v*` tag 时自动生成上述资产与合并后的 `SHA256SUMS`，
  并在发布前校验「出现任何非 `.exe` 资产即中止」。

---

## 5. 相关文档

| 文档 | 内容 |
| --- | --- |
| [`GENERATIONS.md`](GENERATIONS.md) | 代际沿革、判定标准与可见位置 |
| [`INSTALL-WINDOWS.md`](INSTALL-WINDOWS.md) | Windows 安装包与目录结构 |
| [`DEPLOY-VPS.md`](DEPLOY-VPS.md) | VPS 部署与 `deploy-vps.sh upgrade` |
| [`02-api.md`](02-api.md) | 基础 API 契约 |
