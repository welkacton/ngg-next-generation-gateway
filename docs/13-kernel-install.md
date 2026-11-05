# 内核安装（异步任务与配额说明）

> 适用版本：Gen 2 / v2.2.1 起。入口：Web 控制台 → 内核引擎 → 内核管理 → 「安装」。

## 1. 为什么之前三个内核都装不上

浏览器里点「安装」后看似无反应或直接报错，实际是两个真实缺陷叠加：

1. **前端 30 秒超时**：安装需要下载几十 MB 的官方 Release 包、做 SHA256 校验、
   解包、再执行下载下来的二进制做版本自检，通常远超 `web/src/api/client.ts` 的 30 秒
   HTTP 超时。旧实现把整个流程放在 `POST /cores/{id}/install` 的同步请求里，
   浏览器先断开，安装结果再也回不到界面。
2. **GitHub API 匿名限流**：匿名配额只有 60 次/小时。v2.2.0 的协议目录页每 60 秒
   轮询一次 `GET /protocols/catalog`，而该端点在 stale 时会顺带触发 3 次真实 GitHub
   请求，配额很快耗尽；内核安装随后拿到 `HTTP 403`，三个内核一起失败。

三个内核的资产名与官方 Release 是能正确匹配的（实测 Mihomo `v1.19.33`、
sing-box `v1.14.4`、Xray-core `v26.10.31` 的 Windows amd64 资产均存在且带 `sha256:` digest），
所以问题不在协议适配，而在请求生命周期与配额。

## 2. 现在的做法

### 异步后台任务

- `POST /api/v1/cores/{id}/install` **立即返回 `202`** + `task_id`，后台 goroutine 执行
  与原来完全相同的 `engine.Install`（下载 → SHA256 → 上游 `.dgst` 二次校验 → 解包 →
  执行二进制版本校验 → 落盘 → 记录），没有任何"假装成功"。
- 进度通过既有的 `core.install` WebSocket 事件实时推送，界面「安装进度」面板照常显示。
- `GET /api/v1/cores/install/status` 可轮询：`?id=` 查单个任务、`?core=` 查该内核最近一次、
  无参数列全部。任务状态为 `running / succeeded / failed`，失败时带真实错误。
- 前端提交后不再阻塞，每 2 秒轮询一次任务状态，完成后刷新内核列表并提示成功/失败。
- 同一内核在任务运行期间重复提交会返回 `409`，不会并发下载两次。
- 脚本/CLI 可加 `?wait=1` 走原来的同步模式（返回安装结果本体）。

### GitHub 配额治理

- `GET /protocols/catalog` 变成**纯读缓存**，绝不触发 GitHub 请求；目录刷新只由
  6 小时定时器和「刷新目录」按钮负责。
- 安装器的 GitHub API 请求统一读取服务端 `NGG_GITHUB_TOKEN` / `GITHUB_TOKEN`（PAT），
  匿名 60/h → 认证 5000/h；PAT 只放在请求头，不落盘、不写日志。
- 遇到 `403 / 429` 时返回明确提示：「GitHub API 限流…请在服务端设置 NGG_GITHUB_TOKEN
  或 GITHUB_TOKEN 后重试」，而不是笼统的 `HTTP 403`。
- 下载 host 仍严格限制在 `github.com` / `*.githubusercontent.com` /
  `release-assets.githubusercontent.com`，且必须 HTTPS；摘要校验 Fail-Closed。

## 3. 配置 PAT（可选，推荐）

Linux systemd 服务：

```bash
sudo systemctl edit ngg
# [Service]
# Environment="NGG_GITHUB_TOKEN=ghp_xxx"
sudo systemctl daemon-reload && sudo systemctl restart ngg
```

Windows 服务（管理员 PowerShell）：

```powershell
[Environment]::SetEnvironmentVariable('NGG_GITHUB_TOKEN', 'ghp_xxx', 'Machine')
Restart-Service -Name NGG
```

PAT 只需公开仓库的只读权限；不写进 `configs/ngg.yaml`，也不发给浏览器。

## 4. 排障

| 现象 | 原因 / 处理 |
| --- | --- |
| 提示「GitHub API 限流」 | 匿名配额耗尽，配置上面的 PAT 或稍后重试 |
| 任务长时间 running | 查看「安装进度」面板与 `core.install` 事件；下载慢属正常 |
| `SHA256 校验失败` / `版本校验失败` | 上游资产或 digest 变更，拒绝安装（Fail-Closed），不是 NGG 缺陷 |
| `release ... has no asset ...` | 该内核这个版本没有对应平台的包，换一个版本或看官方 Release 资产名 |
| `内核 X 的安装任务仍在进行中` | 等当前任务结束，或到状态端点确认 |

## 5. 测试覆盖

`internal/api/installs_test.go` 验证任务状态机（running → succeeded / failed）、
重复提交冲突、404 未知内核、202 立即返回、`?core=` 与 `?id=` 状态查询。
真实下载链路仍由 `internal/engine` 的既有测试与 SHA256/digest 校验保障。
