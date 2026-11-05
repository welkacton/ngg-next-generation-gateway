# 系统更新栏与版本代际（Gen 2）

> 官方文档：`internal/version/version.go` 是代际与 Release 的单一事实来源；
> `internal/update` 负责真实的 GitHub Release 检查、校验下载与更新交接。
> 界面顶栏的「Gen 2 · v2.1.0」徽章与「系统更新」按钮读取的都是这两个模块的真实数据。

---

## 1. 界面里的更新栏

顶栏右侧：

| 元素 | 行为 |
| --- | --- |
| `Gen 2 · v2.1.0` 徽章 | 来自 `GET /api/v1/update/status`，显示代际、版本号与代际名称 |
| 「系统更新」胶囊按钮 | 打开更新面板；发现新版本时显示蓝点与「有新版本」 |
| 更新面板 | 当前版本 / 仓库 / 平台、手动「检查更新」、自动检查开关、「下载更新」「应用更新」、Release 说明、校验结果、应用日志路径 |

**自动检查**：「自动检查」开关默认开启，界面打开时与每 6 小时各静默调用一次
`POST /api/v1/update/check`。开关保存在 `localStorage` 的 `ngg_auto_update_check`。

**自动应用更新**：「自动应用更新」开关**默认关闭**，开启时会弹出风险确认。开启后，一旦自动检查发现
新版本，界面会真实执行 `download`（含 SHA256 校验）→ `apply`，并把结果、日志路径显示在面板里；
下载校验或脚本未就绪时明确提示失败，不伪装成功。状态保存在 `ngg_auto_update_apply`。

**手动更新**：`检查更新 → 下载更新（强制 SHA256 校验）→ 应用更新（二次确认）`。

> 默认「只检查不安装」是刻意的安全默认值：自动替换会短暂中断服务，生产环境建议手动确认。

---

## 2. 后端 API

所有端点与其它 `/api/v1/**` 一样：只监听 `127.0.0.1`、必须带 Bearer Token。

| 端点 | 说明 |
| --- | --- |
| `GET /api/v1/update/status` | 代际、Release、仓库、OS/Arch、安装目录、更新暂存目录、更新脚本是否就绪 |
| `POST /api/v1/update/check` | 真实请求 GitHub Releases API（`/releases/latest`，无 latest 时回退列表取最新非 draft），返回 tag、发布时间、说明、资产列表、是否有新版本、当前平台匹配的资产 |
| `POST /api/v1/update/download` | body `{"tag":"v2.0.1","asset":"NGG-Setup-v2.0.1.exe"}`（省略则使用最近一次检查结果）；下载资产与 `SHA256SUMS`，**校验通过才落盘**到 `<data>/updates/<tag>/` |
| `POST /api/v1/update/apply` | 校验暂存文件与 `SHA256SUMS` 存在后，启动平台更新脚本；无脚本 / 未下载 / 摘要缺失都会明确报错，不会伪装成功 |

`check` 返回示例（真实字段，非模拟）：

```json
{
  "current": "2.1.0",
  "latest": "v2.0.1",
  "update_available": true,
  "source": "github-api",
  "checked_at": "2026-11-05T03:00:00Z",
  "release": { "tag_name": "v2.0.1", "published_at": "...", "body": "...", "assets": [] },
  "asset": { "name": "NGG-Setup-v2.0.1.exe", "size": 9866685, "url": "..." },
  "status": { "generation": 2, "generation_label": "Gen 2", "release": "2.1.0",
              "platform": "windows/amd64", "script_ready": true }
}
```

**私有仓库**：在服务端设置环境变量 `NGG_GITHUB_TOKEN` 或 `GITHUB_TOKEN`（PAT 需 `repo` / `Contents: Read`）。
不要把 PAT 写进浏览器请求；面板会提示在服务端配置。检查失败时面板显示明确错误，不显示「已是最新」。

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
- Windows 资产命名 `NGG-Setup-<tag>.exe` / `NGG-<tag>-windows-amd64.zip`；
- Linux 资产命名 `ngg-<tag>-linux-<arch>.tar.gz`；
- tar.gz / ZIP 内必须含 `scripts/`，否则 `/update/status` 会显示 `script_ready=false`；
- `release.yml` 在打 `v*` tag 时自动生成上述资产与合并后的 `SHA256SUMS`。

---

## 5. 相关文档

| 文档 | 内容 |
| --- | --- |
| [`GENERATIONS.md`](GENERATIONS.md) | 代际沿革、判定标准与可见位置 |
| [`INSTALL-WINDOWS.md`](INSTALL-WINDOWS.md) | Windows 安装包与目录结构 |
| [`DEPLOY-VPS.md`](DEPLOY-VPS.md) | VPS 部署与 `deploy-vps.sh upgrade` |
| [`02-api.md`](02-api.md) | 基础 API 契约 |
